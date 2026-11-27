package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 고객(end user): 요청의 user 값으로 식별한다. 관리 API 로 만들고, 예산·차단이 그 고객의 요청에만 걸린다

func chatAsCustomer(api apiClient, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	api.h.ServeHTTP(rec, req)
	return rec
}

func chatBody(user string) string {
	extra := ""
	if user != "" {
		extra = `"user":"` + user + `",`
	}
	return `{` + extra + `"model":"m1","messages":[{"role":"user","content":"hi"}]}`
}

func TestCustomer_ManagementAPILifecycle(t *testing.T) {
	api := newAPI(t)
	created := api.ok("POST", "/customer/new", map[string]any{"user_id": "acme-1", "alias": "Acme", "max_budget": 5, "budget_duration": "30d", "tpd_limit": 1000})
	table := created["litellm_budget_table"].(map[string]any)
	if created["user_id"] != "acme-1" || created["alias"] != "Acme" || created["blocked"] != false ||
		table["max_budget"] != 5.0 || table["budget_duration"] != "30d" || table["tpd_limit"] != 1000.0 {
		t.Errorf("생성 응답 = %v", created)
	}
	if status, _ := api.do("POST", "/customer/new", map[string]any{"user_id": "acme-1"}); status != http.StatusBadRequest {
		t.Errorf("같은 고객을 또 만들면 %d, 400 이어야 한다", status)
	}
	if status, _ := api.do("POST", "/customer/new", map[string]any{"alias": "no id"}); status != http.StatusUnprocessableEntity {
		t.Errorf("user_id 없음 = %d, 422 여야 한다", status)
	}

	api.ok("POST", "/customer/update", map[string]any{"user_id": "acme-1", "alias": "Acme Corp", "max_budget": 9})
	info := api.ok("GET", "/customer/info?end_user_id=acme-1", nil)
	if info["alias"] != "Acme Corp" || info["litellm_budget_table"].(map[string]any)["max_budget"] != 9.0 ||
		info["litellm_budget_table"].(map[string]any)["tpd_limit"] != 1000.0 {
		t.Errorf("수정 뒤 info = %v", info)
	}
	if status, _ := api.do("GET", "/customer/info?end_user_id=nobody", nil); status != http.StatusBadRequest {
		t.Errorf("없는 고객 info = %d", status)
	}
	if status, _ := api.do("POST", "/customer/update", map[string]any{"user_id": "nobody"}); status != http.StatusBadRequest {
		t.Errorf("없는 고객 수정 = %d", status)
	}

	status, list := api.do("GET", "/customer/list", nil)
	if status != http.StatusOK || len(list.([]any)) != 1 {
		t.Errorf("목록 = %d %v", status, list)
	}
	deleted := api.ok("POST", "/customer/delete", map[string]any{"user_ids": []string{"acme-1"}})
	if deleted["deleted_customers"] != 1.0 {
		t.Errorf("삭제 응답 = %v", deleted)
	}
	if status, list := api.do("GET", "/customer/list", nil); status != http.StatusOK || len(list.([]any)) != 0 {
		t.Errorf("삭제 뒤 목록 = %d %v", status, list)
	}
	// 지운 고객의 한도도 함께 사라져, 같은 id 로 다시 만들면 깨끗하게 시작한다
	again := api.ok("POST", "/customer/new", map[string]any{"user_id": "acme-1"})
	if again["litellm_budget_table"].(map[string]any)["max_budget"] != nil {
		t.Errorf("다시 만든 고객이 옛 예산을 물려받았다: %v", again)
	}
}

func TestCustomer_BlockAndBudgetApplyOnlyToThatEndUser(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL, "input_cost_per_token": 0.001, "output_cost_per_token": 0.001}})
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)
	api.ok("POST", "/customer/new", map[string]any{"user_id": "acme", "max_budget": 0.001})

	if rec := chatAsCustomer(api, key, chatBody("acme")); rec.Code != http.StatusOK {
		t.Fatalf("첫 요청 = %d %s", rec.Code, rec.Body.String())
	}
	rec := chatAsCustomer(api, key, chatBody("acme"))
	for deadline := time.Now().Add(3 * time.Second); rec.Code == http.StatusOK && time.Now().Before(deadline); {
		<-time.After(20 * time.Millisecond)
		rec = chatAsCustomer(api, key, chatBody("acme"))
	}
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "End User=acme") {
		t.Fatalf("고객 예산을 넘었는데 %d %s", rec.Code, rec.Body.String())
	}
	for name, body := range map[string]string{"다른 고객": chatBody("other"), "user 없음": chatBody("")} {
		if rec := chatAsCustomer(api, key, body); rec.Code != http.StatusOK {
			t.Errorf("%s 가 막혔다: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// 차단: 아직 만들지 않은 고객도 미리 막을 수 있고, 풀면 다시 된다
	blocked := api.ok("POST", "/customer/block", map[string]any{"user_ids": []string{"late"}})
	if len(blocked["blocked_users"].([]any)) != 1 {
		t.Errorf("block 응답 = %v", blocked)
	}
	rec = chatAsCustomer(api, key, chatBody("late"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "User blocked from making LLM API Calls. User=late") {
		t.Errorf("차단한 고객 = %d %s", rec.Code, rec.Body.String())
	}
	api.ok("POST", "/customer/unblock", map[string]any{"user_ids": []string{"late"}})
	if rec := chatAsCustomer(api, key, chatBody("late")); rec.Code != http.StatusOK {
		t.Errorf("차단을 풀었는데 %d %s", rec.Code, rec.Body.String())
	}
}

func TestCustomer_ManagementAPIIsAdminOnly(t *testing.T) {
	api := newAPI(t)
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)
	req := httptest.NewRequest("GET", "/customer/list", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	api.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Errorf("일반 키로 고객 목록 = %d, 거절이어야 한다", rec.Code)
	}
}
