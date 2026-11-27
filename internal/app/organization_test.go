package app

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// 조직: 팀들을 묶고 팀 위에서 예산·허용 모델·RPM·TPM 의 상한을 정한다. 상한은 팀을 만들고 고칠 때 검사하고, 요청 때는 조직 예산을 본다.
// 조직은 전체 관리자만 관리한다

func TestOrganization_ManagementAPILifecycleAndMembers(t *testing.T) {
	api := newAPI(t)
	if status, _ := api.do("POST", "/organization/new", map[string]any{"models": []string{"m1"}}); status != http.StatusUnprocessableEntity {
		t.Errorf("별칭 없는 조직 = %d, 422", status)
	}
	created := api.ok("POST", "/organization/new", map[string]any{"organization_alias": "연구소", "models": []string{"m1", "m2"},
		"max_budget": 100, "budget_duration": "30d", "tpm_limit": 5000, "metadata": map[string]any{"owner": "kim"}})
	id := created["organization_id"].(string)
	table := created["litellm_budget_table"].(map[string]any)
	if created["organization_alias"] != "연구소" || created["spend"] != 0.0 || table["max_budget"] != 100.0 || table["tpm_limit"] != 5000.0 ||
		len(created["models"].([]any)) != 2 || len(created["members"].([]any)) != 0 || len(created["teams"].([]any)) != 0 {
		t.Errorf("생성 응답 = %v", created)
	}

	patched := api.ok("PATCH", "/v2/organization/"+id, map[string]any{"organization_alias": "연구소2", "max_budget": 200, "tpm_limit": nil})
	pt := patched["litellm_budget_table"].(map[string]any)
	if patched["organization_alias"] != "연구소2" || pt["max_budget"] != 200.0 || pt["tpm_limit"] != nil || pt["budget_duration"] != "30d" {
		t.Errorf("수정 응답 = %v (보낸 필드만 바뀌고 null 은 지운다)", patched)
	}
	if status, _ := api.do("PATCH", "/v2/organization/nope", map[string]any{"organization_alias": "x"}); status != http.StatusNotFound {
		t.Errorf("없는 조직 수정 = %d, 404", status)
	}
	if info := api.ok("GET", "/organization/info?organization_id="+id, nil); info["organization_alias"] != "연구소2" {
		t.Errorf("info = %v", info)
	}
	status, list := api.do("GET", "/organization/list?org_alias=연구", nil)
	if status != http.StatusOK || len(list.([]any)) != 1 {
		t.Errorf("목록 = %d %v", status, list)
	}

	// 구성원: user_id 나 user_email 로 넣고, 역할은 정해진 문자열만, 중복은 거절
	user := api.ok("POST", "/user/new", map[string]any{"user_email": "m@corp.local", "user_role": "internal_user", "auto_create_key": false})["user_id"].(string)
	api.ok("POST", "/organization/member_add", map[string]any{"organization_id": id, "member": map[string]any{"user_email": "m@corp.local", "role": "org_admin"}})
	if status, _ := api.do("POST", "/organization/member_add", map[string]any{"organization_id": id, "member": map[string]any{"user_id": user}}); status != http.StatusBadRequest {
		t.Errorf("이미 구성원 = %d, 400", status)
	}
	if status, _ := api.do("POST", "/organization/member_add", map[string]any{"organization_id": id, "member": map[string]any{"user_id": "ghost"}}); status != http.StatusBadRequest {
		t.Errorf("없는 사용자 = %d, 400", status)
	}
	api.ok("PATCH", "/organization/member_update", map[string]any{"organization_id": id, "user_id": user, "role": "internal_user_viewer"})
	if status, _ := api.do("PATCH", "/organization/member_update", map[string]any{"organization_id": id, "user_id": user, "role": "proxy_admin"}); status != http.StatusBadRequest {
		t.Errorf("조직에서 쓸 수 없는 역할 = %d, 400", status)
	}
	members := api.ok("GET", "/organization/info?organization_id="+id, nil)["members"].([]any)
	if len(members) != 1 || members[0].(map[string]any)["user_role"] != "internal_user_viewer" || members[0].(map[string]any)["user_email"] != "m@corp.local" {
		t.Errorf("구성원 = %v", members)
	}
	api.ok("DELETE", "/organization/member_delete", map[string]any{"organization_id": id, "user_id": user})
	if status, _ := api.do("DELETE", "/organization/member_delete", map[string]any{"organization_id": id, "user_id": user}); status != http.StatusBadRequest {
		t.Errorf("구성원이 아닌 사용자 삭제 = %d, 400", status)
	}

	// 팀이 남은 조직은 지울 수 없다
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "organization_id": id})
	if status, _ := api.do("DELETE", "/organization/delete", map[string]any{"organization_ids": []string{id}}); status != http.StatusBadRequest {
		t.Errorf("팀이 있는 조직 삭제 = %d, 400", status)
	}
	if teams := api.ok("GET", "/organization/info?organization_id="+id, nil)["teams"].([]any); len(teams) != 1 || teams[0].(map[string]any)["team_id"] != "t1" {
		t.Errorf("조직의 팀 = %v", teams)
	}
	api.ok("POST", "/team/update", map[string]any{"team_id": "t1", "organization_id": nil})
	deleted := api.ok("DELETE", "/organization/delete", map[string]any{"organization_ids": []string{id}})
	if len(deleted["deleted_organizations"].([]any)) != 1 {
		t.Errorf("삭제 응답 = %v", deleted)
	}
}

func TestOrganization_TeamCapsAreCheckedOnCreateAndUpdateOnly(t *testing.T) {
	api := newAPI(t)
	org := api.ok("POST", "/organization/new", map[string]any{"organization_alias": "o", "models": []string{"m1"}, "max_budget": 10, "tpm_limit": 100, "rpm_limit": 10})["organization_id"].(string)

	cases := map[string]struct {
		body map[string]any
		want string
	}{
		"조직이 없다":   {map[string]any{"organization_id": "nope"}, "Organization not found for organization_id=nope"},
		"예산 초과":    {map[string]any{"max_budget": 11}, "Team max_budget (11) exceeds organization's max_budget (10)"},
		"모델이 상한 밖": {map[string]any{"models": []string{"m1", "m9"}}, "Model 'm9' not in organization's allowed models"},
		"TPM 초과":   {map[string]any{"tpm_limit": 101}, "Team tpm_limit (101) exceeds organization's tpm_limit (100)"},
		"RPM 초과":   {map[string]any{"rpm_limit": 11}, "Team rpm_limit (11) exceeds organization's rpm_limit (10)"},
	}
	for name, c := range cases {
		body := map[string]any{"organization_id": org}
		for k, v := range c.body {
			body[k] = v
		}
		status, resp := api.do("POST", "/team/new", body)
		if status != http.StatusBadRequest || !strings.Contains(responseText(resp), c.want) {
			t.Errorf("%s: %d %v, 기대 %q", name, status, resp, c.want)
		}
	}
	ok := api.ok("POST", "/team/new", map[string]any{"team_id": "within", "organization_id": org, "models": []string{"m1"}, "max_budget": 10, "tpm_limit": 100})
	if ok["organization_id"] != org {
		t.Errorf("상한 안의 팀 응답 organization_id = %v", ok["organization_id"])
	}

	// 고칠 때도 같다. 조직 없는 팀은 상한이 없다
	if status, _ := api.do("POST", "/team/update", map[string]any{"team_id": "within", "max_budget": 99}); status != http.StatusBadRequest {
		t.Errorf("조직 안 팀의 예산을 상한 위로 = %d, 400", status)
	}
	api.ok("POST", "/team/new", map[string]any{"team_id": "free", "models": []string{"m7"}, "max_budget": 999})
	if status, _ := api.do("POST", "/team/update", map[string]any{"team_id": "free", "organization_id": org, "max_budget": 99}); status != http.StatusBadRequest {
		t.Errorf("팀을 조직으로 옮기며 상한 위 예산 = %d, 400", status)
	}
	// litellm 처럼 조직 값을 나중에 줄여도 기존 팀을 건드리지 않는다
	api.ok("PATCH", "/v2/organization/"+org, map[string]any{"max_budget": 1})
	if got := api.ok("GET", "/team/info?team_id=within", nil)["team_info"].(map[string]any); got["max_budget"] != 10.0 {
		t.Errorf("조직 상한을 줄였더니 기존 팀이 바뀌었다: %v", got["max_budget"])
	}
}

func responseText(v any) string {
	if m, ok := v.(map[string]any); ok {
		if detail, ok := m["detail"].(map[string]any); ok {
			return responseText(detail["error"])
		}
		if e, ok := m["error"].(map[string]any); ok {
			return responseText(e["message"])
		}
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestOrganization_BudgetAppliesToTeamKeysInTheOrganizationOnly(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL, "input_cost_per_token": 0.001, "output_cost_per_token": 0.001}})
	org := api.ok("POST", "/organization/new", map[string]any{"organization_alias": "o", "max_budget": 0.001})["organization_id"].(string)
	api.ok("POST", "/team/new", map[string]any{"team_id": "in-org", "organization_id": org})
	api.ok("POST", "/team/new", map[string]any{"team_id": "outside"})
	inKey := api.ok("POST", "/key/generate", map[string]any{"team_id": "in-org"})["key"].(string)
	outKey := api.ok("POST", "/key/generate", map[string]any{"team_id": "outside"})["key"].(string)

	if rec := chatWithKey(api, inKey, "m1"); rec.Code != http.StatusOK {
		t.Fatalf("첫 요청 = %d %s", rec.Code, rec.Body.String())
	}
	rec := chatWithKey(api, inKey, "m1")
	for deadline := time.Now().Add(3 * time.Second); rec.Code == http.StatusOK && time.Now().Before(deadline); {
		<-time.After(20 * time.Millisecond)
		rec = chatWithKey(api, inKey, "m1")
	}
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Organization="+org) {
		t.Fatalf("조직 예산을 넘었는데 %d %s", rec.Code, rec.Body.String())
	}
	if rec := chatWithKey(api, outKey, "m1"); rec.Code != http.StatusOK {
		t.Errorf("조직 밖 팀 키가 막혔다: %d", rec.Code)
	}

	// 팀을 조직에서 빼면 바로 풀린다(키 캐시가 팀 변경에 따라 지워진다)
	api.ok("POST", "/team/update", map[string]any{"team_id": "in-org", "organization_id": ""})
	if rec := chatWithKey(api, inKey, "m1"); rec.Code != http.StatusOK {
		t.Errorf("조직에서 뺀 팀 키 = %d %s", rec.Code, rec.Body.String())
	}
}

func TestOrganization_NonAdminCannotSeeOrManageAndKeyProjectIsAdminOnly(t *testing.T) {
	api := newAPI(t)
	org := api.ok("POST", "/organization/new", map[string]any{"organization_alias": "secret-org"})["organization_id"].(string)
	api.ok("POST", "/team/new", map[string]any{"team_id": "tm"})
	project := api.ok("POST", "/project/new", map[string]any{"team_id": "tm", "max_budget": 1})["project_id"].(string)
	user := api.ok("POST", "/user/new", map[string]any{"user_email": "u@corp.local", "user_role": "internal_user", "auto_create_key": false})["user_id"].(string)
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "tm", "member": map[string]any{"user_id": user, "role": "user"}})
	userKey := api.ok("POST", "/key/generate", map[string]any{"user_id": user, "team_id": "tm"})

	status, list := callAs(api, userKey["key"].(string), "GET", "/organization/list", nil)
	if status != http.StatusOK || len(list.([]any)) != 0 {
		t.Errorf("일반 사용자의 조직 목록 = %d %v, 빈 배열이어야 한다", status, list)
	}
	if status, _ := callAs(api, userKey["key"].(string), "GET", "/organization/info?organization_id="+org, nil); status != http.StatusNotFound {
		t.Errorf("일반 사용자의 조직 info = %d, 404", status)
	}
	for _, c := range []struct{ method, path string }{{"POST", "/organization/new"}, {"PATCH", "/v2/organization/" + org}, {"DELETE", "/organization/delete"},
		{"POST", "/organization/member_add"}} {
		if status, _ := callAs(api, userKey["key"].(string), c.method, c.path, map[string]any{"organization_alias": "x", "organization_ids": []string{org}}); status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Errorf("일반 사용자 %s %s = %d, 거절이어야 한다", c.method, c.path, status)
		}
	}

	// 팀원이 자기 키를 프로젝트에 넣거나 빼는 것은 거절한다(프로젝트의 차단·예산을 벗어나지 못하게)
	api.ok("POST", "/key/update", map[string]any{"key": userKey["key"], "project_id": project})
	status, _ = callAs(api, userKey["key"].(string), "POST", "/key/update", map[string]any{"key": userKey["key"], "project_id": nil})
	if status != http.StatusForbidden {
		t.Errorf("팀원이 키를 프로젝트에서 빼기 = %d, 403", status)
	}
	if got := api.ok("GET", "/key/info?key="+userKey["key"].(string), nil)["info"].(map[string]any); got["project_id"] != project {
		t.Errorf("거절했는데 프로젝트가 바뀌었다: %v", got["project_id"])
	}
}
