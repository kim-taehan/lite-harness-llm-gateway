package app

import (
	"net/url"
	"strings"
	"testing"

	"llm-gateway/internal/auth"
)

// SSO 로그인 때 Keycloak 그룹으로 팀원 기록과 관리자 역할을 맞춘다. 그룹 표는 JWT 직접 호출과 같다

func groupApp(t *testing.T, idp *fakeIdP) (browser, apiClient) {
	t.Helper()
	return ssoAppWith(t, idp, func(cfg *Config) {
		cfg.Groups = auth.GroupMapping{Groups: map[string]auth.GroupGrant{
			"eng": {TeamID: "research"}, "ops": {Admin: true}, "ghost-group": {TeamID: "ghost"}}}
	})
}

// loginAs 는 그룹을 가진(groups 가 nil 이면 클레임이 없는) 사용자의 대시보드 SSO 로그인을 끝까지 한다
func loginAs(t *testing.T, b browser, idp *fakeIdP, email, sub string, groups []string) int {
	t.Helper()
	claims := map[string]any{"sub": sub, "email": email, "name": "김", "email_verified": true}
	if groups != nil {
		claims["groups"] = groups
	}
	idp.setClaims(claims)
	start := b.do("GET", "/sso/key/generate?return_to="+url.QueryEscape("http://gw.test/ui/login/"), nil, nil)
	state := authorizeState(t, start)
	back := b.do("GET", "/sso/callback?code=abc&state="+url.QueryEscape(state), nil, map[string]string{"Cookie": cookieHeader(start)})
	return back.Code
}

func teamMemberIDs(api apiClient, teamID string) []string {
	var ids []string
	team := api.ok("GET", "/team/info?team_id="+teamID, nil)
	members, _ := team["team_info"].(map[string]any)["members_with_roles"].([]any)
	for _, m := range members {
		ids = append(ids, m.(map[string]any)["user_id"].(string))
	}
	return ids
}

func has(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func userRole(api apiClient, userID string) string {
	info := api.ok("GET", "/user/info?user_id="+url.QueryEscape(userID), nil)["user_info"].(map[string]any)
	role, _ := info["user_role"].(string)
	return role
}

func TestSSOGroups_LoginAddsTeamsPromotesAdminThenUndoesOnlyWhatTheGroupsGave(t *testing.T) {
	idp := newFakeIdP(t)
	b, api := groupApp(t, idp)
	api.ok("POST", "/team/new", map[string]any{"team_id": "research"})
	api.ok("POST", "/team/new", map[string]any{"team_id": "manual"}) // 그룹 표에 없는 팀: 수동 소속
	const kim = "kim@corp.local"

	// 그룹에 속해 로그인: 팀원이 되고 관리자가 된다
	if code := loginAs(t, b, idp, kim, "sub-kim", []string{"eng", "ops", "unmapped"}); code != 303 {
		t.Fatalf("로그인 = %d", code)
	}
	if !has(teamMemberIDs(api, "research"), kim) {
		t.Errorf("그룹 eng 로 팀 research 에 들어와야 한다: %v", teamMemberIDs(api, "research"))
	}
	if role := userRole(api, kim); role != "proxy_admin" {
		t.Errorf("그룹 ops 로 관리자여야 한다: %q", role)
	}

	// 수동으로 넣은 팀 소속과 그 팀의 키, 그룹 때문에 들어간 팀의 키
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "manual", "member": map[string]any{"user_id": kim, "role": "user"}})
	researchKey := api.ok("POST", "/key/generate", map[string]any{"user_id": kim, "team_id": "research"})["key"].(string)
	if status, _ := call(b.h, "GET", "/v1/models", researchKey, nil); status != 200 {
		t.Fatalf("팀 키가 동작해야 한다: %d", status)
	}

	// 모든 그룹을 떠난 다음 로그인(클레임은 빈 목록): 그룹이 준 것만 거둔다
	if code := loginAs(t, b, idp, kim, "sub-kim", []string{}); code != 303 {
		t.Fatalf("로그인 = %d", code)
	}
	if has(teamMemberIDs(api, "research"), kim) {
		t.Error("그룹을 떠났는데 팀 research 에 남아 있다")
	}
	if !has(teamMemberIDs(api, "manual"), kim) {
		t.Error("그룹 표에 없는 팀의 수동 소속이 지워졌다")
	}
	if role := userRole(api, kim); role != "internal_user_viewer" {
		t.Errorf("그룹이 올린 관리자는 원래 역할로 돌아와야 한다: %q", role)
	}
	if status, _ := call(b.h, "GET", "/v1/models", researchKey, nil); status != 401 {
		t.Errorf("팀에서 빠진 사용자의 그 팀 키가 아직 동작한다: %d", status)
	}

	// 감사 기록: 누가 어느 그룹 때문에 무엇을 바꿨는지
	var syncs []map[string]any
	for _, row := range api.ok("GET", "/audit?page_size=50", nil)["audit_logs"].([]any) {
		if r := row.(map[string]any); r["action"] == "group_sync" {
			syncs = append(syncs, r["updated_values"].(map[string]any))
		}
	}
	if len(syncs) != 2 {
		t.Fatalf("group_sync 감사 기록 = %d줄(2줄이어야 한다)", len(syncs))
	}
	// 최신순: 먼저 나온 것이 두 번째(거둠) 로그인이다
	if syncs[0]["admin"] != "demoted" || len(syncs[0]["teams_removed"].([]any)) != 1 ||
		syncs[1]["admin"] != "promoted" || len(syncs[1]["teams_added"].([]any)) != 1 ||
		strings.Join(toStrings(syncs[1]["matched_groups"]), ",") != "eng,ops" {
		t.Errorf("감사 기록 내용 = %v", syncs)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, item := range v.([]any) {
		out = append(out, item.(string))
	}
	return out
}

func TestSSOGroups_MissingGroupClaimChangesNothing(t *testing.T) {
	idp := newFakeIdP(t)
	b, api := groupApp(t, idp)
	api.ok("POST", "/team/new", map[string]any{"team_id": "research"})
	const kim = "kim@corp.local"
	loginAs(t, b, idp, kim, "sub-kim", []string{"eng", "ops"})

	// Keycloak 매퍼를 빼먹어 응답에 그룹 클레임이 아예 없다: 전원이 팀·관리자에서 빠지면 안 된다
	if code := loginAs(t, b, idp, kim, "sub-kim", nil); code != 303 {
		t.Fatalf("로그인 = %d", code)
	}
	if !has(teamMemberIDs(api, "research"), kim) || userRole(api, kim) != "proxy_admin" {
		t.Errorf("그룹 클레임이 없는 로그인이 소속을 바꿨다: %v %s", teamMemberIDs(api, "research"), userRole(api, kim))
	}
}

func TestSSOGroups_HandPickedAdminsStayAndAMissingTeamDoesNotBlockLogin(t *testing.T) {
	idp := newFakeIdP(t)
	b, api := groupApp(t, idp)
	const boss = "boss@corp.local"
	// 관리자가 손으로 만든 관리자
	api.ok("POST", "/user/new", map[string]any{"user_id": boss, "user_email": boss, "user_role": "proxy_admin"})
	if code := loginAs(t, b, idp, boss, "sub-boss", []string{"ops"}); code != 303 {
		t.Fatalf("로그인 = %d", code)
	}
	if code := loginAs(t, b, idp, boss, "sub-boss", []string{}); code != 303 {
		t.Fatalf("로그인 = %d", code)
	}
	if role := userRole(api, boss); role != "proxy_admin" {
		t.Errorf("손으로 지정한 관리자가 그룹 때문에 내려갔다: %q", role)
	}

	// 표가 가리키는 팀 ghost 가 없다: 표 오타로 로그인이 막히지 않는다
	if code := loginAs(t, b, idp, "lee@corp.local", "sub-lee", []string{"ghost-group"}); code != 303 {
		t.Errorf("없는 팀을 가리키는 그룹 때문에 로그인이 막혔다: %d", code)
	}
}

func TestSSOGroups_NoMappingMeansGroupsAreIgnored(t *testing.T) {
	idp := newFakeIdP(t)
	b, api := ssoApp(t, idp) // 그룹 표 없음
	api.ok("POST", "/team/new", map[string]any{"team_id": "research"})
	loginAs(t, b, idp, "kim@corp.local", "sub-kim", []string{"eng", "ops"})
	if has(teamMemberIDs(api, "research"), "kim@corp.local") || userRole(api, "kim@corp.local") == "proxy_admin" {
		t.Error("표가 없는데 그룹이 소속·역할을 바꿨다")
	}
}
