package admin

import (
	"context"
	"slices"

	"llm-gateway/internal/auth"
)

// 핸들러 단계 권한(litellm H 2.4·J 3장). 라우트 단계를 통과한 비관리자를 자원 기준으로 다시 본다

// teamRole 은 사용자의 그 팀 역할이다. 팀원이 아니면 ok=false
func (h *Handlers) teamRole(ctx context.Context, teamID, userID string) (auth.TeamRole, bool, error) {
	if userID == "" {
		return "", false, nil
	}
	members, err := h.teams.Members(ctx, []string{teamID})
	if err != nil {
		return "", false, err
	}
	for _, m := range members[teamID] {
		if m.UserID == userID {
			return m.Role, true, nil
		}
	}
	return "", false, nil
}

// isTeamAdmin 은 litellm TEAM_OR_ORG_ADMIN 의 팀 부분이다(조직은 없다): proxy_admin 이거나 그 팀의 admin 멤버
func (h *Handlers) isTeamAdmin(ctx context.Context, p auth.Principal, teamID string) (bool, error) {
	if p.IsAdmin() {
		return true, nil
	}
	role, member, err := h.teamRole(ctx, teamID, p.UserID)
	return member && role == auth.TeamRoleAdmin, err
}

// teamsOf 는 사용자가 속한 팀과 admin 인 팀이다
func (h *Handlers) teamsOf(ctx context.Context, userID string) (member, admin []string, err error) {
	if userID == "" {
		return nil, nil, nil
	}
	teamIDs, err := h.users.TeamIDs(ctx, []string{userID})
	if err != nil {
		return nil, nil, err
	}
	member = teamIDs[userID]
	members, err := h.teams.Members(ctx, member)
	if err != nil {
		return nil, nil, err
	}
	for _, teamID := range member {
		for _, m := range members[teamID] {
			if m.UserID == userID && m.Role == auth.TeamRoleAdmin {
				admin = append(admin, teamID)
			}
		}
	}
	return member, admin, nil
}

// sharesAdminTeam 은 caller 가 admin 인 팀에 target 이 속해 있는지다(litellm /v2/user/info 의 팀 admin 허용)
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
