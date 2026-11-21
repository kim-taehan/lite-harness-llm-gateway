package admin

import (
	"context"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/limits"
)

// 응답 조립. auth(신원)와 limits(한도) 값을 합쳐 litellm 행 모양을 만든다.
// 만들지 않는 기능의 필드는 litellm 기본값으로 채워 대시보드가 깨지지 않게 한다.

type row = map[string]any

// teamRow 는 LiteLLM_TeamTable(litellm/models/team.py) 과 같은 필드다
func teamRow(t auth.Team, members []auth.TeamMember, p limits.Policy) row {
	memberRows := make([]row, 0, len(members))
	for _, m := range members {
		memberRows = append(memberRows, memberRow(m))
	}
	return row{
		"team_alias":                  t.TeamAlias,
		"team_id":                     t.TeamID,
		"organization_id":             nil,
		"admins":                      []string{},
		"members":                     []string{},
		"members_with_roles":          memberRows,
		"team_member_permissions":     t.MemberPermissions,
		"metadata":                    t.Metadata,
		"tpm_limit":                   p.TPMLimit,
		"rpm_limit":                   p.RPMLimit,
		"tpd_limit":                   nil,
		"max_budget":                  p.MaxBudget,
		"soft_budget":                 p.SoftBudget,
		"budget_duration":             p.BudgetDuration,
		"budget_limits":               nil,
		"models":                      nonNil(t.Models),
		"blocked":                     t.Blocked,
		"router_settings":             nil,
		"access_group_ids":            nil,
		"default_team_member_models":  nil,
		"spend":                       p.Spend,
		"max_parallel_requests":       p.MaxParallelRequests,
		"budget_reset_at":             utc(p.BudgetResetAt),
		"model_id":                    nil,
		"model_spend":                 map[string]any{},
		"model_max_budget":            map[string]any{},
		"policies":                    nil,
		"allow_team_guardrail_config": false,
		"litellm_model_table":         nil,
		"object_permission":           nil,
		"object_permission_id":        nil,
		"updated_at":                  t.UpdatedAt.UTC(),
		"created_at":                  t.CreatedAt.UTC(),
	}
}

func memberRow(m auth.TeamMember) row {
	return row{"user_id": m.UserID, "user_email": m.UserEmail, "role": m.Role}
}

// membershipRow 는 LiteLLM_TeamMembership 이다. 팀원 한도가 있으면 예산 표(litellm_budget_table)로 싣는다.
// 예산 행 id 는 따로 없어 팀원 한도의 대상 id 를 쓴다
func membershipRow(m auth.TeamMember, p limits.Policy) row {
	target := limits.MemberTarget(m.TeamID, m.UserID)
	result := row{"user_id": m.UserID, "team_id": m.TeamID, "spend": p.Spend, "total_spend": p.Spend, "budget_id": nil, "budget_source": "none",
		"litellm_budget_table": nil}
	if p.HasLimits() {
		result["budget_id"], result["budget_source"] = target, "member"
		result["litellm_budget_table"] = row{"budget_id": target, "soft_budget": p.SoftBudget, "max_budget": p.MaxBudget,
			"max_parallel_requests": p.MaxParallelRequests, "tpm_limit": p.TPMLimit, "rpm_limit": p.RPMLimit, "model_max_budget": nil,
			"budget_duration": p.BudgetDuration, "budget_reset_at": p.BudgetResetAt, "allowed_models": nil}
	}
	return result
}

// membershipRows 는 팀원들의 한도를 쿼리 한 번으로 읽어 만든 team_memberships 행들이다
func (b rows) membershipRows(ctx context.Context, members []auth.TeamMember) ([]row, error) {
	targets := make([]string, len(members))
	for i, m := range members {
		targets[i] = limits.MemberTarget(m.TeamID, m.UserID)
	}
	policies, err := b.limits.Find(ctx, limits.ScopeTeamMember, targets)
	if err != nil {
		return nil, err
	}
	result := make([]row, len(members))
	for i, m := range members {
		result[i] = membershipRow(m, policies[targets[i]])
	}
	return result, nil
}

// userRow 는 LiteLLM_UserTable(litellm/models/user.py) 과 같은 필드다. password 는 내보내지 않는다
func userRow(u auth.User, teamIDs []string, p limits.Policy) row {
	return row{
		"user_id":                  u.UserID,
		"user_alias":               u.UserAlias,
		"team_id":                  nil,
		"sso_user_id":              u.SSOUserID,
		"organization_id":          nil,
		"object_permission_id":     nil,
		"password_reset_required":  u.PasswordResetRequired,
		"last_breach_check_at":     nil,
		"teams":                    nonNil(teamIDs),
		"user_role":                u.UserRole,
		"max_budget":               p.MaxBudget,
		"spend":                    p.Spend,
		"user_email":               u.UserEmail,
		"models":                   nonNil(u.Models),
		"metadata":                 u.Metadata,
		"max_parallel_requests":    p.MaxParallelRequests,
		"tpm_limit":                p.TPMLimit,
		"rpm_limit":                p.RPMLimit,
		"budget_duration":          p.BudgetDuration,
		"budget_reset_at":          utc(p.BudgetResetAt),
		"allowed_cache_controls":   []string{},
		"policies":                 []string{},
		"model_spend":              map[string]any{},
		"model_max_budget":         map[string]any{},
		"created_at":               u.CreatedAt.UTC(),
		"updated_at":               u.UpdatedAt.UTC(),
		"organization_memberships": nil,
		"object_permission":        nil,
	}
}

// rows 는 목록 단위로 팀원·한도를 한 번씩만 읽어 행을 만든다(N+1 방지)
type rows struct {
	teams  *auth.Teams
	users  *auth.Users
	limits *limits.Policies
}

func (b rows) teamRows(ctx context.Context, teams []auth.Team) ([]row, map[string][]auth.TeamMember, error) {
	ids := make([]string, len(teams))
	for i, t := range teams {
		ids[i] = t.TeamID
	}
	members, err := b.teams.Members(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	policies, err := b.limits.Find(ctx, limits.ScopeTeam, ids)
	if err != nil {
		return nil, nil, err
	}
	result := make([]row, len(teams))
	for i, t := range teams {
		result[i] = teamRow(t, members[t.TeamID], policies[t.TeamID])
	}
	return result, members, nil
}

func (b rows) teamRowOf(ctx context.Context, t auth.Team) (row, error) {
	r, _, err := b.teamRows(ctx, []auth.Team{t})
	if err != nil {
		return nil, err
	}
	return r[0], nil
}

func (b rows) userRows(ctx context.Context, users []auth.User) ([]row, error) {
	ids := make([]string, len(users))
	for i, u := range users {
		ids[i] = u.UserID
	}
	teamIDs, err := b.users.TeamIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	policies, err := b.limits.Find(ctx, limits.ScopeUser, ids)
	if err != nil {
		return nil, err
	}
	result := make([]row, len(users))
	for i, u := range users {
		result[i] = userRow(u, teamIDs[u.UserID], policies[u.UserID])
	}
	return result, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// pageOf 는 목록의 한 쪽(offset 부터 limit 개)이다. 범위를 넘으면 빈 목록이다
func pageOf[T any](items []T, offset, limit int) []T {
	start := min(max(offset, 0), len(items))
	return items[start:min(start+limit, len(items))]
}

func totalPages(total int64, size int) int64 {
	if total == 0 {
		return 0
	}
	return (total + int64(size) - 1) / int64(size)
}
