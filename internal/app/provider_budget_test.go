package app

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// 프로바이더 예산: 예산을 다 쓴 프로바이더의 배포는 후보에서 빠지고, 후보가 없으면 429 다

func addDeployment(api apiClient, name, upstreamModel, base string, priced bool) {
	params := map[string]any{"model": upstreamModel, "api_base": base}
	if priced {
		params["input_cost_per_token"], params["output_cost_per_token"] = 0.001, 0.001
	}
	api.ok("POST", "/model/new", map[string]any{"model_name": name, "litellm_params": params})
}

func TestProviderBudget_ManagementAPIMatchesLitellmShape(t *testing.T) {
	api := newAPI(t)
	if got := api.ok("GET", "/provider/budgets", nil)["providers"].(map[string]any); len(got) != 0 {
		t.Fatalf("처음 = %v", got)
	}
	set := api.ok("POST", "/provider/budgets", map[string]any{"provider": "ollama", "budget_limit": 100, "time_period": "1d"})
	if set["provider"] != "ollama" || set["budget"].(map[string]any)["budget_limit"] != 100.0 {
		t.Errorf("설정 응답 = %v", set)
	}
	got := api.ok("GET", "/provider/budgets", nil)["providers"].(map[string]any)["ollama"].(map[string]any)
	if got["budget_limit"] != 100.0 || got["time_period"] != "1d" || got["spend"] != 0.0 || got["budget_reset_at"] == nil {
		t.Errorf("조회 = %v (litellm: budget_limit·time_period·spend·budget_reset_at)", got)
	}
	if status, _ := api.do("POST", "/provider/budgets", map[string]any{"provider": "x", "budget_limit": 1, "time_period": "soon"}); status != http.StatusBadRequest {
		t.Errorf("틀린 주기 = %d, 400", status)
	}
	if status, _ := api.do("POST", "/provider/budgets", map[string]any{"budget_limit": 1}); status != http.StatusUnprocessableEntity {
		t.Errorf("provider 없음 = %d, 422", status)
	}
	api.ok("DELETE", "/provider/budgets/ollama", nil)
	if status, _ := api.do("DELETE", "/provider/budgets/ollama", nil); status != http.StatusNotFound {
		t.Errorf("두 번 지우기 = %d, 404", status)
	}
	if got := api.ok("GET", "/provider/budgets", nil)["providers"].(map[string]any); len(got) != 0 {
		t.Errorf("지운 뒤 = %v", got)
	}
}

func TestProviderBudget_ExhaustedProviderIsSkippedAndNoCandidateIs429(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	addDeployment(api, "solo", "ollama/x", upstream.URL, true)
	addDeployment(api, "dual", "ollama/x", upstream.URL, true)
	addDeployment(api, "dual", "hosted_vllm/y", upstream.URL, false)
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)
	api.ok("POST", "/provider/budgets", map[string]any{"provider": "ollama", "budget_limit": 0.001, "time_period": "1d"})

	ask := func(model string) int {
		return chatWithKey(api, key, model).Code
	}
	if code := ask("solo"); code != http.StatusOK {
		t.Fatalf("첫 요청 = %d", code)
	}
	// 사용액은 응답 뒤에 비동기로 쌓인다. ollama 가 예산을 다 쓰면 solo 는 후보가 없어 429 가 된다
	code := ask("solo")
	for deadline := time.Now().Add(3 * time.Second); code == http.StatusOK && time.Now().Before(deadline); {
		<-time.After(20 * time.Millisecond)
		code = ask("solo")
	}
	if code != http.StatusTooManyRequests {
		t.Fatalf("프로바이더 예산을 넘었는데 solo = %d, 429 여야 한다", code)
	}
	rec := chatWithKey(api, key, "solo")
	if rec.Header().Get("retry-after") == "" || !strings.Contains(rec.Body.String(), "Provider budget exceeded for ['ollama']") {
		t.Errorf("429 본문·헤더 = %v %s", rec.Header(), rec.Body.String())
	}
	// 다른 프로바이더 배포가 있는 모델은 그쪽으로만 간다
	for i := 0; i < 8; i++ {
		if code := ask("dual"); code != http.StatusOK {
			t.Fatalf("dual %d 번째 = %d, hosted_vllm 배포로 통과해야 한다", i+1, code)
		}
	}
	// 없는 모델은 예산과 무관하게 지금처럼 400 이다
	if code := ask("nope"); code != http.StatusBadRequest {
		t.Errorf("없는 모델 = %d, 400", code)
	}
	// 예산을 지우면 다시 된다
	api.ok("DELETE", "/provider/budgets/ollama", nil)
	if code := ask("solo"); code != http.StatusOK {
		t.Errorf("예산을 지웠는데 solo = %d", code)
	}
}

// 예산을 정하지 않은 프로바이더는 사용액 행을 만들지 않는다(모든 프로바이더가 아니라 예산을 둔 것만 센다)
func TestProviderBudget_OnlyBudgetedProvidersAccrueSpend(t *testing.T) {
	upstream, _ := fakeOllama(t)
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	addDeployment(api, "m1", "ollama/x", upstream.URL, true)
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)
	for i := 0; i < 3; i++ {
		if code := chatWithKey(api, key, "m1").Code; code != http.StatusOK {
			t.Fatalf("요청 = %d", code)
		}
	}
	<-time.After(300 * time.Millisecond) // 사용량 기록이 쓰일 시간
	var rows int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM limit_policy WHERE scope = 'provider'").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("예산을 정하지 않았는데 프로바이더 행이 %d 개 생겼다", rows)
	}
}
