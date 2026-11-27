package app

import (
	"net/http"
	"testing"
)

// 거절된 요청(access_denied): 인증은 됐지만 권한이 없어 거절된 요청이 감사 기록에 남는다. 키가 틀린 요청은 남기지 않는다

func TestAudit_RecordsDeniedRequestsOfAuthenticatedCallersOnceAMinute(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	user := api.ok("POST", "/user/new", map[string]any{"user_email": "u@corp.local", "user_role": "internal_user", "auto_create_key": false})["user_id"].(string)
	key := api.ok("POST", "/key/generate", map[string]any{"user_id": user})
	plain, hash := key["key"].(string), key["token_id"].(string)

	// 라우트 단계 거절: 일반 사용자가 관리자 전용 쓰기를 부른다. 같은 요청을 여러 번 해도 1분에 한 줄이다
	for i := 0; i < 5; i++ {
		if status, _ := callAs(api, plain, "POST", "/organization/new", map[string]any{"organization_alias": "x"}); status != http.StatusUnauthorized {
			t.Fatalf("일반 사용자의 조직 생성 = %d, 401", status)
		}
	}
	denied := auditLogs(t, api, "action=access_denied&page_size=100")
	if len(denied) != 1 {
		t.Fatalf("거절 기록 = %d 줄, 1 줄이어야 한다(같은 거절은 1분에 한 번)", len(denied))
	}
	d := denied[0].(map[string]any)
	values, _ := d["updated_values"].(map[string]any)
	if d["changed_by"] != user || d["changed_by_api_key"] != hash || values["path"] != "/organization/new" || values["method"] != "POST" ||
		values["status"] != 401.0 || values["role"] != "internal_user" || values["ip"] == nil {
		t.Errorf("거절 기록 = %v", d)
	}

	// 다른 경로의 거절은 따로 남는다. 핸들러 안의 거절(남의 팀 수정)은 그 테이블·대상으로 남는다
	if status, _ := callAs(api, plain, "POST", "/team/update", map[string]any{"team_id": "t1", "team_alias": "hijack"}); status != http.StatusForbidden {
		t.Fatalf("팀원이 아닌 사용자의 팀 수정 = %d, 403", status)
	}
	team := auditLogs(t, api, "action=access_denied&table_name=LiteLLM_TeamTable")
	if len(team) != 1 || team[0].(map[string]any)["object_id"] != "t1" || team[0].(map[string]any)["changed_by"] != user {
		t.Errorf("핸들러 안 거절 기록 = %v", team)
	} else if v, _ := team[0].(map[string]any)["updated_values"].(map[string]any); v["attempted"] != "updated" || v["status"] != 403.0 {
		t.Errorf("핸들러 안 거절의 값 = %v", v)
	}

	// 키가 틀리거나 없는 요청은 남기지 않는다(누구나 무한히 보낼 수 있다)
	before := len(auditLogs(t, api, "action=access_denied&page_size=100"))
	for _, bad := range []string{"sk-not-a-real-key", ""} {
		callAs(api, bad, "POST", "/organization/new", map[string]any{"organization_alias": "x"})
	}
	if after := len(auditLogs(t, api, "action=access_denied&page_size=100")); after != before {
		t.Errorf("인증되지 않은 요청이 거절 기록에 남았다: %d → %d", before, after)
	}
	// 검증 실패(422)는 거절이 아니다
	if status, _ := api.do("POST", "/organization/new", map[string]any{}); status != http.StatusUnprocessableEntity {
		t.Fatalf("검증 실패 = %d", status)
	}
	if after := len(auditLogs(t, api, "action=access_denied&page_size=100")); after != before {
		t.Errorf("검증 실패가 거절 기록에 남았다")
	}
}

func TestAudit_RecordsWritesDeniedToAReadOnlyAdmin(t *testing.T) {
	api := newAPI(t)
	viewer := api.ok("POST", "/user/new", map[string]any{"user_email": "v@corp.local", "user_role": "proxy_admin_viewer", "auto_create_key": false})["user_id"].(string)
	key := api.ok("POST", "/key/generate", map[string]any{"user_id": viewer})["key"].(string)
	if status, _ := callAs(api, key, "POST", "/tag/new", map[string]any{"name": "x"}); status != http.StatusForbidden {
		t.Fatalf("읽기 전용 관리자의 쓰기 = %d, 403", status)
	}
	denied := auditLogs(t, api, "action=access_denied")
	if len(denied) != 1 || denied[0].(map[string]any)["changed_by"] != viewer {
		t.Errorf("읽기 전용 관리자의 거절 기록 = %v", denied)
	}
}
