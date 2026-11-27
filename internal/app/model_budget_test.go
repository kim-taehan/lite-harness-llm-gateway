package app

import (
	"net/http"
	"testing"
	"time"
)

// 모델별 한도(model_max_budget): 키·팀에 정하면 그 모델 요청에만 걸리고, 사용량이 모델별로 쌓여 예산을 넘으면 막힌다

func TestModelMaxBudget_KeyBudgetBlocksOnlyThatModelAfterRealUsage(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	for _, name := range []string{"m1", "m2"} {
		api.ok("POST", "/model/new", map[string]any{"model_name": name,
			"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL, "input_cost_per_token": 0.001, "output_cost_per_token": 0.001}})
	}
	generated := api.ok("POST", "/key/generate", map[string]any{
		"model_max_budget": map[string]any{"m1": map[string]any{"max_budget": 0.001, "budget_duration": "1d"}}})
	key := generated["key"].(string)
	got := generated["model_max_budget"].(map[string]any)["m1"].(map[string]any)
	if got["max_budget"] != 0.001 || got["budget_duration"] != "1d" || got["rpm_limit"] != nil {
		t.Errorf("생성 응답의 model_max_budget = %v", generated["model_max_budget"])
	}

	if rec := chatWithKey(api, key, "m1"); rec.Code != http.StatusOK {
		t.Fatalf("첫 요청 = %d %s", rec.Code, rec.Body.String())
	}
	// 사용액은 응답 뒤에 비동기로 쌓인다
	rec := chatWithKey(api, key, "m1")
	for deadline := time.Now().Add(3 * time.Second); rec.Code == http.StatusOK && time.Now().Before(deadline); {
		<-time.After(20 * time.Millisecond)
		rec = chatWithKey(api, key, "m1")
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("모델 예산을 넘었는데 %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatWithKey(api, key, "m2"); rec.Code != http.StatusOK {
		t.Errorf("한도 없는 모델 m2 가 막혔다: %d %s", rec.Code, rec.Body.String())
	}

	// 키 정보에 보이고, 수정으로 지우면 풀린다
	info := api.ok("GET", "/key/info?key="+key, nil)["info"].(map[string]any)
	if len(info["model_max_budget"].(map[string]any)) != 1 {
		t.Errorf("key/info 의 model_max_budget = %v", info["model_max_budget"])
	}
	api.ok("POST", "/key/update", map[string]any{"key": key, "model_max_budget": map[string]any{}})
	if rec := chatWithKey(api, key, "m1"); rec.Code != http.StatusOK {
		t.Errorf("모델 한도를 지웠는데 %d %s", rec.Code, rec.Body.String())
	}
}

func TestModelMaxBudget_TeamRPMAppliesToTeamKeysAndBadValuesAreRejected(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	team := api.ok("POST", "/team/new", map[string]any{"team_id": "t1",
		"model_max_budget": map[string]any{"m1": map[string]any{"rpm_limit": 1}}})
	if team["model_max_budget"].(map[string]any)["m1"].(map[string]any)["rpm_limit"] != 1.0 {
		t.Errorf("팀 응답 = %v", team["model_max_budget"])
	}
	key := api.ok("POST", "/key/generate", map[string]any{"team_id": "t1"})["key"].(string)

	if rec := chatWithKey(api, key, "m1"); rec.Code != http.StatusOK {
		t.Fatalf("첫 요청 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatWithKey(api, key, "m1"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("팀 모델 RPM 을 넘었는데 %d", rec.Code)
	}

	status, _ := api.do("POST", "/key/generate", map[string]any{"model_max_budget": map[string]any{"m1": map[string]any{"max_budget": -5}}})
	if status != http.StatusBadRequest {
		t.Errorf("음수 모델 예산 = %d, 400 이어야 한다", status)
	}
}
