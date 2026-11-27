package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// 조직 관리 API(대시보드 Organizations 화면, litellm /organization/*). 조직은 팀들을 묶고 팀 위에서 예산·허용 모델·RPM·TPM 의 상한을 정한다.
// litellm 처럼 상한은 팀을 만들거나 고칠 때만 검사하고(요청 때는 조직 예산만 본다), 조직 값을 나중에 줄여도 기존 팀은 건드리지 않는다.
// 만들기·고치기·지우기·구성원 관리는 관리자만이고, 조직 관리자 역할은 없다(구성원의 역할 문자열은 화면에 보이는 정보일 뿐이다).
// 조회는 로그인한 누구나 부를 수 있지만 관리자가 아니면 빈 목록이다(대시보드가 부팅 때 모든 사용자에게 목록을 부른다).
// litellm 에서 조직은 라이선스 기능인데 우리는 막지 않는다

type organizationRequest struct {
	OrganizationAlias *string                     `json:"organization_alias"`
	Models            core.Change[[]string]       `json:"models"`
	Metadata          core.Change[map[string]any] `json:"metadata"`
	limitFields
}

// organizationRoles 는 litellm 이 조직 안에서 받는 역할 문자열이다(ROLES_WITHIN_ORG)
var organizationRoles = []string{"org_admin", "internal_user", "internal_user_viewer"}

func organizationRow(o auth.Organization, policy limits.Policy, teams []string, members []auth.OrganizationMember) row {
	teamRows := make([]row, len(teams))
	for i, id := range teams {
		teamRows[i] = row{"team_id": id}
	}
	memberRows := make([]row, len(members))
	for i, m := range members {
		memberRows[i] = row{"user_id": m.UserID, "user_email": m.UserEmail, "user_role": m.UserRole, "spend": 0.0,
			"created_at": isoTime(m.CreatedAt), "updated_at": isoTime(m.UpdatedAt), "organization_id": o.OrganizationID}
	}
	r := row{
		"organization_id": o.OrganizationID, "organization_alias": o.OrganizationAlias, "budget_id": nil, "metadata": orEmptyMetadata(o.Metadata),
		"models": nonNil(o.Models), "spend": policy.Spend, "model_spend": row{}, "created_at": isoTime(o.CreatedAt), "created_by": o.CreatedBy,
		"updated_at": isoTime(o.UpdatedAt), "updated_by": o.UpdatedBy, "litellm_budget_table": nil,
		"teams": teamRows, "users": nil, "members": memberRows, "object_permission": nil,
	}
	if policy.HasLimits() {
		r["litellm_budget_table"] = row{
			"budget_id": o.OrganizationID, "max_budget": policy.MaxBudget, "soft_budget": policy.SoftBudget, "tpm_limit": policy.TPMLimit,
			"rpm_limit": policy.RPMLimit, "tpd_limit": policy.TPDLimit, "max_parallel_requests": policy.MaxParallelRequests,
			"budget_duration": policy.BudgetDuration, "model_max_budget": modelBudgetsView(policy.ModelBudgets),
		}
	}
	return r
}

func (h *Handlers) organizationRows(ctx context.Context, orgs []auth.Organization) ([]row, error) {
	ids := make([]string, len(orgs))
	for i, o := range orgs {
		ids[i] = o.OrganizationID
	}
	policies, err := h.limits.Find(ctx, limits.ScopeOrganization, ids)
	if err != nil {
		return nil, err
	}
	teams, err := h.organizations.Teams(ctx, ids)
	if err != nil {
		return nil, err
	}
	members, err := h.organizations.Members(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make([]row, len(orgs))
	for i, o := range orgs {
		result[i] = organizationRow(o, policies[o.OrganizationID], teams[o.OrganizationID], members[o.OrganizationID])
	}
	return result, nil
}

func organizationNotFound(id string) core.ProxyError {
	return core.ProxyError{Status: http.StatusNotFound, Type: "not_found", Param: "organization_id", Message: "Organization not found. organization_id=" + id}
}

func (h *Handlers) listOrganizations(w http.ResponseWriter, r *http.Request) {
	if !principal(r.Context()).CanViewAll() {
		core.WriteJSON(w, http.StatusOK, []row{})
		return
	}
	orgs, err := h.organizations.List(r.Context(), queryString(r, "org_id"), queryString(r, "org_alias"))
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	rows, err := h.organizationRows(r.Context(), orgs)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if rows == nil {
		rows = []row{}
	}
	core.WriteJSON(w, http.StatusOK, rows)
}

func (h *Handlers) organizationInfo(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("organization_id")
	if id == "" {
		validationError{{Type: "missing", Loc: []any{"query", "organization_id"}, Msg: "Field required", Input: nil}}.write(w)
		return
	}
	if !principal(r.Context()).CanViewAll() {
		organizationNotFound(id).Write(w) // 관리자가 아니면 조직의 존재를 알리지 않는다
		return
	}
	org, err := h.organizations.Find(r.Context(), id)
	switch {
	case errors.Is(err, auth.ErrOrganizationNotFound):
		organizationNotFound(id).Write(w)
		return
	case err != nil:
		h.internalError(w, r, err)
		return
	}
	h.writeOrganization(w, r, org)
}

func (h *Handlers) writeOrganization(w http.ResponseWriter, r *http.Request, org auth.Organization) {
	rows, err := h.organizationRows(r.Context(), []auth.Organization{org})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, rows[0])
}

func (h *Handlers) newOrganization(w http.ResponseWriter, r *http.Request) {
	var req organizationRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.OrganizationAlias == nil || strings.TrimSpace(*req.OrganizationAlias) == "" {
		validationError{missing(raw, "organization_alias")}.write(w)
		return
	}
	var created auth.Organization
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		created, err = h.organizations.Create(ctx, auth.NewOrganization{OrganizationAlias: *req.OrganizationAlias, Models: req.Models.Or(nil),
			Metadata: req.Metadata.Or(nil), CreatedBy: principal(ctx).UserID})
		if err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeOrganization, created.OrganizationID, req.patch())
		return err
	})
	h.finishOrganizationWrite(w, r, created, "", err)
}

func (h *Handlers) updateOrganization(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("organization_id")
	var req organizationRequest
	if _, ok := requestBody(w, r, &req); !ok {
		return
	}
	var updated auth.Organization
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		updated, err = h.organizations.Update(ctx, id, auth.OrganizationPatch{OrganizationAlias: req.OrganizationAlias, Models: req.Models,
			Metadata: req.Metadata, UpdatedBy: principal(ctx).UserID})
		if err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeOrganization, id, req.patch())
		return err
	})
	h.finishOrganizationWrite(w, r, updated, id, err)
}

func (h *Handlers) finishOrganizationWrite(w http.ResponseWriter, r *http.Request, org auth.Organization, id string, err error) {
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		h.writeOrganization(w, r, org)
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.Is(err, auth.ErrOrganizationNotFound):
		organizationNotFound(id).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) deleteOrganizations(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrganizationIDs *[]string `json:"organization_ids"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.OrganizationIDs == nil {
		validationError{missing(raw, "organization_ids")}.write(w)
		return
	}
	ids := unique(*req.OrganizationIDs)
	var deleted []string
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if deleted, err = h.organizations.Delete(ctx, ids); err != nil {
			return err
		}
		return h.limits.Delete(ctx, limits.ScopeOrganization, deleted)
	})
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, row{"deleted_organizations": nonNil(deleted)})
	case errors.Is(err, auth.ErrOrganizationHasTeams):
		proxyError(400, "Cannot delete an organization that still has teams. Move or delete its teams first.").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// 구성원 관리. 역할은 org_admin·internal_user·internal_user_viewer 중 하나여야 하고 정보로만 남는다

type organizationMemberRequest struct {
	OrganizationID *string `json:"organization_id"`
	UserID         *string `json:"user_id"`
	Role           *string `json:"role"`
	Member         *struct {
		UserID    *string `json:"user_id"`
		UserEmail *string `json:"user_email"`
		Role      *string `json:"role"`
	} `json:"member"`
}

func invalidOrganizationRole(role string) core.ProxyError {
	return proxyError(400, fmt.Sprintf("Invalid role. Must be one of: %v", organizationRoles))
}

func (h *Handlers) addOrganizationMember(w http.ResponseWriter, r *http.Request) {
	var req organizationMemberRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	var errs validationError
	if req.OrganizationID == nil {
		errs = append(errs, missing(raw, "organization_id"))
	}
	if req.Member == nil {
		errs = append(errs, missing(raw, "member"))
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	role := derefOr(req.Member.Role, "internal_user")
	if !slices.Contains(organizationRoles, role) {
		invalidOrganizationRole(role).Write(w)
		return
	}
	userID, err := h.memberUserID(r.Context(), req.Member.UserID, req.Member.UserEmail)
	if err != nil {
		h.writeMemberLookupError(w, r, err)
		return
	}
	err = h.organizations.AddMember(r.Context(), *req.OrganizationID, userID, role)
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, row{"organization_id": *req.OrganizationID,
			"updated_organization_memberships": []row{{"organization_id": *req.OrganizationID, "user_id": userID, "user_role": role}}})
	default:
		h.writeOrganizationMemberError(w, r, *req.OrganizationID, userID, err)
	}
}

func (h *Handlers) updateOrganizationMember(w http.ResponseWriter, r *http.Request) {
	var req organizationMemberRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	var errs validationError
	if req.OrganizationID == nil {
		errs = append(errs, missing(raw, "organization_id"))
	}
	if req.UserID == nil {
		errs = append(errs, missing(raw, "user_id"))
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	if req.Role == nil {
		core.WriteJSON(w, http.StatusOK, row{"organization_id": *req.OrganizationID, "user_id": *req.UserID})
		return
	}
	if !slices.Contains(organizationRoles, *req.Role) {
		invalidOrganizationRole(*req.Role).Write(w)
		return
	}
	if err := h.organizations.UpdateMember(r.Context(), *req.OrganizationID, *req.UserID, *req.Role); err != nil {
		h.writeOrganizationMemberError(w, r, *req.OrganizationID, *req.UserID, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"organization_id": *req.OrganizationID, "user_id": *req.UserID, "user_role": *req.Role})
}

func (h *Handlers) deleteOrganizationMember(w http.ResponseWriter, r *http.Request) {
	var req organizationMemberRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	var errs validationError
	if req.OrganizationID == nil {
		errs = append(errs, missing(raw, "organization_id"))
	}
	if req.UserID == nil {
		errs = append(errs, missing(raw, "user_id"))
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	if err := h.organizations.RemoveMember(r.Context(), *req.OrganizationID, *req.UserID); err != nil {
		h.writeOrganizationMemberError(w, r, *req.OrganizationID, *req.UserID, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"organization_id": *req.OrganizationID, "user_id": *req.UserID, "message": "Member removed from organization"})
}

func (h *Handlers) writeOrganizationMemberError(w http.ResponseWriter, r *http.Request, orgID, userID string, err error) {
	switch {
	case errors.Is(err, auth.ErrOrganizationNotFound):
		organizationNotFound(orgID).Write(w)
	case errors.Is(err, auth.ErrUserNotFound):
		proxyError(400, "User not found. user_id="+userID).Write(w)
	case errors.Is(err, auth.ErrAlreadyOrgMember):
		proxyError(400, "User "+userID+" is already a member of organization "+orgID).Write(w)
	case errors.Is(err, auth.ErrNotOrgMember):
		proxyError(400, "User "+userID+" is not a member of organization "+orgID).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// memberUserID 는 user_id 나 user_email 로 찾은 사용자 id 다. 둘 다 없으면 errNoMemberRef
var errNoMemberRef = errors.New("user_id 나 user_email 이 필요하다")

func (h *Handlers) memberUserID(ctx context.Context, userID, email *string) (string, error) {
	switch {
	case userID != nil && *userID != "":
		return *userID, nil
	case email != nil && *email != "":
		user, err := h.users.FindByEmail(ctx, *email)
		if err != nil {
			return "", err
		}
		return user.UserID, nil
	}
	return "", errNoMemberRef
}

func (h *Handlers) writeMemberLookupError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errNoMemberRef):
		proxyError(400, "Either user_id or user_email must be provided").Write(w)
	case errors.Is(err, auth.ErrUserNotFound):
		proxyError(400, "User not found for the given user_email").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// 팀과 조직의 연결 검사(litellm _check_org_team_limits). 상한은 요청이 보낸 값만 검사한다

// organizationCapError 는 팀의 모델·예산·RPM·TPM 이 조직 상한을 넘는지다. 문제가 있으면 사용자에게 보일 문구를, 아니면 빈 문자열을 돌려준다.
// 조직이 없으면 "Organization not found" 문구다. 보내지 않은 값(nil)은 검사하지 않는다
func (h *Handlers) organizationCapError(ctx context.Context, orgID string, models []string, maxBudget *float64, tpm, rpm *int64) (string, error) {
	org, err := h.organizations.Find(ctx, orgID)
	switch {
	case errors.Is(err, auth.ErrOrganizationNotFound):
		return "Organization not found for organization_id=" + orgID, nil
	case err != nil:
		return "", err
	}
	policies, err := h.limits.Find(ctx, limits.ScopeOrganization, []string{orgID})
	if err != nil {
		return "", err
	}
	cap := policies[orgID]
	switch {
	case maxBudget != nil && cap.MaxBudget != nil && *maxBudget > *cap.MaxBudget:
		return fmt.Sprintf("Team max_budget (%s) exceeds organization's max_budget (%s). Organization: %s",
			strconv.FormatFloat(*maxBudget, 'f', -1, 64), strconv.FormatFloat(*cap.MaxBudget, 'f', -1, 64), orgID), nil
	case tpm != nil && cap.TPMLimit != nil && *tpm > *cap.TPMLimit:
		return fmt.Sprintf("Team tpm_limit (%d) exceeds organization's tpm_limit (%d). Organization: %s", *tpm, *cap.TPMLimit, orgID), nil
	case rpm != nil && cap.RPMLimit != nil && *rpm > *cap.RPMLimit:
		return fmt.Sprintf("Team rpm_limit (%d) exceeds organization's rpm_limit (%d). Organization: %s", *rpm, *cap.RPMLimit, orgID), nil
	}
	if len(org.Models) > 0 && !slices.Contains(org.Models, "all-proxy-models") {
		for _, m := range models {
			if !slices.Contains(org.Models, m) {
				return fmt.Sprintf("Model '%s' not in organization's allowed models. Organization allowed models=%s. Organization: %s", m, pyList(org.Models), orgID), nil
			}
		}
	}
	return "", nil
}

// teamOrganizationCheck 는 팀을 고칠 때의 조직 검사다. 조직이나 상한이 걸린 값(모델·예산·RPM·TPM)을 보냈을 때만 한다.
// 고칠 팀이 없으면 검사하지 않는다(그 오류는 고치는 쪽이 낸다)
func (h *Handlers) teamOrganizationCheck(ctx context.Context, req teamUpdate) (string, error) {
	if req.TeamID == nil || !(req.OrganizationID.Set || req.Models.Set || req.MaxBudget.Set || req.TPMLimit.Set || req.RPMLimit.Set) {
		return "", nil
	}
	orgID := nonEmpty(req.OrganizationID.Value)
	if !req.OrganizationID.Set {
		team, err := h.teams.Find(ctx, *req.TeamID)
		if errors.Is(err, auth.ErrTeamNotFound) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		orgID = team.OrganizationID
	}
	if orgID == nil {
		return "", nil
	}
	return h.organizationCapError(ctx, *orgID, derefSlice(req.Models.Value), req.MaxBudget.Value, req.TPMLimit.Value, req.RPMLimit.Value)
}

// normalizeOrganizationChange 는 organization_id "" 를 조직에서 빼는 것(null)으로 본다
func normalizeOrganizationChange(c core.Change[string]) core.Change[string] {
	if c.Set && c.Value != nil && *c.Value == "" {
		return core.Change[string]{Set: true}
	}
	return c
}

// modelBudgetsEqual 은 모델별 한도가 같은지다(없는 것과 빈 것은 같다)
func modelBudgetsEqual(a, b map[string]limits.ModelBudget) bool {
	return (len(a) == 0 && len(b) == 0) || reflect.DeepEqual(a, b)
}
