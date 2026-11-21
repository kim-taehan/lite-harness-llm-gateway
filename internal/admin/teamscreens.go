package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// 팀 화면의 나머지 엔드포인트(I 2.2~2.9). 팀원별 예산(litellm LiteLLM_BudgetTable 연결)은 아직 없다(설계 12장 M5).
// 그래서 팀원 사용액은 늘 0 이고, 팀원 예산을 "기본값으로 되돌리는" 요청만 성공한다(이미 기본값이다)

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
	core.WriteJSON(w, http.StatusOK, row{"team_id": team.TeamID, "team_member_permissions": memberKeyRoutes(team),
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
	// 팀원 사용액은 아직 따로 세지 않아 늘 0 이다. 그보다 큰 값으로는 litellm 처럼 바꿀 수 없다
	if *req.ResetTo != 0 {
		detailError(400, "reset_to must be >= 0 and <= current spend (0.0)").Write(w)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"team_id": teamID, "user_id": userID, "spend": 0.0, "previous_spend": 0.0, "max_budget": nil})
}

func (h *Handlers) resetTeamMemberBudget(w http.ResponseWriter, r *http.Request) {
	teamID, userID, ok := h.memberOfTeamForAdmin(w, r)
	if !ok {
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"team_id": teamID, "user_id": userID, "budget_id": nil, "previous_budget_id": nil, "budget_source": "none"})
}

// writeProblem 은 /management/v1/* 의 RFC 9457 오류 문서다(I 2.9)
func writeProblem(w http.ResponseWriter, status int, kind, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	// 쓰기 실패는 클라이언트가 끊은 것이다(core.WriteJSON 과 같다)
	_ = json.NewEncoder(w).Encode(row{"type": "urn:litellm:error:" + kind, "title": title, "status": status, "detail": detail})
}

// bulkUpdateMemberBudgets 는 팀원 예산 일괄 수정이다. 팀원 예산이 없으므로 "기본값으로 되돌리기"(모든 값 null)만 성공한다
func (h *Handlers) bulkUpdateMemberBudgets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Members []map[string]any `json:"members"`
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
	for _, m := range req.Members {
		userID, _ := m["user_id"].(string)
		email, _ := m["user_email"].(string)
		result := row{"user_id": m["user_id"], "user_email": m["user_email"], "success": true, "error": nil, "budget_id": nil,
			"max_budget": nil, "max_budget_source": nil, "tpm_limit": nil, "rpm_limit": nil, "budget_duration": nil, "allowed_models": nil}
		if !inRoster(members[teamID], userID, email) {
			result["success"], result["error"] = false, "User is not a member of team "+teamID
		} else if setsAnyBudget(m) {
			result["success"], result["error"] = false, "Per-member team budgets are not supported by this gateway yet"
		}
		results = append(results, result)
	}
	core.WriteJSON(w, http.StatusOK, row{"data": results})
}

func inRoster(members []auth.TeamMember, userID, email string) bool {
	for _, m := range members {
		if (userID != "" && m.UserID == userID) || (email != "" && m.UserEmail != nil && *m.UserEmail == email) {
			return true
		}
	}
	return false
}

func setsAnyBudget(patch map[string]any) bool {
	for _, k := range []string{"max_budget_in_team", "tpm_limit", "rpm_limit", "budget_duration", "allowed_models"} {
		if v, ok := patch[k]; ok && v != nil {
			return true
		}
	}
	return false
}
