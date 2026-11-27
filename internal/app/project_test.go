package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 프로젝트: 팀 안에서 키를 묶고 그 묶음에 예산·허용 모델·차단을 건다. 팀 필수, 키의 프로젝트 연결은 선택

// callAs 는 마스터 키가 아니라 주어진 키로 부른다(배열 응답도 읽는다)
func callAs(api apiClient, key, method, path string, body any) (int, any) {
	api.t.Helper()
	data, _ := json.Marshal(body)
	if body == nil {
		data = nil
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	api.h.ServeHTTP(rec, req)
	var decoded any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded) // 본문이 JSON 이 아니면 nil 이다. 이 시험은 상태 코드와 JSON 본문만 본다
	return rec.Code, decoded
}

func TestProject_ManagementAPILifecycleAndTeamRule(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t2"})

	if status, _ := api.do("POST", "/project/new", map[string]any{"project_alias": "no team"}); status != http.StatusUnprocessableEntity {
		t.Errorf("팀 없는 프로젝트 = %d, 422(팀은 필수)", status)
	}
	if status, _ := api.do("POST", "/project/new", map[string]any{"team_id": "ghost"}); status != http.StatusBadRequest {
		t.Errorf("없는 팀의 프로젝트 = %d, 400", status)
	}
	created := api.ok("POST", "/project/new", map[string]any{"project_alias": "rag", "description": "검색", "team_id": "t1",
		"models": []string{"m1"}, "max_budget": 5, "budget_duration": "30d", "metadata": map[string]any{"owner": "kim"}})
	id := created["project_id"].(string)
	table := created["litellm_budget_table"].(map[string]any)
	if created["project_alias"] != "rag" || created["team_id"] != "t1" || created["blocked"] != false || created["spend"] != 0.0 ||
		table["max_budget"] != 5.0 || table["budget_duration"] != "30d" || len(created["models"].([]any)) != 1 {
		t.Errorf("생성 응답 = %v", created)
	}

	updated := api.ok("POST", "/project/update", map[string]any{"project_id": id, "description": "검색 서비스", "max_budget": 9, "blocked": true})
	if updated["description"] != "검색 서비스" || updated["blocked"] != true || updated["litellm_budget_table"].(map[string]any)["max_budget"] != 9.0 ||
		updated["litellm_budget_table"].(map[string]any)["budget_duration"] != "30d" {
		t.Errorf("수정 응답 = %v", updated)
	}
	if status, _ := api.do("POST", "/project/update", map[string]any{"project_id": "nope"}); status != http.StatusNotFound {
		t.Errorf("없는 프로젝트 수정 = %d, 404", status)
	}
	if info := api.ok("GET", "/project/info?project_id="+id, nil); info["project_alias"] != "rag" {
		t.Errorf("info = %v", info)
	}
	status, list := api.do("GET", "/project/list", nil)
	if status != http.StatusOK || len(list.([]any)) != 1 {
		t.Errorf("목록 = %d %v", status, list)
	}

	// 키가 있으면 팀을 옮기거나 지울 수 없다
	key := api.ok("POST", "/key/generate", map[string]any{"project_id": id})
	if key["team_id"] != "t1" || key["project_id"] != id {
		t.Errorf("프로젝트 키 = team %v, project %v (팀은 프로젝트에서 채워진다)", key["team_id"], key["project_id"])
	}
	if status, _ := api.do("POST", "/project/update", map[string]any{"project_id": id, "team_id": "t2"}); status != http.StatusBadRequest {
		t.Errorf("키가 있는 프로젝트의 팀 이동 = %d, 400", status)
	}
	if status, _ := api.do("DELETE", "/project/delete", map[string]any{"project_ids": []string{id}}); status != http.StatusBadRequest {
		t.Errorf("키가 있는 프로젝트 삭제 = %d, 400", status)
	}
	api.ok("POST", "/key/update", map[string]any{"key": key["key"], "project_id": nil})
	deleted := api.ok("DELETE", "/project/delete", map[string]any{"project_ids": []string{id}})
	if len(deleted["deleted_projects"].([]any)) != 1 {
		t.Errorf("삭제 응답 = %v", deleted)
	}
	if status, _ := api.do("GET", "/project/info?project_id="+id, nil); status != http.StatusNotFound {
		t.Errorf("지운 프로젝트 info = %d", status)
	}

	// 팀을 지우면 그 팀 프로젝트도 함께 지워진다
	again := api.ok("POST", "/project/new", map[string]any{"team_id": "t2"})["project_id"].(string)
	api.ok("POST", "/team/delete", map[string]any{"team_ids": []string{"t2"}})
	if status, _ := api.do("GET", "/project/info?project_id="+again, nil); status != http.StatusNotFound {
		t.Errorf("팀을 지웠는데 프로젝트가 남았다: %d", status)
	}
}

func TestProject_KeyLinkRulesAndListFilter(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t2"})
	project := api.ok("POST", "/project/new", map[string]any{"team_id": "t1"})["project_id"].(string)

	if status, _ := api.do("POST", "/key/generate", map[string]any{"project_id": "nope"}); status != http.StatusBadRequest {
		t.Errorf("없는 프로젝트의 키 = %d, 400", status)
	}
	if status, _ := api.do("POST", "/key/generate", map[string]any{"project_id": project, "team_id": "t2"}); status != http.StatusBadRequest {
		t.Errorf("다른 팀의 프로젝트에 키 = %d, 400", status)
	}
	inProject := api.ok("POST", "/key/generate", map[string]any{"project_id": project, "team_id": "t1", "key_alias": "in-project"})
	api.ok("POST", "/key/generate", map[string]any{"team_id": "t1", "key_alias": "plain"}) // 프로젝트 없는 키는 그대로 만든다

	// 프로젝트가 있는 키의 팀만 바꾸는 것은 거절한다. 프로젝트를 비우면서 바꾸면 된다
	if status, _ := api.do("POST", "/key/update", map[string]any{"key": inProject["key"], "team_id": "t2"}); status != http.StatusBadRequest {
		t.Errorf("프로젝트 키의 팀 변경 = %d, 400", status)
	}
	if got := api.ok("POST", "/key/update", map[string]any{"key": inProject["key"], "team_id": "t2", "project_id": nil}); got["team_id"] != "t2" || got["project_id"] != nil {
		t.Errorf("프로젝트를 비우며 팀 변경 = %v", got)
	}
	api.ok("POST", "/key/update", map[string]any{"key": inProject["key"], "team_id": "t1", "project_id": project})

	keys := api.ok("GET", "/key/list?project_id="+project+"&return_full_object=true", nil)
	if keys["total_count"] != 1.0 {
		t.Errorf("project_id 로 거른 키 목록 = %v", keys)
	}
	if info := api.ok("GET", "/key/info?key="+inProject["key"].(string), nil)["info"].(map[string]any); info["project_id"] != project {
		t.Errorf("key/info 의 project_id = %v", info["project_id"])
	}
}

func TestProject_EnforcesBlockModelsAndBudgetOnItsKeysOnly(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	for _, name := range []string{"m1", "m2"} {
		api.ok("POST", "/model/new", map[string]any{"model_name": name,
			"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL, "input_cost_per_token": 0.001, "output_cost_per_token": 0.001}})
	}
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	project := api.ok("POST", "/project/new", map[string]any{"team_id": "t1", "models": []string{"m1"}, "max_budget": 0.001})["project_id"].(string)
	projectKey := api.ok("POST", "/key/generate", map[string]any{"project_id": project})["key"].(string)
	teamKey := api.ok("POST", "/key/generate", map[string]any{"team_id": "t1"})["key"].(string)

	// 허용 모델: 프로젝트 모델 밖은 403, 팀의 다른 키는 영향이 없다
	if rec := chatWithKey(api, projectKey, "m2"); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "project_model_access_denied") {
		t.Errorf("프로젝트 모델 밖 = %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatWithKey(api, teamKey, "m2"); rec.Code != http.StatusOK {
		t.Errorf("프로젝트 없는 팀 키가 막혔다: %d", rec.Code)
	}

	// 예산: 응답 뒤 사용액이 프로젝트에 쌓여 넘으면 422(Project=…)
	if rec := chatWithKey(api, projectKey, "m1"); rec.Code != http.StatusOK {
		t.Fatalf("첫 요청 = %d %s", rec.Code, rec.Body.String())
	}
	rec := chatWithKey(api, projectKey, "m1")
	for deadline := time.Now().Add(3 * time.Second); rec.Code == http.StatusOK && time.Now().Before(deadline); {
		<-time.After(20 * time.Millisecond)
		rec = chatWithKey(api, projectKey, "m1")
	}
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Project="+project) {
		t.Fatalf("프로젝트 예산을 넘었는데 %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatWithKey(api, teamKey, "m1"); rec.Code != http.StatusOK {
		t.Errorf("프로젝트 예산이 팀의 다른 키까지 막았다: %d", rec.Code)
	}

	// 차단: 캐시를 거치지 않고 바로 반영되고, 풀면 다시 된다
	api.ok("POST", "/project/update", map[string]any{"project_id": project, "blocked": true, "max_budget": 100})
	rec = chatWithKey(api, projectKey, "m1")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Project="+project+" is blocked") {
		t.Errorf("차단한 프로젝트 = %d %s", rec.Code, rec.Body.String())
	}
	api.ok("POST", "/project/update", map[string]any{"project_id": project, "blocked": false})
	if rec := chatWithKey(api, projectKey, "m1"); rec.Code != http.StatusOK {
		t.Errorf("차단을 풀었는데 %d %s", rec.Code, rec.Body.String())
	}
}

func TestProject_NonAdminSeesOnlyOwnTeamsProjectsAndCannotWrite(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/team/new", map[string]any{"team_id": "mine"})
	api.ok("POST", "/team/new", map[string]any{"team_id": "other"})
	mine := api.ok("POST", "/project/new", map[string]any{"team_id": "mine", "project_alias": "mine-p"})["project_id"].(string)
	theirs := api.ok("POST", "/project/new", map[string]any{"team_id": "other", "project_alias": "other-p"})["project_id"].(string)
	user := api.ok("POST", "/user/new", map[string]any{"user_email": "u@corp.local", "user_role": "internal_user", "auto_create_key": false})["user_id"].(string)
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "mine", "member": map[string]any{"user_id": user, "role": "user"}})
	userKey := api.ok("POST", "/key/generate", map[string]any{"user_id": user})["key"].(string)

	status, list := callAs(api, userKey, "GET", "/project/list", nil)
	if status != http.StatusOK || len(list.([]any)) != 1 || list.([]any)[0].(map[string]any)["project_id"] != mine {
		t.Errorf("일반 사용자 목록 = %d %v, 자기 팀 프로젝트 하나여야 한다", status, list)
	}
	if status, _ := callAs(api, userKey, "GET", "/project/info?project_id="+mine, nil); status != http.StatusOK {
		t.Errorf("자기 팀 프로젝트 info = %d", status)
	}
	other, _ := callAs(api, userKey, "GET", "/project/info?project_id="+theirs, nil)
	missing, _ := callAs(api, userKey, "GET", "/project/info?project_id=nope", nil)
	if other != http.StatusNotFound || other != missing {
		t.Errorf("남의 팀 프로젝트 = %d, 없는 프로젝트 = %d (같은 404 여야 존재를 알리지 않는다)", other, missing)
	}
	for method, path := range map[string]string{"POST /project/new": "/project/new", "POST /project/update": "/project/update"} {
		if status, _ := callAs(api, userKey, "POST", path, map[string]any{"team_id": "mine", "project_id": mine}); status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Errorf("%s 일반 사용자 = %d, 거절이어야 한다", method, status)
		}
	}
}
