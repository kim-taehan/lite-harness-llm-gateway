package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// 팀 화면의 나머지 엔드포인트(I 2.2~2.9). 팀원별 한도는 limit_policy 의 team_member 종류(대상 id "팀:사용자")로 저장한다.
// litellm 의 팀 기본 팀원 예산(team_member_budget_id)과 팀원별 허용 모델은 아직 없다(설계 9장)

// allTeamMemberKeyRoutes 는 litellm key_management_routes 다(I 2.4). 팀 권한 화면이 체크박스로 그린다
var allTeamMemberKeyRoutes = []string{"/key/generate", "/key/update", "/key/delete", "/key/info", "/key/regenerate",
	"/key/service-account/generate", "/key/{key_id}/regenerate", "/key/list", "/key/block", "/key/unblock", "/key/bulk_update",
	"/team/key/bulk_update", "/team/daily/activity", "/team/daily/activity/aggregated", "/team/daily/activity/aggregated/keys",
	"/team/daily/activity/aggregated/search", "/team/daily/activity/aggregated/model_top_keys", "/team/daily/activity/export",
	"/spend/logs", "/spend/logs/v2", "/key/{key_id}/reset_spend", "/key/aliases", "/key/access_group_assignment", "/auto_router/manage"}

func teamMissing(w http.ResponseWriter, teamID string) {
	detailError(404, "Team doesn't exist in db. Team="+teamID+". Create team via `/team/new` call.").Write(w)
}

// availableTeams 는 스스로 가입할 수 있는 팀이다. litellm 은 config 파일의 available_teams 에서만 정하고 우리는 그 설정이 없어 늘 빈 목록이다
func (h *Handlers) availableTeams(w http.ResponseWriter, _ *http.Request) {
	core.WriteJSON(w, http.StatusOK, []any{})
}

// teamMetadataSchema 는 팀 메타데이터 입력란 정의다. litellm 도 설정(team_metadata_schema)이 없으면 빈 목록이다
func (h *Handlers) teamMetadataSchema(w http.ResponseWriter, _ *http.Request) {
	core.WriteJSON(w, http.StatusOK, row{"fields": []any{}})
}

// teamForAdmin 은 팀을 찾고 호출자가 그 팀의 admin 인지 본다. 아니면 오류를 쓰고 ok=false
func (h *Handlers) teamForAdmin(w http.ResponseWriter, r *http.Request, teamID string, denied func()) (auth.Team, bool) {
	team, err := h.teams.Find(r.Context(), teamID)
	if errors.Is(err, auth.ErrTeamNotFound) {
		teamMissing(w, teamID)
		return auth.Team{}, false
	}
	if err != nil {
		h.internalError(w, r, err)
		return auth.Team{}, false
	}
	allowed, err := h.isTeamAdmin(r.Context(), principal(r.Context()), teamID)
	if err != nil {
		h.internalError(w, r, err)
		return auth.Team{}, false
	}
	if !allowed {
		denied()
		return auth.Team{}, false
	}
	return team, true
}

func (h *Handlers) teamPermissionsList(w http.ResponseWriter, r *http.Request) {
	teamID := r.URL.Query().Get("team_id")
	// litellm 문구의 route 가 /team/member_add 로 박혀 있다(I 2.4). 대시보드가 보이는 문구라 그대로 둔다
	denied := func() {
		detailError(403, "Call not allowed. User not proxy admin OR team admin. route=/team/member_add, team_id="+teamID).Write(w)
	}
	p := principal(r.Context())
	var team auth.Team
	if p.CanViewAll() {
		found, err := h.teams.Find(r.Context(), teamID)
		if errors.Is(err, auth.ErrTeamNotFound) {
			teamMissing(w, teamID)
			return
		}
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		team = found
	} else {
		found, ok := h.teamForAdmin(w, r, teamID, denied)
		if !ok {
			return
		}
		team = found
	}
	core.WriteJSON(w, http.StatusOK, row{"team_id": team.TeamID, "team_member_permissions": memberKeyRoutes(team.MemberPermissions),
		"all_available_permissions": allTeamMemberKeyRoutes})
}

// teamPermissionsUpdate 는 팀 일반 멤버의 키 권한을 정한다. 키 라우트 판정(teamMemberMay)이 이 값을 바로 쓴다
func (h *Handlers) teamPermissionsUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TeamID      *string  `json:"team_id"`
		Permissions []string `json:"team_member_permissions"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	var errs validationError
	if req.TeamID == nil {
		errs = append(errs, missing(raw, "team_id"))
	}
	if req.Permissions == nil {
		errs = append(errs, missing(raw, "team_member_permissions"))
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	denied := func() {
		detailError(403, "Call not allowed. User not proxy admin OR team admin. route=/team/permissions_update, team_id="+*req.TeamID).Write(w)
	}
	if _, ok := h.teamForAdmin(w, r, *req.TeamID, denied); !ok {
		return
	}
	team, err := h.teams.Update(r.Context(), *req.TeamID, auth.TeamPatch{MemberPermissions: core.Change[[]string]{Set: true, Value: &req.Permissions}})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	teamRow, err := h.rows.teamRowOf(r.Context(), team)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, teamRow)
}

// myTeamMembership 은 GET /team/{team_id}/members/me 다. 멤버가 아니면 404 여야 대시보드가 "멤버 아님"으로 읽는다(proxy_admin 도)
func (h *Handlers) myTeamMembership(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, teamID := principal(ctx), r.PathValue("team_id")
	if p.UserID == "" {
		detailError(400, "API key has no associated user_id; cannot resolve 'me' for team membership.").Write(w)
		return
	}
	team, err := h.teams.Find(ctx, teamID)
	if errors.Is(err, auth.ErrTeamNotFound) {
		teamMissing(w, teamID)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	role, member, err := h.teamRole(ctx, teamID, p.UserID)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !member {
		detailError(404, "User user_id="+p.UserID+" is not a member of team_id="+teamID+".").Write(w)
		return
	}
	var email *string
	if user, err := h.users.Find(ctx, p.UserID); err == nil {
		email = user.UserEmail
	}
	core.WriteJSON(w, http.StatusOK, row{"user_id": p.UserID, "team_id": teamID, "team_alias": team.TeamAlias, "role": role,
		"user_email": email, "budget_id": nil, "spend": 0.0, "total_spend": 0.0, "litellm_budget_table": nil})
}

// memberOfTeamForAdmin 은 reset_spend·reset_budget 공통 검사다(I 2.7·2.8). 권한 없음의 detail 은 litellm 처럼 문자열이다
func (h *Handlers) memberOfTeamForAdmin(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	teamID, userID := r.PathValue("team_id"), r.PathValue("user_id")
	denied := func() { core.DetailError{Status: 403, Detail: "You do not have access to this team"}.Write(w) }
	if _, ok := h.teamForAdmin(w, r, teamID, denied); !ok {
		return "", "", false
	}
	_, member, err := h.teamRole(r.Context(), teamID, userID)
	if err != nil {
		h.internalError(w, r, err)
		return "", "", false
	}
	if !member {
		detailError(404, "User "+userID+" is not a member of team "+teamID+".").Write(w)
		return "", "", false
	}
	return teamID, userID, true
}

func (h *Handlers) resetTeamMemberSpend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResetTo *float64 `json:"reset_to"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.ResetTo == nil {
		validationError{missing(raw, "reset_to")}.write(w)
		return
	}
	p := principal(r.Context())
	if !p.IsAdmin() && r.PathValue("user_id") == p.UserID {
		detailError(403, "Cannot reset your own spend. Ask a proxy admin.").Write(w)
		return
	}
	teamID, userID, ok := h.memberOfTeamForAdmin(w, r)
	if !ok {
		return
	}
	ctx, target := r.Context(), limits.MemberTarget(teamID, userID)
	policy, err := h.memberPolicy(ctx, target)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	// litellm 처럼 사용액보다 큰 값으로는 바꿀 수 없다
	if *req.ResetTo < 0 || *req.ResetTo > policy.Spend {
		detailError(400, "reset_to must be >= 0 and <= current spend ("+pyFloat(policy.Spend)+")").Write(w)
		return
	}
	previous, err := h.limits.SetSpend(ctx, limits.ScopeTeamMember, target, *req.ResetTo)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"team_id": teamID, "user_id": userID, "spend": *req.ResetTo, "previous_spend": previous, "max_budget": policy.MaxBudget})
}

// pyFloat 는 파이썬 float 문자열이다(0 은 "0.0"). litellm 문구에 그대로 들어간다
func pyFloat(v float64) string {
	text := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return text
}

func (h *Handlers) memberPolicy(ctx context.Context, target string) (limits.Policy, error) {
	policies, err := h.limits.Find(ctx, limits.ScopeTeamMember, []string{target})
	return policies[target], err
}

// resetTeamMemberBudget 은 팀원 한도(예산·TPM·RPM·기간)를 모두 지운다. 팀 기본 팀원 예산은 없으므로 늘 "none" 이다. 사용액은 그대로다
func (h *Handlers) resetTeamMemberBudget(w http.ResponseWriter, r *http.Request) {
	teamID, userID, ok := h.memberOfTeamForAdmin(w, r)
	if !ok {
		return
	}
	ctx, target := r.Context(), limits.MemberTarget(teamID, userID)
	policy, err := h.memberPolicy(ctx, target)
	if err == nil {
		_, err = h.limits.Apply(ctx, limits.ScopeTeamMember, target, clearedMemberLimits)
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	var previous any
	if policy.MaxBudget != nil || policy.TPMLimit != nil || policy.RPMLimit != nil || policy.BudgetDuration != nil {
		previous = target // 우리는 예산 행 id 대신 팀원 한도 자체가 대상 id 로 저장된다
	}
	core.WriteJSON(w, http.StatusOK, row{"team_id": teamID, "user_id": userID, "budget_id": nil, "previous_budget_id": previous, "budget_source": "none"})
}

// clearedMemberLimits 는 팀원 한도를 모두 null 로 만드는 변경이다
var clearedMemberLimits = limits.Patch{MaxBudget: core.Change[float64]{Set: true}, TPMLimit: core.Change[int64]{Set: true},
	RPMLimit: core.Change[int64]{Set: true}, BudgetDuration: core.Change[string]{Set: true}}

// writeProblem 은 /management/v1/* 의 RFC 9457 오류 문서다(I 2.9)
func writeProblem(w http.ResponseWriter, status int, kind, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	// 쓰기 실패는 클라이언트가 끊은 것이다(core.WriteJSON 과 같다)
	_ = json.NewEncoder(w).Encode(row{"type": "urn:litellm:error:" + kind, "title": title, "status": status, "detail": detail})
}

// memberBudgetPatch 는 팀원 한도 일괄 수정의 한 사람이다. merge-patch 라 보내지 않은 필드는 그대로 두고 null 은 지운다(I 2.9)
type memberBudgetPatch struct {
	UserID        *string               `json:"user_id"`
	UserEmail     *string               `json:"user_email"`
	AllowedModels core.Change[[]string] `json:"allowed_models"`
	memberLimitFields
}

// bulkUpdateMemberBudgets 는 팀원 한도 일괄 수정이다. 팀원별 허용 모델(allowed_models)은 아직 없어 값을 정하는 행은 실패로 남긴다
func (h *Handlers) bulkUpdateMemberBudgets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Members []memberBudgetPatch `json:"members"`
	}
	if _, ok := requestBody(w, r, &req); !ok {
		return
	}
	teamID := r.PathValue("team_id")
	route := "/management/v1/teams/" + teamID + "/members/bulk_update"
	denied := func() {
		writeProblem(w, 403, "forbidden", "Forbidden", "Call not allowed. User not proxy admin OR team admin OR org admin for this team. route='"+route+"'")
	}
	team, err := h.teams.Find(r.Context(), teamID)
	if errors.Is(err, auth.ErrTeamNotFound) {
		writeProblem(w, 404, "team-not-found", "Team not found", "Team id="+teamID+" does not exist in db")
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if _, ok := h.teamForAdmin(w, r, team.TeamID, denied); !ok {
		return
	}
	members, err := h.teams.Members(r.Context(), []string{teamID})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	results := make([]row, 0, len(req.Members))
	err = h.db.InTx(r.Context(), func(ctx context.Context) error {
		for _, m := range req.Members {
			result, err := h.updateMemberBudget(ctx, teamID, members[teamID], m)
			if err != nil {
				return err
			}
			results = append(results, result)
		}
		return nil
	})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"data": results})
}

// updateMemberBudget 는 한 팀원의 결과 행이다. 명단에 없거나 값이 틀린 행은 실패로 남기고 DB 오류만 돌려준다
func (h *Handlers) updateMemberBudget(ctx context.Context, teamID string, roster []auth.TeamMember, m memberBudgetPatch) (row, error) {
	result := row{"user_id": m.UserID, "user_email": m.UserEmail, "success": true, "error": nil, "budget_id": nil,
		"max_budget": nil, "max_budget_source": nil, "tpm_limit": nil, "rpm_limit": nil, "budget_duration": nil, "allowed_models": nil}
	fail := func(message string) (row, error) {
		result["success"], result["error"] = false, message
		return result, nil
	}
	userID, found := rosterUser(roster, derefOr(m.UserID, ""), derefOr(m.UserEmail, ""))
	switch {
	case !found:
		return fail("User is not a member of team " + teamID)
	case m.AllowedModels.Set && m.AllowedModels.Value != nil:
		return fail("Per-member allowed_models are not supported by this gateway yet")
	case !m.anySet():
		return result, nil
	}
	policy, err := h.limits.Apply(ctx, limits.ScopeTeamMember, limits.MemberTarget(teamID, userID), m.patch())
	var invalid *limits.InvalidError
	if errors.As(err, &invalid) {
		return fail(invalid.Message)
	}
	if err != nil {
		return nil, err
	}
	result["max_budget"], result["tpm_limit"], result["rpm_limit"], result["budget_duration"] = policy.MaxBudget, policy.TPMLimit, policy.RPMLimit, policy.BudgetDuration
	if policy.MaxBudget != nil {
		result["max_budget_source"] = "member"
	}
	return result, nil
}

// rosterUser 는 팀 명단에서 user_id 나 이메일로 찾은 사용자 id 다
func rosterUser(members []auth.TeamMember, userID, email string) (string, bool) {
	for _, m := range members {
		if (userID != "" && m.UserID == userID) || (email != "" && m.UserEmail != nil && *m.UserEmail == email) {
			return m.UserID, true
		}
	}
	return "", false
}
