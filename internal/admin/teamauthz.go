package admin

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"slices"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// 팀·사용자 라우트의 핸들러 단계 권한(H 1장 표). 라우트 단계는 "인증만 되면 통과"(handlerDecide)라서
// 누가 어느 팀·사용자를 건드릴 수 있는지는 모두 여기서 정한다

// teamState 는 호출자 입장에서 본 팀이다
type teamState int

const (
	stateAllowed   teamState = iota // 있고, 호출자가 proxy_admin 이거나 그 팀의 admin 이다
	stateNoTeam                     // 팀이 없다
	stateForbidden                  // 있지만 호출자가 admin 이 아니다
)

// teamAdminAccess 는 팀 관리 쓰기(멤버 추가·삭제·수정)의 호출자 권한이다. proxy_admin 은 팀을 읽지 않고 통과시킨다
func (h *Handlers) teamAdminAccess(ctx context.Context, p auth.Principal, teamID string) (teamState, error) {
	if p.IsAdmin() {
		return stateAllowed, nil
	}
	if _, err := h.teams.Find(ctx, teamID); errors.Is(err, auth.ErrTeamNotFound) {
		return stateNoTeam, nil
	} else if err != nil {
		return 0, err
	}
	admin, err := h.isTeamAdmin(ctx, p, teamID)
	if err != nil {
		return 0, err
	}
	if !admin {
		return stateForbidden, nil
	}
	return stateAllowed, nil
}

// notTeamAdmin 은 litellm 문구다. 대시보드가 그대로 보이므로 route 를 바꾸지 않는다
func notTeamAdmin(route, teamID string) core.DetailError {
	return detailError(403, "Call not allowed. User not proxy admin OR team admin. route="+route+", team_id="+teamID)
}

// authorizeTeamMemberWrite 는 member_add·member_delete·member_update 공통 검사다. 통과하지 못하면 응답을 쓰고 false 다.
// missing 은 팀이 없을 때 쓸 응답이다(litellm 은 엔드포인트마다 404·400 으로 다르다)
func (h *Handlers) authorizeTeamMemberWrite(w http.ResponseWriter, r *http.Request, route, teamID string, missing func()) bool {
	state, err := h.teamAdminAccess(r.Context(), principal(r.Context()), teamID)
	switch {
	case err != nil:
		h.internalError(w, r, err)
	case state == stateNoTeam:
		missing()
	case state == stateForbidden:
		notTeamAdmin(route, teamID).Write(w)
	default:
		return true
	}
	return false
}

// mayAddMembers 는 proxy_admin 이 아니면 아직 없는 user_id 를 팀에 넣어 사용자를 만들 수 없다는 규칙이다.
// 이메일로 넣으면 새 사용자로 초대된다(litellm TE:3349-3381)
func (h *Handlers) mayAddMembers(w http.ResponseWriter, r *http.Request, refs []auth.MemberRef) bool {
	if principal(r.Context()).IsAdmin() {
		return true
	}
	for _, ref := range refs {
		if ref.UserID == nil || ref.UserEmail != nil {
			continue
		}
		_, err := h.users.Find(r.Context(), *ref.UserID)
		if errors.Is(err, auth.ErrUserNotFound) {
			detailError(403, "Only proxy admins can add a user_id that does not exist yet: "+*ref.UserID+
				". Add the member by user_email to invite a new user, or ask a proxy admin to create the user first.").Write(w)
			return false
		}
		if err != nil {
			h.internalError(w, r, err)
			return false
		}
	}
	return true
}

// authorizeTeamUpdate 는 /team/update 다. 팀이 없는 것도 권한 없음과 같은 403 으로 답한다(존재 여부를 숨긴다).
// 팀 admin 은 설정(team_admin_editable_team_fields)이 허용한 필드만 바꿀 수 있다
func (h *Handlers) authorizeTeamUpdate(w http.ResponseWriter, r *http.Request, req teamUpdate) bool {
	ctx, p := r.Context(), principal(r.Context())
	if p.IsAdmin() {
		return true
	}
	deny := func(message string) bool {
		plainProxyError(403, message).Write(w)
		return false
	}
	const noAccess = "You do not have access to this team"
	team, err := h.teams.Find(ctx, *req.TeamID)
	if errors.Is(err, auth.ErrTeamNotFound) {
		return deny(noAccess)
	}
	if err != nil {
		h.internalError(w, r, err)
		return false
	}
	admin, err := h.isTeamAdmin(ctx, p, team.TeamID)
	if err != nil {
		h.internalError(w, r, err)
		return false
	}
	if !admin {
		return deny(noAccess)
	}
	policies, err := h.limits.Find(ctx, limits.ScopeTeam, []string{team.TeamID})
	if err != nil {
		h.internalError(w, r, err)
		return false
	}
	permitted, err := h.teamAdminEditableFields(ctx)
	if err != nil {
		h.internalError(w, r, err)
		return false
	}
	if message := teamAdminEditDenial(req, team, policies[team.TeamID], permitted); message != "" {
		return deny(message)
	}
	return true
}

const teamAdminSettingsHint = "under Admin Settings > UI Settings (team_admin_editable_team_fields)"

// teamAdminEditableFields 는 팀 admin 에게 열어 둔 팀 설정이다. 기본은 비어 있어 팀 admin 은 팀 설정을 못 바꾼다
func (h *Handlers) teamAdminEditableFields(ctx context.Context) ([]string, error) {
	stored, err := h.settings.Get(ctx, uiSettingsForm.name)
	if err != nil {
		return nil, err
	}
	return stringsOf(stored["team_admin_editable_team_fields"]), nil
}

// teamAdminEditDenial 은 litellm team_admin_edit_verdict 의 거부 사유다. 허용이면 빈 문자열이다.
// 요청이 그대로 다시 보낸 값(화면은 폼 전체를 보낸다)은 바뀐 것으로 치지 않는다
func teamAdminEditDenial(req teamUpdate, team auth.Team, policy limits.Policy, permitted []string) string {
	if len(permitted) == 0 {
		return "Team admins on this proxy cannot edit team settings. Ask a proxy admin to enable fields " + teamAdminSettingsHint + "."
	}
	for _, field := range changedTeamFields(req, team, policy) {
		if !slices.Contains(permitted, field) || !slices.Contains(teamAdminSupportedFields, field) {
			return "Team admins on this proxy do not have permission to update '" + field + "'. Ask a proxy admin to add it " + teamAdminSettingsHint + "."
		}
	}
	if req.MaxBudget.Set && !slices.Contains(permitted, "raise_max_budget") && budgetRaised(req.MaxBudget.Value, policy.MaxBudget) {
		return "Team admins on this proxy do not have permission to raise 'max_budget'. Ask a proxy admin to add 'raise_max_budget' " + teamAdminSettingsHint + "."
	}
	return ""
}

// teamAdminSupportedFields 는 팀 admin 에게 열 수 있는 필드다(litellm SUPPORTED_TEAM_ADMIN_EDITABLE_TEAM_FIELDS 중 우리가 가진 것)
var teamAdminSupportedFields = []string{"tpm_limit", "rpm_limit", "max_budget", "soft_budget", "metadata"}

// budgetRaised 는 예산 상한을 올리거나 없애는지다. 상한이 없던 팀에 상한을 거는 것은 낮추는 것이다
func budgetRaised(next, current *float64) bool {
	switch {
	case current == nil:
		return false
	case next == nil:
		return true
	}
	return *next > *current
}

// changedTeamFields 는 요청이 현재 값과 다르게 보낸 필드 이름이다(이름순)
func changedTeamFields(req teamUpdate, team auth.Team, policy limits.Policy) []string {
	changed := map[string]bool{}
	mark := func(name string, set bool, same bool) {
		if set && !same {
			changed[name] = true
		}
	}
	mark("team_alias", req.TeamAlias.Set, equalPtr(req.TeamAlias.Value, team.TeamAlias))
	mark("models", req.Models.Set, slices.Equal(derefSlice(req.Models.Value), team.Models))
	mark("metadata", req.Metadata.Set, maps.EqualFunc(derefMap(req.Metadata.Value), team.Metadata, reflect.DeepEqual))
	mark("blocked", req.Blocked.Set, derefBool(req.Blocked.Value) == team.Blocked)
	mark("team_member_permissions", req.MemberPermissions.Set, slices.Equal(derefSlice(req.MemberPermissions.Value), team.MemberPermissions))
	mark("organization_id", req.OrganizationID.Set, equalPtr(nonEmpty(req.OrganizationID.Value), team.OrganizationID))
	mark("tpd_limit", req.TPDLimit.Set, equalPtr(req.TPDLimit.Value, policy.TPDLimit))
	mark("model_max_budget", req.ModelMaxBudget.Set, modelBudgetsEqual(derefMap(req.ModelMaxBudget.Value), policy.ModelBudgets))
	mark("max_budget", req.MaxBudget.Set, equalPtr(req.MaxBudget.Value, policy.MaxBudget))
	mark("soft_budget", req.SoftBudget.Set, equalPtr(req.SoftBudget.Value, policy.SoftBudget))
	mark("tpm_limit", req.TPMLimit.Set, equalPtr(req.TPMLimit.Value, policy.TPMLimit))
	mark("rpm_limit", req.RPMLimit.Set, equalPtr(req.RPMLimit.Value, policy.RPMLimit))
	mark("max_parallel_requests", req.MaxParallelRequests.Set, equalPtr(req.MaxParallelRequests.Value, policy.MaxParallelRequests))
	mark("budget_duration", req.BudgetDuration.Set, equalPtr(req.BudgetDuration.Value, policy.BudgetDuration))
	names := make([]string, 0, len(changed))
	for name := range changed {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func derefSlice[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}

func derefMap[K comparable, V any](p *map[K]V) map[K]V {
	if p == nil {
		return nil
	}
	return *p
}

func derefBool(p *bool) bool { return p != nil && *p }

// authorizeTeamRead 는 /team/info 다. proxy_admin 계열은 모든 팀, 나머지는 그 팀의 멤버만 본다.
// 사용자 없는 팀 키는 자기 팀만 본다(litellm validate_membership)
func (h *Handlers) authorizeTeamRead(w http.ResponseWriter, r *http.Request, teamID string, deny func(message string)) bool {
	ctx, p := r.Context(), principal(r.Context())
	if p.CanViewAll() {
		return true
	}
	if p.UserID == "" {
		if p.TeamID == teamID {
			return true
		}
		deny(pyDict("error", "Team key for team="+p.TeamID+" not authorized to access this team="+teamID))
		return false
	}
	_, member, err := h.teamRole(ctx, teamID, p.UserID)
	if err != nil {
		h.internalError(w, r, err)
		return false
	}
	if !member {
		deny(pyDict("error", "User="+p.UserID+" not authorized to access this team="+teamID))
	}
	return member
}

// scopeTeamListUser 는 /team/list·/v2/team/list 의 user_id 다. 비관리자는 본인 팀만 본다.
// litellm 은 user_id 가 없으면 401 이지만 우리는 호출자 id 로 채운다(설계 9장). 남의 user_id 는 401 이다
func scopeTeamListUser(w http.ResponseWriter, p auth.Principal, userID *string) (*string, bool) {
	if p.CanViewAll() {
		return userID, true
	}
	if userID == nil {
		self := p.UserID
		return &self, true
	}
	if *userID != p.UserID {
		core.DetailError{Status: http.StatusUnauthorized, Detail: map[string]string{
			"error": "Only admin users can query all teams/other teams. Your user role=" + string(p.Role)}}.Write(w)
		return nil, false
	}
	return userID, true
}
