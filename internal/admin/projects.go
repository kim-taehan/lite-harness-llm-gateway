package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// 프로젝트 관리 API(대시보드 Projects 화면, litellm /project/*). 프로젝트는 팀 안에서 키를 묶고 그 묶음에 예산·허용 모델·차단을 건다.
// 조회(목록·상세)는 로그인한 누구나 부르고 자기 팀의 프로젝트만 보인다(키 만들기 화면이 일반 사용자에게도 프로젝트를 고르게 한다).
// 만들기·고치기·지우기는 관리자만이다. litellm 은 팀 관리자에게도 열어 두지만(화면 버튼도 그렇다) 권한 검사를 새로 만들지 않으려고 뺐다.
// litellm 에서 프로젝트는 enterprise 기능인데 우리는 막지 않는다

type projectRequest struct {
	ProjectID    *string                     `json:"project_id"`
	ProjectAlias core.Change[string]         `json:"project_alias"`
	Description  core.Change[string]         `json:"description"`
	TeamID       *string                     `json:"team_id"`
	Models       core.Change[[]string]       `json:"models"`
	Metadata     core.Change[map[string]any] `json:"metadata"`
	Blocked      core.Change[bool]           `json:"blocked"`
	limitFields
}

// projectRow 는 대시보드 ProjectResponse 다. 모델별 사용액(model_spend)·모델별 한도 사전은 우리 방식과 달라 비워 둔다
func projectRow(p auth.Project, policy limits.Policy) row {
	r := row{
		"project_id": p.ProjectID, "project_alias": p.ProjectAlias, "description": p.Description, "team_id": p.TeamID,
		"budget_id": nil, "metadata": orEmptyMetadata(p.Metadata), "models": nonNil(p.Models), "spend": policy.Spend, "model_spend": row{},
		"model_rpm_limit": row{}, "model_tpm_limit": row{}, "blocked": p.Blocked, "object_permission_id": nil,
		"created_at": isoTime(p.CreatedAt), "created_by": p.CreatedBy, "updated_at": isoTime(p.UpdatedAt), "updated_by": p.UpdatedBy,
		"litellm_budget_table": nil,
	}
	if policy.HasLimits() {
		r["litellm_budget_table"] = row{
			"budget_id": p.ProjectID, "max_budget": policy.MaxBudget, "soft_budget": policy.SoftBudget, "tpm_limit": policy.TPMLimit,
			"rpm_limit": policy.RPMLimit, "tpd_limit": policy.TPDLimit, "max_parallel_requests": policy.MaxParallelRequests,
			"budget_duration": policy.BudgetDuration, "model_max_budget": modelBudgetsView(policy.ModelBudgets),
		}
	}
	return r
}

func orEmptyMetadata(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (h *Handlers) projectRows(ctx context.Context, projects []auth.Project) ([]row, error) {
	ids := make([]string, len(projects))
	for i, p := range projects {
		ids[i] = p.ProjectID
	}
	policies, err := h.limits.Find(ctx, limits.ScopeProject, ids)
	if err != nil {
		return nil, err
	}
	result := make([]row, len(projects))
	for i, p := range projects {
		result[i] = projectRow(p, policies[p.ProjectID])
	}
	return result, nil
}

// visibleTeams 는 이 사용자가 프로젝트를 볼 수 있는 팀이다. 관리자(읽기 전용 포함)는 nil(전부), 나머지는 소속 팀이다
func (h *Handlers) visibleTeams(ctx context.Context, p auth.Principal) ([]string, error) {
	if p.CanViewAll() {
		return nil, nil
	}
	teams, err := h.users.TeamIDs(ctx, []string{p.UserID})
	if err != nil {
		return nil, err
	}
	return append([]string{}, teams[p.UserID]...), nil
}

func (h *Handlers) listProjects(w http.ResponseWriter, r *http.Request) {
	teams, err := h.visibleTeams(r.Context(), principal(r.Context()))
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	projects, err := h.projects.List(r.Context(), teams)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	rows, err := h.projectRows(r.Context(), projects)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if rows == nil {
		rows = []row{}
	}
	core.WriteJSON(w, http.StatusOK, rows)
}

func (h *Handlers) projectInfo(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("project_id")
	if id == "" {
		validationError{{Type: "missing", Loc: []any{"query", "project_id"}, Msg: "Field required", Input: nil}}.write(w)
		return
	}
	project, ok := h.visibleProject(w, r, id)
	if !ok {
		return
	}
	rows, err := h.projectRows(r.Context(), []auth.Project{project})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, rows[0])
}

// visibleProject 는 이 호출자가 볼 수 있는 프로젝트다. 없는 프로젝트와 남의 팀 프로젝트는 같은 404 로 답해 존재를 알리지 않는다
func (h *Handlers) visibleProject(w http.ResponseWriter, r *http.Request, id string) (auth.Project, bool) {
	project, err := h.projects.Find(r.Context(), id)
	switch {
	case errors.Is(err, auth.ErrProjectNotFound):
		projectNotFound(id).Write(w)
		return auth.Project{}, false
	case err != nil:
		h.internalError(w, r, err)
		return auth.Project{}, false
	}
	teams, err := h.visibleTeams(r.Context(), principal(r.Context()))
	if err != nil {
		h.internalError(w, r, err)
		return auth.Project{}, false
	}
	if teams != nil && !slices.Contains(teams, project.TeamID) {
		projectNotFound(id).Write(w)
		return auth.Project{}, false
	}
	return project, true
}

func projectNotFound(id string) core.ProxyError {
	return core.ProxyError{Status: http.StatusNotFound, Type: "not_found", Param: "project_id", Message: "Project not found. project_id=" + id}
}

func (h *Handlers) newProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.TeamID == nil || strings.TrimSpace(*req.TeamID) == "" {
		validationError{missing(raw, "team_id")}.write(w)
		return
	}
	var created auth.Project
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		created, err = h.projects.Create(ctx, auth.NewProject{ProjectAlias: req.ProjectAlias.Value, Description: req.Description.Value, TeamID: *req.TeamID,
			Models: req.Models.Or(nil), Metadata: req.Metadata.Or(nil), Blocked: req.Blocked.Value != nil && *req.Blocked.Value, CreatedBy: principal(ctx).UserID})
		if err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeProject, created.ProjectID, req.patch())
		return err
	})
	h.writeProjectResult(w, r, created, "", *req.TeamID, err)
}

func (h *Handlers) updateProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.ProjectID == nil {
		validationError{missing(raw, "project_id")}.write(w)
		return
	}
	var updated auth.Project
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		updated, err = h.projects.Update(ctx, *req.ProjectID, auth.ProjectPatch{ProjectAlias: req.ProjectAlias, Description: req.Description,
			TeamID: req.TeamID, Models: req.Models, Metadata: req.Metadata, Blocked: req.Blocked, UpdatedBy: principal(ctx).UserID})
		if err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeProject, updated.ProjectID, req.patch())
		return err
	})
	h.writeProjectResult(w, r, updated, *req.ProjectID, derefOr(req.TeamID, ""), err)
}

// writeProjectResult 는 만들기·고치기의 공통 응답이다. projectID 는 고치기에서 없는 프로젝트를 알리는 데, teamID 는 없는 팀을 알리는 데 쓴다
func (h *Handlers) writeProjectResult(w http.ResponseWriter, r *http.Request, project auth.Project, projectID, teamID string, err error) {
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		rows, err := h.projectRows(r.Context(), []auth.Project{project})
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		core.WriteJSON(w, http.StatusOK, rows[0])
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.Is(err, auth.ErrProjectNotFound):
		projectNotFound(projectID).Write(w)
	case errors.Is(err, auth.ErrTeamNotFound):
		proxyError(400, "Team not found for team_id="+teamID).Write(w)
	case errors.Is(err, auth.ErrProjectHasKeys):
		proxyError(400, "Cannot move a project that still has keys to another team. Reassign or delete its keys first.").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) deleteProjects(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectIDs *[]string `json:"project_ids"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.ProjectIDs == nil {
		validationError{missing(raw, "project_ids")}.write(w)
		return
	}
	ids := unique(*req.ProjectIDs)
	var deleted []string
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if deleted, err = h.projects.Delete(ctx, ids); err != nil {
			return err
		}
		return h.limits.Delete(ctx, limits.ScopeProject, deleted)
	})
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, row{"deleted_projects": nonNil(deleted)})
	case errors.Is(err, auth.ErrProjectHasKeys):
		proxyError(400, "Cannot delete a project that still has keys. Reassign or delete its keys first.").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// 키와 프로젝트의 연결 검사. 프로젝트의 팀이 키의 팀과 같아야 한다. 문제가 있으면 사용자에게 보일 문구를, 아니면 빈 문자열을 돌려준다

func projectNotFoundForKey(projectID string) string {
	return "Project not found for project_id=" + projectID
}

func projectTeamMismatch(projectID, projectTeam string) string {
	return "project_id=" + projectID + " belongs to team_id=" + projectTeam + ". A key in a project must belong to the same team."
}

// keyProjectForGenerate 는 새 키의 프로젝트를 검사한다. 키의 팀을 안 줬으면 프로젝트의 팀으로 채운다
func (h *Handlers) keyProjectForGenerate(ctx context.Context, projectID string, teamID **string) (string, error) {
	project, err := h.projects.Find(ctx, projectID)
	switch {
	case errors.Is(err, auth.ErrProjectNotFound):
		return projectNotFoundForKey(projectID), nil
	case err != nil:
		return "", err
	}
	if *teamID == nil {
		team := project.TeamID
		*teamID = &team
	} else if **teamID != project.TeamID {
		return projectTeamMismatch(projectID, project.TeamID), nil
	}
	return "", nil
}

// keyProjectPatch 는 키를 고칠 때의 프로젝트 변경을 검사하고 patch 에 담는다. 프로젝트를 새로 고르거나 비울 수 있고,
// 프로젝트가 있는 키의 팀을 바꾸면 프로젝트의 팀과 달라지므로 프로젝트를 함께 비우지 않는 한 거절한다
func (h *Handlers) keyProjectPatch(ctx context.Context, req keyFields, key auth.Key, patch *auth.KeyPatch) (string, error) {
	team := key.TeamID
	if patch.TeamID.Set {
		team = patch.TeamID.Value
	}
	projectID := key.ProjectID
	if req.ProjectID.Set {
		projectID = nonEmpty(req.ProjectID.Value)
		patch.ProjectID = core.Change[string]{Set: true, Value: projectID}
	} else if !patch.TeamID.Set || projectID == nil {
		return "", nil
	}
	if projectID == nil {
		return "", nil
	}
	project, err := h.projects.Find(ctx, *projectID)
	switch {
	case errors.Is(err, auth.ErrProjectNotFound):
		return projectNotFoundForKey(*projectID), nil
	case err != nil:
		return "", err
	}
	if team == nil || *team != project.TeamID {
		return projectTeamMismatch(*projectID, project.TeamID), nil
	}
	return "", nil
}
