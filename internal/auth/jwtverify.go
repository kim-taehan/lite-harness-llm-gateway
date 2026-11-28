package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Keycloak 이 발급한 JWT 의 서명·발급자·대상·시간을 검증한다. 서명 키는 설정한 발급자의 JWKS 에서만 가져온다

const (
	jwksTTL         = 10 * time.Minute // 공개키 캐시 수명
	jwksStaleTTL    = time.Hour        // 인증 서버가 죽었을 때 마지막 키로 버티는 시간
	jwksMinRefetch  = 30 * time.Second // 모르는 kid 가 와도 이 간격보다 자주 다시 가져오지 않는다
	jwtClockSkew    = 30 * time.Second
	maxJWKSBytes    = 1 << 20
	minRSAKeyBits   = 2048
	jwtWarnInterval = time.Minute // 같은 거절 사유는 이 간격에 한 번만 로그로 남긴다
)

// 거절 사유. 토큰 내용은 담지 않는다
var (
	errJWTInvalid      = errors.New("JWT 가 올바르지 않다")
	errJWKSUnavailable = errors.New("JWT 검증 키를 가져올 수 없다")
)

func invalidJWT(reason string) error { return fmt.Errorf("%w: %s", errJWTInvalid, reason) }

// jwtVerifier 는 JWT 하나를 검증한다. 공개키 캐시를 여러 요청이 함께 쓴다
type jwtVerifier struct {
	cfg    JWTConfig
	http   *http.Client
	now    func() time.Time
	logger *slog.Logger

	mu          sync.Mutex
	keys        map[string]crypto.PublicKey // kid → 공개키
	fetchedAt   time.Time
	lastAttempt time.Time
	jwksURL     string

	warnMu sync.Mutex
	warned map[string]time.Time
}

func newJWTVerifier(cfg JWTConfig, client *http.Client, now func() time.Time, logger *slog.Logger) *jwtVerifier {
	return &jwtVerifier{cfg: cfg, http: client, now: now, logger: logger, jwksURL: cfg.JWKSURL, warned: map[string]time.Time{}}
}

// jwtClaims 는 사용자·그룹을 정하는 데 쓰는 검증된 토큰 내용이다
type jwtClaims struct {
	raw    map[string]any
	claims oidcClaims
}

// verify 는 서명·발급자·대상·만료를 확인하고 검증된 내용을 돌려준다. 거절은 errJWTInvalid, 검증 키를 못 구하면 errJWKSUnavailable 이다
func (v *jwtVerifier) verify(ctx context.Context, token string) (jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, invalidJWT("형식")
	}
	var header struct {
		Alg  string          `json:"alg"`
		Kid  string          `json:"kid"`
		Crit json.RawMessage `json:"crit"`
	}
	if err := decodeSegment(parts[0], &header); err != nil {
		return jwtClaims{}, invalidJWT("헤더")
	}
	// 헤더가 가리키는 키 위치(jku·x5u·jwk)는 읽지 않는다. 알고리즘은 서버가 허용한 둘뿐이다(none·HS256 거절)
	if header.Alg != "RS256" && header.Alg != "ES256" {
		return jwtClaims{}, invalidJWT("허용하지 않는 알고리즘")
	}
	if len(header.Crit) > 0 {
		return jwtClaims{}, invalidJWT("crit 헤더")
	}
	key, err := v.keyFor(ctx, header.Kid)
	if err != nil {
		return jwtClaims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtClaims{}, invalidJWT("서명 형식")
	}
	if err := verifySignature(header.Alg, key, parts[0]+"."+parts[1], signature); err != nil {
		return jwtClaims{}, err
	}
	var payload map[string]any
	if err := decodeSegment(parts[1], &payload); err != nil {
		return jwtClaims{}, invalidJWT("본문")
	}
	if err := v.checkClaims(payload); err != nil {
		return jwtClaims{}, err
	}
	return jwtClaims{raw: payload, claims: oidcClaimsFrom(payload)}, nil
}

func decodeSegment(segment string, into any) error {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

func verifySignature(alg string, key crypto.PublicKey, signingInput string, signature []byte) error {
	digest := sha256.Sum256([]byte(signingInput))
	switch alg {
	case "RS256":
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return invalidJWT("키 종류가 알고리즘과 다르다")
		}
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature) != nil {
			return invalidJWT("서명")
		}
	case "ES256":
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return invalidJWT("키 종류가 알고리즘과 다르다")
		}
		if len(signature) != 64 {
			return invalidJWT("서명 길이")
		}
		r, s := new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])
		if !ecdsa.Verify(pub, digest[:], r, s) {
			return invalidJWT("서명")
		}
	default:
		return invalidJWT("허용하지 않는 알고리즘") // 알고리즘 검사가 빠져도 서명 없이 통과하지 못하게 한다
	}
	return nil
}

// checkClaims 는 발급자·대상·만료를 확인한다. 만료가 없는 토큰은 받지 않는다
func (v *jwtVerifier) checkClaims(payload map[string]any) error {
	if iss, _ := payload["iss"].(string); iss != v.cfg.Issuer {
		return invalidJWT("발급자")
	}
	if !audienceContains(payload["aud"], v.cfg.Audience) {
		return invalidJWT("대상(aud)")
	}
	now := v.now()
	exp, ok := numericDate(payload["exp"])
	if !ok {
		return invalidJWT("exp 없음")
	}
	if now.After(exp.Add(jwtClockSkew)) {
		return invalidJWT("만료")
	}
	if raw, present := payload["nbf"]; present {
		nbf, ok := numericDate(raw)
		if !ok || now.Add(jwtClockSkew).Before(nbf) {
			return invalidJWT("아직 유효하지 않다(nbf)")
		}
	}
	return nil
}

func audienceContains(aud any, want string) bool {
	switch a := aud.(type) {
	case string:
		return a == want
	case []any:
		for _, item := range a {
			if s, _ := item.(string); s == want {
				return true
			}
		}
	}
	return false
}

func numericDate(v any) (time.Time, bool) {
	seconds, ok := v.(float64)
	if !ok || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(seconds), 0), true
}

func oidcClaimsFrom(payload map[string]any) oidcClaims {
	str := func(k string) string { s, _ := payload[k].(string); return s }
	c := oidcClaims{Subject: str("sub"), Email: str("email"), Name: str("name"), PreferredUsername: str("preferred_username")}
	if verified, ok := payload["email_verified"].(bool); ok {
		c.EmailVerified = &verified
	}
	return c
}

// keyFor 는 kid 의 공개키를 돌려준다. 캐시에 없거나 만료됐으면 JWKS 를 다시 가져온다(다만 jwksMinRefetch 간격으로만:
// 모르는 kid 를 계속 보내 인증 서버를 두드리지 못하게 한다). 가져오지 못해도 마지막 키가 jwksStaleTTL 안이면 쓴다
func (v *jwtVerifier) keyFor(ctx context.Context, kid string) (crypto.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if v.keys != nil && now.Sub(v.fetchedAt) < jwksTTL {
		if key, ok := v.lookup(kid); ok {
			return key, nil
		}
	}
	if now.Sub(v.lastAttempt) >= jwksMinRefetch {
		v.lastAttempt = now
		if err := v.refresh(ctx, now); err != nil {
			v.warn("jwks", "JWKS 가져오기 실패", err)
		}
	}
	if v.keys == nil || now.Sub(v.fetchedAt) >= jwksTTL+jwksStaleTTL {
		return nil, errJWKSUnavailable
	}
	if key, ok := v.lookup(kid); ok {
		return key, nil
	}
	return nil, invalidJWT("알 수 없는 kid")
}

// lookup 은 kid 로 찾는다. 토큰에 kid 가 없으면 키가 하나뿐일 때만 그 키를 쓴다
func (v *jwtVerifier) lookup(kid string) (crypto.PublicKey, bool) {
	if kid == "" {
		if len(v.keys) != 1 {
			return nil, false
		}
		for _, key := range v.keys {
			return key, true
		}
	}
	key, ok := v.keys[kid]
	return key, ok
}

// refresh 는 JWKS 를 받아 캐시를 바꾼다. 실패하면 이전 캐시를 그대로 둔다
func (v *jwtVerifier) refresh(ctx context.Context, now time.Time) error {
	if v.jwksURL == "" {
		discovered, err := v.discoverJWKSURL(ctx)
		if err != nil {
			return err
		}
		v.jwksURL = discovered
	}
	body, err := v.get(ctx, v.jwksURL)
	if err != nil {
		return err
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("JWKS 해석: %w", err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			if pub, ok := rsaKey(k.N, k.E); ok {
				keys[k.Kid] = pub
			}
		case "EC":
			if pub, ok := ecKey(k.Crv, k.X, k.Y); ok {
				keys[k.Kid] = pub
			}
		}
	}
	if len(keys) == 0 {
		return errors.New("JWKS 에 쓸 수 있는 키가 없다")
	}
	v.keys, v.fetchedAt = keys, now
	return nil
}

func (v *jwtVerifier) discoverJWKSURL(ctx context.Context) (string, error) {
	body, err := v.get(ctx, strings.TrimRight(v.cfg.Issuer, "/")+"/.well-known/openid-configuration")
	if err != nil {
		return "", err
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.JWKSURI == "" {
		return "", errors.New("발급자 설정에 jwks_uri 가 없다")
	}
	return doc.JWKSURI, nil
}

func (v *jwtVerifier) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes))
}

func rsaKey(n, e string) (*rsa.PublicKey, bool) {
	nb, err1 := base64.RawURLEncoding.DecodeString(n)
	eb, err2 := base64.RawURLEncoding.DecodeString(e)
	if err1 != nil || err2 != nil || len(eb) == 0 || len(eb) > 4 {
		return nil, false
	}
	modulus := new(big.Int).SetBytes(nb)
	exponent := int(new(big.Int).SetBytes(eb).Int64())
	if modulus.BitLen() < minRSAKeyBits || exponent < 3 || exponent%2 == 0 {
		return nil, false
	}
	return &rsa.PublicKey{N: modulus, E: exponent}, true
}

func ecKey(crv, x, y string) (*ecdsa.PublicKey, bool) {
	if crv != "P-256" {
		return nil, false
	}
	xb, err1 := base64.RawURLEncoding.DecodeString(x)
	yb, err2 := base64.RawURLEncoding.DecodeString(y)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}
	if _, err := pub.ECDH(); err != nil { // 곡선 위의 점인지 확인한다
		return nil, false
	}
	return pub, true
}

// warn 은 같은 사유를 jwtWarnInterval 에 한 번만 로그로 남긴다(토큰을 던져 로그를 불리지 못하게). 토큰 내용은 남기지 않는다
func (v *jwtVerifier) warn(reason, msg string, err error) {
	if v.logger == nil {
		return
	}
	v.warnMu.Lock()
	defer v.warnMu.Unlock()
	now := v.now()
	if last, ok := v.warned[reason]; ok && now.Sub(last) < jwtWarnInterval {
		return
	}
	v.warned[reason] = now
	v.logger.Warn(msg, "reason", reason, "err", err)
}
