package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// 감사 범위 확대: 모델·설정·조직·프로젝트·태그·고객·프로바이더 같은 관리 변경과 로그인·비밀번호 사건도 남고, 어떤 깊이에서도 비밀 값은 남지 않는다

func auditObjects(t *testing.T, api apiClient, table string) map[string][]string {
	t.Helper()
	result := map[string][]string{} // 대상 id → 행동들
	for _, entry := range auditLogs(t, api, "table_name="+table+"&page_size=100&sort_order=asc") {
		e := entry.(map[string]any)
		result[e["object_id"].(string)] = append(result[e["object_id"].(string)], e["action"].(string))
	}
	return result
}

func TestAudit_CoversModelsSettingsAndTheNewManagementAreasWithoutSecrets(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)

	created := api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL, "api_key": "sk-upstream-secret-value"}})
	modelID := created["model_info"].(map[string]any)["id"].(string)
	api.ok("PATCH", "/model/"+modelID+"/update", map[string]any{"blocked": true})
	api.ok("POST", "/model/delete", map[string]any{"id": modelID})

	api.ok("PATCH", "/update/ui_settings", map[string]any{"enabled_ui_pages_internal_users": []string{"api-keys"}})
	org := api.ok("POST", "/organization/new", map[string]any{"organization_alias": "o"})["organization_id"].(string)
	api.ok("PATCH", "/v2/organization/"+org, map[string]any{"organization_alias": "o2"})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	project := api.ok("POST", "/project/new", map[string]any{"team_id": "t1"})["project_id"].(string)
	api.ok("POST", "/project/update", map[string]any{"project_id": project, "blocked": true})
	api.ok("POST", "/tag/new", map[string]any{"name": "ops"})
	api.ok("POST", "/tag/delete", map[string]any{"name": "ops"})
	api.ok("POST", "/customer/new", map[string]any{"user_id": "acme"})
	api.ok("POST", "/customer/block", map[string]any{"user_ids": []string{"acme"}})
	api.ok("POST", "/provider/budgets", map[string]any{"provider": "ollama", "budget_limit": 5})
	api.ok("DELETE", "/provider/budgets/ollama", nil)
	user := api.ok("POST", "/user/new", map[string]any{"user_email": "i@corp.local", "user_role": "internal_user", "auto_create_key": false})["user_id"].(string)
	api.ok("POST", "/invitation/new", map[string]any{"user_id": user})
	api.ok("POST", "/team/permissions_update", map[string]any{"team_id": "t1", "team_member_permissions": []string{"/key/generate"}})

	// 대시보드 감사 화면의 테이블 필터 값(litellm 이름)이 곧 외부 계약이다
	want := map[string]map[string][]string{
		"LiteLLM_ProxyModelTable":   {modelID: {"created", "updated", "deleted"}},
		"LiteLLM_OrganizationTable": {org: {"created", "updated"}},
		"LiteLLM_ProjectTable":      {project: {"created", "updated"}},
		"LiteLLM_TagTable":          {"ops": {"created", "deleted"}},
		"LiteLLM_EndUserTable":      {"acme": {"created", "blocked"}},
		"LiteLLM_Config":            {"ui_settings": {"updated"}, "ollama": {"updated", "deleted"}},
		"LiteLLM_InvitationLink":    {user: {"created"}},
	}
	for table, objects := range want {
		got := auditObjects(t, api, table)
		for id, actions := range objects {
			if !slices.Equal(got[id], actions) {
				t.Errorf("%s %s 기록 = %v, 기대 %v", table, id, got[id], actions)
			}
		}
	}
	if got := auditObjects(t, api, "LiteLLM_TeamTable"); !slices.Contains(got["t1"], "updated") {
		t.Errorf("팀 권한 변경 기록 = %v", got["t1"])
	}

	// 어떤 기록에도 업스트림 키 값이 없다(litellm_params 안의 api_key 도 뺀다)
	dump, _ := json.Marshal(auditLogs(t, api, "page_size=100"))
	if strings.Contains(string(dump), "sk-upstream-secret-value") {
		t.Errorf("감사 기록에 업스트림 키가 남았다")
	}
}

func TestAudit_RecordsLoginPasswordChangeAndLogoutAndNeverThePasswords(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	api.ok("POST", "/user/new", map[string]any{"user_id": "kim", "user_email": "kim@corp.local", "user_role": "internal_user"})
	api.ok("POST", "/user/update", map[string]any{"user_id": "kim", "password": "Admin-set-2026"})

	// 실패: 시도한 아이디가 대상이다. 비밀번호는 어디에도 남지 않는다
	if code, _, _ := dashboardLogin(t, b, "kim@corp.local", "wrong-password-attempt"); code != 401 {
		t.Fatalf("틀린 비밀번호 = %d", code)
	}
	failed := auditLogs(t, api, "action=login_failed")
	if len(failed) != 1 {
		t.Fatalf("로그인 실패 기록 = %v", failed)
	}
	f := failed[0].(map[string]any)
	values, _ := f["updated_values"].(map[string]any)
	if f["object_id"] != "kim@corp.local" || f["changed_by"] != "kim@corp.local" || values["reason"] != "bad_credentials" || values["method"] != "username_password" || values["ip"] == nil {
		t.Errorf("실패 기록 = %v", f)
	}

	// 성공 → 비밀번호 변경 → 다시 로그인 → 로그아웃
	code, _, cookie := dashboardLogin(t, b, "kim@corp.local", "Admin-set-2026")
	if code != 200 {
		t.Fatalf("로그인 = %d", code)
	}
	first := map[string]string{"Authorization": "Bearer " + uiClaims(t, cookie)["key"].(string)}
	if rec := b.doJSON("POST", "/user/password/change", `{"current_password":"Admin-set-2026","new_password":"Kim-own-pass-1"}`, first); rec.Code != 200 {
		t.Fatalf("비밀번호 변경 = %d %s", rec.Code, rec.Body.String())
	}
	_, _, cookie = dashboardLogin(t, b, "kim@corp.local", "Kim-own-pass-1")
	second := map[string]string{"Authorization": "Bearer " + uiClaims(t, cookie)["key"].(string)}
	if rec := b.do("POST", "/session/logout", nil, second); rec.Code != 200 {
		t.Fatalf("로그아웃 = %d", rec.Code)
	}

	for action, want := range map[string]int{"login_success": 2, "password_changed": 1, "logout": 1} {
		got := auditLogs(t, api, "action="+action)
		if len(got) != want {
			t.Errorf("%s 기록 = %d 건, 기대 %d", action, len(got), want)
		}
		for _, entry := range got {
			if entry.(map[string]any)["object_id"] != "kim" || entry.(map[string]any)["changed_by"] != "kim" {
				t.Errorf("%s 기록의 대상·행위자 = %v", action, entry)
			}
		}
	}

	dump, _ := json.Marshal(auditLogs(t, api, "page_size=100"))
	for _, secret := range []string{"wrong-password-attempt", "Admin-set-2026", "Kim-own-pass-1", testMasterKey} {
		if strings.Contains(string(dump), secret) {
			t.Errorf("감사 기록에 비밀 값이 남았다: %s", secret)
		}
	}
}
