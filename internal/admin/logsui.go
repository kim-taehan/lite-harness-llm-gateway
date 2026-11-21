package admin

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
	"llm-gateway/internal/limits"
)

// 대시보드 로그 화면(N 6~9장). 우리 request_log 에는 본문·세션·태그·캐시·최종 사용자가 없다(설계 10장: 본문을 남기지 않는다).
// 그 칸은 화면이 견디는 빈 값으로 내고, 그 값으로 거르는 필터는 빈 결과로 답한다(무시하면 결과가 너무 많아진다)

const logTimeFormat = "2006-01-02 15:04:05"

// logSorts 는 litellm sort_by 값 → limits 정렬 이름이다
var logSorts = map[string]string{"startTime": "created_at", "endTime": "ended_at", "spend": "cost", "total_tokens": "total_tokens",
	"request_duration_ms": "latency_ms", "ttft_ms": "ttft_ms", "model": "model"}

func badRequest(message, param string) core.ProxyError {
	return core.ProxyError{Status: 400, Message: message, Type: "bad_request", Param: param}
}

// logScope 는 비관리자가 볼 수 있는 로그다: 자기 요청 또는 팀 admin 이거나 /spend/logs 권한이 있는 팀의 요청(N 6.4).
// 사용자가 없는 키(서비스 계정)는 litellm 에서는 전부 보지만 우리는 아무것도 보지 않는다(설계 9장)
func (h *Handlers) logScope(ctx context.Context, p auth.Principal) (*limits.LogScope, error) {
	if p.CanViewAll() {
		return nil, nil
	}
	memberships, err := h.teams.MembershipsOf(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	viewable := []string{}
	for _, m := range memberships {
		if mayCall(m, "/spend/logs") {
			viewable = append(viewable, m.TeamID)
		}
	}
	return &limits.LogScope{UserID: p.UserID, TeamIDs: viewable}, nil
}

// spendLogsUI 는 GET /spend/logs/ui 다. 표는 늘 group_by_session=true 로 부른다. 우리는 세션이 없어 요청 하나가 세션 하나다
func (h *Handlers) spendLogsUI(w http.ResponseWriter, r *http.Request) {
	ctx, q, p := r.Context(), r.URL.Query(), principal(r.Context())
	var errs validationError
	page := queryInt(r, "page", 1, 1, 0, &errs)
	pageSize := queryInt(r, "page_size", 50, 1, 1000, &errs)
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	lq := limits.LogQuery{RequestID: queryString(r, "request_id"), UserID: queryString(r, "user_id"), TeamID: queryString(r, "team_id"),
		KeyToken: queryString(r, "api_key"), Search: queryString(r, "search"), Limit: pageSize, Offset: (page - 1) * pageSize}
	if lq.RequestID == nil {
		start, end, ok := logWindow(w, q)
		if !ok {
			return
		}
		lq.Start, lq.End = &start, &end
	}
	sortBy := q.Get("sort_by")
	if sortBy == "" {
		sortBy = "startTime"
	}
	order, known := logSorts[sortBy]
	if !known {
		badRequest("Invalid sort_by: "+sortBy+". Allowed: spend, total_tokens, startTime, endTime, request_duration_ms, model, ttft_ms", "sort_by").Write(w)
		return
	}
	lq.SortBy = order
	switch q.Get("sort_order") {
	case "", "desc":
		lq.Desc = true
	case "asc":
	default:
		badRequest("Invalid sort_order. Allowed: asc, desc", "sort_order").Write(w)
		return
	}
	if v := q.Get("cache_hit_filter"); v != "" && v != "hit" && v != "miss" {
		badRequest("Invalid cache_hit_filter: "+v+". Allowed: hit, miss", "cache_hit_filter").Write(w)
		return
	}
	if v := q.Get("span_type"); v != "" && !slices.Contains([]string{"llm", "agent", "mcp", "batch"}, v) {
		badRequest("Invalid span_type: "+v+". Allowed: llm, agent, mcp, batch", "span_type").Write(w)
		return
	}
	impossible, err := h.applyLogFilters(ctx, q, &lq)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if lq.TeamID != nil && !p.CanViewAll() {
		allowed, err := h.teamMemberMay(ctx, p, *lq.TeamID, "/spend/logs")
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		if !allowed {
			proxyError(403, "Not authorized to view team spend for team_id="+*lq.TeamID).Write(w)
			return
		}
	} else if lq.Scope, err = h.logScope(ctx, p); err != nil {
		h.internalError(w, r, err)
		return
	}
	grouped := q.Get("group_by_session") == "true" && lq.RequestID == nil && lq.Search == nil && sortBy == "startTime"
	if cursor, err := strconv.Atoi(q.Get("session_cursor")); grouped && err == nil && cursor >= 0 {
		lq.Offset = cursor // 우리 커서는 다음 쪽의 시작 위치다(대시보드는 받은 값을 그대로 돌려보내기만 한다)
	}
	var logs []limits.LogRow
	total, hasMore := 0, false
	if !impossible {
		if logs, total, hasMore, err = h.usage.LogPage(ctx, lq); err != nil {
			h.internalError(w, r, err)
			return
		}
	}
	if lq.RequestID != nil && !p.CanViewAll() && len(logs) == 0 {
		proxyError(403, "Not authorized to view spend log for request_id="+*lq.RequestID).Write(w)
		return
	}
	data, err := h.logRows(ctx, logs, true)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	body := row{"data": data, "total": total, "page": page, "page_size": pageSize, "total_pages": (total + pageSize - 1) / pageSize,
		"total_is_capped": total >= limits.MaxLogTotal}
	if grouped {
		var next any
		if hasMore {
			next = strconv.Itoa(lq.Offset + len(logs))
		}
		body["has_more"], body["next_session_cursor"] = hasMore, next
	}
	core.WriteJSON(w, http.StatusOK, body)
}

// logWindow 는 litellm 이 받는 유일한 형식 "YYYY-MM-DD HH:MM:SS"(UTC) 의 기간이다
func logWindow(w http.ResponseWriter, q url.Values) (time.Time, time.Time, bool) {
	startRaw, endRaw := q.Get("start_date"), q.Get("end_date")
	if startRaw == "" || endRaw == "" {
		badRequest("Start date and end date are required", "None").Write(w)
		return time.Time{}, time.Time{}, false
	}
	start, err := time.Parse(logTimeFormat, startRaw)
	if err != nil {
		proxyError(400, "Invalid date format: "+startRaw+". Expected: 'YYYY-MM-DD HH:MM:SS'").Write(w)
		return time.Time{}, time.Time{}, false
	}
	end, err := time.Parse(logTimeFormat, endRaw)
	if err != nil {
		proxyError(400, "Invalid date format: "+endRaw+". Expected: 'YYYY-MM-DD HH:MM:SS'").Write(w)
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// applyLogFilters 는 다른 모듈 값이 필요한 필터를 바꿔 넣는다. 맞는 행이 있을 수 없으면 impossible 이다
func (h *Handlers) applyLogFilters(ctx context.Context, q url.Values, lq *limits.LogQuery) (bool, error) {
	for _, k := range []string{"end_user", "session_id", "used_client_oauth_token", "error_message"} {
		if q.Get(k) != "" {
			return true, nil
		}
	}
	if q.Get("cache_hit_filter") == "hit" || slices.Contains([]string{"agent", "mcp", "batch"}, q.Get("span_type")) {
		return true, nil
	}
	switch q.Get("status_filter") {
	case "":
	case "success", "failure":
		failed := q.Get("status_filter") == "failure"
		lq.Failed = &failed
	default:
		return true, nil
	}
	if v := q.Get("error_code"); v != "" {
		code, err := strconv.Atoi(v)
		if err != nil {
			return true, nil
		}
		lq.HTTPStatus = &code
	}
	if v := q.Get("model_group"); v != "" {
		lq.Model = &v
	}
	if v := q.Get("model_id"); v != "" {
		lq.DeploymentIDs = []string{v}
	}
	if v := q.Get("model"); v != "" {
		// litellm 의 model 은 실제(업스트림) 모델이다. 그런 배포가 없으면 모델 그룹 이름으로 본다
		all, err := h.deployments.All(ctx)
		if err != nil {
			return false, err
		}
		var ids []string
		for _, d := range all {
			if d.UpstreamModel == v {
				ids = append(ids, d.ID)
			}
		}
		if ids == nil {
			lq.Model = &v
		} else {
			lq.DeploymentIDs = ids
		}
	}
	if v := q.Get("key_alias"); v != "" {
		page, err := h.keys.Page(ctx, auth.KeyQuery{KeyAlias: &v, AliasContains: true, Page: 1, PageSize: 10_000, Now: h.now()})
		if err != nil {
			return false, err
		}
		lq.KeyTokens = []string{}
		for _, k := range page.Items {
			lq.KeyTokens = append(lq.KeyTokens, k.Token)
		}
	}
	return false, nil
}

// logRows 는 litellm SpendLogs 행 모양이다(N 10.2). withList 면 목록 전용 칸(litellm_call_id·세션 집계)을 더한다
func (h *Handlers) logRows(ctx context.Context, logs []limits.LogRow, withList bool) ([]row, error) {
	all, err := h.deployments.All(ctx)
	if err != nil {
		return nil, err
	}
	deployments := map[string]inference.Deployment{}
	for _, d := range all {
		deployments[d.ID] = d
	}
	var tokens, teamIDs []string
	for _, l := range logs {
		if l.KeyToken != nil {
			tokens = append(tokens, *l.KeyToken)
		}
		if l.TeamID != nil {
			teamIDs = append(teamIDs, *l.TeamID)
		}
	}
	keys, err := h.keyMetadata(ctx, tokens)
	if err != nil {
		return nil, err
	}
	aliases, err := h.teamAliases(ctx, teamIDs)
	if err != nil {
		return nil, err
	}
	out := make([]row, 0, len(logs))
	for _, l := range logs {
		out = append(out, logRow(l, deployments, keys, aliases, withList))
	}
	return out, nil
}

func logRow(l limits.LogRow, deployments map[string]inference.Deployment, keys map[string]row, aliases map[string]*string, withList bool) row {
	status := "success"
	if l.Status != "success" {
		status = "failure" // 취소(클라이언트 끊김)도 실패로 센다
	}
	model, provider, apiBase := l.Model, "", ""
	if l.DeploymentID != nil {
		if d, ok := deployments[*l.DeploymentID]; ok {
			model, provider, apiBase = d.UpstreamModel, d.Provider, d.BaseURL
		}
	}
	start := l.CreatedAt.UTC()
	var firstToken any
	if l.TTFTMS != nil {
		firstToken = start.Add(time.Duration(*l.TTFTMS) * time.Millisecond)
	}
	metadata := row{"user_api_key": l.KeyToken, "user_api_key_alias": nil, "user_api_key_team_id": l.TeamID, "user_api_key_team_alias": nil,
		"user_api_key_user_id": l.UserID, "user_api_key_user_email": nil, "status": status}
	if l.KeyToken != nil {
		if m, ok := keys[*l.KeyToken]; ok {
			metadata["user_api_key_alias"], metadata["user_api_key_user_email"] = m["key_alias"], m["user_email"]
		}
	}
	if l.TeamID != nil {
		metadata["user_api_key_team_alias"] = aliases[*l.TeamID]
	}
	if status == "failure" {
		// litellm 은 오류 코드로 HTTP 상태를 문자열로 남긴다. 오류 본문은 저장하지 않는다
		metadata["error_information"] = row{"error_code": strconv.Itoa(l.HTTPStatus), "error_class": nil, "llm_provider": provider,
			"error_message": nil, "traceback": nil}
	}
	r := row{"request_id": l.RequestID, "call_type": "acompletion", "api_key": l.KeyToken, "spend": l.Cost,
		"total_tokens": l.PromptTokens + l.CompletionTokens, "prompt_tokens": l.PromptTokens, "completion_tokens": l.CompletionTokens,
		"startTime": start, "endTime": start.Add(time.Duration(l.LatencyMS) * time.Millisecond), "completionStartTime": firstToken,
		"model": model, "model_id": l.DeploymentID, "model_group": l.Model, "custom_llm_provider": provider, "api_base": apiBase,
		"user": l.UserID, "metadata": metadata, "cache_hit": "", "cache_key": "", "request_tags": []string{}, "team_id": l.TeamID,
		"organization_id": nil, "end_user": nil, "requester_ip_address": nil, "session_id": nil, "status": status,
		"mcp_namespaced_tool_name": nil, "agent_id": nil}
	if withList {
		r["litellm_call_id"], r["request_duration_ms"], r["session_total_count"] = l.RequestID, l.LatencyMS, 1
	}
	return r
}

// spendLogDetail 은 GET /spend/logs/ui/{request_id} 다. 본문을 저장하지 않아 litellm 이 프롬프트를 저장하지 않을 때와 같은 빈 칸이다
func (h *Handlers) spendLogDetail(w http.ResponseWriter, r *http.Request) {
	ctx, p, id := r.Context(), principal(r.Context()), r.PathValue("request_id")
	scope, err := h.logScope(ctx, p)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	logs, _, _, err := h.usage.LogPage(ctx, limits.LogQuery{RequestID: &id, Scope: scope, Limit: 1})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	switch {
	case len(logs) == 0 && scope != nil:
		detailError(403, "Not authorized to view spend log for request_id="+id).Write(w)
	case len(logs) == 0:
		core.WriteJSON(w, http.StatusOK, nil)
	default:
		core.WriteJSON(w, http.StatusOK, row{"messages": row{}, "response": row{}, "proxy_server_request": row{}})
	}
}

// sessionLogs 는 세션의 요청들이다. 우리는 세션 id 를 기록하지 않아 늘 빈 목록이다(표가 세션 서랍을 열지 않는다)
func (h *Handlers) sessionLogs(w http.ResponseWriter, r *http.Request) {
	var errs validationError
	page := queryInt(r, "page", 1, 1, 0, &errs)
	pageSize := queryInt(r, "page_size", 50, 1, 100, &errs)
	if r.URL.Query().Get("session_id") == "" {
		errs = append(errs, FieldError{Type: "missing", Loc: []any{"query", "session_id"}, Msg: "Field required"})
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"data": []any{}, "total": 0, "page": page, "page_size": pageSize, "total_pages": 1})
}

var spendLogFacetParams = []string{"filter[startTime][gte]", "filter[startTime][lte]", "q", "page", "page_size"}

// spendLogFacet 은 로그 화면의 사용자·최종 사용자 필터 목록이다(N 9장). 대시보드는 links.next 의 page 로 다음 쪽을 연다
func (h *Handlers) spendLogFacet(endUsers bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for k := range q {
			if !slices.Contains(spendLogFacetParams, k) {
				writeProblem(w, 400, "unknown-query-parameter", "Unknown query parameter", "Unknown query parameter: "+k)
				return
			}
		}
		start, err1 := parseFacetTime(q.Get("filter[startTime][gte]"))
		end, err2 := parseFacetTime(q.Get("filter[startTime][lte]"))
		page, size := positiveInt(q.Get("page"), 1), positiveInt(q.Get("page_size"), 50)
		if err1 != nil || err2 != nil || size > 100 {
			writeProblem(w, 400, "invalid-query-parameter", "Invalid query parameter",
				"filter[startTime][gte] and filter[startTime][lte] are required ISO datetimes, page_size must be 1..100")
			return
		}
		ids, hasMore := []string{}, false
		if !endUsers {
			scope, err := h.logScope(r.Context(), principal(r.Context()))
			if err != nil {
				h.internalError(w, r, err)
				return
			}
			if ids, hasMore, err = h.usage.LogUsers(r.Context(), start, end, queryString(r, "q"), scope, (page-1)*size, size); err != nil {
				h.internalError(w, r, err)
				return
			}
		}
		link := func(p int) string {
			v := url.Values{}
			for k, vs := range q {
				v[k] = vs
			}
			v.Set("page", strconv.Itoa(p))
			return r.URL.Path + "?" + v.Encode()
		}
		var next, prev any
		if hasMore {
			next = link(page + 1)
		}
		if page > 1 {
			prev = link(page - 1)
		}
		core.WriteJSON(w, http.StatusOK, row{"data": ids, "meta": row{"page": page, "page_size": size, "has_more": hasMore},
			"links": row{"self": link(page), "prev": prev, "next": next}})
	}
}

func parseFacetTime(v string) (time.Time, error) {
	if t, err := time.Parse(logTimeFormat, v); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, v)
}
