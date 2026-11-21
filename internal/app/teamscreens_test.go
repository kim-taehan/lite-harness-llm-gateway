package app

import (
	"strings"
	"testing"
)

// 팀 화면의 권한 탭·내 멤버십·팀원 초기화(I 2.2~2.9)

func TestTeamScreens_MemberPermissionsGateTeamKeys(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	api.ok("POST", "/user/new", map[string]any{"user_id": "kim", "user_email": "kim@corp.local", "user_role": "internal_user"})
	api.ok("POST", "/user/new", map[string]any{"user_id": "lee", "user_role": "internal_user"})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "team_alias": "팀1"})
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "t1", "member": map[string]any{"user_id": "kim", "role": "admin"}})
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "t1", "member": map[string]any{"user_id": "lee", "role": "user"}})
	bearer := func(user string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + api.ok("POST", "/key/generate", map[string]any{"user_id": user})["key"].(string)}
	}
	kim, lee := bearer("kim"), bearer("lee")

	// 기본 권한으로는 일반 멤버가 팀 키를 못 만든다
	if rec := b.doJSON("POST", "/key/generate", `{"team_id":"t1"}`, lee); rec.Code != 401 || !strings.Contains(rec.Body.String(), "team_member_permission_error") {
		t.Fatalf("기본 권한: %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.do("GET", "/team/permissions_list?team_id=t1", nil, lee); rec.Code != 403 {
		t.Errorf("일반 멤버의 권한 목록: %d", rec.Code)
	}
	list := jsonOf(t, b.do("GET", "/team/permissions_list?team_id=t1", nil, kim))
	if perms := list["team_member_permissions"].([]any); len(perms) != 2 || len(list["all_available_permissions"].([]any)) < 10 {
		t.Errorf("권한 목록 = %v", list)
	}
	// 팀 admin 이 /key/generate 를 주면 바로 된다
	if rec := b.doJSON("POST", "/team/permissions_update", `{"team_id":"t1","team_member_permissions":["/key/info","/key/generate"]}`, kim); rec.Code != 200 {
		t.Fatalf("권한 변경: %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.doJSON("POST", "/key/generate", `{"team_id":"t1"}`, lee); rec.Code != 200 {
		t.Errorf("권한을 받은 뒤 팀 키: %d %s", rec.Code, rec.Body.String())
	}
	if info := api.ok("GET", "/team/info?team_id=t1", nil)["team_info"].(map[string]any); len(info["team_member_permissions"].([]any)) != 2 {
		t.Errorf("팀 정보의 권한 = %v", info["team_member_permissions"])
	}

	// 내 멤버십: 멤버면 역할, 아니면 404(대시보드는 404 를 "멤버 아님"으로 읽는다)
	me := jsonOf(t, b.do("GET", "/team/t1/members/me", nil, lee))
	if me["role"] != "user" || me["team_alias"] != "팀1" || me["spend"] != 0.0 {
		t.Errorf("members/me = %v", me)
	}
	api.ok("POST", "/user/new", map[string]any{"user_id": "park", "user_role": "proxy_admin"})
	if rec := b.do("GET", "/team/t1/members/me", nil, bearer("park")); rec.Code != 404 {
		t.Errorf("멤버 아닌 관리자: %d", rec.Code)
	}

	// 팀원 예산 초기화: 팀 admin 만, 일반 멤버는 문자열 detail 403
	if rec := b.do("POST", "/team/t1/member/lee/reset_budget", nil, kim); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"budget_source":"none"`) {
		t.Errorf("reset_budget: %d %s", rec.Code, rec.Body.String())
	}
	if rec := b.do("POST", "/team/t1/member/kim/reset_budget", nil, lee); rec.Code != 403 || !strings.Contains(rec.Body.String(), `"detail":"You do not have access to this team"`) {
		t.Errorf("일반 멤버의 reset_budget: %d %s", rec.Code, rec.Body.String())
	}
	bulk := jsonOf(t, b.doJSON("POST", "/management/v1/teams/t1/members/bulk_update",
		`{"members":[{"user_id":"lee","max_budget_in_team":null},{"user_id":"lee","max_budget_in_team":5},{"user_id":"nobody"}]}`, kim))
	rows := bulk["data"].([]any)
	if rows[0].(map[string]any)["success"] != true || rows[1].(map[string]any)["success"] != false || rows[2].(map[string]any)["success"] != false {
		t.Errorf("bulk_update = %v", bulk)
	}
}
