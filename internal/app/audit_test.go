package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// 관리 변경이 audit_log 에 남고 GET /audit 으로 읽히는지 본다(설계 8.1)

func auditLogs(t *testing.T, api apiClient, query string) []any {
	t.Helper()
	return api.ok("GET", "/audit?"+query, nil)["audit_logs"].([]any)
}

func TestAudit_RecordsAdminChangesWithoutSecretsAndFiltersThem(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/team/new", map[string]any{"team_id": "research", "team_alias": "Research"})
	generated := api.ok("POST", "/key/generate", map[string]any{"key_alias": "ci", "team_id": "research", "key": "sk-custom-secret-value"})
	hash := generated["token_id"].(string)
	api.ok("POST", "/key/update", map[string]any{"key": "sk-custom-secret-value", "max_budget": 5})
	api.ok("POST", "/key/block", map[string]any{"key": hash})

	// 실패한 요청은 남기지 않는다
	if status, _ := api.do("POST", "/key/update", map[string]any{"key": "sk-nonexistent"}); status != 404 {
		t.Fatalf("없는 키 수정 = %d", status)
	}

	logs := auditLogs(t, api, "table_name=LiteLLM_VerificationToken&sort_order=asc")
	var actions []string
	for _, entry := range logs {
		e := entry.(map[string]any)
		actions = append(actions, e["action"].(string))
		if e["object_id"] != hash || e["changed_by"] != "default_user_id" {
			t.Errorf("대상·변경자 = %v", e)
		}
		if values, _ := e["updated_values"].(map[string]any); values != nil {
			if _, leaked := values["key"]; leaked {
				t.Errorf("키 원문이 기록에 들어갔다: %v", values)
			}
		}
	}
	if strings.Join(actions, ",") != "created,updated,blocked" {
		t.Errorf("키 기록 = %v", actions)
	}

	teamLogs := auditLogs(t, api, "table_name=LiteLLM_TeamTable")
	if len(teamLogs) != 1 || teamLogs[0].(map[string]any)["object_id"] != "research" || teamLogs[0].(map[string]any)["action"] != "created" {
		t.Errorf("팀 기록 = %v", teamLogs)
	}
	if byAction := auditLogs(t, api, "action=blocked"); len(byAction) != 1 {
		t.Errorf("action 필터 = %v", byAction)
	}
	if byHash := auditLogs(t, api, "object_key_hash="+hash); len(byHash) != 3 {
		t.Errorf("object_key_hash 필터 = %d 건", len(byHash))
	}
	// 키 원문을 요청에 넣지 않으면 대상은 응답의 해시다. 한 번 만들면 한 줄이다
	auto := api.ok("POST", "/key/generate", map[string]any{"key_alias": "auto"})["token_id"].(string)
	if byAuto := auditLogs(t, api, "object_id="+auto); len(byAuto) != 1 {
		t.Errorf("자동 생성 키의 기록 = %d 건, want 1", len(byAuto))
	}

	page := api.ok("GET", "/audit?page_size=2", nil)
	if page["total"] != 5.0 || page["total_pages"] != 3.0 || len(page["audit_logs"].([]any)) != 2 {
		t.Errorf("쪽 나누기 = %v", page)
	}
	id := logs[0].(map[string]any)["id"].(string)
	if one := api.ok("GET", "/audit/"+id, nil); one["id"] != id {
		t.Errorf("단건 조회 = %v", one)
	}
	if status, _ := api.do("GET", "/audit/nope", nil); status != 404 {
		t.Errorf("없는 기록 = %d", status)
	}
}

func TestAudit_IsNotReadableByInternalUsers(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/user/new", map[string]any{"user_id": "kim", "user_role": "internal_user"})
	key := api.ok("POST", "/key/generate", map[string]any{"user_id": "kim"})["key"].(string)

	req := httptest.NewRequest("GET", "/audit", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	api.h.ServeHTTP(rec, req)
	if rec.Code != 401 { // 라우트 권한 거절은 litellm 처럼 401 이다
		t.Errorf("일반 사용자의 감사 조회 = %d %s", rec.Code, rec.Body)
	}
}
