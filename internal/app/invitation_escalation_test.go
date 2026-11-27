package app

import (
	"testing"
)

// 팀 admin 은 자기 팀 사용자에게 초대 링크를 만들 수 있다(litellm 과 같다). 그런데 대상이 관리자 계정이면
// 링크로 비밀번호를 새로 정해 그 관리자로 로그인할 수 있으므로 권한 상승이 된다(실제로 proxy_admin 전용 라우트까지 불렀다).
// 관리자 계정은 거부해야 한다
func TestInvitation_TeamAdminCannotInviteAdminAccounts(t *testing.T) {
	api := newAPI(t)
	b := browser{t: t, h: api.h}
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	api.ok("POST", "/user/new", map[string]any{"user_id": "tadmin", "user_role": "internal_user"})
	api.ok("POST", "/user/update", map[string]any{"user_id": "tadmin", "password": "Team-admin-2026"})
	api.ok("POST", "/user/new", map[string]any{"user_id": "boss", "user_role": "proxy_admin"})
	api.ok("POST", "/user/new", map[string]any{"user_id": "viewer", "user_role": "proxy_admin_viewer"})
	api.ok("POST", "/user/new", map[string]any{"user_id": "member", "user_role": "internal_user"})
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "t1", "member": map[string]any{"user_id": "tadmin", "role": "admin"}})
	for _, user := range []string{"boss", "viewer", "member"} {
		api.ok("POST", "/team/member_add", map[string]any{"team_id": "t1", "member": map[string]any{"user_id": user, "role": "user"}})
	}

	// 관리자가 정한 비밀번호는 본인이 바꿔야 세션을 쓸 수 있다
	_, _, cookie := dashboardLogin(t, b, "tadmin", "Team-admin-2026")
	first := map[string]string{"Authorization": "Bearer " + uiClaims(t, cookie)["key"].(string)}
	if rec := b.doJSON("POST", "/user/password/change", `{"current_password":"Team-admin-2026","new_password":"Own-pass-12345"}`, first); rec.Code != 200 {
		t.Fatalf("비밀번호 변경: %d %s", rec.Code, rec.Body.String())
	}
	_, _, cookie = dashboardLogin(t, b, "tadmin", "Own-pass-12345")
	asTeamAdmin := map[string]string{"Authorization": "Bearer " + uiClaims(t, cookie)["key"].(string)}
	invite := func(userID string) int {
		return b.doJSON("POST", "/invitation/new", `{"user_id":"`+userID+`"}`, asTeamAdmin).Code
	}

	if code := invite("member"); code != 200 {
		t.Errorf("팀 admin 이 일반 팀원을 초대하지 못한다(litellm 과 다르다): %d", code)
	}
	for _, admin := range []string{"boss", "viewer"} {
		if code := invite(admin); code == 200 {
			t.Errorf("팀 admin 이 같은 팀의 관리자 계정(%s)에게 초대 링크를 만들었다 — 그 링크로 관리자 비밀번호를 새로 정할 수 있다", admin)
		}
	}
	// proxy_admin 본인은 누구든 초대할 수 있다
	if status, _ := api.do("POST", "/invitation/new", map[string]any{"user_id": "boss"}); status != 200 {
		t.Errorf("proxy_admin 이 관리자 계정을 초대하지 못한다: %d", status)
	}
}
