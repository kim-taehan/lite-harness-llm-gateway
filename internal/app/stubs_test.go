package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

// 비거나 모양이 틀리면 화면이 깨지는 응답(M 문서 2.2). want 는 응답이 담아야 하는 부분 모양이다:
// 객체는 적은 키만 보고, 배열은 통째로 같아야 한다(맨 배열 [] 인지 객체인지가 화면을 가른다)
func TestStubs_CrashSensitiveShapes(t *testing.T) {
	b := browser{t: t, h: newAPI(t).h}
	master := map[string]string{"Authorization": "Bearer " + testMasterKey}
	notSupported := `{"detail":{}}`
	cases := []struct {
		method, path string
		status       int
		want         string
	}{
		{"GET", "/telemetry/settings", 200, `{"environment_variables":[],"vetoed":true,"groups":[],"available_destinations":[]}`},
		{"GET", "/get/config/callbacks", 200, `{"callbacks":[],"available_callbacks":{},"alerts":[],"router_settings":{}}`},
		{"GET", "/management/v1/budgets", 200, `{"data":[],"meta":{"total_count":0}}`},
		{"GET", "/v1/mcp/server", 200, `[]`},
		{"GET", "/v1/mcp/server/health?include_reachability=true", 200, `[]`},
		{"GET", "/v1/mcp/toolset", 200, `[]`},
		{"GET", "/v1/access_group", 200, `[]`},
		{"GET", "/project/list", 200, `[]`},
		{"GET", "/budget/list", 200, `[]`},
		{"GET", "/v1/agents", 200, `[]`},
		{"GET", "/public/agents/fields", 200, `[]`},
		{"GET", "/customer/list", 200, `[]`},
		{"GET", "/global/spend/provider", 200, `[]`},
		{"POST", "/global/spend/end_users", 200, `[]`},
		{"GET", "/config/list?config_type=general_settings", 200, `[]`},
		{"GET", "/alerting/settings", 200, `[]`},
		{"GET", "/policy/templates", 200, `[]`},
		{"GET", "/callbacks/configs", 200, `[]`},
		{"GET", "/organization/list", 200, `[]`},
		{"GET", "/v1/mcp/user-env-vars/status", 200, `[]`},
		{"GET", "/v1/mcp/user-credentials", 200, `[]`},
		{"GET", "/tag/list", 200, `{}`},
		{"GET", "/prompts/list", 200, `{"prompts":[]}`},
		{"GET", "/claude-code/plugins", 200, `{"plugins":[]}`},
		{"GET", "/guardrails/list", 200, `{"guardrails":[]}`},
		{"GET", "/v2/guardrails/list", 200, `{"guardrails":[]}`},
		{"GET", "/model/cost_map/source", 404, notSupported},
		{"GET", "/gateway/errors/activity", 200, `{"by_date":[],"by_status_code":[],"by_key":[],"by_team":[],"by_user":[],"by_model":[]}`},
		{"GET", "/global/activity/cache_hits", 200, `{"filter_options":{"key_aliases":[],"models":[]},"groups":[],"error_breakdown":[],"totals":{"cache_hits":0}}`},
		{"GET", "/auto_router/benchmarks", 404, notSupported},
		{"GET", "/lens", 200, `{"lenses":[]}`},
		{"GET", "/tag/dau", 200, `{"results":[]}`},
		{"GET", "/tag/user-agent/per-user-analytics", 200, `{"results":[]}`},
		{"GET", "/v1/mcp/server/submissions", 200, `{"items":[]}`},
		{"GET", "/v1/mcp/sessions", 200, `{"total_sessions":0,"by_client":[],"sessions":[]}`},
		{"GET", "/guardrails/submissions", 200, `{"summary":{"total":0}}`},
		{"GET", "/get/sso_settings", 200, `{"values":{}}`},
		{"GET", "/coordination_redis/settings", 200, `{"values":{}}`},
		{"GET", "/email/event_settings", 200, `{"settings":[]}`},
		{"GET", "/model-insights?metric=spend", 200, `{"top_models":[],"daily":[],"daily_totals":[]}`},
		{"GET", "/get/latest_release_info", 404, notSupported},
		{"GET", "/get/user_banner", 200, `{"enabled":false,"message":""}`},
		{"GET", "/tag/daily/activity/aggregated", 200, `{"results":[],"metadata":{}}`},
		{"POST", "/reload/model_cost_map", 501, `{"status":"error"}`},
		{"POST", "/auto_router/availability", 200, `{"allowances":[],"error":null}`},
		{"POST", "/budget/new", 501, notSupported},
	}
	for _, c := range cases {
		rec := b.do(c.method, c.path, nil, master)
		if rec.Code != c.status || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s %s: %d %q %s", c.method, c.path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
			continue
		}
		var got, want any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Errorf("%s %s: JSON 이 아니다: %q", c.method, c.path, rec.Body.String())
			continue
		}
		_ = json.Unmarshal([]byte(c.want), &want) // want 는 이 파일의 리터럴이다
		if !containsShape(got, want) {
			t.Errorf("%s %s: %s 에 %s 가 없다", c.method, c.path, rec.Body.String(), c.want)
		}
	}
}

// 공개 경로는 로그인 전 화면이 인증 없이 부른다
func TestStubs_PublicWithoutAuth(t *testing.T) {
	b := browser{t: t, h: newAPI(t).h}
	for _, path := range []string{"/public/model_hub", "/public/agents/fields", "/telemetry/ui_events/enabled", "/openapi.json"} {
		if rec := b.do("GET", path, nil, nil); rec.Code != 200 {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := b.do("GET", "/get/latest_release_info", nil, nil); rec.Code != 404 {
		t.Errorf("latest_release_info: %d", rec.Code)
	}
}

// 모든 GET 라우트(빈 응답 라우트 포함)는 JSON 으로 답한다. 이미지·SSO 브라우저 흐름·CSV 내보내기만 뺀다
func TestStubs_EveryGETAnswersJSON(t *testing.T) {
	db, _ := dbtest.Open(t)
	cfg, _ := LoadConfig(testEnv)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := newIdentity(db)
	usage := startUsage(t, db, logger)
	routes := assemble(cfg, parts{lifecycle: &core.Lifecycle{}, db: db, id: id, login: testLogin(t, cfg, id), usage: usage, logger: logger}, usage).routes
	b := browser{t: t, h: newAPI(t).h}
	master := map[string]string{"Authorization": "Bearer " + testMasterKey}
	wildcard := regexp.MustCompile(`\{[^}]+\}`)
	for _, r := range routes {
		if r.Method != http.MethodGet || nonJSONRoute(r.Path) {
			continue
		}
		path := wildcard.ReplaceAllString(r.Path, "x")
		rec := b.do("GET", path, nil, master)
		// 사용량 필터는 litellm 처럼 application/problem+json 으로 답한다
		if ct := rec.Header().Get("Content-Type"); !strings.HasSuffix(ct, "json") || !json.Valid(rec.Body.Bytes()) {
			t.Errorf("GET %s: %d %q %.200s", path, rec.Code, ct, rec.Body.String())
		}
	}
}

func nonJSONRoute(path string) bool {
	return path == "/get_image" || path == "/get_favicon" || strings.HasPrefix(path, "/sso/") ||
		path == "/user/daily/activity/export" || path == "/team/daily/activity/export"
}

// containsShape 는 got 이 want 의 키를 모두 같은 모양으로 담는지다. 배열은 통째로 비교한다
func containsShape(got, want any) bool {
	wantMap, isMap := want.(map[string]any)
	if !isMap {
		return reflect.DeepEqual(got, want)
	}
	gotMap, isMap := got.(map[string]any)
	if !isMap {
		return false
	}
	for k, w := range wantMap {
		g, present := gotMap[k]
		if !present || !containsShape(g, w) {
			return false
		}
	}
	return true
}
