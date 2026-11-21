package auth

import (
	"errors"
	"os"
	"testing"
	"time"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

func newIdentity(t *testing.T) (*Teams, *Users) {
	db, _ := dbtest.Open(t)
	cache := NewKeyCache(time.Minute, time.Now)
	users := NewUsers(db, cache)
	return NewTeams(db, users, cache), users
}

func ptr[T any](v T) *T { return &v }

func TestCreateTeam_AddsCreatorAsAdminLikeLitellm(t *testing.T) {
	teams, users := newIdentity(t)
	ctx := t.Context()

	team, err := teams.Create(ctx, NewTeam{TeamAlias: ptr("research")}, "default_user_id")
	if err != nil {
		t.Fatal(err)
	}
	if len(team.TeamID) != 36 {
		t.Errorf("team_id 가 uuid 가 아니다: %q", team.TeamID)
	}
	members, _ := teams.Members(ctx, []string{team.TeamID})
	if got := members[team.TeamID]; len(got) != 1 || got[0].UserID != "default_user_id" || got[0].Role != TeamRoleAdmin {
		t.Errorf("팀원 = %+v", got)
	}
	creator, err := users.Find(ctx, "default_user_id")
	if err != nil || creator.UserRole == nil || *creator.UserRole != core.RoleInternalUser {
		t.Errorf("호출자 사용자 행 = %+v (%v)", creator, err)
	}
}

func TestCreateTeam_RejectsTakenAndReservedIDs(t *testing.T) {
	teams, _ := newIdentity(t)
	ctx := t.Context()
	if _, err := teams.Create(ctx, NewTeam{TeamID: ptr("t1")}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := teams.Create(ctx, NewTeam{TeamID: ptr("t1")}, ""); !errors.Is(err, ErrTeamIDTaken) {
		t.Errorf("중복 id: %v", err)
	}
	if _, err := teams.Create(ctx, NewTeam{TeamID: ptr("litellm-dashboard")}, ""); !errors.Is(err, ErrTeamIDReserved) {
		t.Errorf("예약 id: %v", err)
	}
}

func TestAddMembers_CreatesUsersAndHandlesDuplicatesLikeLitellm(t *testing.T) {
	teams, users := newIdentity(t)
	ctx := t.Context()
	_, _ = teams.Create(ctx, NewTeam{TeamID: ptr("t1")}, "")

	_, added, err := teams.AddMembers(ctx, "t1", []MemberRef{{UserEmail: ptr("A@x.io"), Role: TeamRoleUser}}, true)
	if err != nil || len(added) != 1 {
		t.Fatalf("추가 실패: %v", err)
	}
	if _, err := users.FindByEmail(ctx, "a@X.io"); err != nil {
		t.Errorf("이메일로 넣은 사용자가 만들어지지 않았다(대소문자 무시): %v", err)
	}

	_, _, err = teams.AddMembers(ctx, "t1", []MemberRef{{UserEmail: ptr("a@x.io"), Role: TeamRoleUser}}, true)
	var already *AlreadyMemberError
	if !errors.As(err, &already) || !already.Single {
		t.Errorf("이미 있는 멤버 하나: %v", err)
	}

	// 일부만 이미 있으면 그것만 건너뛴다
	_, added, err = teams.AddMembers(ctx, "t1", []MemberRef{
		{UserEmail: ptr("a@x.io"), Role: TeamRoleUser},
		{UserID: ptr("u2"), Role: TeamRoleAdmin},
	}, false)
	if err != nil || len(added) != 1 || added[0].UserID != "u2" {
		t.Errorf("일부 중복: %v %+v", err, added)
	}

	if _, _, err := teams.AddMembers(ctx, "nope", []MemberRef{{UserID: ptr("u3"), Role: TeamRoleUser}}, true); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("없는 팀: %v", err)
	}
}

func TestAddMembers_RejectsEmailOfAnotherUser(t *testing.T) {
	teams, users := newIdentity(t)
	ctx := t.Context()
	_, _ = teams.Create(ctx, NewTeam{TeamID: ptr("t1")}, "")
	_, _, _ = users.Create(ctx, NewUser{UserID: ptr("owner"), UserEmail: ptr("o@x.io")})

	_, _, err := teams.AddMembers(ctx, "t1", []MemberRef{{UserID: ptr("other"), UserEmail: ptr("o@x.io"), Role: TeamRoleUser}}, true)
	var mismatch *EmailOwnerMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v", err)
	}
	if _, err := users.Find(ctx, "other"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("거절됐는데 사용자가 만들어졌다(트랜잭션이 되돌려지지 않음)")
	}
}

func TestRemoveAndUpdateMember(t *testing.T) {
	teams, _ := newIdentity(t)
	ctx := t.Context()
	_, _ = teams.Create(ctx, NewTeam{TeamID: ptr("t1"), Members: []MemberRef{{UserID: ptr("u1"), UserEmail: ptr("u1@x.io"), Role: TeamRoleUser}}}, "")

	admin := TeamRoleAdmin
	if id, err := teams.UpdateMember(ctx, "t1", nil, ptr("U1@x.io"), &admin); err != nil || id != "u1" {
		t.Errorf("이메일로 역할 변경: %q %v", id, err)
	}
	members, _ := teams.Members(ctx, []string{"t1"})
	if members["t1"][0].Role != TeamRoleAdmin {
		t.Errorf("역할이 바뀌지 않았다")
	}
	if _, err := teams.RemoveMember(ctx, "t1", ptr("u1"), nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := teams.RemoveMember(ctx, "t1", ptr("u1"), nil, ""); !errors.Is(err, ErrNotMember) {
		t.Errorf("두 번째 제거: %v", err)
	}
	if _, err := teams.UpdateMember(ctx, "nope", ptr("u1"), nil, nil); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("없는 팀: %v", err)
	}
}

func TestDeleteTeams_AllOrNothing(t *testing.T) {
	teams, _ := newIdentity(t)
	ctx := t.Context()
	_, _ = teams.Create(ctx, NewTeam{TeamID: ptr("t1")}, "")

	var notFound *NotFoundError
	if err := teams.Delete(ctx, []string{"t1", "missing"}, ""); !errors.As(err, &notFound) || notFound.ID != "missing" {
		t.Fatalf("err = %v", err)
	}
	if _, err := teams.Find(ctx, "t1"); err != nil {
		t.Errorf("하나가 없는데 다른 팀이 지워졌다")
	}
	if err := teams.Delete(ctx, []string{"t1"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := teams.Find(ctx, "t1"); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("지워지지 않았다")
	}
}

func TestTeamPage_FiltersLikeLitellmV2(t *testing.T) {
	teams, _ := newIdentity(t)
	ctx := t.Context()
	for _, n := range []struct{ id, alias string }{{"team-a1", "Alpha"}, {"team-a2", "beta"}, {"other", "alphabet"}} {
		_, _ = teams.Create(ctx, NewTeam{TeamID: ptr(n.id), TeamAlias: ptr(n.alias)}, "")
	}
	_, _, _ = teams.AddMembers(ctx, "other", []MemberRef{{UserID: ptr("u1"), Role: TeamRoleUser}}, true)

	check := func(name string, q TeamQuery, want ...string) {
		t.Helper()
		q.Page, q.PageSize = 1, 10
		page, err := teams.Page(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, team := range page.Items {
			got = append(got, team.TeamID)
		}
		if len(got) != len(want) || int(page.Total) != len(want) {
			t.Errorf("%s: %v (total %d), 기대 %v", name, got, page.Total, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: %v, 기대 %v", name, got, want)
				return
			}
		}
	}
	check("별칭 부분 일치(대소문자 무시)", TeamQuery{TeamAliasContains: ptr("ALPHA"), SortBy: "team_id"}, "other", "team-a1")
	check("검색 접두어", TeamQuery{Search: ptr("team-a"), SearchTeamIDPrefix: true, SortBy: "team_id"}, "team-a1", "team-a2")
	check("검색 정확히", TeamQuery{Search: ptr("team-a"), SortBy: "team_id"})
	check("팀원 필터", TeamQuery{MemberUserID: ptr("u1")}, "other")
	check("정렬 내림차순", TeamQuery{SortBy: "team_alias", SortOrder: SortDesc}, "team-a2", "other", "team-a1")
}

func TestUsers_CreateUpdateDeleteLikeLitellm(t *testing.T) {
	teams, users := newIdentity(t)
	ctx := t.Context()
	_, _ = teams.Create(ctx, NewTeam{TeamID: ptr("t1")}, "")

	user, teamIDs, err := users.Create(ctx, NewUser{UserEmail: ptr("Kim@x.io"), TeamID: ptr("t1")})
	if err != nil || user.UserRole != nil || len(teamIDs) != 1 {
		t.Fatalf("생성: %+v %v %v (역할을 안 보내면 null 이어야 한다)", user, teamIDs, err)
	}
	var taken *EmailTakenError
	if _, _, err := users.Create(ctx, NewUser{UserEmail: ptr("kim@X.io")}); !errors.As(err, &taken) || taken.Existing != "Kim@x.io" {
		t.Errorf("이메일 중복: %v", err)
	}
	if _, _, err := users.Create(ctx, NewUser{UserID: ptr(user.UserID)}); !errors.Is(err, ErrUserIDTaken) {
		t.Errorf("id 중복: %v", err)
	}
	if _, _, err := users.Create(ctx, NewUser{UserID: ptr("u9"), TeamID: ptr("nope")}); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("없는 팀: %v", err)
	}
	if _, err := users.Find(ctx, "u9"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("없는 팀으로 실패했는데 사용자가 남았다")
	}

	updated, err := users.Update(ctx, user.UserID, UserPatch{UserAlias: core.To("kim"), UserRole: core.To(core.RoleProxyAdminViewer)})
	if err != nil || deref(updated.UserAlias) != "kim" || *updated.UserRole != core.RoleProxyAdminViewer || deref(updated.UserEmail) != "Kim@x.io" {
		t.Errorf("수정: %+v %v", updated, err)
	}
	if _, err := users.Update(ctx, "nope", UserPatch{}); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("없는 사용자 수정은 만들지 않고 ErrUserNotFound 여야 한다(설계 9장): %v", err)
	}

	n, err := users.Delete(ctx, []string{user.UserID})
	if err != nil || n != 1 {
		t.Errorf("삭제: %d %v", n, err)
	}
	if members, _ := teams.Members(ctx, []string{"t1"}); len(members["t1"]) != 0 {
		t.Errorf("지운 사용자가 팀원으로 남았다")
	}
}

func TestUserPage_UserIDsMatchLikeLitellm(t *testing.T) {
	_, users := newIdentity(t)
	ctx := t.Context()
	for _, id := range []string{"alice", "alicia", "bob"} {
		_, _, _ = users.Create(ctx, NewUser{UserID: ptr(id)})
	}
	one, _ := users.Page(ctx, UserQuery{UserIDs: []string{"ALI"}, Page: 1, PageSize: 25})
	many, _ := users.Page(ctx, UserQuery{UserIDs: []string{"alice", "bob"}, Page: 1, PageSize: 25})
	if one.Total != 2 || many.Total != 2 {
		t.Errorf("하나면 부분 일치 %d(기대 2), 여럿이면 정확히 %d(기대 2)", one.Total, many.Total)
	}
}

func TestSSOUser_NewUserGetsDefaultUserSettings(t *testing.T) {
	db, _ := dbtest.Open(t)
	users := NewUsers(db, NewKeyCache(time.Minute, time.Now))
	ctx := t.Context()

	first, err := users.upsertSSOUser(ctx, oidcClaims{Subject: "s1", Email: "Kim@corp.local", Name: "김"})
	if err != nil || first.UserID != "kim@corp.local" || *first.UserRole != core.RoleInternalUserViewer || len(first.Models) != 0 {
		t.Fatalf("기본값(litellm) = %+v %v", first, err)
	}
	if _, err := core.NewSettings(db).Merge(ctx, "default_internal_user_params", map[string]any{"user_role": "internal_user", "models": []string{"m1"}}); err != nil {
		t.Fatal(err)
	}
	second, _ := users.upsertSSOUser(ctx, oidcClaims{Subject: "s2", Email: "lee@corp.local"})
	if *second.UserRole != core.RoleInternalUser || len(second.Models) != 1 || second.Models[0] != "m1" {
		t.Errorf("대시보드 기본 사용자 설정 = %+v", second)
	}
	// 이미 있는 사용자의 역할은 바꾸지 않는다
	again, _ := users.upsertSSOUser(ctx, oidcClaims{Subject: "s1", Email: "kim@corp.local"})
	if *again.UserRole != core.RoleInternalUserViewer {
		t.Errorf("기존 사용자 역할이 바뀌었다: %+v", again)
	}
}
