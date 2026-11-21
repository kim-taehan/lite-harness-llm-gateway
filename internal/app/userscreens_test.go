package app

import (
	"strings"
	"testing"
)

// 사용자 화면의 역할 목록·검색·일괄 수정·팀 일괄 추가(I 3.2~3.4, 2.10)

func TestUserScreens_RolesSearchAndBulkEdits(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	for _, u := range []string{"kim", "kang", "lee"} {
		api.ok("POST", "/user/new", map[string]any{"user_id": u, "user_email": u + "@corp.local"})
	}
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	master := map[string]string{"Authorization": "Bearer " + testMasterKey}

	// 역할 순서는 litellm 선언 순서 그대로(화면이 그 순서로 그린다)
	roles := b.do("GET", "/user/available_roles", nil, master).Body.String()
	if strings.Index(roles, "proxy_admin_viewer") > strings.Index(roles, "internal_user\"") || !strings.Contains(roles, "Admin (All Permissions)") {
		t.Errorf("역할 = %s", roles)
	}
	found := b.do("GET", "/user/filter/ui?user_email=K", nil, master).Body.String()
	if !strings.Contains(found, `"user_id":"kim"`) || !strings.Contains(found, `"user_id":"kang"`) || strings.Contains(found, "lee") || !strings.HasPrefix(found, "[") {
		t.Errorf("검색 = %s", found)
	}

	updated := api.ok("POST", "/user/bulk_update", map[string]any{"users": []map[string]any{
		{"user_id": "kim", "user_role": "internal_user_viewer", "max_budget": 5},
		{"user_id": "nobody", "user_role": "internal_user_viewer"},
	}})
	if updated["successful_updates"] != 1.0 || updated["failed_updates"] != 1.0 {
		t.Errorf("선택 일괄 수정 = %v", updated)
	}
	// 전체 = 만든 3명 + 팀을 만든 default_user_id(litellm 처럼 팀 생성자가 사용자로 생긴다)
	if all := api.ok("POST", "/user/bulk_update", map[string]any{"all_users": true, "user_updates": map[string]any{"models": []string{"m1"}}}); all["total_requested"] != 4.0 || all["successful_updates"] != 4.0 {
		t.Errorf("전체 일괄 수정 = %v", all)
	}
	if info := api.ok("GET", "/user/info?user_id=lee", nil)["user_info"].(map[string]any); len(info["models"].([]any)) != 1 {
		t.Errorf("전체 수정 결과 = %v", info["models"])
	}

	added := api.ok("POST", "/team/bulk_member_add", map[string]any{"team_id": "t1", "members": []map[string]any{{"user_id": "kim", "role": "user"}, {"user_id": "lee", "role": "user"}}})
	if added["successful_additions"] != 2.0 || added["failed_additions"] != 0.0 {
		t.Errorf("팀 일괄 추가 = %v", added)
	}
	again := api.ok("POST", "/team/bulk_member_add", map[string]any{"team_id": "t1", "members": []map[string]any{{"user_id": "kim"}}})
	if again["successful_additions"] != 0.0 || again["results"].([]any)[0].(map[string]any)["error"] == nil {
		t.Errorf("이미 있는 멤버는 200 의 실패 행 = %v", again)
	}
}
