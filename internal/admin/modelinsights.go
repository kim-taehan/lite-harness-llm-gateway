package admin

import (
	"cmp"
	"net/http"
	"slices"
	"time"

	"llm-gateway/internal/limits"
)

// 모델 리더보드(GET /model-insights, litellm model_insights_endpoints.py). 관리자만 본다.
// 기간 안의 (모델 그룹, 업스트림 모델, 제공자) 사용량에서 metric 상위 10개와 그 일별 값, 날짜별 전체 합을 낸다

const (
	insightsTopModels = 10  // litellm MODEL_INSIGHTS_TOP_MODELS
	insightsMaxDays   = 365 // litellm MODEL_INSIGHTS_MAX_RANGE_DAYS
)

type insightKey struct{ group, model, provider string }

func (h *Handlers) modelInsights(w http.ResponseWriter, r *http.Request) {
	if !principal(r.Context()).CanViewAll() {
		detailError(403, "Only proxy admins can view deployment-wide model insights").Write(w)
		return
	}
	q := r.URL.Query()
	today := h.now().UTC().Truncate(24 * time.Hour)
	end, err1 := parseDay(q.Get("end_date"), today)
	start, err2 := parseDay(q.Get("start_date"), end.AddDate(0, 0, -(insightsMaxDays-1)))
	if err1 != nil || err2 != nil || start.After(end) || end.Sub(start) >= insightsMaxDays*24*time.Hour {
		detailError(400, "Date range must be between 1 and 365 days").Write(w)
		return
	}
	metric := cmp.Or(q.Get("metric"), "tokens")
	rows, err := h.usage.Activity(r.Context(), limits.ActivityFilter{Start: start, End: end})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	c, err := h.activityContext(r.Context(), rows)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	top, days, totalsByDate := insightTables(rows, c, metric)
	writeJSON(w, row{"start_date": start.Format(time.DateOnly), "end_date": end.Format(time.DateOnly), "top_models": top,
		"daily": nonNilRows(days), "daily_totals": totalsByDate})
}

// insightTables 는 사용량 행을 metric 상위 모델, 그 모델들의 일별 값, 날짜별 전체 합으로 모은다
func insightTables(rows []limits.ActivityRow, c activityContext, metric string) (top, days, totalsByDate []row) {
	totals := map[insightKey]*limits.Metrics{}
	daily := map[insightKey]map[string]*limits.Metrics{}
	byDate := map[string]*limits.Metrics{}
	for _, a := range rows {
		k := insightKey{a.Model, c.upstreamModel(a), c.provider(a)}
		if daily[k] == nil {
			daily[k] = map[string]*limits.Metrics{}
		}
		limits.Accumulate(totals, k, a.Metrics)
		limits.Accumulate(daily[k], a.Date, a.Metrics)
		limits.Accumulate(byDate, a.Date, a.Metrics)
	}
	keys := make([]insightKey, 0, len(totals))
	for k := range totals {
		keys = append(keys, k)
	}
	rank := func(m *limits.Metrics) float64 {
		switch metric {
		case "spend":
			return m.Spend
		case "requests":
			return float64(m.Requests())
		}
		return float64(m.Tokens())
	}
	slices.SortStableFunc(keys, func(a, b insightKey) int { return cmp.Compare(rank(totals[b]), rank(totals[a])) })
	keys = keys[:min(insightsTopModels, len(keys))]

	top = make([]row, 0, len(keys))
	for _, k := range keys {
		top = append(top, insightRow(k, *totals[k]))
		for date, m := range daily[k] {
			r := insightRow(k, *m)
			r["date"] = date
			days = append(days, r)
		}
	}
	slices.SortStableFunc(days, func(a, b row) int { return cmp.Compare(a["date"].(string), b["date"].(string)) })
	dates := make([]string, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	slices.Sort(dates)
	totalsByDate = make([]row, 0, len(dates))
	for _, d := range dates {
		m := byDate[d]
		totalsByDate = append(totalsByDate, row{"date": d, "spend": m.Spend, "prompt_tokens": m.PromptTokens,
			"completion_tokens": m.CompletionTokens, "requests": m.Requests()})
	}
	return top, days, totalsByDate
}

func insightRow(k insightKey, m limits.Metrics) row {
	return row{"model_group": k.group, "model": k.model, "provider": k.provider, "spend": m.Spend, "prompt_tokens": m.PromptTokens,
		"completion_tokens": m.CompletionTokens, "requests": m.Requests(),
		"successful_requests": m.SuccessfulRequests, "failed_requests": m.FailedRequests}
}

func parseDay(v string, fallback time.Time) (time.Time, error) {
	if v == "" {
		return fallback, nil
	}
	return time.Parse(time.DateOnly, v)
}

func nonNilRows(rows []row) []row {
	if rows == nil {
		return []row{}
	}
	return rows
}
