package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// litellm 팀 관리 API(docs/litellm-inventory/H_team_user_contract.md 3장, I 2.1).
// 오류 본문 모양은 엔드포인트마다 litellm 과 같게 고른다: (A) proxyError, (B) detailError, (C) 422.

// limitFields 는 팀·사용자 요청에 함께 오는 한도 필드다. limits 모듈에 저장한다
type limitFields struct {
	MaxBudget           core.Change[float64] `json:"max_budget"`
	SoftBudget          core.Change[float64] `json:"soft_budget"`
	TPMLimit            core.Change[int64]   `json:"tpm_limit"`
	TPDLimit            core.Change[int64]   `json:"tpd_limit"`
	RPMLimit            core.Change[int64]   `json:"rpm_limit"`
	MaxParallelRequests core.Change[int64]   `json:"max_parallel_requests"`
	BudgetDuration      core.Change[string]  `json:"budget_duration"`
	// ModelMaxBudget 는 모델별 예산·RPM·TPM 이다. litellm 에서는 유료 기능이지만 우리는 막지 않는다
	ModelMaxBudget core.Change[map[string]limits.ModelBudget] `json:"model_max_budget"`
}

func (l limitFields) patch() limits.Patch {
	return limits.Patch{MaxBudget: l.MaxBudget, SoftBudget: l.SoftBudget, TPMLimit: l.TPMLimit, TPDLimit: l.TPDLimit, RPMLimit: l.RPMLimit,
		MaxParallelRequests: l.MaxParallelRequests, BudgetDuration: l.BudgetDuration, ModelBudgets: l.ModelMaxBudget}
}

type memberJSON struct {
	UserID    *string `json:"user_id"`
	UserEmail *string `json:"user_email"`
	Role      *string `json:"role"`
}

// ref 는 litellm Member 검증과 같다: 역할은 admin·user, 기본 user. user_id·user_email 둘 다 없으면 오류
func (m memberJSON) ref(loc []any, errs *validationError) auth.MemberRef {
	role := auth.TeamRoleUser
	if m.Role != nil {
		role = auth.TeamRole(*m.Role)
		if !role.Valid() {
			*errs = append(*errs, FieldError{Type: "literal_error", Loc: append(loc, "role"), Msg: "Input should be 'admin' or 'user'", Input: *m.Role})
		}
	}
	if m.UserID == nil && m.UserEmail == nil {
		*errs = append(*errs, FieldError{Type: "value_error", Loc: loc, Msg: "Value error, Either user_id or user_email must be provided", Input: map[string]any{}})
	}
	return auth.MemberRef{UserID: m.UserID, UserEmail: m.UserEmail, Role: role}
}

func (h *Handlers) createTeam(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TeamID           *string        `json:"team_id"`
		TeamAlias        *string        `json:"team_alias"`
		Models           []string       `json:"models"`
		Metadata         map[string]any `json:"metadata"`
		Blocked          *bool          `json:"blocked"`
		MembersWithRoles []memberJSON   `json:"members_with_roles"`
		limitFields
	}
	if _, ok := requestBody(w, r, &req); !ok {
		return
	}
	var errs validationError
	refs := make([]auth.MemberRef, 0, len(req.MembersWithRoles))
	for i, m := range req.MembersWithRoles {
		refs = append(refs, m.ref([]any{"body", "members_with_roles", i}, &errs))
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	caller := principal(r.Context())
	input := auth.NewTeam{TeamID: req.TeamID, TeamAlias: req.TeamAlias, Models: req.Models, Metadata: req.Metadata,
		Blocked: req.Blocked != nil && *req.Blocked, Members: refs}

	// 팀과 그 한도를 한 트랜잭션에서 만든다. 한도 값이 틀리면 팀도 만들지 않는다
	var team auth.Team
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if team, err = h.teams.Create(ctx, input, caller.UserID); err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeTeam, team.TeamID, req.patch())
		return err
	})
	var invalid *limits.InvalidError
	var mismatch *auth.EmailOwnerMismatchError
	switch {
	case err == nil:
		h.writeTeam(w, r, team)
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.Is(err, auth.ErrTeamIDTaken):
		proxyError(400, "Team id = "+derefOr(req.TeamID, "")+" already exists. Please use a different team id.").Write(w)
	case errors.Is(err, auth.ErrTeamIDReserved):
		proxyError(400, "team_id="+derefOr(req.TeamID, "")+" is reserved for internal use. Please use a different team_id.").Write(w)
	case errors.As(err, &mismatch):
		proxyError(400, mismatchMessage(mismatch)).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// teamUpdate 는 /team/update 본문이다(litellm UpdateTeamRequest). 팀 권한 화면 말고 팀 수정 폼도 team_member_permissions 를 보낸다
type teamUpdate struct {
	TeamID            *string                     `json:"team_id"`
	TeamAlias         core.Change[string]         `json:"team_alias"`
	Models            core.Change[[]string]       `json:"models"`
	Metadata          core.Change[map[string]any] `json:"metadata"`
	Blocked           core.Change[bool]           `json:"blocked"`
	MemberPermissions core.Change[[]string]       `json:"team_member_permissions"`
	limitFields
}

func (h *Handlers) updateTeam(w http.ResponseWriter, r *http.Request) {
	var req teamUpdate
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.TeamID == nil {
		validationError{missing(raw, "team_id")}.write(w)
		return
	}
	if !h.authorizeTeamUpdate(w, r, req) {
		return
	}
	var team auth.Team
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		patch := auth.TeamPatch{TeamAlias: req.TeamAlias, Models: req.Models, Metadata: req.Metadata, Blocked: req.Blocked,
			MemberPermissions: req.MemberPermissions}
		if team, err = h.teams.Update(ctx, *req.TeamID, patch); err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeTeam, *req.TeamID, req.patch())
		return err
	})
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		teamRow, rowErr := h.rows.teamRowOf(r.Context(), team)
		if rowErr != nil {
			h.internalError(w, r, rowErr)
			return
		}
		core.WriteJSON(w, http.StatusOK, row{"team_id": team.TeamID, "data": teamRow})
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.Is(err, auth.ErrTeamNotFound):
		proxyError(404, "Team not found, passed team_id="+*req.TeamID).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) deleteTeams(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TeamIDs []string `json:"team_ids"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.TeamIDs == nil {
		validationError{missing(raw, "team_ids")}.write(w)
		return
	}
	ids := unique(req.TeamIDs)
	caller := principal(r.Context())
	// 팀과 그 팀·팀원의 한도를 함께 지운다(팀 키는 auth 가 사본으로 옮긴다)
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		memberTargets, err := h.memberTargetsOfTeams(ctx, ids)
		if err != nil {
			return err
		}
		if err := h.teams.Delete(ctx, ids, caller.UserID); err != nil {
			return err
		}
		if err := h.limits.Delete(ctx, limits.ScopeTeamMember, memberTargets); err != nil {
			return err
		}
		return h.limits.Delete(ctx, limits.ScopeTeam, ids)
	})
	var notFound *auth.NotFoundError
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, row{"deleted_teams": ids})
	case errors.As(err, &notFound):
		detailError(404, "Team not found, passed team_id="+notFound.ID).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) blockTeam(w http.ResponseWriter, r *http.Request)   { h.setBlocked(w, r, true) }
func (h *Handlers) unblockTeam(w http.ResponseWriter, r *http.Request) { h.setBlocked(w, r, false) }

func (h *Handlers) setBlocked(w http.ResponseWriter, r *http.Request, blocked bool) {
	var req struct {
		TeamID *string `json:"team_id"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.TeamID == nil {
		validationError{missing(raw, "team_id")}.write(w)
		return
	}
	team, err := h.teams.Update(r.Context(), *req.TeamID, auth.TeamPatch{Blocked: core.To(blocked)})
	switch {
	case err == nil:
		h.writeTeam(w, r, team)
	case errors.Is(err, auth.ErrTeamNotFound):
		detailError(404, "Team not found, passed team_id="+*req.TeamID).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) addMember(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TeamID *string         `json:"team_id"`
		Member json.RawMessage `json:"member"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	var errs validationError
	if req.TeamID == nil {
		errs = append(errs, missing(raw, "team_id"))
	}
	refs, single := memberRefs(req.Member, raw, &errs)
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	teamMissing := func() {
		detailError(404, "Team doesn't exist in db. Team="+*req.TeamID+". Create team via `/team/new` call.").Write(w)
	}
	if !h.authorizeTeamMemberWrite(w, r, "/team/member_add", *req.TeamID, teamMissing) || !h.mayAddMembers(w, r, refs) {
		return
	}
	team, added, err := h.teams.AddMembers(r.Context(), *req.TeamID, refs, single)
	var already *auth.AlreadyMemberError
	var mismatch *auth.EmailOwnerMismatchError
	switch {
	case err == nil:
		h.writeAddedMembers(w, r, team, added)
	case errors.Is(err, auth.ErrTeamNotFound):
		detailError(404, "Team doesn't exist in db. Team="+*req.TeamID+". Create team via `/team/new` call.").Write(w)
	case errors.As(err, &already):
		// 중복 멤버만 litellm 에서 (A) 모양이고 type·param 이 다르다
		core.ProxyError{Status: 400, Message: alreadyMessage(already), Type: "team_member_already_in_team", Param: "member"}.Write(w)
	case errors.As(err, &mismatch):
		detailError(400, mismatchMessage(mismatch)).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// memberRefs 는 member 필드(객체 하나 또는 배열)를 읽는다
func memberRefs(data json.RawMessage, raw map[string]any, errs *validationError) ([]auth.MemberRef, bool) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*errs = append(*errs, missing(raw, "member"))
		return nil, true
	}
	if strings.HasPrefix(trimmed, "[") {
		var list []memberJSON
		if err := json.Unmarshal(data, &list); err != nil {
			*errs = append(*errs, FieldError{Type: "list_type", Loc: []any{"body", "member"}, Msg: "Input should be a valid list", Input: raw["member"]})
			return nil, false
		}
		refs := make([]auth.MemberRef, len(list))
		for i, m := range list {
			refs[i] = m.ref([]any{"body", "member", i}, errs)
		}
		if len(refs) == 0 {
			*errs = append(*errs, FieldError{Type: "too_short", Loc: []any{"body", "member"}, Msg: "List should have at least 1 item after validation, not 0", Input: raw["member"]})
		}
		return refs, false
	}
	var one memberJSON
	if err := json.Unmarshal(data, &one); err != nil {
		*errs = append(*errs, FieldError{Type: "model_type", Loc: []any{"body", "member"}, Msg: "Input should be a valid dictionary or instance of Member", Input: raw["member"]})
		return nil, true
	}
	return []auth.MemberRef{one.ref([]any{"body", "member"}, errs)}, true
}

func (h *Handlers) writeAddedMembers(w http.ResponseWriter, r *http.Request, team auth.Team, added []auth.User) {
	ctx := r.Context()
	teamRows, members, err := h.rows.teamRows(ctx, []auth.Team{team})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	userRows, err := h.rows.userRows(ctx, added)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	addedIDs := map[string]bool{}
	for _, u := range added {
		addedIDs[u.UserID] = true
	}
	var addedMembers []auth.TeamMember
	for _, m := range members[team.TeamID] {
		if addedIDs[m.UserID] {
			addedMembers = append(addedMembers, m)
		}
	}
	memberships, err := h.rows.membershipRows(ctx, addedMembers)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	response := teamRows[0]
	response["updated_users"] = userRows
	response["updated_team_memberships"] = memberships
	core.WriteJSON(w, http.StatusOK, response)
}

func (h *Handlers) deleteMember(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TeamID    *string `json:"team_id"`
		UserID    *string `json:"user_id"`
		UserEmail *string `json:"user_email"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if errs := memberTargetErrors(raw, req.TeamID, req.UserID, req.UserEmail); len(errs) > 0 {
		errs.write(w)
		return
	}
	// litellm 은 여기서 팀이 없으면 404 가 아니라 400 이다
	teamMissing := func() { detailError(400, "Team id="+*req.TeamID+" does not exist in db").Write(w) }
	if !h.authorizeTeamMemberWrite(w, r, "/team/member_delete", *req.TeamID, teamMissing) {
		return
	}
	var team auth.Team
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		target, err := h.memberTarget(ctx, *req.TeamID, req.UserID, req.UserEmail)
		if err != nil {
			return err
		}
		if team, err = h.teams.RemoveMember(ctx, *req.TeamID, req.UserID, req.UserEmail, principal(ctx).UserID); err != nil || target == "" {
			return err
		}
		return h.limits.Delete(ctx, limits.ScopeTeamMember, []string{target}) // 다시 들어오는 팀원이 옛 한도·사용액을 물려받지 않게 한다
	})
	switch {
	case err == nil:
		h.writeTeam(w, r, team)
	case errors.Is(err, auth.ErrTeamNotFound):
		// litellm 은 여기서 404 가 아니라 400 이다
		detailError(400, "Team id="+*req.TeamID+" does not exist in db").Write(w)
	case errors.Is(err, auth.ErrNotMember):
		detailError(400, "User not found in team").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// memberTarget 은 user_id 나 이메일로 가리킨 팀원의 한도 대상 id 다. 팀원이 아니면 빈 문자열이다
func (h *Handlers) memberTarget(ctx context.Context, teamID string, userID, email *string) (string, error) {
	members, err := h.teams.Members(ctx, []string{teamID})
	if err != nil {
		return "", err
	}
	id, found := rosterUser(members[teamID], derefOr(userID, ""), derefOr(email, ""))
	if !found {
		return "", nil
	}
	return limits.MemberTarget(teamID, id), nil
}

// memberTargetsOfTeams 는 팀들의 모든 팀원 한도 대상 id 다
func (h *Handlers) memberTargetsOfTeams(ctx context.Context, teamIDs []string) ([]string, error) {
	members, err := h.teams.Members(ctx, teamIDs)
	if err != nil {
		return nil, err
	}
	var targets []string
	for teamID, roster := range members {
		for _, m := range roster {
			targets = append(targets, limits.MemberTarget(teamID, m.UserID))
		}
	}
	return targets, nil
}

// updateMember 는 litellm 처럼 저장 결과가 아니라 요청 값을 되돌려 준다. 팀원 한도는 limits 에 저장한다
// memberUpdateRequest 는 /team/member_update 본문이다
type memberUpdateRequest struct {
	TeamID             *string        `json:"team_id"`
	UserID             *string        `json:"user_id"`
	UserEmail          *string        `json:"user_email"`
	Role               *string        `json:"role"`
	AllowedModels      []string       `json:"allowed_models"`
	TempBudgetIncrease *float64       `json:"temp_budget_increase"`
	TempBudgetExpiry   *string        `json:"temp_budget_expiry"`
	ModelMaxBudget     map[string]any `json:"model_max_budget"`
	memberLimitFields
}

// parse 는 바꿀 역할(보내지 않았으면 nil)과 형식 오류를 낸다
func (req memberUpdateRequest) parse(raw map[string]any) (*auth.TeamRole, validationError) {
	errs := memberTargetErrors(raw, req.TeamID, req.UserID, req.UserEmail)
	if req.Role == nil {
		return nil, errs
	}
	role := auth.TeamRole(*req.Role)
	if !role.Valid() {
		return nil, append(errs, FieldError{Type: "literal_error", Loc: []any{"body", "role"}, Msg: "Input should be 'admin' or 'user'", Input: *req.Role})
	}
	return &role, errs
}

func (h *Handlers) updateMember(w http.ResponseWriter, r *http.Request) {
	var req memberUpdateRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	role, errs := req.parse(raw)
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	teamMissing := func() { detailError(400, "Team id="+*req.TeamID+" does not exist in db").Write(w) }
	// 거부 문구의 route 가 member_delete 로 찍히는 것은 litellm 원본 버그다(H 3.7). 대시보드가 보이는 문구라 그대로 둔다
	if !h.authorizeTeamMemberWrite(w, r, "/team/member_delete", *req.TeamID, teamMissing) {
		return
	}
	var userID string
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if userID, err = h.teams.UpdateMember(ctx, *req.TeamID, req.UserID, req.UserEmail, role); err != nil || !req.anySet() {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeTeamMember, limits.MemberTarget(*req.TeamID, userID), req.patch())
		return err
	})
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, req.response(userID))
	case errors.As(err, &invalid):
		detailError(400, invalid.Message).Write(w)
	case errors.Is(err, auth.ErrTeamNotFound):
		detailError(400, "Team id="+*req.TeamID+" does not exist in db").Write(w)
	case errors.Is(err, auth.ErrNotMember):
		detailError(400, fmt.Sprintf("User id doesn't exist in team table. Data=user_id=%s, user_email=%s",
			derefOr(req.UserID, "None"), derefOr(req.UserEmail, "None"))).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (req memberUpdateRequest) response(userID string) row {
	return row{
		"user_id": userID, "user_email": req.UserEmail, "team_id": *req.TeamID,
		"max_budget_in_team": req.MaxBudgetInTeam.Value, "tpm_limit": req.TPMLimit.Value, "rpm_limit": req.RPMLimit.Value,
		"budget_duration": req.BudgetDuration.Value, "allowed_models": req.AllowedModels,
		"temp_budget_increase": req.TempBudgetIncrease, "temp_budget_expiry": req.TempBudgetExpiry,
		"model_max_budget": req.ModelMaxBudget,
	}
}

// memberLimitFields 는 팀원 한도 필드다(/team/member_update, 팀원 일괄 수정). 보내지 않으면 그대로 두고 null 이면 지운다
type memberLimitFields struct {
	MaxBudgetInTeam core.Change[float64] `json:"max_budget_in_team"`
	TPMLimit        core.Change[int64]   `json:"tpm_limit"`
	RPMLimit        core.Change[int64]   `json:"rpm_limit"`
	BudgetDuration  core.Change[string]  `json:"budget_duration"`
}

func (f memberLimitFields) anySet() bool {
	return f.MaxBudgetInTeam.Set || f.TPMLimit.Set || f.RPMLimit.Set || f.BudgetDuration.Set
}

func (f memberLimitFields) patch() limits.Patch {
	return limits.Patch{MaxBudget: f.MaxBudgetInTeam, TPMLimit: f.TPMLimit, RPMLimit: f.RPMLimit, BudgetDuration: f.BudgetDuration}
}

func memberTargetErrors(raw map[string]any, teamID, userID, userEmail *string) validationError {
	var errs validationError
	if teamID == nil {
		errs = append(errs, missing(raw, "team_id"))
	}
	if userID == nil && userEmail == nil {
		errs = append(errs, FieldError{Type: "value_error", Loc: []any{"body"}, Msg: "Value error, Either user_id or user_email must be provided", Input: raw})
	}
	return errs
}

func (h *Handlers) teamInfo(w http.ResponseWriter, r *http.Request) {
	// /team/info 는 litellm 에서 auth_error·"None" 으로 감싼다(TE:5389-5408)
	teamID := r.URL.Query().Get("team_id")
	if teamID == "" {
		authStyle(422, pyDict("message", "Malformed request. No team id passed in.")).Write(w)
		return
	}
	team, err := h.teams.Find(r.Context(), teamID)
	if errors.Is(err, auth.ErrTeamNotFound) {
		authStyle(404, pyDict("message", "Team not found, passed team id: "+teamID+".")).Write(w)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !h.authorizeTeamRead(w, r, teamID, func(message string) { authStyle(403, message).Write(w) }) {
		return
	}
	teamRows, members, err := h.rows.teamRows(r.Context(), []auth.Team{team})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	info := teamRows[0]
	withAlias := []row{}
	for _, m := range members[teamID] {
		mr := memberRow(m)
		mr["user_alias"] = m.UserAlias
		withAlias = append(withAlias, mr)
	}
	memberships, err := h.rows.membershipRows(r.Context(), members[teamID])
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	info["members_with_roles"] = withAlias
	for k, v := range teamInfoExtras() {
		info[k] = v
	}
	core.WriteJSON(w, http.StatusOK, row{
		"team_id":   teamID,
		"team_info": info,
		// 키는 M2 에서 붙는다. litellm 과 달리 키 해시(token)는 내보내지 않는다(설계 9장)
		"keys":             []any{},
		"team_memberships": memberships,
	})
}

func teamInfoExtras() row {
	return row{
		"team_member_budget_table": nil, "access_group_models": nil, "access_group_mcp_server_ids": nil,
		"access_group_agent_ids": nil, "access_group_skill_names": nil, "unresolved_access_group_ids": nil,
		"access_group_details": nil, "organization_models": nil, "model_max_budget_usage": nil,
		"caller_edit_access": row{"kind": "unrestricted"},
	}
}

// listTeamsV1 은 맨 배열, 별칭 오름차순이다
func (h *Handlers) listTeamsV1(w http.ResponseWriter, r *http.Request) {
	userID, ok := scopeTeamListUser(w, principal(r.Context()), queryString(r, "user_id"))
	if !ok {
		return
	}
	teams, err := h.teams.ListByAlias(r.Context(), userID)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	teamRows, members, err := h.rows.teamRows(r.Context(), teams)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	for i, t := range teams {
		var shown []auth.TeamMember
		for _, m := range members[t.TeamID] {
			if userID == nil || m.UserID == *userID {
				shown = append(shown, m)
			}
		}
		memberships, err := h.rows.membershipRows(r.Context(), shown)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		teamRows[i]["team_memberships"] = memberships
		teamRows[i]["keys"] = []any{}
	}
	core.WriteJSON(w, http.StatusOK, teamRows)
}

func (h *Handlers) listTeamsV2(w http.ResponseWriter, r *http.Request) {
	var errs validationError
	page := queryInt(r, "page", 1, 1, 0, &errs)
	pageSize := queryInt(r, "page_size", 10, 1, 100, &errs)
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	status := queryString(r, "status")
	if status != nil && *status != "deleted" {
		detailError(400, "Invalid status value. Currently only 'deleted' is supported.").Write(w)
		return
	}
	userID, ok := scopeTeamListUser(w, principal(r.Context()), queryString(r, "user_id"))
	if !ok {
		return
	}
	teamID := queryString(r, "team_id")
	if userID != nil {
		if ok := h.checkTeamListUser(w, r, *userID, teamID); !ok {
			return
		}
	}
	empty := func() { core.WriteJSON(w, http.StatusOK, teamPage([]row{}, 0, page, pageSize)) }
	// 팀을 실제로 지우므로 삭제된 팀 목록은 늘 비어 있다
	if status != nil {
		empty()
		return
	}
	order := auth.SortAsc
	if r.URL.Query().Get("sort_order") == "desc" {
		order = auth.SortDesc
	}
	result, err := h.teams.Page(r.Context(), auth.TeamQuery{
		MemberUserID: userID, TeamID: teamID, TeamAliasContains: queryString(r, "team_alias"),
		Search: queryString(r, "search"), SearchTeamIDPrefix: r.URL.Query().Get("search_team_id_match") == "prefix",
		// litellm 은 모르는 정렬 열·방향을 오류 없이 무시한다
		SortBy: r.URL.Query().Get("sort_by"), SortOrder: order, Page: page, PageSize: pageSize,
	})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	teamRows, members, err := h.rows.teamRows(r.Context(), result.Items)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	for i, t := range result.Items {
		teamRows[i]["members_count"] = len(members[t.TeamID])
		teamRows[i]["keys_count"] = 0
		for _, k := range []string{"access_group_models", "access_group_mcp_server_ids", "access_group_agent_ids", "access_group_skill_names"} {
			teamRows[i][k] = nil
		}
	}
	core.WriteJSON(w, http.StatusOK, teamPage(teamRows, result.Total, page, pageSize))
}

func (h *Handlers) checkTeamListUser(w http.ResponseWriter, r *http.Request, userID string, teamID *string) bool {
	if _, err := h.users.Find(r.Context(), userID); errors.Is(err, auth.ErrUserNotFound) {
		detailError(404, "User not found, passed user_id="+userID).Write(w)
		return false
	} else if err != nil {
		h.internalError(w, r, err)
		return false
	}
	if teamID == nil {
		return true
	}
	teamIDs, err := h.users.TeamIDs(r.Context(), []string{userID})
	if err != nil {
		h.internalError(w, r, err)
		return false
	}
	for _, id := range teamIDs[userID] {
		if id == *teamID {
			return true
		}
	}
	detailError(404, "User is not a member of team_id="+*teamID).Write(w)
	return false
}

func teamPage(items []row, total int64, page, size int) row {
	return row{"teams": items, "total": total, "page": page, "page_size": size, "total_pages": totalPages(total, size)}
}

func (h *Handlers) writeTeam(w http.ResponseWriter, r *http.Request, team auth.Team) {
	teamRow, err := h.rows.teamRowOf(r.Context(), team)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, teamRow)
}

func alreadyMessage(e *auth.AlreadyMemberError) string {
	existing := make([]string, len(e.Existing))
	for i, m := range e.Existing {
		existing[i] = fmt.Sprintf("Member(user_id=%s, user_email=%s, role=%s)", m.UserID, derefOr(m.UserEmail, "None"), m.Role)
	}
	list := "[" + strings.Join(existing, ", ") + "]"
	if !e.Single {
		return "All users are already in team. Existing members=" + list
	}
	return fmt.Sprintf("User already in team. Member: user_id=%s, user_email=%s. Existing members=%s",
		derefOr(e.Member.UserID, "None"), derefOr(e.Member.UserEmail, "None"), list)
}

func mismatchMessage(e *auth.EmailOwnerMismatchError) string {
	return fmt.Sprintf("user_email '%s' and user_id '%s' do not belong to the same user.", e.Email, e.UserID)
}

func derefOr(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

func unique(ids []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}
