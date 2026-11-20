package app

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// 수정하지 않은 litellm 대시보드의 로그인·세션 유지·로그아웃(L 문서)을 대시보드가 부르는 모양 그대로 확인한다

// uiClaims 는 token JWT 의 클레임이다. 대시보드처럼 서명은 보지 않고 가운데만 푼다
func uiClaims(t *testing.T, jwt string) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT 가 아니다: %q", jwt)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func dashboardLogin(t *testing.T, b browser, username, password string) (int, map[string]any, string) {
	t.Helper()
	req := `{"username":` + jsonString(username) + `,"password":` + jsonString(password) + `}`
	rec := b.doJSON("POST", "/v2/login", req, nil)
	cookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == "token" {
			cookie = c.Value
			if c.HttpOnly || c.Path != "/" {
				t.Errorf("token 쿠키는 대시보드가 읽어야 한다: %+v", c)
			}
		}
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body, cookie
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestDashboard_BootConfigIsPublic(t *testing.T) {
	b := browser{t: t, h: newAPI(t).h}
	for _, path := range []string{"/litellm/.well-known/litellm-ui-config", "/.well-known/litellm-ui-config"} {
		cfg := jsonOf(t, b.do("GET", path, nil, nil))
		if cfg["sso_configured"] != false || cfg["admin_ui_disabled"] != false || cfg["is_control_plane"] != false || cfg["proxy_base_url"] != nil {
			t.Errorf("%s = %v", path, cfg)
		}
	}
}

func TestDashboard_AdminLoginWithMasterKey(t *testing.T) {
	b := browser{t: t, h: newAPI(t).h}
	code, body, cookie := dashboardLogin(t, b, "admin", testMasterKey)
	if code != 200 || body["redirect_url"] != "http://localhost:4000/ui?login=success" || body["token"] != cookie || cookie == "" {
		t.Fatalf("로그인 = %d %v (쿠키 %q)", code, body, cookie)
	}
	claims := uiClaims(t, cookie)
	key, _ := claims["key"].(string)
	if claims["user_id"] != "default_user_id" || claims["user_role"] != "proxy_admin" || claims["premium_user"] != true ||
		claims["login_method"] != "username_password" || claims["auth_header_name"] != "Authorization" || !strings.HasPrefix(key, "litellm_login_") {
		t.Errorf("클레임 = %v", claims)
	}
	auth := map[string]string{"Authorization": "Bearer " + key}
	me := jsonOf(t, b.do("GET", "/management/v1/users/me", nil, auth))
	if me["user_id"] != "default_user_id" || me["user_role"] != "proxy_admin" {
		t.Errorf("users/me = %v", me)
	}
	// 관리자 세션으로 관리 API 를 쓴다
	if rec := b.doJSON("POST", "/team/new", `{"team_id":"t1"}`, auth); rec.Code != 200 {
		t.Errorf("관리자 세션 /team/new: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDashboard_UserPasswordLoginAndLogout(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	api.ok("POST", "/user/new", map[string]any{"user_id": "kim", "user_email": "kim@corp.local", "user_role": "internal_user"})
	api.ok("POST", "/user/update", map[string]any{"user_id": "kim", "password": "s3cret-pass"})

	code, body, _ := dashboardLogin(t, b, "kim@corp.local", "wrong")
	if code != 401 || body["error"].(map[string]any)["param"] != "invalid_credentials" {
		t.Errorf("틀린 비밀번호 = %d %v", code, body)
	}
	code, _, cookie := dashboardLogin(t, b, "KIM@corp.local", "s3cret-pass")
	if code != 200 {
		t.Fatalf("로그인 = %d", code)
	}
	claims := uiClaims(t, cookie)
	auth := map[string]string{"Authorization": "Bearer " + claims["key"].(string)}
	if claims["user_role"] != "internal_user" || claims["user_email"] != "kim@corp.local" {
		t.Errorf("클레임 = %v", claims)
	}
	if me := jsonOf(t, b.do("GET", "/management/v1/users/me", nil, auth)); me["user_id"] != "kim" || me["user_email"] != "kim@corp.local" {
		t.Errorf("users/me = %v", me)
	}
	if rec := b.do("GET", "/sso/get/ui_settings", nil, auth); rec.Code != 200 {
		t.Errorf("ui_settings: %d", rec.Code)
	}

	// 로그아웃하면 쿠키를 지우고, 그 세션 키는 대시보드가 로그아웃으로 알아듣는 문구로 거부된다
	out := b.do("POST", "/session/logout", nil, auth)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Session revoked.") || !strings.Contains(out.Header().Get("Set-Cookie"), "token=;") {
		t.Fatalf("로그아웃 = %d %s %q", out.Code, out.Body.String(), out.Header().Get("Set-Cookie"))
	}
	after := b.do("GET", "/management/v1/users/me", nil, auth)
	if after.Code != 401 || !strings.Contains(after.Body.String(), "Authentication Error - Expired Key") {
		t.Errorf("로그아웃한 세션 = %d %s", after.Code, after.Body.String())
	}
	// 다시 로그인하면 새 세션은 된다
	_, _, again := dashboardLogin(t, b, "kim", "s3cret-pass")
	if rec := b.do("GET", "/management/v1/users/me", nil, map[string]string{"Authorization": "Bearer " + uiClaims(t, again)["key"].(string)}); rec.Code != 200 {
		t.Errorf("다시 로그인: %d", rec.Code)
	}
	// 세션 키가 아니면 로그아웃할 수 없다(litellm 과 같다)
	if rec := b.do("POST", "/session/logout", nil, map[string]string{"Authorization": "Bearer " + testMasterKey}); rec.Code != 403 {
		t.Errorf("마스터 키 로그아웃: %d", rec.Code)
	}
}

func TestDashboard_FormLoginRedirects(t *testing.T) {
	b := browser{t: t, h: newAPI(t).h}
	// SSO 가 없는 빌드의 SSO 버튼은 /login 으로 보내는 폼이다
	page := b.do("GET", "/sso/key/generate?return_to="+url.QueryEscape("http://localhost:4000/ui/login/"), nil, nil)
	if page.Code != 200 || path(t, formAction.FindStringSubmatch(page.Body.String())[1]) != "/login" {
		t.Fatalf("폼 = %d %s", page.Code, page.Body.String())
	}
	rec := b.do("POST", "/login", url.Values{"username": {"admin"}, "password": {testMasterKey}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "http://localhost:4000/ui?login=success" || !strings.Contains(rec.Header().Get("Set-Cookie"), "token=") {
		t.Errorf("폼 로그인 = %d %v", rec.Code, rec.Header())
	}
}
