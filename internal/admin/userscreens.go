package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// 사용자 화면의 나머지 엔드포인트(I 3.2~3.4, 2.10)

// maxBulk 는 litellm 일괄 작업 상한이다
const maxBulk = 500

// availableRoles 는 역할 선택지와 화면 라벨이다(litellm ROLES 문구 그대로, 선언 순서)
func (h *Handlers) availableRoles(w http.ResponseWriter, _ *http.Request) {
	core.WriteJSON(w, http.StatusOK, orderedRoles)
}

// orderedRoles 는 키 순서를 지키려고 미리 만든 JSON 이다(map 은 순서가 없다)
var orderedRoles = json.RawMessage(`{` +
	`"proxy_admin":{"description":"admin over litellm proxy, has all permissions","ui_label":"Admin (All Permissions)"},` +
	`"proxy_admin_viewer":{"description":"view all keys, view all spend","ui_label":"Admin (View Only)"},` +
	`"internal_user":{"description":"view/create/delete their own keys, view their own spend","ui_label":"Internal User (Create/Delete/View)"},` +
	`"internal_user_viewer":{"description":"view their own keys, view their own spend","ui_label":"Internal User (View Only)"}}`)

// filterUsers 는 사용자 검색 드롭다운이다(맨 배열, id·이메일만). litellm 처럼 인증된 누구나 검색한다
func (h *Handlers) filterUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := auth.UserQuery{Page: positiveInt(q.Get("page"), 1), PageSize: min(positiveInt(q.Get("page_size"), 50), 100)}
	for _, v := range []string{q.Get("user_id"), q.Get("user_email"), q.Get("search")} {
		if v != "" {
			query.Search = &v // 우리 검색은 id·이메일 부분 일치(대소문자 무시)를 함께 본다
			break
		}
	}
	page, err := h.users.Page(r.Context(), query)
	if err != nil {
		h.internalError(w, r, err) // DB 오류 원문은 로그에만 남기고 응답에 싣지 않는다
		return
	}
	result := make([]row, 0, len(page.Items))
	for _, u := range page.Items {
		result = append(result, row{"user_id": u.UserID, "user_email": u.UserEmail})
	}
	core.WriteJSON(w, http.StatusOK, result)
}

// bulkUpdateUsers 는 선택한 사용자들(users) 또는 전체(all_users + user_updates)를 같은 값으로 고친다.
// 사용자마다 따로 처리하고, 개별 실패는 결과에 남기며 전체는 200 이다(litellm 과 같다)
func (h *Handlers) bulkUpdateUsers(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Users       []userUpdate `json:"users"`
		AllUsers    bool         `json:"all_users"`
		UserUpdates *userUpdate  `json:"user_updates"`
	}
	if _, ok := requestBody(w, r, &req); !ok {
		return
	}
	targets := req.Users
	switch {
	case req.AllUsers && req.UserUpdates != nil:
		if req.UserUpdates.Password != nil {
			detailError(400, "Setting one password for all users is not supported. Set passwords per user via /user/update.").Write(w)
			return
		}
		page, err := h.users.Page(r.Context(), auth.UserQuery{Page: 1, PageSize: maxBulk + 1})
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		targets = make([]userUpdate, 0, len(page.Items))
		for _, u := range page.Items {
			t := *req.UserUpdates
			t.UserID, t.UserEmail = &u.UserID, core.Change[string]{}
			targets = append(targets, t)
		}
	case len(req.Users) == 0:
		detailError(400, "Must specify either 'users' for individual updates or 'all_users=True' with 'user_updates' for bulk updates").Write(w)
		return
	}
	switch {
	case len(targets) == 0:
		detailError(400, "No users found to update").Write(w)
		return
	case len(targets) > maxBulk:
		detailError(400, "Maximum 500 users can be updated at once").Write(w)
		return
	}
	results := make([]row, 0, len(targets))
	succeeded := 0
	for _, t := range targets {
		result := row{"user_id": t.UserID, "user_email": t.UserEmail.Value, "success": false, "error": nil, "updated_user": nil}
		if errs := t.validate(nil); len(errs) > 0 {
			result["error"] = errs[0].Msg
			results = append(results, result)
			continue
		}
		user, policy, err := h.applyUserUpdate(r.Context(), t)
		if err != nil {
			result["error"] = bulkErrorText(err)
			results = append(results, result)
			continue
		}
		teamIDs, err := h.users.TeamIDs(r.Context(), []string{user.UserID})
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		result["success"], result["user_id"], result["updated_user"] = true, user.UserID, userRow(user, teamIDs[user.UserID], policy)
		results = append(results, result)
		succeeded++
	}
	core.WriteJSON(w, http.StatusOK, row{"results": results, "total_requested": len(targets),
		"successful_updates": succeeded, "failed_updates": len(targets) - succeeded})
}

// bulkErrorText 는 일괄 작업 결과의 error 문자열이다. 예상한 실패만 내용을 싣고 나머지는 숨긴다
func bulkErrorText(err error) string {
	var taken *auth.EmailTakenError
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		return "User not found"
	case errors.As(err, &taken):
		return "User with email " + taken.Existing + " already exists"
	case errors.Is(err, auth.ErrTeamNotFound):
		return "Team not found"
	}
	var already *auth.AlreadyMemberError
	if errors.As(err, &already) {
		return alreadyMessage(already)
	}
	var weak *auth.PasswordPolicyError
	if errors.As(err, &weak) {
		return weak.Message
	}
	var invalid *limits.InvalidError
	if errors.As(err, &invalid) {
		return invalid.Message
	}
	return "Internal Server Error"
}

// bulkAddTeamMembers 는 POST /team/bulk_member_add 다. 선택한 사용자들 또는 전체 사용자를 팀에 넣는다.
// litellm 처럼 실패해도 200 이고 결과 행에 success:false 를 남긴다. max_budget_in_team 이 있으면 새로 들어온 팀원의 예산으로 정한다
func (h *Handlers) bulkAddTeamMembers(w http.ResponseWriter, r *http.Request) {
	var req bulkAddRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.TeamID == nil {
		validationError{missing(raw, "team_id")}.write(w)
		return
	}
	refs, ok := h.bulkMemberRefs(w, r, req)
	if !ok {
		return
	}
	var failure error
	if err := h.addMembersWithBudget(r.Context(), *req.TeamID, refs, req.MaxBudgetInTeam); err != nil {
		failure = err
		if bulkErrorText(err) == "Internal Server Error" {
			h.logger.Error("팀원 일괄 추가 실패", "team_id", *req.TeamID, "err", err)
		}
	}
	results := make([]row, 0, len(refs))
	for _, ref := range refs {
		result := row{"user_id": ref.UserID, "user_email": ref.UserEmail, "success": failure == nil, "error": nil,
			"updated_user": nil, "updated_team_membership": nil}
		if failure != nil {
			result["error"] = bulkErrorText(failure)
		}
		results = append(results, result)
	}
	succeeded := 0
	if failure == nil {
		succeeded = len(refs)
	}
	core.WriteJSON(w, http.StatusOK, row{"team_id": *req.TeamID, "results": results, "total_requested": len(refs),
		"successful_additions": succeeded, "failed_additions": len(refs) - succeeded, "updated_team": nil})
}

// bulkAddRequest 는 /team/bulk_member_add 본문이다
type bulkAddRequest struct {
	TeamID          *string      `json:"team_id"`
	Members         []memberJSON `json:"members"`
	AllUsers        bool         `json:"all_users"`
	MaxBudgetInTeam *float64     `json:"max_budget_in_team"`
}

// bulkMemberRefs 는 넣을 팀원 목록이다(all_users 면 전체 사용자). 형식이 틀리거나 개수가 범위 밖이면 응답을 쓰고 false 를 돌려준다
func (h *Handlers) bulkMemberRefs(w http.ResponseWriter, r *http.Request, req bulkAddRequest) ([]auth.MemberRef, bool) {
	var errs validationError
	refs := make([]auth.MemberRef, 0, len(req.Members))
	for i, m := range req.Members {
		refs = append(refs, m.ref([]any{"body", "members", i}, &errs))
	}
	if len(errs) > 0 {
		errs.write(w)
		return nil, false
	}
	if req.AllUsers {
		page, err := h.users.Page(r.Context(), auth.UserQuery{Page: 1, PageSize: maxBulk + 1})
		if err != nil {
			h.internalError(w, r, err)
			return nil, false
		}
		refs = refs[:0]
		for _, u := range page.Items {
			refs = append(refs, auth.MemberRef{UserID: &u.UserID, Role: auth.TeamRoleUser})
		}
	}
	switch {
	case len(refs) == 0:
		detailError(400, "At least one member is required").Write(w)
		return nil, false
	case len(refs) > maxBulk:
		detailError(400, "Maximum 500 members can be added at once").Write(w)
		return nil, false
	}
	return refs, true
}

// addMembersWithBudget 는 팀원을 넣고, 예산이 있으면 새로 들어온 팀원마다 그 값으로 팀원 예산을 정한다. 한 트랜잭션이다
func (h *Handlers) addMembersWithBudget(ctx context.Context, teamID string, refs []auth.MemberRef, maxBudget *float64) error {
	return h.db.InTx(ctx, func(ctx context.Context) error {
		_, added, err := h.teams.AddMembers(ctx, teamID, refs, false)
		if err != nil || maxBudget == nil {
			return err
		}
		for _, u := range added {
			if _, err := h.limits.Apply(ctx, limits.ScopeTeamMember, limits.MemberTarget(teamID, u.UserID), limits.Patch{MaxBudget: core.To(*maxBudget)}); err != nil {
				return err
			}
		}
		return nil
	})
}
