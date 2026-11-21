package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// 대시보드 모델 화면(M 5장): 모델 표(/v2/model/info), 상세(/v1/model/info), 편집·일시정지(PATCH /model/{id}/update)

func TestModels_DashboardTableEditAndPause(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	created := api.ok("POST", "/model/new", map[string]any{
		"model_name": "m1",
		"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL, "api_key": "secret-should-not-stick",
			"guardrails": []string{"g1"}},
		"model_info": map[string]any{"access_groups": []string{"dev"}, "max_input_tokens": 8192},
	})
	id := created["model_info"].(map[string]any)["id"].(string)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m2", "litellm_params": map[string]any{"model": "ollama/y", "api_base": upstream.URL}})

	table := api.ok("GET", "/v2/model/info?include_team_models=true&page=1&size=1&sortBy=model_name&sortOrder=desc", nil)
	rows := table["data"].([]any)
	if table["total_count"] != 2.0 || table["total_pages"] != 2.0 || len(rows) != 1 || rows[0].(map[string]any)["model_name"] != "m2" {
		t.Fatalf("v2 표 = %v", table)
	}
	one := api.ok("GET", "/v1/model/info?litellm_model_id="+id, nil)["data"].([]any)[0].(map[string]any)
	params, info := one["litellm_params"].(map[string]any), one["model_info"].(map[string]any)
	if params["model"] != "ollama/x" || params["api_key"] != nil || len(params["guardrails"].([]any)) != 1 ||
		info["blocked"] != false || info["max_input_tokens"] != 8192.0 || info["access_groups"].([]any)[0] != "dev" {
		t.Errorf("v1 상세 = %v", one)
	}
	if got := api.ok("GET", "/v2/model/info?access_group=dev", nil); got["total_count"] != 1.0 {
		t.Errorf("access_group 거르기 = %v", got["total_count"])
	}

	// 상세 편집: 화면은 받은 model_info 를 통째로 되돌려 보낸다(id·created_at 등은 저장하지 않는다). 비용은 litellm_params 로 온다
	info["access_groups"] = []string{"dev", "qa"}
	edited := api.ok("PATCH", "/model/"+id+"/update", map[string]any{"model_name": "m1-renamed",
		"litellm_params": map[string]any{"input_cost_per_token": 0.000001, "guardrails": nil}, "model_info": info})
	ei, ep := edited["model_info"].(map[string]any), edited["litellm_params"].(map[string]any)
	if edited["model_name"] != "m1-renamed" || ei["input_cost_per_token"] != 0.000001 || len(ei["access_groups"].([]any)) != 2 ||
		ep["guardrails"] != nil || ep["api_base"] != upstream.URL {
		t.Errorf("편집 = %v", edited)
	}

	// 일시정지: 표에 blocked 로 보이고 추론에서 빠진다
	api.ok("PATCH", "/model/"+id+"/update", map[string]any{"blocked": true})
	if paused := api.ok("GET", "/v2/model/info?blocked=true", nil); paused["total_count"] != 1.0 {
		t.Errorf("일시정지 거르기 = %v", paused)
	}
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m1-renamed","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+testMasterKey)
	rec := httptest.NewRecorder()
	api.h.ServeHTTP(rec, req)
	if rec.Code == 200 {
		t.Errorf("일시정지한 모델로 추론이 됐다: %s", rec.Body.String())
	}
	if status, _ := api.do("PATCH", "/model/nope/update", map[string]any{"blocked": true}); status != 404 {
		t.Errorf("없는 모델: %d", status)
	}
}

func TestModels_HealthChecksCallUpstream(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	id := api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})["model_info"].(map[string]any)["id"].(string)
	down := api.ok("POST", "/model/new", map[string]any{"model_name": "m2", "litellm_params": map[string]any{"model": "ollama/y", "api_base": "http://127.0.0.1:1"}})["model_info"].(map[string]any)["id"].(string)

	if ok := api.ok("GET", "/health?model_id="+id, nil); ok["healthy_count"] != 1.0 || ok["unhealthy_count"] != 0.0 {
		t.Errorf("정상 배포 = %v", ok)
	}
	bad := api.ok("GET", "/health?model_id="+down, nil)
	if bad["unhealthy_count"] != 1.0 || bad["unhealthy_endpoints"].([]any)[0].(map[string]any)["error"] == "" {
		t.Errorf("닿지 않는 배포 = %v", bad)
	}
	if all := api.ok("GET", "/health", nil); all["healthy_count"] != 1.0 || all["unhealthy_count"] != 1.0 {
		t.Errorf("전체 = %v", all)
	}

	conn := func(base string) map[string]any {
		return api.ok("POST", "/health/test_connection", map[string]any{"litellm_params": map[string]any{"model": "ollama/x", "api_base": base}, "mode": "chat"})
	}
	if got := conn(upstream.URL); got["status"] != "success" {
		t.Errorf("연결 시험 성공 = %v", got)
	}
	if got := conn("http://127.0.0.1:1"); got["status"] != "error" || got["result"].(map[string]any)["error"] == "" {
		t.Errorf("연결 시험 실패 = %v", got)
	}
}
