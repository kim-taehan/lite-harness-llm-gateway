package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 태그: 관리 API 로 등록하고 예산·한도를 정하면, 요청이 그 태그를 붙였을 때 걸린다. 등록하지 않은 태그는 아무 영향이 없다

func chatWithTags(api apiClient, key, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	api.h.ServeHTTP(rec, req)
	return rec
}

func tagBody(extra string) string {
	return `{` + extra + `"model":"m1","messages":[{"role":"user","content":"hi"}]}`
}

func TestTag_ManagementAPILifecycleMatchesDashboardContract(t *testing.T) {
	api := newAPI(t)
	if list := api.ok("GET", "/tag/list", nil); len(list) != 0 {
		t.Fatalf("빈 목록 = %v, {} 여야 한다(대시보드가 Object.values 로 읽는다)", list)
	}
	created := api.ok("POST", "/tag/new", map[string]any{"name": "ops", "description": "운영", "models": []string{"m1"},
		"max_budget": 5, "budget_duration": "30d", "rpm_limit": 10})
	tag := created["tag"].(map[string]any)
	table := tag["litellm_budget_table"].(map[string]any)
	if created["message"] != "Tag ops created successfully" || tag["name"] != "ops" || tag["description"] != "운영" ||
		table["max_budget"] != 5.0 || table["budget_duration"] != "30d" || table["rpm_limit"] != 10.0 {
		t.Errorf("생성 응답 = %v", created)
	}
	if status, _ := api.do("POST", "/tag/new", map[string]any{"name": "ops"}); status != http.StatusBadRequest {
		t.Errorf("같은 태그 또 만들기 = %d, 400", status)
	}
	if status, _ := api.do("POST", "/tag/new", map[string]any{"description": "이름 없음"}); status != http.StatusUnprocessableEntity {
		t.Errorf("이름 없음 = %d, 422", status)
	}

	updated := api.ok("POST", "/tag/update", map[string]any{"name": "ops", "description": "운영팀", "max_budget": 9})
	if updated["message"] != "Tag ops updated successfully" {
		t.Errorf("수정 응답 = %v", updated)
	}
	info := api.ok("POST", "/tag/info", map[string]any{"names": []string{"ops"}})["ops"].(map[string]any)
	if info["description"] != "운영팀" || info["litellm_budget_table"].(map[string]any)["max_budget"] != 9.0 ||
		info["litellm_budget_table"].(map[string]any)["rpm_limit"] != 10.0 || len(info["models"].([]any)) != 1 {
		t.Errorf("수정 뒤 info = %v", info)
	}
	status, body := api.do("POST", "/tag/info", map[string]any{"names": []string{"ops", "nope"}})
	if status != http.StatusNotFound || !strings.Contains(body.(map[string]any)["detail"].(string), "Tags not found: ['nope']") {
		t.Errorf("없는 태그 info = %d %v", status, body)
	}
	if status, _ := api.do("POST", "/tag/update", map[string]any{"name": "nope"}); status != http.StatusNotFound {
		t.Errorf("없는 태그 수정 = %d, 404", status)
	}
	if list := api.ok("GET", "/tag/list", nil); len(list) != 1 || list["ops"] == nil {
		t.Errorf("목록 = %v", list)
	}

	api.ok("POST", "/tag/delete", map[string]any{"name": "ops"})
	if status, _ := api.do("POST", "/tag/delete", map[string]any{"name": "ops"}); status != http.StatusNotFound {
		t.Errorf("두 번 지우기 = %d, 404", status)
	}
	again := api.ok("POST", "/tag/new", map[string]any{"name": "ops"})["tag"].(map[string]any)
	if again["litellm_budget_table"] != nil {
		t.Errorf("다시 만든 태그가 옛 한도를 물려받았다: %v", again)
	}
}

func TestTag_BudgetAndRateLimitApplyOnlyToRegisteredTagsOnTheRequest(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL, "input_cost_per_token": 0.001, "output_cost_per_token": 0.001}})
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)
	api.ok("POST", "/tag/new", map[string]any{"name": "pricey", "max_budget": 0.001})
	api.ok("POST", "/tag/new", map[string]any{"name": "slow", "rpm_limit": 1})

	// 본문 metadata.tags 로 붙인 태그의 예산이 사용액으로 차고, 넘으면 422
	if rec := chatWithTags(api, key, tagBody(`"metadata":{"tags":["pricey"]},`), nil); rec.Code != http.StatusOK {
		t.Fatalf("첫 요청 = %d %s", rec.Code, rec.Body.String())
	}
	rec := chatWithTags(api, key, tagBody(`"metadata":{"tags":["pricey"]},`), nil)
	for deadline := time.Now().Add(3 * time.Second); rec.Code == http.StatusOK && time.Now().Before(deadline); {
		<-time.After(20 * time.Millisecond)
		rec = chatWithTags(api, key, tagBody(`"metadata":{"tags":["pricey"]},`), nil)
	}
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Tag=pricey") {
		t.Fatalf("태그 예산을 넘었는데 %d %s", rec.Code, rec.Body.String())
	}
	// 같은 태그를 헤더나 본문 최상위 tags 로 붙여도 걸린다. 태그가 없거나 등록하지 않은 태그면 영향이 없다
	if rec := chatWithTags(api, key, tagBody(``), map[string]string{"x-litellm-tags": "other, pricey"}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("헤더 태그 = %d, 422", rec.Code)
	}
	if rec := chatWithTags(api, key, tagBody(`"tags":["pricey"],`), nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("본문 tags = %d, 422", rec.Code)
	}
	for name, body := range map[string]string{"태그 없음": tagBody(``), "등록하지 않은 태그": tagBody(`"tags":["unknown"],`)} {
		if rec := chatWithTags(api, key, body, nil); rec.Code != http.StatusOK {
			t.Errorf("%s 가 막혔다: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// 태그 RPM: 한 요청이 여러 태그를 붙이면 걸린 태그의 한도로 막힌다
	body := tagBody(`"tags":["slow","unknown"],`)
	if rec := chatWithTags(api, key, body, nil); rec.Code != http.StatusOK {
		t.Fatalf("slow 첫 요청 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatWithTags(api, key, body, nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("slow RPM 초과 = %d, 429", rec.Code)
	}
}

// 고객·태그는 호출자가 요청에 적는 값이다. 등록하지 않은 값으로 한도 행이 늘어나면 안 된다
func TestTagAndEndUser_UnregisteredValuesDoNotCreateLimitRows(t *testing.T) {
	upstream, _ := fakeOllama(t)
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL, "input_cost_per_token": 0.001, "output_cost_per_token": 0.001}})
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)
	api.ok("POST", "/tag/new", map[string]any{"name": "known"})
	api.ok("POST", "/customer/new", map[string]any{"user_id": "known-user"})

	for _, body := range []string{
		tagBody(`"user":"stranger","tags":["stranger-tag"],`),
		tagBody(`"user":"known-user","tags":["known"],`),
	} {
		if rec := chatWithTags(api, key, body, nil); rec.Code != http.StatusOK {
			t.Fatalf("요청 = %d %s", rec.Code, rec.Body.String())
		}
	}
	rows := func() map[string]float64 {
		out := map[string]float64{}
		result, err := pool.Query(t.Context(), "SELECT scope || ':' || target_id, spend::float8 FROM limit_policy WHERE scope IN ('end_user', 'tag')")
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		for result.Next() {
			var id string
			var spend float64
			if err := result.Scan(&id, &spend); err != nil {
				t.Fatal(err)
			}
			out[id] = spend
		}
		return out
	}
	// 사용액은 비동기로 쌓이므로 등록한 두 행이 채워질 때까지 기다린다
	var got map[string]float64
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if got = rows(); got["tag:known"] > 0 && got["end_user:known-user"] > 0 {
			break
		}
		<-time.After(20 * time.Millisecond)
	}
	if len(got) != 2 || got["tag:known"] == 0 || got["end_user:known-user"] == 0 {
		t.Errorf("한도 행 = %v, 등록한 태그·고객 둘만 있어야 한다", got)
	}
}
