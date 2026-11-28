package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 가짜 Keycloak: JWKS 를 내놓고, 요청 수를 센다. 서명 키는 시험이 만든다

const (
	testIssuer   = "http://keycloak.test/realms/corp"
	testAudience = "llm-gateway"
)

type fakeJWKS struct {
	srv      *httptest.Server
	mu       sync.Mutex
	keys     []map[string]any
	failing  bool
	requests atomic.Int32
}

func newFakeJWKS(t *testing.T) *fakeJWKS {
	t.Helper()
	f := &fakeJWKS{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failing {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": f.keys})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJWKS) publish(keys ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = keys
}

func (f *fakeJWKS) setFailing(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = v
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func rsaJWK(kid string, pub *rsa.PublicKey) map[string]any {
	return map[string]any{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes())}
}

func ecJWK(kid string, pub *ecdsa.PublicKey) map[string]any {
	return map[string]any{"kty": "EC", "kid": kid, "use": "sig", "crv": "P-256",
		"x": b64(pub.X.FillBytes(make([]byte, 32))), "y": b64(pub.Y.FillBytes(make([]byte, 32)))}
}

func mustRSA(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// signRS256 은 토큰을 만든다. header 를 바꿔 넣어 공격 모양도 만든다
func signRS256(t *testing.T, key *rsa.PrivateKey, header, payload map[string]any) string {
	t.Helper()
	input := segment(t, header) + "." + segment(t, payload)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + b64(sig)
}

func signES256(t *testing.T, key *ecdsa.PrivateKey, header, payload map[string]any) string {
	t.Helper()
	input := segment(t, header) + "." + segment(t, payload)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return input + "." + b64(sig)
}

func segment(t *testing.T, v map[string]any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b64(data)
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func validPayload(now time.Time, extra map[string]any) map[string]any {
	p := map[string]any{"iss": testIssuer, "aud": testAudience, "sub": "kc-1", "email": "kim@corp.test", "exp": now.Add(5 * time.Minute).Unix()}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func newTestVerifier(t *testing.T, jwks *fakeJWKS, clock *testClock) *jwtVerifier {
	t.Helper()
	cfg := JWTConfig{Issuer: testIssuer, Audience: testAudience, JWKSURL: jwks.srv.URL}
	return newJWTVerifier(cfg, jwks.srv.Client(), clock.Now, nil)
}

func newClock() *testClock { return &testClock{now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)} }

func TestJWT_AcceptsRS256AndES256FromTheConfiguredJWKS(t *testing.T) {
	jwks, clock := newFakeJWKS(t), newClock()
	rsaKey := mustRSA(t, 2048)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	jwks.publish(rsaJWK("rsa-1", &rsaKey.PublicKey), ecJWK("ec-1", &ecKey.PublicKey))
	v := newTestVerifier(t, jwks, clock)

	claims, err := v.verify(t.Context(), signRS256(t, rsaKey, map[string]any{"alg": "RS256", "kid": "rsa-1"}, validPayload(clock.Now(), map[string]any{"groups": []string{"eng"}})))
	if err != nil || claims.claims.Email != "kim@corp.test" || claims.claims.Subject != "kc-1" {
		t.Fatalf("RS256: %v %+v", err, claims.claims)
	}
	if _, err := v.verify(t.Context(), signES256(t, ecKey, map[string]any{"alg": "ES256", "kid": "ec-1"}, validPayload(clock.Now(), nil))); err != nil {
		t.Fatalf("ES256: %v", err)
	}
}

func TestJWT_RejectsForgedAndMisaddressedTokens(t *testing.T) {
	jwks, clock := newFakeJWKS(t), newClock()
	good, attacker := mustRSA(t, 2048), mustRSA(t, 2048)
	jwks.publish(rsaJWK("k1", &good.PublicKey))
	v := newTestVerifier(t, jwks, clock)
	hdr := map[string]any{"alg": "RS256", "kid": "k1"}
	now := clock.Now()

	valid := signRS256(t, good, hdr, validPayload(now, nil))
	tamperedPayload := segment(t, validPayload(now, map[string]any{"email": "admin@corp.test"}))
	parts := splitToken(valid)

	// HS256: 공개키 바이트를 비밀로 써서 서명한 알고리즘 혼동 공격
	pubDER := good.PublicKey.N.Bytes()
	hsInput := segment(t, map[string]any{"alg": "HS256", "kid": "k1"}) + "." + segment(t, validPayload(now, nil))
	mac := hmac.New(sha256.New, pubDER)
	mac.Write([]byte(hsInput))

	cases := map[string]string{
		"본문을 바꿈":          parts[0] + "." + tamperedPayload + "." + parts[2],
		"다른 키로 서명":        signRS256(t, attacker, hdr, validPayload(now, nil)),
		"alg none":        segment(t, map[string]any{"alg": "none", "kid": "k1"}) + "." + segment(t, validPayload(now, nil)) + ".",
		"HS256 혼동":        hsInput + "." + b64(mac.Sum(nil)),
		"발급자가 다름":         signRS256(t, good, hdr, validPayload(now, map[string]any{"iss": "http://evil.test/realms/corp"})),
		"대상이 다름":          signRS256(t, good, hdr, validPayload(now, map[string]any{"aud": "other-app"})),
		"대상 목록에 없음":       signRS256(t, good, hdr, validPayload(now, map[string]any{"aud": []string{"account", "other"}})),
		"만료(오차 밖)":        signRS256(t, good, hdr, validPayload(now, map[string]any{"exp": now.Add(-time.Minute).Unix()})),
		"exp 없음":          signRS256(t, good, hdr, withoutKey(validPayload(now, nil), "exp")),
		"아직 유효하지 않음(nbf)": signRS256(t, good, hdr, validPayload(now, map[string]any{"nbf": now.Add(5 * time.Minute).Unix()})),
		"crit 헤더":         signRS256(t, good, map[string]any{"alg": "RS256", "kid": "k1", "crit": []string{"x"}}, validPayload(now, nil)),
		"모르는 kid":         signRS256(t, good, map[string]any{"alg": "RS256", "kid": "nope"}, validPayload(now, nil)),
		"형식이 아님":          "eyJhbGciOiJSUzI1NiJ9.only-two",
	}
	for name, token := range cases {
		if _, err := v.verify(t.Context(), token); !errors.Is(err, errJWTInvalid) {
			t.Errorf("%s: 거절돼야 한다, err=%v", name, err)
		}
	}
	if _, err := v.verify(t.Context(), valid); err != nil {
		t.Fatalf("정상 토큰이 거절됐다: %v", err)
	}
}

func splitToken(token string) [3]string {
	var out [3]string
	start, n := 0, 0
	for i := 0; i < len(token) && n < 2; i++ {
		if token[i] == '.' {
			out[n], start = token[start:i], i+1
			n++
		}
	}
	out[2] = token[start:]
	return out
}

func withoutKey(m map[string]any, key string) map[string]any {
	delete(m, key)
	return m
}

func TestJWT_AllowsSmallClockSkewOnly(t *testing.T) {
	jwks, clock := newFakeJWKS(t), newClock()
	key := mustRSA(t, 2048)
	jwks.publish(rsaJWK("k1", &key.PublicKey))
	v := newTestVerifier(t, jwks, clock)
	hdr := map[string]any{"alg": "RS256", "kid": "k1"}
	now := clock.Now()

	justExpired := signRS256(t, key, hdr, validPayload(now, map[string]any{"exp": now.Add(-10 * time.Second).Unix()}))
	if _, err := v.verify(t.Context(), justExpired); err != nil {
		t.Errorf("10초 지난 토큰은 시계 오차로 받아야 한다: %v", err)
	}
	soonValid := signRS256(t, key, hdr, validPayload(now, map[string]any{"nbf": now.Add(10 * time.Second).Unix()}))
	if _, err := v.verify(t.Context(), soonValid); err != nil {
		t.Errorf("nbf 가 10초 뒤인 토큰은 시계 오차로 받아야 한다: %v", err)
	}
}

func TestJWT_TokenHeaderCannotPointToItsOwnKey(t *testing.T) {
	jwks, clock := newFakeJWKS(t), newClock()
	good, attacker := mustRSA(t, 2048), mustRSA(t, 2048)
	jwks.publish(rsaJWK("k1", &good.PublicKey))
	v := newTestVerifier(t, jwks, clock)

	var hits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{rsaJWK("k1", &attacker.PublicKey)}})
	}))
	defer evil.Close()
	// 공격자가 자기 키로 서명하고 jku·jwk 로 자기 키를 가리킨다
	token := signRS256(t, attacker, map[string]any{"alg": "RS256", "kid": "k1", "jku": evil.URL, "x5u": evil.URL, "jwk": rsaJWK("k1", &attacker.PublicKey)}, validPayload(clock.Now(), nil))
	if _, err := v.verify(t.Context(), token); !errors.Is(err, errJWTInvalid) {
		t.Fatalf("공격자 키로 서명한 토큰이 받아졌다: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("토큰 헤더의 jku 를 따라갔다(%d번)", hits.Load())
	}
}

func TestJWT_RefetchesForUnknownKidButNotMoreOftenThanTheMinimumInterval(t *testing.T) {
	jwks, clock := newFakeJWKS(t), newClock()
	oldKey, newKey := mustRSA(t, 2048), mustRSA(t, 2048)
	jwks.publish(rsaJWK("old", &oldKey.PublicKey))
	v := newTestVerifier(t, jwks, clock)
	if _, err := v.verify(t.Context(), signRS256(t, oldKey, map[string]any{"alg": "RS256", "kid": "old"}, validPayload(clock.Now(), nil))); err != nil {
		t.Fatal(err)
	}
	base := jwks.requests.Load()

	// 공격: 모르는 kid 를 계속 보내도 인증 서버를 두드리지 않는다
	for range 20 {
		_, _ = v.verify(t.Context(), signRS256(t, oldKey, map[string]any{"alg": "RS256", "kid": "junk"}, validPayload(clock.Now(), nil)))
	}
	if got := jwks.requests.Load() - base; got > 1 {
		t.Fatalf("모르는 kid 20번에 JWKS 를 %d번 가져왔다(최대 1번)", got)
	}

	// 키 회전: 새 kid 가 JWKS 에 생기면, 간격이 지난 뒤 처음 보는 새 kid 토큰이 받아진다
	jwks.publish(rsaJWK("old", &oldKey.PublicKey), rsaJWK("new", &newKey.PublicKey))
	clock.Advance(jwksMinRefetch + time.Second)
	if _, err := v.verify(t.Context(), signRS256(t, newKey, map[string]any{"alg": "RS256", "kid": "new"}, validPayload(clock.Now(), nil))); err != nil {
		t.Fatalf("회전한 새 키의 토큰이 거절됐다: %v", err)
	}
}

func TestJWT_KeepsWorkingOnStaleKeysWhileKeycloakIsDownThenFailsClosed(t *testing.T) {
	jwks, clock := newFakeJWKS(t), newClock()
	key := mustRSA(t, 2048)
	jwks.publish(rsaJWK("k1", &key.PublicKey))
	v := newTestVerifier(t, jwks, clock)
	sign := func() string {
		return signRS256(t, key, map[string]any{"alg": "RS256", "kid": "k1"}, validPayload(clock.Now(), nil))
	}
	if _, err := v.verify(t.Context(), sign()); err != nil {
		t.Fatal(err)
	}

	jwks.setFailing(true)
	clock.Advance(jwksTTL + time.Minute) // 캐시는 만료, 인증 서버는 죽음
	if _, err := v.verify(t.Context(), sign()); err != nil {
		t.Fatalf("인증 서버가 죽어도 마지막 키로 버텨야 한다: %v", err)
	}
	clock.Advance(jwksStaleTTL) // 마지막 키도 너무 오래됨
	if _, err := v.verify(t.Context(), sign()); !errors.Is(err, errJWKSUnavailable) {
		t.Fatalf("너무 오래된 키는 쓰면 안 된다(검증 실패로 닫는다): %v", err)
	}
}

func TestJWT_IgnoresWeakRSAKeysInTheJWKS(t *testing.T) {
	jwks, clock := newFakeJWKS(t), newClock()
	weak := mustRSA(t, 1024)
	jwks.publish(rsaJWK("weak", &weak.PublicKey))
	v := newTestVerifier(t, jwks, clock)
	token := signRS256(t, weak, map[string]any{"alg": "RS256", "kid": "weak"}, validPayload(clock.Now(), nil))
	if _, err := v.verify(t.Context(), token); err == nil {
		t.Fatal("1024비트 RSA 키로 서명한 토큰이 받아졌다")
	}
}

func TestJWT_FindsTheJWKSThroughTheIssuerDiscoveryDocument(t *testing.T) {
	key := mustRSA(t, 2048)
	clock := newClock()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/realms/corp/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jwks_uri": srv.URL + "/realms/corp/certs"})
	})
	mux.HandleFunc("/realms/corp/certs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{rsaJWK("k1", &key.PublicKey)}})
	})
	cfg := JWTConfig{Issuer: srv.URL + "/realms/corp", Audience: testAudience}
	v := newJWTVerifier(cfg, srv.Client(), clock.Now, nil)
	payload := validPayload(clock.Now(), map[string]any{"iss": cfg.Issuer})
	if _, err := v.verify(t.Context(), signRS256(t, key, map[string]any{"alg": "RS256", "kid": "k1"}, payload)); err != nil {
		t.Fatalf("발급자 설정에서 JWKS 를 찾지 못했다: %v", err)
	}
}

func TestJWTConfig_Validate(t *testing.T) {
	ok := JWTConfig{Issuer: "http://kc/realms/corp", Audience: "gw", Groups: map[string]GroupGrant{"eng": {TeamID: "t1"}, "ops": {Admin: true}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("정상 설정이 거절됐다: %v", err)
	}
	for name, cfg := range map[string]JWTConfig{
		"대상 없음":         {Issuer: "http://kc/realms/corp"},
		"발급자가 주소가 아님":   {Issuer: "not a url", Audience: "gw"},
		"아무것도 주지 않는 그룹": {Issuer: "http://kc/realms/corp", Audience: "gw", Groups: map[string]GroupGrant{"eng": {}}},
	} {
		if cfg.Validate() == nil {
			t.Errorf("%s: 거절돼야 한다", name)
		}
	}
	if err := (JWTConfig{}).Validate(); err != nil {
		t.Errorf("꺼진 설정은 검사하지 않는다: %v", err)
	}
}

func TestGroupsOf_ReadsNestedClaimPaths(t *testing.T) {
	payload := map[string]any{"groups": []any{"a", "b"}, "realm_access": map[string]any{"roles": []any{"r1"}}, "single": "x"}
	for path, want := range map[string]string{"groups": "a,b", "realm_access.roles": "r1", "single": "x", "missing": "", "groups.deep": ""} {
		got := ""
		for i, g := range groupsOf(payload, path) {
			if i > 0 {
				got += ","
			}
			got += g
		}
		if got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestLooksLikeJWT(t *testing.T) {
	for token, want := range map[string]bool{
		"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxIn0.sig": true,
		"sk-abc.def.ghi":    false,
		"litellm_login_xxx": false,
		"eyJonlytwo.parts":  false,
		"plain":             false,
	} {
		if got := looksLikeJWT(token); got != want {
			t.Errorf("%q = %v, want %v", token, got, want)
		}
	}
}
