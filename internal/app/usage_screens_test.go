package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

// 대시보드 사용량·로그 화면(N 문서)을 대시보드가 보내는 쿼리 그대로 확인한다

func TestUsageScreens_AggregatedKeysUsersExportAndLogs(t *testing.T) {
	upstream, _ := fakeOllama(t)
	cfg, _ := LoadConfig(testEnv)
	db, _ := dbtest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := newIdentity(db)
	h := Handler(cfg, &core.Lifecycle{}, db, id, testLogin(t, cfg, id), startUsage(t, db, logger), logger)
	api := apiClient{t: t, h: h}
	b := browser{t: t, h: h}

	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	api.ok("POST", "/user/new", map[string]any{"user_id": "kim", "user_email": "kim@corp.local", "user_role": "internal_user"})
	api.ok("POST", "/user/new", map[string]any{"user_id": "lee", "user_role": "internal_user"})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "team_alias": "팀1"})
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "t1", "member": map[string]any{"user_id": "kim", "role": "user"}})
	kimKey := api.ok("POST", "/key/generate", map[string]any{"user_id": "kim", "team_id": "t1", "key_alias": "kim-work"})
	leeKey := api.ok("POST", "/key/generate", map[string]any{"user_id": "lee"})
	chat := func(key string) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+key)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	chat(kimKey["key"].(string))
	chat(kimKey["key"].(string))
	chat(leeKey["key"].(string))

	today := time.Now().UTC().Format(time.DateOnly)
	dates := "start_date=" + today + "&end_date=" + today
	master := map[string]string{"Authorization": "Bearer " + testMasterKey}
	kim := map[string]string{"Authorization": "Bearer " + kimKey["key"].(string)}

	var agg map[string]any
	for deadline := time.Now().Add(3 * time.Second); ; {
		agg = jsonOf(t, b.do("GET", "/user/daily/activity/aggregated?"+dates+"&timezone=-540", nil, master))
		if len(agg["results"].([]any)) > 0 || time.Now().After(deadline) {
			break
		}
		<-time.After(20 * time.Millisecond)
	}
	day := agg["results"].([]any)[0].(map[string]any)
	breakdown := day["breakdown"].(map[string]any)
	kimToken := kimKey["token"].(string)
	keyMeta := breakdown["api_keys"].(map[string]any)[kimToken].(map[string]any)["metadata"].(map[string]any)
	meta := agg["metadata"].(map[string]any)
	if day["metrics"].(map[string]any)["api_requests"] != 3.0 || breakdown["model_groups"].(map[string]any)["m1"] == nil ||
		breakdown["providers"].(map[string]any)["ollama"] == nil || breakdown["models"].(map[string]any)["x"] == nil ||
		keyMeta["key_alias"] != "kim-work" || keyMeta["user_email"] != "kim@corp.local" || keyMeta["key_exists"] != true ||
		meta["total_api_requests"] != 3.0 || meta["total_api_keys"] != 2.0 || meta["total_spend"] == nil {
		t.Fatalf("aggregated = %v", agg)
	}

	// 응답 시간 합계는 화면이 필수로 읽고, 이름이 겹친 키(total_total_tokens)는 없어야 한다
	if meta["total_response_time_ms"] == nil || meta["total_timed_requests"] != 3.0 || meta["total_tokens"] == nil || meta["total_total_tokens"] != nil {
		t.Errorf("metadata 합계 키 = %v", meta)
	}
	// 예산을 다 쓴 키의 거절 문구는 키 별명을 보인다
	budgetKey := api.ok("POST", "/key/generate", map[string]any{"user_id": "lee", "key_alias": "tight-budget", "max_budget": 0})
	over := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`))
	over.Header.Set("Authorization", "Bearer "+budgetKey["key"].(string))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, over)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Key=tight-budget") {
		t.Errorf("예산 초과 문구: %d %s", rec.Code, rec.Body.String())
	}

	// 비관리자는 자기 것만, 남의 것은 403
	if mine := jsonOf(t, b.do("GET", "/user/daily/activity/aggregated?"+dates+"&user_id=kim", nil, kim)); mine["metadata"].(map[string]any)["total_api_requests"] != 2.0 {
		t.Errorf("내 사용량 = %v", mine["metadata"])
	}
	if rec := b.do("GET", "/user/daily/activity/aggregated?"+dates+"&user_id=lee", nil, kim); rec.Code != 403 {
		t.Errorf("남의 사용량: %d", rec.Code)
	}

	team := jsonOf(t, b.do("GET", "/team/daily/activity/aggregated?"+dates+"&team_ids=t1&exclude_team_ids=litellm-dashboard", nil, master))
	entity := team["results"].([]any)[0].(map[string]any)["breakdown"].(map[string]any)["entities"].(map[string]any)["t1"].(map[string]any)
	if entity["metadata"].(map[string]any)["team_alias"] != "팀1" || entity["metrics"].(map[string]any)["api_requests"] != 2.0 {
		t.Errorf("팀 사용량 = %v", team)
	}
	// 팀 일반 멤버는 팀 전체 권한이 없어 자기 키 사용량만 본다
	if own := jsonOf(t, b.do("GET", "/team/daily/activity/aggregated?"+dates+"&team_ids=t1", nil, kim)); own["metadata"].(map[string]any)["total_api_requests"] != 2.0 {
		t.Errorf("팀 멤버의 팀 사용량 = %v", own["metadata"])
	}

	keys := jsonOf(t, b.do("GET", "/user/daily/activity/aggregated/keys?"+dates+"&offset=0&limit=50", nil, master))
	if keys["total_api_keys"] != 2.0 || keys["api_keys"].([]any)[0].(map[string]any)["metrics"].(map[string]any)["api_requests"] == nil {
		t.Errorf("keys = %v", keys)
	}
	if found := jsonOf(t, b.do("GET", "/user/daily/activity/aggregated/search?"+dates+"&search=KIM-W", nil, master)); len(found["api_keys"].([]any)) != 1 {
		t.Errorf("search = %v", found)
	}
	if top := jsonOf(t, b.do("GET", "/user/daily/activity/aggregated/model_top_keys?"+dates+"&model_group=m1", nil, master)); len(top["api_keys"].([]any)) != 2 {
		t.Errorf("model_top_keys = %v", top)
	}
	users := jsonOf(t, b.do("GET", "/user/daily/activity/aggregated/users?"+dates, nil, master))
	if users["total_users"] != 2.0 || users["users"].([]any)[0].(map[string]any)["user_id"] != "kim" {
		t.Errorf("users = %v", users)
	}
	csv := b.do("GET", "/user/daily/activity/export?"+dates+"&export_type=daily_with_keys&format=csv", nil, master)
	if !strings.HasPrefix(csv.Body.String(), "date,entity_id,entity_alias,api_key,key_alias") || !strings.Contains(csv.Body.String(), "kim-work") {
		t.Errorf("export = %s", csv.Body.String())
	}
	byUser := jsonOf(t, b.do("GET", "/team/spend/by_user?"+dates+"&team_ids=t1", nil, master))
	if rows := byUser["results"].([]any); len(rows) != 1 || rows[0].(map[string]any)["user_email"] != "kim@corp.local" {
		t.Errorf("team/spend/by_user = %v", byUser)
	}

	// 로그 화면: 표는 늘 group_by_session=true·startTime 정렬로 부른다
	now := time.Now().UTC()
	window := "start_date=" + strings.ReplaceAll(now.Add(-time.Hour).Format("2006-01-02 15:04:05"), " ", "%20") +
		"&end_date=" + strings.ReplaceAll(now.Add(time.Minute).Format("2006-01-02 15:04:05"), " ", "%20")
	logs := jsonOf(t, b.do("GET", "/spend/logs/ui?"+window+"&page=1&page_size=2&sort_by=startTime&sort_order=desc&group_by_session=true", nil, master))
	rows := logs["data"].([]any)
	first := rows[0].(map[string]any)
	if logs["total"] != 3.0 || len(rows) != 2 || logs["has_more"] != true || logs["next_session_cursor"] != "2" ||
		first["cache_hit"] != "" || !strings.HasSuffix(first["startTime"].(string), "Z") || first["metadata"].(map[string]any)["status"] != "success" {
		t.Fatalf("logs = %v", logs)
	}
	next := jsonOf(t, b.do("GET", "/spend/logs/ui?"+window+"&page=2&page_size=2&group_by_session=true&session_cursor=2", nil, master))
	if len(next["data"].([]any)) != 1 || next["has_more"] != false {
		t.Errorf("다음 쪽 = %v", next)
	}
	if mine := jsonOf(t, b.do("GET", "/spend/logs/ui?"+window, nil, kim)); mine["total"] != 2.0 {
		t.Errorf("내 로그 = %v", mine["total"])
	}
	if rec := b.do("GET", "/spend/logs/ui?"+window+"&team_id=t1", nil, kim); rec.Code != 403 {
		t.Errorf("권한 없는 팀 로그: %d", rec.Code)
	}
	if none := jsonOf(t, b.do("GET", "/spend/logs/ui?"+window+"&end_user=x", nil, master)); none["total"] != 0.0 {
		t.Errorf("기록하지 않는 칸으로 거르면 빈 결과여야 한다: %v", none["total"])
	}
	detail := b.do("GET", "/spend/logs/ui/"+first["request_id"].(string), nil, master)
	var body map[string]any
	if err := json.Unmarshal(detail.Body.Bytes(), &body); err != nil || body["messages"] == nil {
		t.Errorf("로그 상세 = %s", detail.Body.String())
	}
	facet := jsonOf(t, b.do("GET", "/management/v1/spend_logs/users?filter[startTime][gte]="+strings.ReplaceAll(now.Add(-time.Hour).Format("2006-01-02 15:04:05"), " ", "%20")+
		"&filter[startTime][lte]="+strings.ReplaceAll(now.Add(time.Minute).Format("2006-01-02 15:04:05"), " ", "%20")+"&page_size=25", nil, master))
	if ids := facet["data"].([]any); len(ids) != 2 || ids[0] != "kim" || facet["links"] == nil {
		t.Errorf("사용자 필터 = %v", facet)
	}
}
