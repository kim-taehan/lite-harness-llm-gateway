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

// 키 조회·목록 핸들러(/key/info, /key/list, /key/aliases). 생성·수정·삭제는 keys.go

func (h *Handlers) keyInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(ctx)
	query := r.URL.Query().Get("key")
	token := auth.TokenOf(query)
	if query == "" {
		token = p.KeyToken // 없으면 호출자 자신의 키
	}
	notFound := core.ProxyError{Status: 404, Message: "Key not found in database", Type: "not_found_error", Param: "key"}
	key, err := h.keys.Find(ctx, token)
	var info row
	switch {
	case err == nil:
		allowed, err := h.canViewKey(ctx, p, key)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		if !allowed {
			plainProxyError(403, "You are not allowed to access this key's info. Your role="+string(p.Role)).Write(w)
			return
		}
		if info, err = h.rows.keyRowOf(ctx, key); err != nil {
			h.internalError(w, r, err)
			return
		}
		info["status"] = keyStatus(key, h.now().UTC())
	case errors.Is(err, auth.ErrKeyNotFound):
		// litellm: 지운 키는 사본을 status=deleted 로 200 응답한다
		deleted, err := h.keys.FindDeleted(ctx, token)
		if errors.Is(err, auth.ErrKeyNotFound) || token == "" {
			notFound.Write(w)
			return
		}
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		allowed, err := h.canViewKey(ctx, p, deleted.Key)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		if !allowed {
			notFound.Write(w)
			return
		}
		info = keyRow(deleted.Key, limits.Policy{})
		info["status"] = "deleted"
		info["deleted_at"] = deleted.DeletedAt.UTC()
		info["deleted_by"] = deleted.DeletedBy
	default:
		h.internalError(w, r, err)
		return
	}
	delete(info, "token") // litellm /key/info 는 token 을 뺀다
	core.WriteJSON(w, http.StatusOK, row{"key": query, "info": info})
}

// canViewKey 는 litellm _can_user_query_key_info 다: admin·viewer, 호출 키 자신, 키 소유자, 키 팀의 멤버
func (h *Handlers) canViewKey(ctx context.Context, p auth.Principal, k auth.Key) (bool, error) {
	if p.CanViewAll() || k.Token == p.KeyToken || (k.UserID != nil && p.UserID != "" && *k.UserID == p.UserID) {
		return true, nil
	}
	if k.TeamID == nil {
		return false, nil
	}
	_, member, err := h.teamRole(ctx, *k.TeamID, p.UserID)
	return member, err
}

var keySortColumns = []string{"spend", "max_budget", "created_at", "updated_at", "token", "key_alias"}

func (h *Handlers) listKeys(w http.ResponseWriter, r *http.Request) {
	var errs validationError
	page := queryInt(r, "page", 1, 1, 0, &errs)
	size := queryInt(r, "size", 10, 1, 100, &errs)
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	if status != "" && !slices.Contains([]string{"active", "expired", "revoked", "deleted"}, status) {
		proxyError(400, "Invalid status value. Supported: 'active', 'expired', 'revoked', 'deleted'.").Write(w)
		return
	}
	sortBy := q.Get("sort_by")
	if sortBy != "" && !slices.Contains(keySortColumns, sortBy) {
		proxyError(400, "Invalid sort_by value. Supported: "+strings.Join(keySortColumns, ", ")+".").Write(w)
		return
	}
	order := auth.SortDesc
	switch q.Get("sort_order") {
	case "", "desc":
	case "asc":
		order = auth.SortAsc
	default:
		proxyError(400, "Invalid sort_order value. Supported: 'asc', 'desc'.").Write(w)
		return
	}
	ctx := r.Context()
	p := principal(ctx)
	substring := q.Get("substring_matching") == "true"
	query := auth.KeyQuery{TeamID: queryString(r, "team_id"), ProjectID: queryString(r, "project_id"), KeyHash: queryString(r, "key_hash"), KeyAlias: queryString(r, "key_alias"),
		AliasContains: substring, Search: queryString(r, "search"), Status: status, SortBy: sortBy, SortOrder: order,
		Page: page, PageSize: size, Now: h.now().UTC()}
	if p.CanViewAll() {
		query.UserID, query.UserIDContains = queryString(r, "user_id"), substring
	} else if denial, denied := h.restrictKeyList(ctx, p, r, &query); denied {
		denial.Write(w)
		return
	}
	if status == "deleted" {
		h.writeDeletedKeys(w, r, query)
		return
	}
	result, err := h.keys.Page(ctx, query)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	keys, err := h.listedKeys(ctx, result.Items, q.Get("return_full_object") == "true" || q.Get("expand") == "user", q.Get("expand") == "user")
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"keys": keys, "total_count": result.Total, "current_page": page, "total_pages": totalPages(result.Total, size)})
}

// restrictKeyList 는 비관리자가 볼 수 있는 키로 좁힌다(J 3.6 가시성)
func (h *Handlers) restrictKeyList(ctx context.Context, p auth.Principal, r *http.Request, q *auth.KeyQuery) (core.ProxyError, bool) {
	if p.UserID == "" {
		return core.ProxyError{Status: 403, Type: "bad_request_error", Param: "user_id",
			Message: "You are not authorized to access this endpoint. No 'user_id' is associated with your API key."}, true
	}
	if uid := queryString(r, "user_id"); uid != nil && *uid != p.UserID {
		return core.ProxyError{Status: 403, Type: "internal_server_error", Param: "None", Message: "You are not authorized to check another user's keys"}, true
	}
	memberTeams, adminTeams, err := h.teamsOf(ctx, p.UserID)
	if err != nil {
		return h.keyCheckFailed(err), true
	}
	if q.TeamID != nil && !slices.Contains(memberTeams, *q.TeamID) {
		return core.ProxyError{Status: 403, Type: "internal_server_error", Param: "None", Message: "You are not authorized to check this team's keys"}, true
	}
	query := r.URL.Query()
	q.Visible = &auth.KeyVisibility{UserID: p.UserID, CreatedBy: query.Get("include_created_by_keys") == "true",
		MemberTeams: memberTeams, AdminTeams: adminTeams, IncludeTeams: query.Get("include_team_keys") == "true"}
	return core.ProxyError{}, false
}

// listedKeys 는 /key/list 원소다. full 이 아니면 litellm 처럼 해시 문자열 배열이다
func (h *Handlers) listedKeys(ctx context.Context, keys []auth.Key, full, expandUser bool) ([]any, error) {
	result := make([]any, len(keys))
	if !full {
		for i, k := range keys {
			result[i] = k.Token
		}
		return result, nil
	}
	rows, err := h.rows.keyRows(ctx, keys)
	if err != nil {
		return nil, err
	}
	users := map[string]row{}
	if expandUser {
		if users, err = h.ownerRows(ctx, keys); err != nil {
			return nil, err
		}
	}
	for i, kr := range rows {
		// /key/list 원소는 litellm UserAPIKeyAuth 직렬화라 조직이 org_id 로 나가고 팀 정보 칸이 더 붙는다(J 2.3)
		delete(kr, "organization_id")
		kr["org_id"] = nil
		kr["team_alias"], kr["team_models"], kr["team_blocked"], kr["api_key"], kr["user_role"] = nil, []string{}, false, nil, nil
		if expandUser {
			k := keys[i]
			kr["user"] = users[derefOr(k.UserID, "")]
			if creator, ok := users[derefOr(k.CreatedBy, "")]; ok {
				kr["created_by_user"] = row{"user_id": creator["user_id"], "user_email": creator["user_email"], "user_alias": creator["user_alias"]}
			} else {
				kr["created_by_user"] = nil
			}
		}
		result[i] = kr
	}
	return result, nil
}

func (h *Handlers) ownerRows(ctx context.Context, keys []auth.Key) (map[string]row, error) {
	var ids []string
	for _, k := range keys {
		for _, id := range []*string{k.UserID, k.CreatedBy} {
			if id != nil && !slices.Contains(ids, *id) {
				ids = append(ids, *id)
			}
		}
	}
	users, err := h.users.FindAll(ctx, ids)
	if err != nil {
		return nil, err
	}
	userRows, err := h.rows.userRows(ctx, users)
	if err != nil {
		return nil, err
	}
	result := map[string]row{}
	for i, u := range users {
		result[u.UserID] = userRows[i]
	}
	return result, nil
}

func (h *Handlers) writeDeletedKeys(w http.ResponseWriter, r *http.Request, q auth.KeyQuery) {
	result, err := h.keys.PageDeleted(r.Context(), q)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	keys := make([]any, len(result.Items))
	for i, d := range result.Items {
		kr := keyRow(d.Key, limits.Policy{})
		kr["deleted_at"] = d.DeletedAt.UTC()
		kr["deleted_by"] = d.DeletedBy
		keys[i] = kr
	}
	core.WriteJSON(w, http.StatusOK, row{"keys": keys, "total_count": result.Total, "current_page": q.Page, "total_pages": totalPages(result.Total, q.PageSize)})
}

func (h *Handlers) keyAliases(w http.ResponseWriter, r *http.Request) {
	var errs validationError
	page := queryInt(r, "page", 1, 1, 0, &errs)
	size := queryInt(r, "size", 50, 1, 100, &errs)
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	ctx := r.Context()
	p := principal(ctx)
	var visible *auth.KeyVisibility
	if !p.CanViewAll() {
		memberTeams, _, err := h.teamsOf(ctx, p.UserID)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		visible = &auth.KeyVisibility{UserID: p.UserID, MemberTeams: memberTeams}
	}
	result, err := h.keys.Aliases(ctx, visible, queryString(r, "search"), queryString(r, "team_id"), page, size)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"aliases": nonNil(result.Items), "total_count": result.Total, "current_page": page,
		"total_pages": totalPages(result.Total, size), "size": size})
}
