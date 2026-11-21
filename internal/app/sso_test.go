package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

// SSO 콜백(K 2장, L 5장)을 가짜 IdP 로 확인한다: 웹(대시보드)·CLI(데스크탑) 분기, state·PKCE 쿠키 검증, 오류 노출, 계정 가로채기 거부

type fakeIdP struct {
	*httptest.Server
	mu           sync.Mutex
	claims       map[string]any
	rejectTokens bool
}

func newFakeIdP(t *testing.T) *fakeIdP {
	idp := &fakeIdP{claims: map[string]any{"sub": "sub-kim", "email": "Kim@corp.local", "name": "김", "email_verified": true}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"authorization_endpoint":"%[1]s/auth","token_endpoint":"%[1]s/token","userinfo_endpoint":"%[1]s/userinfo"}`, idp.URL)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		idp.mu.Lock()
		defer idp.mu.Unlock()
		if idp.rejectTokens {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"at-1"}`)
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		idp.mu.Lock()
		defer idp.mu.Unlock()
		_ = json.NewEncoder(w).Encode(idp.claims)
	})
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	return idp
}

func (i *fakeIdP) setClaims(claims map[string]any) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.claims = claims
}

// ssoApp 은 SSO 와 비밀번호 로그인을 모두 넣은 빌드다
func ssoApp(t *testing.T, idp *fakeIdP) (browser, apiClient) {
	t.Helper()
	cfg, _ := LoadConfig(testEnv)
	cfg.Login = auth.LoginMethods{SSO: true, Password: true}
	cfg.SSO = auth.OIDCConfig{Issuer: idp.URL, ClientID: "gateway", PublicURL: "http://gw.test"}
	db, _ := dbtest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := newIdentity(db)
	login, err := newLogin(t.Context(), cfg, id, logger)
	if err != nil {
		t.Fatal(err)
	}
	h := buildHandler(cfg, &core.Lifecycle{}, db, id, login, startUsage(t, db, logger), logger)
	return browser{t: t, h: h}, apiClient{t: t, h: h}
}

// cookieHeader 는 응답이 심은 쿠키를 다음 요청의 Cookie 헤더로 되돌린다
func cookieHeader(rec *httptest.ResponseRecorder) string {
	var parts []string
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge >= 0 && c.Value != "" {
			parts = append(parts, c.Name+"="+c.Value)
		}
	}
	return strings.Join(parts, "; ")
}

// authorizeState 는 IdP 로 보내는 리다이렉트의 state 다
func authorizeState(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != 303 {
		t.Fatalf("IdP 로 보내야 한다: %d %s", rec.Code, rec.Body.String())
	}
	target, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := target.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "gateway" || q.Get("redirect_uri") != "http://gw.test/sso/callback" {
		t.Errorf("인가 요청 = %v", q)
	}
	return q.Get("state")
}

func TestSSO_DashboardWebLoginEndToEnd(t *testing.T) {
	idp := newFakeIdP(t)
	b, _ := ssoApp(t, idp)

	start := b.do("GET", "/sso/key/generate?return_to="+url.QueryEscape("http://gw.test/ui/login/"), nil, nil)
	state := authorizeState(t, start)
	cookies := map[string]string{"Cookie": cookieHeader(start)}

	back := b.do("GET", "/sso/callback?code=abc&state="+url.QueryEscape(state), nil, cookies)
	if back.Code != 303 || back.Header().Get("Location") != "http://gw.test/ui/?login=success" {
		t.Fatalf("콜백 = %d %v %s", back.Code, back.Header(), back.Body.String())
	}
	var token string
	for _, c := range back.Result().Cookies() {
		if c.Name == "token" {
			token = c.Value
			if c.HttpOnly {
				t.Error("token 쿠키는 대시보드가 읽어야 한다")
			}
		}
	}
	claims := uiClaims(t, token)
	if claims["login_method"] != "sso" || claims["user_id"] != "kim@corp.local" || claims["user_role"] != "internal_user_viewer" {
		t.Errorf("클레임 = %v", claims)
	}
	// SSO 세션은 비밀번호 변경이 막힌다(litellm 과 같다)
	session := map[string]string{"Authorization": "Bearer " + claims["key"].(string)}
	if rec := b.doJSON("POST", "/user/password/change", `{"current_password":"x","new_password":"Y-new-pass-1234"}`, session); rec.Code != 403 {
		t.Errorf("SSO 세션의 비밀번호 변경: %d", rec.Code)
	}
}

func TestSSO_CallbackRejectsBadStateAndMissingVerifier(t *testing.T) {
	idp := newFakeIdP(t)
	b, _ := ssoApp(t, idp)
	start := b.do("GET", "/sso/key/generate", nil, nil)
	state := authorizeState(t, start)
	good := cookieHeader(start)

	if rec := b.do("GET", "/sso/callback?code=abc&state=forged", nil, map[string]string{"Cookie": good}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "state") {
		t.Errorf("state 가 쿠키와 다르면 400: %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.do("GET", "/sso/callback?code=abc&state="+url.QueryEscape(state), nil, nil); rec.Code != 400 {
		t.Errorf("state 쿠키가 없으면 400: %d", rec.Code)
	}
	onlyState := "litellm_oauth_state=" + state
	if rec := b.do("GET", "/sso/callback?code=abc&state="+url.QueryEscape(state), nil, map[string]string{"Cookie": onlyState}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "PKCE") {
		t.Errorf("PKCE 쿠키가 없으면 400: %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.do("GET", "/sso/callback?state="+url.QueryEscape(state), nil, map[string]string{"Cookie": good}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "code") {
		t.Errorf("code 가 없으면 400: %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.do("GET", "/sso/callback?error=access_denied&error_description=nope", nil, nil); rec.Code != 401 || !strings.Contains(rec.Body.String(), "OAuth error: access_denied") {
		t.Errorf("IdP 가 보낸 오류: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSSO_ExchangeFailuresShowReasonButNeverInternalAddresses(t *testing.T) {
	idp := newFakeIdP(t)
	b, _ := ssoApp(t, idp)
	login := func() *httptest.ResponseRecorder {
		start := b.do("GET", "/sso/key/generate", nil, nil)
		state := authorizeState(t, start)
		return b.do("GET", "/sso/callback?code=abc&state="+url.QueryEscape(state), nil, map[string]string{"Cookie": cookieHeader(start)})
	}
	idp.mu.Lock()
	idp.rejectTokens = true
	idp.mu.Unlock()
	if rec := login(); rec.Code != 401 || !strings.Contains(rec.Body.String(), "Token exchange failed: 400") {
		t.Errorf("IdP 가 거절하면 그 이유를 보인다: %d %s", rec.Code, rec.Body.String())
	}
	idp.mu.Lock()
	idp.rejectTokens = false
	idp.mu.Unlock()
	host := strings.TrimPrefix(idp.URL, "http://")
	idp.CloseClientConnections()
	idp.Close() // 이제 IdP 에 닿지 않는다
	rec := login()
	if rec.Code != 401 || strings.Contains(rec.Body.String(), host) || strings.Contains(rec.Body.String(), "dial") {
		t.Errorf("연결 오류에 내부 주소가 새면 안 된다: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSSO_CLILoginThroughIdPAndAccountTakeoverIsRefused(t *testing.T) {
	idp := newFakeIdP(t)
	b, api := ssoApp(t, idp)
	api.ok("POST", "/user/new", map[string]any{"user_id": "kim@corp.local", "user_email": "kim@corp.local", "user_role": "internal_user"})

	start := jsonOf(t, b.do("POST", "/sso/cli/start", nil, nil))
	loginID, code := start["login_id"].(string), start["user_code"].(string)
	// 비밀번호도 함께 있는 빌드는 선택 화면을 보이고, method=sso 로 IdP 로 간다
	chooser := b.do("GET", "/sso/key/generate?source=litellm-cli&key="+loginID, nil, nil)
	if chooser.Code != 200 || !strings.Contains(chooser.Body.String(), "method=sso") {
		t.Fatalf("선택 화면: %d %s", chooser.Code, chooser.Body.String())
	}
	toIdP := b.do("GET", "/sso/key/generate?source=litellm-cli&key="+loginID+"&method=sso", nil, nil)
	state := authorizeState(t, toIdP)
	if state != "litellm-session-token:"+loginID {
		t.Errorf("CLI state = %q", state)
	}
	cookies := map[string]string{"Cookie": cookieHeader(toIdP)}
	callbackURL := "/sso/callback?code=abc&state=" + url.QueryEscape(state)

	// 관리자가 만든 계정(SSO 에 안 묶임)에 확인되지 않은 이메일로는 못 들어온다
	idp.setClaims(map[string]any{"sub": "sub-kim", "email": "kim@corp.local", "email_verified": false})
	if rec := b.do("GET", callbackURL, nil, cookies); rec.Code != 403 {
		t.Errorf("확인 안 된 이메일: %d %s", rec.Code, rec.Body.String())
	}
	// 확인된 이메일이면 묶이고 확인 화면으로 간다
	idp.setClaims(map[string]any{"sub": "sub-kim", "email": "kim@corp.local", "email_verified": true})
	ok := b.do("GET", callbackURL, nil, cookies)
	if ok.Code != 200 || !strings.Contains(ok.Body.String(), "Complete CLI Login") {
		t.Fatalf("CLI 콜백: %d %s", ok.Code, ok.Body.String())
	}
	if !strings.Contains(ok.Body.String(), "browser_complete_token") || code == "" {
		t.Error("코드 확인 폼이 없다")
	}
	// 같은 이메일의 다른 주체는 거부한다
	second := jsonOf(t, b.do("POST", "/sso/cli/start", nil, nil))["login_id"].(string)
	toIdP2 := b.do("GET", "/sso/key/generate?source=litellm-cli&key="+second+"&method=sso", nil, nil)
	state2 := authorizeState(t, toIdP2)
	idp.setClaims(map[string]any{"sub": "sub-intruder", "email": "kim@corp.local", "email_verified": true})
	if rec := b.do("GET", "/sso/callback?code=abc&state="+url.QueryEscape(state2), nil, map[string]string{"Cookie": cookieHeader(toIdP2)}); rec.Code != 403 ||
		!strings.Contains(rec.Body.String(), "different SSO identity") {
		t.Errorf("다른 SSO 주체: %d %s", rec.Code, rec.Body.String())
	}
}
