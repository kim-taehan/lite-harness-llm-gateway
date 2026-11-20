package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// 데스크탑 CLI 로그인(K 2장)을 아이디·비밀번호 경로로 끝까지 확인한다: start → 브라우저 로그인 → 코드 확인 → poll → 키로 API

type browser struct {
	t *testing.T
	h http.Handler
}

func (b browser) do(method, target string, form url.Values, header map[string]string) *httptest.ResponseRecorder {
	b.t.Helper()
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	return rec
}

func jsonOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	return m
}

var (
	formAction = regexp.MustCompile(`<form[^>]*action="([^"]+)"`)
	hiddenTok  = regexp.MustCompile(`name="browser_complete_token" value="([^"]+)"`)
)

// path 는 폼 action(PUBLIC_URL 이 붙은 절대 주소)에서 경로만 뗀다
func path(t *testing.T, action string) string {
	u, err := url.Parse(action)
	if err != nil {
		t.Fatal(err)
	}
	return u.Path
}

func TestCLILogin_PasswordFlowIssuesWorkingLoginKey(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "models": []string{"m1"}})
	api.ok("POST", "/user/new", map[string]any{"user_id": "kim", "user_email": "kim@corp.local", "user_role": "internal_user"})
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "t1", "member": map[string]any{"user_id": "kim", "role": "user"}})
	api.ok("POST", "/user/update", map[string]any{"user_id": "kim", "password": "s3cret-pass"})

	start := jsonOf(t, b.do("POST", "/sso/cli/start", nil, nil))
	loginID, secret, code := start["login_id"].(string), start["poll_secret"].(string), start["user_code"].(string)
	if !strings.HasPrefix(loginID, "cli-") || start["expires_in"] != 600.0 || len(code) != 9 {
		t.Fatalf("start = %v", start)
	}
	poll := func(query string) *httptest.ResponseRecorder {
		return b.do("GET", "/sso/cli/poll/"+loginID+query, nil, map[string]string{"x-litellm-cli-poll-secret": secret})
	}
	if p := jsonOf(t, poll("")); p["status"] != "pending" {
		t.Errorf("로그인 전 poll = %v", p)
	}
	if rec := b.do("GET", "/sso/cli/poll/"+loginID, nil, map[string]string{"x-litellm-cli-poll-secret": "wrong"}); rec.Code != 403 {
		t.Errorf("틀린 비밀: %d", rec.Code)
	}

	// 브라우저: 로그인 화면 → 틀린 비밀번호 → 맞는 비밀번호 → 코드 확인 화면
	page := b.do("GET", "/sso/key/generate?source=litellm-cli&key="+loginID+"&user_code="+code, nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Sign in to LiteLLM") {
		t.Fatalf("로그인 화면: %d %s", page.Code, page.Body.String())
	}
	action := path(t, formAction.FindStringSubmatch(page.Body.String())[1])
	if rec := b.do("POST", action, url.Values{"username": {"kim@corp.local"}, "password": {"wrong"}}, nil); rec.Code != 401 {
		t.Errorf("틀린 비밀번호: %d", rec.Code)
	}
	confirm := b.do("POST", action, url.Values{"username": {"KIM@corp.local"}, "password": {"s3cret-pass"}}, nil)
	if confirm.Code != 200 || !strings.Contains(confirm.Body.String(), "Complete CLI Login") {
		t.Fatalf("확인 화면: %d %s", confirm.Code, confirm.Body.String())
	}
	token := hiddenTok.FindStringSubmatch(confirm.Body.String())[1]
	completeAt := path(t, formAction.FindStringSubmatch(confirm.Body.String())[1])
	if rec := b.do("POST", completeAt, url.Values{"user_code": {"AAAA-AAAA"}, "browser_complete_token": {token}}, nil); rec.Code != 400 {
		t.Errorf("틀린 코드: %d", rec.Code)
	}
	done := b.do("POST", completeAt, url.Values{"user_code": {strings.ToLower(code)}, "browser_complete_token": {token}}, nil)
	if done.Code != 200 || !strings.Contains(done.Body.String(), "Authentication Successful!") {
		t.Fatalf("완료: %d %s", done.Code, done.Body.String())
	}

	// 팀이 하나면 바로 키를 준다. 받은 뒤 흐름은 지워진다(일회성)
	ready := jsonOf(t, poll(""))
	key, _ := ready["key"].(string)
	if ready["status"] != "ready" || !strings.HasPrefix(key, "litellm_login_") || ready["team_id"] != "t1" || ready["user_id"] != "kim" {
		t.Fatalf("poll = %v", ready)
	}
	if rec := poll(""); rec.Code != 400 {
		t.Errorf("키를 준 뒤 poll: %d", rec.Code)
	}

	// 로그인 키로 계정·추론 API 를 부른다
	auth := map[string]string{"Authorization": "Bearer " + key}
	info := jsonOf(t, b.do("GET", "/v2/user/info", nil, auth))
	if info["user_id"] != "kim" || info["user_role"] != "internal_user" {
		t.Errorf("/v2/user/info = %v", info)
	}
	if rec := b.do("GET", "/team/info?team_id=t1", nil, auth); rec.Code != 200 {
		t.Errorf("/team/info: %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.do("GET", "/v1/models", nil, auth); rec.Code != 200 {
		t.Errorf("/v1/models: %d", rec.Code)
	}

	// 팀에서 빠지면 그 로그인 키는 바로 막힌다(요청마다 다시 읽는다)
	api.ok("POST", "/team/member_delete", map[string]any{"team_id": "t1", "user_id": "kim"})
	if rec := b.do("GET", "/v2/user/info", nil, auth); rec.Code != 403 {
		t.Errorf("팀에서 빠진 뒤: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCLILogin_TeamSelectionWhenTwoTeams(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	for _, team := range []string{"a", "b"} {
		api.ok("POST", "/team/new", map[string]any{"team_id": team, "team_alias": "팀 " + team})
		api.ok("POST", "/team/member_add", map[string]any{"team_id": team, "member": map[string]any{"user_id": "lee", "role": "user"}})
	}
	api.ok("POST", "/user/update", map[string]any{"user_id": "lee", "password": "pw-lee-123"})

	start := jsonOf(t, b.do("POST", "/sso/cli/start", nil, nil))
	loginID, secret, code := start["login_id"].(string), start["poll_secret"].(string), start["user_code"].(string)
	page := b.do("GET", "/sso/key/generate?source=litellm-cli&key="+loginID, nil, nil)
	confirm := b.do("POST", path(t, formAction.FindStringSubmatch(page.Body.String())[1]), url.Values{"username": {"lee"}, "password": {"pw-lee-123"}}, nil)
	b.do("POST", path(t, formAction.FindStringSubmatch(confirm.Body.String())[1]),
		url.Values{"user_code": {code}, "browser_complete_token": {hiddenTok.FindStringSubmatch(confirm.Body.String())[1]}}, nil)

	headers := map[string]string{"x-litellm-cli-poll-secret": secret}
	choose := jsonOf(t, b.do("GET", "/sso/cli/poll/"+loginID, nil, headers))
	if choose["requires_team_selection"] != true || choose["key"] != nil || len(choose["teams"].([]any)) != 2 {
		t.Fatalf("팀 선택 요구 = %v", choose)
	}
	if rec := b.do("GET", "/sso/cli/poll/"+loginID+"?team_id=zzz", nil, headers); rec.Code != 403 {
		t.Errorf("속하지 않은 팀: %d", rec.Code)
	}
	if ready := jsonOf(t, b.do("GET", "/sso/cli/poll/"+loginID+"?team_id=b", nil, headers)); ready["team_id"] != "b" || ready["key"] == nil {
		t.Errorf("팀을 고른 poll = %v", ready)
	}
}

func TestCLILogin_SSORoutesAbsentInPasswordOnlyBuild(t *testing.T) {
	b := browser{t: t, h: newAPI(t).h}
	if rec := b.do("GET", "/sso/callback?code=x&state=y", nil, nil); rec.Code != 404 {
		t.Errorf("비밀번호만 넣은 빌드에 SSO 콜백이 있다: %d", rec.Code)
	}
}

func TestCLILogin_TooManyWrongPasswordsAreBlocked(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	api.ok("POST", "/user/new", map[string]any{"user_id": "park"})
	api.ok("POST", "/user/update", map[string]any{"user_id": "park", "password": "right-pass"})
	start := jsonOf(t, b.do("POST", "/sso/cli/start", nil, nil))
	action := "/sso/cli/password/" + start["login_id"].(string)
	for i := 0; i < 10; i++ {
		if rec := b.do("POST", action, url.Values{"username": {"park"}, "password": {"wrong"}}, nil); rec.Code != 401 {
			t.Fatalf("%d 번째 실패: %d", i+1, rec.Code)
		}
	}
	// 맞는 비밀번호도 창이 지날 때까지 막힌다
	if rec := b.do("POST", action, url.Values{"username": {"park"}, "password": {"right-pass"}}, nil); rec.Code != 429 {
		t.Errorf("막히지 않았다: %d", rec.Code)
	}
}
