package admin

import (
	"net/http"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// 계정·사용량 화면 API(K 9장). 데스크탑 계정 화면과 대시보드 사용량 화면이 부른다

func metricsRow(m limits.Metrics) row {
	// litellm SpendMetrics 의 모든 칸. 우리가 세지 않는 칸(캐시·압축·PTU 등)은 0 이다
	return row{
		"spend": m.Spend, "flat_cost": 0.0, "ptu_hours": 0.0,
		"prompt_tokens": m.PromptTokens, "completion_tokens": m.CompletionTokens,
		"cache_read_input_tokens": 0, "cache_creation_input_tokens": 0, "compression_saved_tokens": 0,
		"compression_savings_spend": 0.0, "prompt_caching_savings_spend": 0.0, "gateway_injected_caching_savings_spend": 0.0,
		"autorouter_savings_spend": 0.0, "total_tokens": m.PromptTokens + m.CompletionTokens,
		"successful_requests": m.SuccessfulRequests, "failed_requests": m.FailedRequests,
		"api_requests":           m.SuccessfulRequests + m.FailedRequests,
		"total_response_time_ms": m.TotalResponseTimeMS, "timed_requests": m.TimedRequests,
	}
}

// dailyActivity 는 litellm /user/daily/activity 다. 비관리자는 자기 것만 본다
func (h *Handlers) dailyActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	startRaw, endRaw := q.Get("start_date"), q.Get("end_date")
	if startRaw == "" || endRaw == "" {
		detailError(400, "Please provide start_date and end_date").Write(w)
		return
	}
	start, err1 := time.Parse(time.DateOnly, startRaw)
	end, err2 := time.Parse(time.DateOnly, endRaw)
	switch {
	case err1 != nil || err2 != nil:
		detailError(400, "start_date and end_date must be valid YYYY-MM-DD dates").Write(w)
		return
	case end.Before(start):
		detailError(400, "end_date must be on or after start_date").Write(w)
		return
	}
	var errs validationError
	page := queryInt(r, "page", 1, 1, 0, &errs)
	pageSize := queryInt(r, "page_size", 50, 1, 1000, &errs)
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	p := principal(r.Context())
	userID := queryString(r, "user_id")
	if !p.CanViewAll() {
		if userID != nil && *userID != p.UserID {
			detailError(403, "Non-admin users can only view their own spend data.").Write(w)
			return
		}
		self := p.UserID
		userID = &self
	}
	days, err := h.usage.Daily(r.Context(), userID, start, end)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	// litellm 은 원시 행(날짜×키×모델) 단위로 쪽을 나눈다. 우리는 날짜 단위로 나눈다(데스크탑은 page_size=1000 으로 한 쪽만 읽는다)
	total := len(days)
	results := []row{}
	var sum limits.Metrics
	for _, d := range pageOf(days, (page-1)*pageSize, pageSize) {
		groups := row{}
		for model, m := range d.ByModel {
			groups[model] = row{"metrics": metricsRow(m), "metadata": row{}, "api_key_breakdown": row{}}
		}
		results = append(results, row{"date": d.Date, "metrics": metricsRow(d.Total), "breakdown": row{
			"mcp_servers": row{}, "models": groups, "model_groups": groups, "providers": row{}, "endpoints": row{}, "api_keys": row{}, "entities": row{},
		}})
		sum.Add(d.Total)
	}
	metadata := totalsRow(sum)
	metadata["page"], metadata["total_pages"], metadata["has_more"] = page, totalPages(int64(total), pageSize), page*pageSize < total
	metadata["api_key_limit"], metadata["total_api_keys"], metadata["entity_total_api_keys"] = nil, nil, nil
	core.WriteJSON(w, http.StatusOK, row{"results": results, "metadata": metadata})
}

// spendLogs 는 litellm /spend/logs 다. internal_user 계열은 자기 것으로 고정한다. summarize=true(날짜별 요약)는 아직 없다
func (h *Handlers) spendLogs(w http.ResponseWriter, r *http.Request) {
	p := principal(r.Context())
	f := limits.LogFilter{UserID: queryString(r, "user_id"), RequestID: queryString(r, "request_id")}
	if p.Role == core.RoleInternalUser || p.Role == core.RoleInternalUserViewer || p.Role == "" {
		self := p.UserID
		f.UserID = &self
	}
	if key := queryString(r, "api_key"); key != nil {
		token := auth.TokenOf(*key)
		f.KeyToken = &token
	}
	if s, e := r.URL.Query().Get("start_date"), r.URL.Query().Get("end_date"); s != "" && e != "" {
		start, err1 := time.Parse(time.DateOnly, s)
		end, err2 := time.Parse(time.DateOnly, e)
		if err1 != nil || err2 != nil {
			core.ProxyError{Status: 500, Type: "internal_error", Param: "None", Message: "/spend/logs Error: dates must be YYYY-MM-DD"}.Write(w)
			return
		}
		f.Start, f.End = &start, &end
	}
	logs, err := h.usage.Logs(r.Context(), f)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if len(logs) == limits.LogLimit {
		w.Header().Set("x-litellm-spend-logs-truncated", "true")
	}
	rows := make([]row, len(logs))
	for i, l := range logs {
		end := l.CreatedAt.Add(time.Duration(l.LatencyMS) * time.Millisecond)
		var firstToken any
		if l.TTFTMS != nil {
			firstToken = l.CreatedAt.Add(time.Duration(*l.TTFTMS) * time.Millisecond).UTC()
		}
		rows[i] = row{
			"request_id": l.RequestID, "litellm_call_id": l.RequestID, "call_type": "acompletion", "api_key": l.KeyToken,
			"spend": l.Cost, "total_tokens": l.PromptTokens + l.CompletionTokens, "prompt_tokens": l.PromptTokens,
			"completion_tokens": l.CompletionTokens, "startTime": l.CreatedAt.UTC(), "endTime": end.UTC(),
			"request_duration_ms": l.LatencyMS, "completionStartTime": firstToken, "model": l.Model, "model_id": l.DeploymentID,
			"model_group": l.Model, "custom_llm_provider": nil, "api_base": nil, "user": l.UserID, "team_id": l.TeamID,
			"metadata":  row{"status": l.Status, "user_api_key_user_id": l.UserID, "user_api_key_team_id": l.TeamID},
			"cache_hit": nil, "cache_key": nil, "request_tags": []string{}, "organization_id": nil, "end_user": nil,
			"requester_ip_address": nil, "messages": row{}, "response": row{}, "session_id": nil, "status": l.Status,
			"created_at": l.CreatedAt.UTC(), "updated_at": end.UTC(), "proxy_server_request": row{},
		}
	}
	core.WriteJSON(w, http.StatusOK, rows)
}

// 아직 없는 기능의 목록. 화면이 깨지지 않게 빈 목록으로 답한다(설계 9장: 빈 응답)
func (h *Handlers) emptyGuardrails(w http.ResponseWriter, _ *http.Request) {
	core.WriteJSON(w, http.StatusOK, row{"guardrails": []any{}})
}

func (h *Handlers) emptyMCPServers(w http.ResponseWriter, _ *http.Request) {
	core.WriteJSON(w, http.StatusOK, []any{})
}
