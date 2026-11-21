package app

import (
	"testing"
)

// 키 화면의 서비스 계정 키·재발급(J 3.2·3.9)

func TestKeys_RegenerateGracePeriodKeepsOldKeyUntilItEnds(t *testing.T) {
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	b := browser{t: t, h: h}
	status := func(key string) int {
		return b.do("GET", "/v1/models", nil, map[string]string{"Authorization": "Bearer " + key}).Code
	}

	first := api.ok("POST", "/key/generate", map[string]any{"key_alias": "rotating", "max_budget": 7})["key"].(string)
	second := api.ok("POST", "/key/"+first+"/regenerate", map[string]any{"grace_period": "1h"})["key"].(string)
	if status(first) != 200 || status(second) != 200 {
		t.Fatalf("유예 중: 옛 키 %d, 새 키 %d", status(first), status(second))
	}
	// 또 재발급해도 먼저 약속한 유예는 지켜진다. 옛 해시가 가장 새 키를 가리키게 옮겨 간다
	third := api.ok("POST", "/key/"+second+"/regenerate", map[string]any{})["key"].(string)
	if status(first) != 200 || status(second) != 401 || status(third) != 200 {
		t.Errorf("두 번 재발급 뒤: 처음 키 %d(유예), 둘째 키 %d(바로 폐기), 셋째 키 %d", status(first), status(second), status(third))
	}

	if _, err := pool.Exec(t.Context(), "UPDATE deprecated_api_key SET revoke_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if status(first) != 401 {
		t.Errorf("유예가 끝났는데 옛 키가 된다: %d", status(first))
	}
	for _, bad := range []string{"soon", "0h"} { // litellm 처럼 틀린 값은 무시하고 바로 폐기한다
		next := api.ok("POST", "/key/"+third+"/regenerate", map[string]any{"grace_period": bad})["key"].(string)
		if status(third) != 401 {
			t.Errorf("grace_period=%q 인데 옛 키가 된다", bad)
		}
		third = next
	}
}

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
