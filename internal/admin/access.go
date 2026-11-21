package admin

import (
	"context"
	"slices"

	"llm-gateway/internal/auth"
)

// 핸들러 단계 권한(litellm H 2.4·J 3장). 라우트 단계를 통과한 비관리자를 자원 기준으로 다시 본다.
// 팀 소속 조회는 auth.Teams 의 MembershipIn·MembershipsOf 한 가지 길로 한다(쿼리 하나, 팀 명단을 읽지 않는다)

// 팀 일반 멤버(role=user)가 기본으로 부를 수 있는 키 라우트(litellm TMP:18-23)
var defaultTeamMemberKeyRoutes = []string{"/key/info", "/key/health"}

// memberKeyRoutes 는 팀 일반 멤버가 부를 수 있는 키 라우트다. 팀 관리 화면(/team/permissions_update)에서 바꾼다
func memberKeyRoutes(permissions []string) []string {
	if permissions != nil {
		return permissions
	}
	return defaultTeamMemberKeyRoutes
}

// mayCall 은 litellm does_team_member_have_permissions_for_endpoint 다: 팀 admin 은 모든 팀 키 라우트,
// 일반 멤버는 팀 권한 목록에 그 라우트가 있을 때만
func mayCall(m auth.Membership, route string) bool {
	return m.Role == auth.TeamRoleAdmin || slices.Contains(memberKeyRoutes(m.MemberPermissions), route)
}

// membership 은 사용자의 그 팀 소속이다. 팀원이 아니면 ok=false
func (h *Handlers) membership(ctx context.Context, teamID, userID string) (auth.Membership, bool, error) {
	if userID == "" {
		return auth.Membership{}, false, nil
	}
	return h.teams.MembershipIn(ctx, teamID, userID)
}

// teamRole 은 사용자의 그 팀 역할이다. 팀원이 아니면 ok=false
func (h *Handlers) teamRole(ctx context.Context, teamID, userID string) (auth.TeamRole, bool, error) {
	m, member, err := h.membership(ctx, teamID, userID)
	return m.Role, member, err
}

// isTeamAdmin 은 litellm TEAM_OR_ORG_ADMIN 의 팀 부분이다(조직은 없다): proxy_admin 이거나 그 팀의 admin 멤버
func (h *Handlers) isTeamAdmin(ctx context.Context, p auth.Principal, teamID string) (bool, error) {
	if p.IsAdmin() {
		return true, nil
	}
	role, member, err := h.teamRole(ctx, teamID, p.UserID)
	return member && role == auth.TeamRoleAdmin, err
}

// teamMemberMay 는 proxy_admin 이거나 그 팀에서 route 를 부를 수 있는 멤버인지다
func (h *Handlers) teamMemberMay(ctx context.Context, p auth.Principal, teamID, route string) (bool, error) {
	if p.IsAdmin() {
		return true, nil
	}
	m, member, err := h.membership(ctx, teamID, p.UserID)
	return member && mayCall(m, route), err
}

// teamsOf 는 사용자가 속한 팀과 admin 인 팀이다
func (h *Handlers) teamsOf(ctx context.Context, userID string) (member, admin []string, err error) {
	if userID == "" {
		return nil, nil, nil
	}
	memberships, err := h.teams.MembershipsOf(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range memberships {
		member = append(member, m.TeamID)
		if m.Role == auth.TeamRoleAdmin {
			admin = append(admin, m.TeamID)
		}
	}
	return member, admin, nil
}

// sharesAdminTeam 은 caller 가 admin 인 팀에 target 이 속해 있는지다(litellm /v2/user/info 의 팀 admin 허용, 팀원 초대)
func (h *Handlers) sharesAdminTeam(ctx context.Context, caller, target string) (bool, error) {
	_, adminTeams, err := h.teamsOf(ctx, caller)
	if err != nil || len(adminTeams) == 0 {
		return false, err
	}
	targetTeams, err := h.users.TeamIDs(ctx, []string{target})
	if err != nil {
		return false, err
	}
	for _, t := range targetTeams[target] {
		if slices.Contains(adminTeams, t) {
			return true, nil
		}
	}
	return false, nil
}

func principal(ctx context.Context) auth.Principal {
	p, _ := auth.PrincipalFrom(ctx)
	return p
}
