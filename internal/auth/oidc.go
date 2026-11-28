package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// OIDCConfig 는 Keycloak 연결 정보다. 고객사 환경마다 달라 환경 변수로 받는다(설계 6장). 주소는 폐쇄망 안 주소다
type OIDCConfig struct {
	Issuer       string // GATEWAY_SSO_ISSUER, 예: http://keycloak:8080/realms/corp
	ClientID     string // GATEWAY_SSO_CLIENT_ID
	ClientSecret string // GATEWAY_SSO_CLIENT_SECRET (공개 클라이언트면 비운다)
	PublicURL    string // GATEWAY_PUBLIC_URL, 브라우저가 보는 게이트웨이 주소(redirect_uri 의 앞부분, litellm PROXY_BASE_URL)
}

// LogValue 는 로그에 클라이언트 비밀이 나가지 않게 가린다
func (c OIDCConfig) LogValue() slog.Value {
	secret := ""
	if c.ClientSecret != "" {
		secret = "****"
	}
	return slog.GroupValue(slog.String("issuer", c.Issuer), slog.String("client_id", c.ClientID),
		slog.String("client_secret", secret), slog.String("public_url", c.PublicURL))
}

// String 은 %v 로 찍어도 클라이언트 비밀이 나가지 않게 한다
func (c OIDCConfig) String() string {
	return fmt.Sprintf("OIDCConfig{issuer=%s client_id=%s client_secret=****}", c.Issuer, c.ClientID)
}

// oidcClient 는 authorization code + PKCE(S256) 흐름이다(K 2.2·2.3, litellm generic SSO 와 같다)
type oidcClient struct {
	cfg       OIDCConfig
	http      *http.Client
	authorize string
	token     string
	userinfo  string
}

// discoverOIDC 는 issuer 의 /.well-known/openid-configuration 에서 엔드포인트를 읽는다
func discoverOIDC(ctx context.Context, cfg OIDCConfig, client *http.Client) (*oidcClient, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SSO 설정 읽기: %w", err)
	}
	defer resp.Body.Close()
	var doc struct {
		Authorization string `json:"authorization_endpoint"`
		Token         string `json:"token_endpoint"`
		Userinfo      string `json:"userinfo_endpoint"`
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SSO 설정 읽기: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("SSO 설정 해석: %w", err)
	}
	return &oidcClient{cfg: cfg, http: client, authorize: doc.Authorization, token: doc.Token, userinfo: doc.Userinfo}, nil
}

func (o *oidcClient) redirectURI() string {
	return strings.TrimRight(o.cfg.PublicURL, "/") + "/sso/callback"
}

// authorizeURL 은 Keycloak 로그인 주소다. verifier 는 콜백에서 코드 교환에 쓴다
func (o *oidcClient) authorizeURL(state string) (string, string) {
	verifier := randomURLSafe(32)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {o.cfg.ClientID},
		"redirect_uri":          {o.redirectURI()},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	return o.authorize + "?" + q.Encode(), verifier
}

// oidcClaims 는 사용자 행을 만드는 데 쓰는 클레임이다
type oidcClaims struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     *bool  `json:"email_verified"` // IdP 가 안 주면 nil
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	// Raw 는 userinfo 응답 전체다. 그룹 같은, 설정으로 이름을 정하는 클레임을 읽는 데 쓴다
	Raw map[string]any `json:"-"`
}

// tokenExchangeError 는 IdP 가 코드 교환을 거절한 것이다. 문구는 litellm 그대로 클라이언트에 보여 준다.
// 연결 실패 같은 그 밖의 오류는 내부 주소가 들어 있을 수 있어 이 타입이 아니고, 로그에만 남긴다
type tokenExchangeError struct{ message string }

func (e *tokenExchangeError) Error() string { return e.message }

// exchange 는 코드를 토큰으로 바꾸고 userinfo 를 읽는다
func (o *oidcClient) exchange(ctx context.Context, code, verifier string) (oidcClaims, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {o.redirectURI()}}
	// litellm: secret 이 있으면 Basic 인증, 없으면(공개 클라이언트) 본문에 client_id
	if o.cfg.ClientSecret == "" {
		form.Set("client_id", o.cfg.ClientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.token, strings.NewReader(form.Encode()))
	if err != nil {
		return oidcClaims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if o.cfg.ClientSecret != "" {
		req.SetBasicAuth(o.cfg.ClientID, o.cfg.ClientSecret)
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return oidcClaims{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		// litellm: "Token exchange failed: <status> - <본문 500자>"
		return oidcClaims{}, &tokenExchangeError{fmt.Sprintf("Token exchange failed: %d - %s", resp.StatusCode, truncate(string(body), 500))}
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.AccessToken == "" {
		return oidcClaims{}, &tokenExchangeError{"Token exchange failed: no access_token"}
	}
	return o.readUserinfo(ctx, token.AccessToken)
}

func (o *oidcClient) readUserinfo(ctx context.Context, accessToken string) (oidcClaims, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.userinfo, nil)
	if err != nil {
		return oidcClaims{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := o.http.Do(req)
	if err != nil {
		return oidcClaims{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return oidcClaims{}, fmt.Errorf("userinfo: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oidcClaims{}, err
	}
	var claims oidcClaims
	if err := json.Unmarshal(body, &claims); err != nil {
		return oidcClaims{}, err
	}
	if err := json.Unmarshal(body, &claims.Raw); err != nil {
		return oidcClaims{}, err
	}
	return claims, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
