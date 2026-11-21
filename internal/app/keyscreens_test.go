package app

import (
	"testing"
)

// 키 화면의 서비스 계정 키·재발급(J 3.2·3.9)

func TestKeys_ServiceAccountAndRegenerate(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})

	if status, _ := api.do("POST", "/key/service-account/generate", map[string]any{"key_alias": "bot"}); status != 400 {
		t.Errorf("팀 없는 서비스 계정 키: %d", status)
	}
	sa := api.ok("POST", "/key/service-account/generate", map[string]any{"team_id": "t1", "key_alias": "bot", "user_id": "someone"})
	if sa["user_id"] != nil || sa["team_id"] != "t1" || sa["metadata"].(map[string]any)["service_account_id"] != "bot" {
		t.Errorf("서비스 계정 키 = %v", sa)
	}

	old := api.ok("POST", "/key/generate", map[string]any{"team_id": "t1", "key_alias": "rotating", "max_budget": 7})
	oldKey := old["key"].(string)
	regen := api.ok("POST", "/key/"+oldKey+"/regenerate", map[string]any{"key_alias": "rotated", "duration": "", "grace_period": ""})
	newKey := regen["key"].(string)
	if newKey == oldKey || regen["token"] != nil || regen["token_id"] == old["token_id"] || regen["key_alias"] != "rotated" ||
		regen["max_budget"] != 7.0 || regen["team_id"] != "t1" {
		t.Fatalf("재발급 = %v", regen)
	}
	auth := func(k string) map[string]string { return map[string]string{"Authorization": "Bearer " + k} }
	if rec := b.do("GET", "/v1/models", nil, auth(newKey)); rec.Code != 200 {
		t.Errorf("새 키: %d", rec.Code)
	}
	if rec := b.do("GET", "/v1/models", nil, auth(oldKey)); rec.Code != 401 {
		t.Errorf("옛 키가 아직 된다: %d", rec.Code)
	}
	if status, _ := api.do("POST", "/key/sk-does-not-exist-123/regenerate", map[string]any{}); status != 404 {
		t.Errorf("없는 키: %d", status)
	}
}
