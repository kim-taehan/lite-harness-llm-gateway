package app

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
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
	if status, body := api.do("POST", "/user/update", map[string]any{"user_id": "kim", "password": "short"}); status != 400 ||
		!strings.Contains(body.(map[string]any)["error"].(map[string]any)["message"].(string), "Password does not meet the required policy") {
		t.Errorf("정책에 안 맞는 비밀번호: %d %v", status, body)
	}
	api.ok("POST", "/user/update", map[string]any{"user_id": "kim", "password": "Admin-set-2026"})

	code, body, _ := dashboardLogin(t, b, "kim@corp.local", "wrong")
	if code != 401 || body["error"].(map[string]any)["param"] != "invalid_credentials" {
		t.Errorf("틀린 비밀번호 = %d %v", code, body)
	}
	// 관리자가 정한 비밀번호로 들어오면 비밀번호를 바꿀 때까지 세 경로만 된다
	code, _, cookie := dashboardLogin(t, b, "KIM@corp.local", "Admin-set-2026")
	if code != 200 || uiClaims(t, cookie)["password_reset_required"] != true {
		t.Fatalf("첫 로그인 = %d %v", code, uiClaims(t, cookie))
	}
	first := map[string]string{"Authorization": "Bearer " + uiClaims(t, cookie)["key"].(string)}
	if rec := b.do("GET", "/sso/get/ui_settings", nil, first); rec.Code != 403 || !strings.Contains(rec.Body.String(), "/ui/change-password") {
		t.Errorf("재설정 전 다른 경로: %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.doJSON("POST", "/user/password/change", `{"current_password":"wrong","new_password":"Kim-own-pass-1"}`, first); rec.Code != 400 {
		t.Errorf("틀린 현재 비밀번호: %d", rec.Code)
	}
	if rec := b.doJSON("POST", "/user/password/change", `{"current_password":"Admin-set-2026","new_password":"Kim-own-pass-1"}`, first); rec.Code != 200 {
		t.Fatalf("비밀번호 변경: %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.do("GET", "/management/v1/users/me", nil, first); rec.Code != 401 {
		t.Errorf("바꾼 뒤 옛 세션: %d", rec.Code)
	}

	code, _, cookie = dashboardLogin(t, b, "KIM@corp.local", "Kim-own-pass-1")
	if code != 200 {
		t.Fatalf("로그인 = %d", code)
	}
	claims := uiClaims(t, cookie)
	auth := map[string]string{"Authorization": "Bearer " + claims["key"].(string)}
	if claims["user_role"] != "internal_user" || claims["user_email"] != "kim@corp.local" || claims["password_reset_required"] != false {
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
	_, _, again := dashboardLogin(t, b, "kim", "Kim-own-pass-1")
	if rec := b.do("GET", "/management/v1/users/me", nil, map[string]string{"Authorization": "Bearer " + uiClaims(t, again)["key"].(string)}); rec.Code != 200 {
		t.Errorf("다시 로그인: %d", rec.Code)
	}
	// 세션 키가 아니면 로그아웃할 수 없다(litellm 과 같다)
	if rec := b.do("POST", "/session/logout", nil, map[string]string{"Authorization": "Bearer " + testMasterKey}); rec.Code != 403 {
		t.Errorf("마스터 키 로그아웃: %d", rec.Code)
	}
}

func TestDashboard_InvitationOnboardingSetsPassword(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	api.ok("POST", "/user/new", map[string]any{"user_id": "new1", "user_email": "new1@corp.local", "user_role": "internal_user"})
	if status, _ := api.do("POST", "/invitation/new", map[string]any{"user_id": "ghost"}); status != 400 {
		t.Errorf("없는 사용자 초대: %d", status)
	}
	inv := api.ok("POST", "/invitation/new", map[string]any{"user_id": "new1"})
	link := inv["id"].(string)

	got := jsonOf(t, b.do("GET", "/onboarding/get_token?invite_link="+link, nil, nil))
	outer := uiClaims(t, got["token"].(string))
	if outer["user_id"] != "new1" || outer["exp"] != nil || got["user_email"] != "new1@corp.local" {
		t.Fatalf("get_token = %v / %v", got, outer)
	}
	onboarding := map[string]string{"Authorization": "Bearer " + outer["key"].(string)}
	claim := func(password string, header map[string]string) *httptest.ResponseRecorder {
		return b.doJSON("POST", "/onboarding/claim_token", `{"invitation_link":"`+link+`","user_id":"new1","password":"`+password+`"}`, header)
	}
	if rec := claim("Good-pass-12345", nil); rec.Code != 401 || !strings.Contains(rec.Body.String(), "Missing onboarding session") {
		t.Errorf("온보딩 토큰 없음: %d %s", rec.Code, rec.Body.String())
	}
	if rec := claim("weak", onboarding); rec.Code != 400 {
		t.Errorf("약한 비밀번호: %d", rec.Code)
	}
	done := claim("Good-pass-12345", onboarding)
	if done.Code != 200 {
		t.Fatalf("claim = %d %s", done.Code, done.Body.String())
	}
	session := uiClaims(t, jsonOf(t, done)["token"].(string))
	if rec := b.do("GET", "/management/v1/users/me", nil, map[string]string{"Authorization": "Bearer " + session["key"].(string)}); rec.Code != 200 {
		t.Errorf("온보딩 세션: %d", rec.Code)
	}
	// 링크는 한 번만 쓴다. 정한 비밀번호로 로그인한다(본인이 정했으니 재설정 요구 없음)
	if rec := claim("Other-pass-12345", onboarding); rec.Code != 401 || !strings.Contains(rec.Body.String(), "already been used") {
		t.Errorf("다시 쓴 링크: %d %s", rec.Code, rec.Body.String())
	}
	if code, _, cookie := dashboardLogin(t, b, "new1@corp.local", "Good-pass-12345"); code != 200 || uiClaims(t, cookie)["password_reset_required"] != false {
		t.Errorf("정한 비밀번호로 로그인 = %d", code)
	}
}

func TestDashboard_SettingsKeepLitellmDefaultsAndStoreChanges(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	// 로그인 전에 부르는 것: 화면 설정(사이드바), 테마, 로고
	ui := jsonOf(t, b.do("GET", "/get/ui_settings", nil, nil))
	values := ui["values"].(map[string]any)
	if values["enabled_ui_pages_internal_users"] != nil || values["disable_model_add_for_internal_users"] != false ||
		ui["field_schema"].(map[string]any)["properties"].(map[string]any)["enabled_ui_pages_internal_users"] == nil {
		t.Errorf("ui_settings = %v", ui)
	}
	if theme := jsonOf(t, b.do("GET", "/get/ui_theme_settings", nil, nil)); theme["values"].(map[string]any)["logo_url"] != nil {
		t.Errorf("theme = %v", theme)
	}
	if rec := b.do("GET", "/get_image", nil, nil); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("로고: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	// 모델 추가 폼: 우리가 중계하는 제공자만, 항목마다 credential_fields 가 있어야 화면이 깨지지 않는다
	var providers []map[string]any
	_ = json.Unmarshal(b.do("GET", "/public/providers/fields", nil, nil).Body.Bytes(), &providers)
	if len(providers) != 3 || providers[0]["litellm_provider"] != "ollama" || providers[0]["credential_fields"] == nil {
		t.Errorf("제공자 = %v", providers)
	}
	if cost := jsonOf(t, b.do("GET", "/public/litellm_model_cost_map?catalog_only=true", nil, nil)); cost["ollama/llama3"] == nil {
		t.Errorf("가격표에 ollama 모델이 없다")
	}

	// 관리자가 바꾼 값만 저장되고 기본값과 합쳐 나온다
	updated := api.ok("PATCH", "/update/internal_user_settings", map[string]any{"user_role": "internal_user", "models": []string{"m1"}})
	if updated["status"] != "success" || updated["settings"].(map[string]any)["user_role"] != "internal_user" {
		t.Errorf("update = %v", updated)
	}
	got := api.ok("GET", "/get/internal_user_settings", nil)["values"].(map[string]any)
	if got["user_role"] != "internal_user" || got["max_budget"] != nil || len(got["models"].([]any)) != 1 {
		t.Errorf("internal_user_settings = %v", got)
	}
	if status, _ := api.do("PATCH", "/update/default_team_settings", map[string]any{"nope": 1}); status != 400 {
		t.Errorf("모르는 설정: %d", status)
	}
	api.ok("PATCH", "/update/ui_settings", map[string]any{"enabled_ui_pages_internal_users": []string{"api-keys", "usage"}})
	ui = jsonOf(t, b.do("GET", "/get/ui_settings", nil, nil))
	if pages := ui["values"].(map[string]any)["enabled_ui_pages_internal_users"].([]any); len(pages) != 2 || ui["source"].(map[string]any)["enabled_ui_pages_internal_users"] != "db" {
		t.Errorf("ui_settings 저장 = %v", ui)
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
