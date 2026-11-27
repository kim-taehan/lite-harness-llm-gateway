package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// 이전 값(before_value): 바꾸는 요청을 처리하기 전 대상의 모습이 감사 기록에 함께 남는다

func auditEntryFor(t *testing.T, api apiClient, query string) map[string]any {
	t.Helper()
	logs := auditLogs(t, api, query+"&page_size=100&sort_order=desc")
	if len(logs) == 0 {
		t.Fatalf("감사 기록이 없다: %s", query)
	}
	return logs[0].(map[string]any)
}

func TestAudit_KeepsTheValueBeforeAnUpdateOrDeleteAndNothingForCreates(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "team_alias": "연구", "max_budget": 5, "models": []string{"m1"}})
	create := auditEntryFor(t, api, "table_name=LiteLLM_TeamTable&object_id=t1&action=created")
	if create["before_value"] != nil {
		t.Errorf("만들기에 이전 값이 있다: %v", create["before_value"])
	}

	api.ok("POST", "/team/update", map[string]any{"team_id": "t1", "team_alias": "연구소", "max_budget": 9})
	update := auditEntryFor(t, api, "table_name=LiteLLM_TeamTable&object_id=t1&action=updated")
	before, _ := update["before_value"].(map[string]any)
	values, _ := update["updated_values"].(map[string]any)
	if before["team_alias"] != "연구" || before["max_budget"] != 5.0 || values["team_alias"] != "연구소" || values["max_budget"] != 9.0 {
		t.Errorf("수정 기록: 이전 %v / 바꾼 값 %v", before, values)
	}

	// 지우면 그 모습은 더 읽을 수 없으니 기록에만 남는다
	api.ok("POST", "/team/delete", map[string]any{"team_ids": []string{"t1"}})
	deleted := auditEntryFor(t, api, "table_name=LiteLLM_TeamTable&object_id=t1&action=deleted")
	if gone, _ := deleted["before_value"].(map[string]any); gone["team_alias"] != "연구소" || gone["max_budget"] != 9.0 {
		t.Errorf("삭제 기록의 이전 값 = %v", deleted["before_value"])
	}
}

func TestAudit_BeforeValuesCoverKeysModelsSettingsAndNeverHoldSecrets(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)

	key := api.ok("POST", "/key/generate", map[string]any{"key_alias": "ci", "max_budget": 3, "key": "sk-custom-secret-value"})
	hash := key["token_id"].(string)
	api.ok("POST", "/key/update", map[string]any{"key": "sk-custom-secret-value", "max_budget": 8, "key_alias": "ci"})
	keyUpdate := auditEntryFor(t, api, "table_name=LiteLLM_VerificationToken&object_id="+hash+"&action=updated")
	if before, _ := keyUpdate["before_value"].(map[string]any); before["max_budget"] != 3.0 || before["key_alias"] != "ci" {
		t.Errorf("키 수정의 이전 값 = %v", keyUpdate["before_value"])
	}

	model := api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL, "api_key": "sk-upstream-secret-value"}})
	modelID := model["model_info"].(map[string]any)["id"].(string)
	api.ok("PATCH", "/model/"+modelID+"/update", map[string]any{"blocked": true})
	modelUpdate := auditEntryFor(t, api, "table_name=LiteLLM_ProxyModelTable&object_id="+modelID+"&action=updated")
	if before, _ := modelUpdate["before_value"].(map[string]any); before["model_name"] != "m1" {
		t.Errorf("모델 수정의 이전 값 = %v", modelUpdate["before_value"])
	}

	api.ok("PATCH", "/update/ui_settings", map[string]any{"enabled_ui_pages_internal_users": []string{"api-keys"}})
	api.ok("PATCH", "/update/ui_settings", map[string]any{"enabled_ui_pages_internal_users": []string{"usage"}})
	settings := auditEntryFor(t, api, "table_name=LiteLLM_Config&object_id=ui_settings&action=updated")
	if before, _ := settings["before_value"].(map[string]any); before == nil || !strings.Contains(toJSON(before), "api-keys") {
		t.Errorf("설정의 이전 값 = %v", settings["before_value"])
	}

	api.ok("POST", "/add/allowed_ip", map[string]any{"ip": "192.0.2.1"})
	api.ok("POST", "/add/allowed_ip", map[string]any{"ip": "192.0.2.2"})
	ips := auditEntryFor(t, api, "table_name=LiteLLM_Config&object_id=general_settings&action=updated")
	if before, _ := ips["before_value"].(map[string]any); !strings.Contains(toJSON(before), "192.0.2.1") || strings.Contains(toJSON(before), "192.0.2.2") {
		t.Errorf("IP 허용 목록의 이전 값 = %v (방금 더한 192.0.2.2 는 없어야 한다)", ips["before_value"])
	}

	// 이전 값에도 비밀이 없다: 키 원문, 업스트림 키
	dump, _ := json.Marshal(auditLogs(t, api, "page_size=100"))
	for _, secret := range []string{"sk-custom-secret-value", "sk-upstream-secret-value"} {
		if strings.Contains(string(dump), secret) {
			t.Errorf("감사 기록에 비밀 값이 남았다: %s", secret)
		}
	}
}

func toJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
