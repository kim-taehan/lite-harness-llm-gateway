package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 모델 액세스 그룹: 키·팀의 models 에 그룹 이름이 있으면 그 그룹 모델이 허용된다(litellm 모델 액세스 그룹)

func chatWithKey(api apiClient, key, model string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	api.h.ServeHTTP(rec, req)
	return rec
}

func TestAccessGroups_KeyAndTeamModelsAcceptGroupNames(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	for name, group := range map[string]string{"m1": "dev", "m2": "dev", "m3": "prod"} {
		api.ok("POST", "/model/new", map[string]any{"model_name": name,
			"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL},
			"model_info":     map[string]any{"access_groups": []string{group}}})
	}
	key := api.ok("POST", "/key/generate", map[string]any{"models": []string{"dev"}})["key"].(string)

	if rec := chatWithKey(api, key, "m1"); rec.Code != http.StatusOK {
		t.Errorf("그룹 모델 m1 이 거절됐다: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatWithKey(api, key, "m2"); rec.Code != http.StatusOK {
		t.Errorf("그룹 모델 m2 가 거절됐다: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatWithKey(api, key, "m3"); rec.Code != http.StatusForbidden {
		t.Errorf("그룹 밖 모델 m3 = %d, 403 이어야 한다", rec.Code)
	}

	// 팀 목록에 그룹을 넣어도 같다. 키는 팀 목록을 따른다
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "models": []string{"prod"}})
	teamKey := api.ok("POST", "/key/generate", map[string]any{"team_id": "t1", "models": []string{"all-team-models"}})["key"].(string)
	if rec := chatWithKey(api, teamKey, "m3"); rec.Code != http.StatusOK {
		t.Errorf("팀 그룹 모델 m3 가 거절됐다: %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatWithKey(api, teamKey, "m1"); rec.Code != http.StatusForbidden {
		t.Errorf("팀 그룹 밖 모델 m1 = %d, 403 이어야 한다", rec.Code)
	}

	// 모델을 그룹에서 빼면 바로 막힌다(그룹은 배포에서 요청마다 계산한다)
	list := api.ok("GET", "/v2/model/info?model=m2", nil)["data"].([]any)
	id := list[0].(map[string]any)["model_info"].(map[string]any)["id"].(string)
	api.ok("PATCH", "/model/"+id+"/update", map[string]any{"model_info": map[string]any{"access_groups": []string{}}})
	if rec := chatWithKey(api, key, "m2"); rec.Code != http.StatusForbidden {
		t.Errorf("그룹에서 뺀 m2 = %d, 403 이어야 한다", rec.Code)
	}
}

func TestAccessGroups_ListInfoAndModelsDropdown(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	for _, name := range []string{"m1", "m2"} {
		api.ok("POST", "/model/new", map[string]any{"model_name": name,
			"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL},
			"model_info":     map[string]any{"access_groups": []string{"dev"}}})
	}
	groups := api.ok("GET", "/access_group/list", nil)["access_groups"].([]any)
	first := groups[0].(map[string]any)
	if len(groups) != 1 || first["access_group"] != "dev" || first["deployment_count"] != 2.0 || len(first["model_names"].([]any)) != 2 {
		t.Fatalf("/access_group/list = %v", groups)
	}
	if info := api.ok("GET", "/access_group/dev/info", nil); info["access_group"] != "dev" {
		t.Errorf("info = %v", info)
	}
	if status, _ := api.do("GET", "/access_group/none/info", nil); status != http.StatusNotFound {
		t.Errorf("없는 그룹 info = %d", status)
	}

	ids := func(path string) []string {
		var out []string
		for _, m := range api.ok("GET", path, nil)["data"].([]any) {
			out = append(out, m.(map[string]any)["id"].(string))
		}
		return out
	}
	if got := ids("/v1/models"); len(got) != 2 {
		t.Errorf("기본 /v1/models 에 그룹 이름이 섞였다: %v", got)
	}
	if got := ids("/models?include_model_access_groups=True"); len(got) != 3 {
		t.Errorf("그룹 포함 /models = %v, 3개(m1·m2·dev)여야 한다", got)
	}
}
