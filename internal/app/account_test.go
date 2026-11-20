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

// 데스크탑 계정 화면(K 9장)이 부르는 API 를 데스크탑과 같은 쿼리로 확인한다

func TestAccountScreen_DesktopCallsWork(t *testing.T) {
	upstream, _ := fakeOllama(t)
	cfg, _ := LoadConfig(testEnv)
	db, _ := dbtest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := newIdentity(db)
	h := Handler(cfg, &core.Lifecycle{}, db, id, testLogin(t, cfg, id), startUsage(t, db, logger), logger)
	api := apiClient{t: t, h: h}

	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	api.ok("POST", "/user/new", map[string]any{"user_id": "kim", "user_role": "internal_user"})
	api.ok("POST", "/user/new", map[string]any{"user_id": "viewer", "user_role": "internal_user_viewer"})
	key := api.ok("POST", "/key/generate", map[string]any{"user_id": "kim"})["key"].(string)
	viewerKey := api.ok("POST", "/key/generate", map[string]any{"user_id": "viewer"})["key"].(string)
	call := func(k, path string) (int, []byte) {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+k)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	chat := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`))
	chat.Header.Set("Authorization", "Bearer "+key)
	h.ServeHTTP(httptest.NewRecorder(), chat)

	today := time.Now().UTC().Format(time.DateOnly)
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format(time.DateOnly)
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)

	// 배치가 쓰일 때까지 기다린다
	var activity struct {
		Results []struct {
			Date      string         `json:"date"`
			Metrics   map[string]any `json:"metrics"`
			Breakdown struct {
				ModelGroups map[string]struct {
					Metrics map[string]any `json:"metrics"`
				} `json:"model_groups"`
			} `json:"breakdown"`
		} `json:"results"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, body := call(key, "/user/daily/activity?start_date="+yesterday+"&end_date="+today+"&page_size=1000&user_id=kim")
		_ = json.Unmarshal(body, &activity)
		if len(activity.Results) > 0 || time.Now().After(deadline) {
			break
		}
		<-time.After(20 * time.Millisecond)
	}
	if len(activity.Results) != 1 || activity.Results[0].Date != today || activity.Results[0].Metrics["total_tokens"] != 6.0 ||
		activity.Results[0].Metrics["successful_requests"] != 1.0 || activity.Results[0].Breakdown.ModelGroups["m1"].Metrics["total_tokens"] != 6.0 {
		t.Errorf("daily activity = %+v", activity)
	}
	if code, _ := call(key, "/user/daily/activity?start_date="+today+"&end_date="+today+"&user_id=viewer"); code != 403 {
		t.Errorf("남의 사용량: %d", code)
	}

	code, body := call(key, "/spend/logs?start_date="+yesterday+"&end_date="+tomorrow+"&summarize=false")
	var logs []map[string]any
	if err := json.Unmarshal(body, &logs); err != nil || code != 200 || len(logs) != 1 || logs[0]["user"] != "kim" ||
		logs[0]["status"] != "success" || logs[0]["model_group"] != "m1" || logs[0]["startTime"] == nil {
		t.Errorf("spend logs = %d %s", code, body)
	}
	if code, body := call(viewerKey, "/spend/logs?start_date="+yesterday+"&end_date="+tomorrow+"&summarize=false"); code != 200 || string(body) != "[]\n" {
		t.Errorf("viewer 는 자기 것만(없음): %d %s", code, body)
	}

	if code, body := call(key, "/health/readiness"); code != 200 || !strings.Contains(string(body), `"db":"connected"`) {
		t.Errorf("readiness = %d %s", code, body)
	}
	if code, _ := call(key, "/guardrails/list"); code != 200 {
		t.Errorf("internal_user 의 guardrails: %d", code)
	}
	if code, _ := call(viewerKey, "/guardrails/list"); code != 401 {
		t.Errorf("internal_user_viewer 의 guardrails 는 litellm 처럼 401: %d", code)
	}
	if code, body := call(key, "/v1/mcp/server"); code != 200 || string(body) != "[]\n" {
		t.Errorf("mcp servers = %d %s", code, body)
	}
}
