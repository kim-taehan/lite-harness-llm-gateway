package admin

import (
	"context"
	"errors"
	"net/http"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// litellm 사용자 관리 API(H_team_user_contract.md 4장, I 3.1)

// litellm /user/new·/user/update 에 들어갈 수 있는 역할(UR:21-30). 다른 값은 422
var assignableRoles = []core.Role{core.RoleProxyAdmin, core.RoleProxyAdminViewer, core.RoleInternalUser, core.RoleInternalUserViewer}

func roleError(field string, value core.Role) *FieldError {
	for _, r := range assignableRoles {
		if r == value {
			return nil
		}
	}
	return &FieldError{Type: "enum", Loc: []any{"body", field},
		Msg: "Input should be 'proxy_admin', 'proxy_admin_viewer', 'internal_user' or 'internal_user_viewer'", Input: string(value)}
}

func (h *Handlers) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID    *string        `json:"user_id"`
		UserEmail *string        `json:"user_email"`
		UserAlias *string        `json:"user_alias"`
		UserRole  *core.Role     `json:"user_role"`
		Models    []string       `json:"models"`
		Metadata  map[string]any `json:"metadata"`
		TeamID    *string        `json:"team_id"`
		Password  *string        `json:"password"`
		limitFields
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	var errs validationError
	if req.UserRole != nil {
		if e := roleError("user_role", *req.UserRole); e != nil {
			errs = append(errs, *e)
		}
	}
	if req.Password != nil {
		// litellm 은 비밀번호를 /user/new 로 받지 않는다(UR:36-44)
		errs = append(errs, FieldError{Type: "value_error", Loc: []any{"body"}, Msg: "Value error, Setting password via /user/new is not allowed. Use /invitation/new instead.", Input: raw})
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	if req.Metadata == nil {
		req.Metadata = map[string]any{}
	}
	input := auth.NewUser{UserID: req.UserID, UserEmail: req.UserEmail, UserAlias: req.UserAlias, UserRole: req.UserRole,
		Models: req.Models, Metadata: req.Metadata, TeamID: req.TeamID}

	var user auth.User
	var teamIDs []string
	var policy limits.Policy
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if user, teamIDs, err = h.users.Create(ctx, input); err != nil {
			return err
		}
		policy, err = h.limits.Apply(ctx, limits.ScopeUser, user.UserID, req.patch())
		return err
	})
	var invalid *limits.InvalidError
	var taken *auth.EmailTakenError
	switch {
	case err == nil:
		response := userRow(user, teamIDs, policy)
		// NewUserResponse 는 키 발급 응답 모양을 겸한다. 키 발급은 M2 라 auto_create_key 와 상관없이 빈 문자열이다
		// (대시보드는 auto_create_key=false 를 보내므로 litellm 에서도 빈 문자열이다)
		response["key"] = ""
		response["key_name"] = nil
		response["expires"] = nil
		response["team_id"] = req.TeamID
		core.WriteJSON(w, http.StatusOK, response)
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.Is(err, auth.ErrUserIDTaken):
		proxyError(409, "User with id "+derefOr(req.UserID, "")+" already exists").Write(w)
	case errors.As(err, &taken):
		proxyError(409, "User with email "+taken.Existing+" already exists").Write(w)
	case errors.Is(err, auth.ErrTeamNotFound):
		proxyError(400, "Team doesn't exist in db. Team="+derefOr(req.TeamID, "")+". Create team via `/team/new` call.").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// userUpdate 는 /user/update 본문이다. /user/bulk_update 의 사용자 한 명도 같은 모양이다(I 3.4)
type userUpdate struct {
	UserID    *string                     `json:"user_id"`
	UserEmail core.Change[string]         `json:"user_email"`
	UserAlias core.Change[string]         `json:"user_alias"`
	UserRole  core.Change[core.Role]      `json:"user_role"`
	Models    core.Change[[]string]       `json:"models"`
	Metadata  core.Change[map[string]any] `json:"metadata"`
	Password  *string                     `json:"password"`
	limitFields
}

func (u userUpdate) validate(raw any) validationError {
	var errs validationError
	if u.UserID == nil && u.UserEmail.Value == nil {
		errs = append(errs, FieldError{Type: "value_error", Loc: []any{"body"}, Msg: "Value error, Either user_id or user_email must be provided", Input: raw})
	}
	if u.UserRole.Value != nil {
		if e := roleError("user_role", *u.UserRole.Value); e != nil {
			errs = append(errs, *e)
		}
	}
	return errs
}

// applyUserUpdate 는 사용자 한 명을 고친다(신원·비밀번호·한도를 한 트랜잭션으로)
func (h *Handlers) applyUserUpdate(ctx context.Context, req userUpdate) (auth.User, limits.Policy, error) {
	var user auth.User
	var policy limits.Policy
	err := h.db.InTx(ctx, func(ctx context.Context) error {
		userID, err := h.updateTarget(ctx, req.UserID, req.UserEmail.Value)
		if err != nil {
			return err
		}
		patch := auth.UserPatch{UserEmail: req.UserEmail, UserAlias: req.UserAlias, UserRole: req.UserRole, Models: req.Models, Metadata: req.Metadata}
		if req.UserID == nil {
			patch.UserEmail = core.Change[string]{} // 이메일로 찾았으면 그 이메일은 바꾸는 값이 아니다
		}
		if user, err = h.users.Update(ctx, userID, patch); err != nil {
			return err
		}
		// litellm: 관리자가 /user/update 로 비밀번호를 정할 수 있다(해시로 저장, 응답에는 싣지 않는다)
		if req.Password != nil {
			if err := h.users.SetPassword(ctx, userID, *req.Password, true, h.now()); err != nil {
				return err
			}
		}
		policy, err = h.limits.Apply(ctx, limits.ScopeUser, userID, req.patch())
		return err
	})
	return user, policy, err
}

func (h *Handlers) updateUser(w http.ResponseWriter, r *http.Request) {
	var req userUpdate
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if errs := req.validate(raw); len(errs) > 0 {
		errs.write(w)
		return
	}
	// /user/update 는 litellm 에서 auth_error·"None" 으로 감싼다(IU:1729-1746)
	authStyle := func(status int, message string) core.ProxyError {
		return core.ProxyError{Status: status, Message: pyDict("error", message), Type: "auth_error", Param: "None"}
	}
	user, policy, err := h.applyUserUpdate(r.Context(), req)
	var invalid *limits.InvalidError
	var taken *auth.EmailTakenError
	var weak *auth.PasswordPolicyError
	switch {
	case errors.As(err, &weak):
		core.ProxyError{Status: 400, Message: weak.Message, Type: "validation_error", Param: "password"}.Write(w)
	case err == nil:
		teamIDs, err := h.users.TeamIDs(r.Context(), []string{user.UserID})
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		core.WriteJSON(w, http.StatusOK, row{"user_id": user.UserID, "data": userRow(user, teamIDs[user.UserID], policy)})
	case errors.As(err, &invalid):
		authStyle(400, invalid.Message).Write(w)
	case errors.Is(err, auth.ErrUserNotFound):
		// litellm 은 proxy_admin 이 없는 user_id 로 부르면 사용자를 만든다. 버그로 보고 따르지 않는다(설계 9장)
		authStyle(404, "User not found, passed user_id="+derefOr(req.UserID, derefOr(req.UserEmail.Value, ""))).Write(w)
	case errors.As(err, &taken):
		authStyle(409, "User with email "+taken.Existing+" already exists").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// updateTarget 은 수정할 사용자의 id 다. user_id 가 없으면 이메일로 찾는다
func (h *Handlers) updateTarget(ctx context.Context, userID, email *string) (string, error) {
	if userID != nil {
		return *userID, nil
	}
	user, err := h.users.FindByEmail(ctx, *email)
	return user.UserID, err
}

func (h *Handlers) deleteUsers(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.UserIDs == nil {
		validationError{missing(raw, "user_ids")}.write(w)
		return
	}
	ids := unique(req.UserIDs)
	var deleted int64
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if deleted, err = h.users.Delete(ctx, ids); err != nil {
			return err
		}
		return h.limits.Delete(ctx, limits.ScopeUser, ids)
	})
	var notFound *auth.NotFoundError
	switch {
	case err == nil:
		// litellm 은 지운 사용자 수를 JSON 정수 하나로 돌려준다
		core.WriteJSON(w, http.StatusOK, deleted)
	case errors.As(err, &notFound):
		detailError(404, "User not found, passed user_id="+notFound.ID).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// userInfo 는 v1 /user/info 다. 대시보드는 쓰지 않지만(v2 를 쓴다) API 호환을 위해 둔다
func (h *Handlers) userInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		// 관리자가 user_id 없이 부르면 litellm 은 모든 팀과 호출자 본인 행을 준다
		h.adminUserInfo(w, r)
		return
	}
	user, err := h.users.Find(ctx, userID)
	if errors.Is(err, auth.ErrUserNotFound) {
		core.ProxyError{Status: 404, Message: "User " + userID + " not found", Type: "internal_server_error"}.Write(w)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	userRows, err := h.rows.userRows(ctx, []auth.User{user})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	teamIDs, _ := userRows[0]["teams"].([]string)
	teams, err := h.teams.FindAll(ctx, teamIDs)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	teamRows, _, err := h.rows.teamRows(ctx, teams)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"user_id": userID, "user_info": userRows[0], "keys": []any{}, "teams": teamRows})
}

func (h *Handlers) adminUserInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal, _ := auth.PrincipalFrom(ctx)
	var userInfo any
	if user, err := h.users.Find(ctx, principal.UserID); err == nil {
		userRows, err := h.rows.userRows(ctx, []auth.User{user})
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		userInfo = userRows[0]
	} else if !errors.Is(err, auth.ErrUserNotFound) {
		h.internalError(w, r, err)
		return
	}
	teams, err := h.teams.ListByAlias(ctx, nil)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	teamRows, _, err := h.rows.teamRows(ctx, teams)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"user_id": principal.UserID, "user_info": userInfo, "keys": []any{}, "teams": teamRows})
}

// userInfoV2 는 사용자 상세 화면의 본체다. 평면 객체이고 teams 는 id 배열이다
func (h *Handlers) userInfoV2(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		principal, _ := auth.PrincipalFrom(r.Context())
		userID = principal.UserID
	}
	user, err := h.users.Find(r.Context(), userID)
	if errors.Is(err, auth.ErrUserNotFound) {
		// litellm 은 사용자 없음과 권한 없음을 구분하지 않고 둘 다 404 다
		core.ProxyError{Status: 404, Message: "User not found: " + userID, Type: "internal_server_error"}.Write(w)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	userRows, err := h.rows.userRows(r.Context(), []auth.User{user})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	full := userRows[0]
	response := row{"model_max_budget_usage": nil}
	for _, k := range []string{"user_id", "user_email", "user_alias", "user_role", "spend", "max_budget", "tpm_limit", "rpm_limit",
		"models", "budget_duration", "budget_reset_at", "metadata", "created_at", "updated_at", "sso_user_id", "teams",
		"object_permission", "model_max_budget"} {
		response[k] = full[k]
	}
	core.WriteJSON(w, http.StatusOK, response)
}

func (h *Handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	var errs validationError
	page := queryInt(r, "page", 1, 1, 0, &errs)
	pageSize := queryInt(r, "page_size", 25, 1, 100, &errs)
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	sortBy := r.URL.Query().Get("sort_by")
	switch sortBy {
	case "", "user_id", "user_email", "created_at", "user_alias", "user_role":
	case "spend":
		// 사용량은 M3 전까지 모두 0 이라 spend 정렬은 기본 정렬과 같다. M3 에서 limits 값으로 정렬한다
		sortBy = ""
	default:
		detailError(400, "Invalid sort column. Must be one of: user_id, user_email, created_at, spend, user_alias, user_role").Write(w)
		return
	}
	order := auth.SortAsc
	switch r.URL.Query().Get("sort_order") {
	case "", "asc":
	case "desc":
		order = auth.SortDesc
	default:
		detailError(400, "Invalid sort order. Must be 'asc' or 'desc'").Write(w)
		return
	}
	result, err := h.users.Page(r.Context(), auth.UserQuery{
		Role: queryString(r, "role"), UserIDs: queryList(r, "user_ids"), SSOUserIDs: queryList(r, "sso_user_ids"),
		UserEmailContains: queryString(r, "user_email"), Search: queryString(r, "search"), TeamID: queryString(r, "team"),
		SortBy: sortBy, SortOrder: order, Page: page, PageSize: pageSize,
	})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	userRows, err := h.rows.userRows(r.Context(), result.Items)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	for _, u := range userRows {
		u["key_count"] = 0 // 키는 M2
	}
	core.WriteJSON(w, http.StatusOK, row{
		"users": userRows, "total": result.Total, "page": page, "page_size": pageSize, "total_pages": totalPages(result.Total, pageSize),
	})
}
