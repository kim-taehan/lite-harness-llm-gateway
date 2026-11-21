package admin

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
	"llm-gateway/internal/limits"
)

// 대시보드 사용량 화면(N 2~5장): /{user|team}/daily/activity/aggregated 와 하위 경로, /team/spend/by_user.
// limits 가 request_log 를 (날짜, 모델 그룹, 배포, 키, 팀, 사용자)로 묶어 주면 여기서 litellm 응답 모양으로 조립한다.
// 키 별명·팀 이름·이메일·제공자는 각 모듈에서 읽어 붙인다

const (
	entityUser = "user"
	entityTeam = "team"
)

// maxActivityDays 는 litellm 의 기간 상한이다(user aggregated 만 예외, N 2.3)
const maxActivityDays = 400

// activityRange 는 start_date·end_date 를 litellm 규칙대로 읽는다. 틀리면 400 (B) 를 쓰고 ok=false
func activityRange(w http.ResponseWriter, r *http.Request, limitDays bool) (time.Time, time.Time, bool) {
	q := r.URL.Query()
	startRaw, endRaw := q.Get("start_date"), q.Get("end_date")
	if startRaw == "" || endRaw == "" {
		detailError(400, "Please provide start_date and end_date").Write(w)
		return time.Time{}, time.Time{}, false
	}
	start, err1 := time.Parse(time.DateOnly, startRaw)
	end, err2 := time.Parse(time.DateOnly, endRaw)
	switch {
	case err1 != nil || err2 != nil || start.Format(time.DateOnly) != startRaw || end.Format(time.DateOnly) != endRaw:
		detailError(400, "start_date and end_date must be valid YYYY-MM-DD dates").Write(w)
	case end.Before(start):
		detailError(400, "end_date must be on or after start_date").Write(w)
	case limitDays && end.Sub(start) > maxActivityDays*24*time.Hour:
		detailError(400, "Date range must be at most 400 days").Write(w)
	default:
		return start, end, true
	}
	return time.Time{}, time.Time{}, false
}

func splitIDs(v string) []string {
	if v == "" {
		return nil
	}
	var ids []string
	for _, id := range strings.Split(v, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// activityScope 는 엔티티와 역할에 따른 조회 범위다(N 3.2). team 이면 팀 이름표도 함께 돌려준다
func (h *Handlers) activityScope(w http.ResponseWriter, r *http.Request, entity string, f *limits.ActivityFilter) (map[string]*string, bool) {
	ctx, q, p := r.Context(), r.URL.Query(), principal(r.Context())
	if key := q.Get("api_key"); key != "" { // litellm 은 해시로 바꾸지 않고 그대로 맞춘다
		f.KeyTokens = []string{key}
	}
	if entity == entityUser {
		userID := queryString(r, "user_id")
		switch {
		case p.CanViewAll():
			f.UserID = userID
		case p.UserID == "":
			detailError(403, "Service-account keys cannot query user analytics. Use a user-bound key, or call as a proxy admin.").Write(w)
			return nil, false
		case userID != nil && *userID != p.UserID:
			detailError(403, "Non-admin users can only view their own spend data.").Write(w)
			return nil, false
		default:
			f.UserID = &p.UserID
		}
		return nil, true
	}
	teamIDs := splitIDs(q.Get("team_ids"))
	f.ExcludeTeamIDs = splitIDs(q.Get("exclude_team_ids"))
	if !p.CanViewAll() {
		mine, err := h.users.TeamIDs(ctx, []string{p.UserID})
		if err != nil {
			h.internalError(w, r, err)
			return nil, false
		}
		if teamIDs == nil {
			teamIDs = append([]string{}, mine[p.UserID]...)
		}
		fullView := true
		for _, t := range teamIDs {
			if !slices.Contains(mine[p.UserID], t) {
				detailError(404, "User does not belong to Team= "+t+". Call `/user/info` to see user's teams").Write(w)
				return nil, false
			}
			allowed, err := h.teamMemberMay(ctx, p, t, "/team/daily/activity")
			if err != nil {
				h.internalError(w, r, err)
				return nil, false
			}
			fullView = fullView && allowed
		}
		if !fullView {
			// 팀 전체를 볼 권한이 없으면 자기 키 사용량만 본다(litellm 과 같다)
			own, err := h.ownKeyTokens(ctx, p.UserID)
			if err != nil {
				h.internalError(w, r, err)
				return nil, false
			}
			f.KeyTokens = intersectKeys(f.KeyTokens, own)
		}
	}
	f.TeamIDs = teamIDs
	aliases, err := h.teamAliases(ctx, teamIDs)
	if err != nil {
		h.internalError(w, r, err)
		return nil, false
	}
	return aliases, true
}

func intersectKeys(requested, allowed []string) []string {
	if requested == nil {
		return allowed
	}
	return slices.DeleteFunc(requested, func(k string) bool { return !slices.Contains(allowed, k) })
}

func (h *Handlers) ownKeyTokens(ctx context.Context, userID string) ([]string, error) {
	page, err := h.keys.Page(ctx, auth.KeyQuery{UserID: &userID, Page: 1, PageSize: 10_000, Now: h.now()})
	if err != nil {
		return nil, err
	}
	tokens := []string{}
	for _, k := range page.Items {
		tokens = append(tokens, k.Token)
	}
	return tokens, nil
}

// teamAliases 는 팀 id → 별명이다. ids 가 nil 이면 모든 팀이다(litellm team_alias_metadata)
func (h *Handlers) teamAliases(ctx context.Context, ids []string) (map[string]*string, error) {
	var teams []auth.Team
	var err error
	if ids == nil {
		teams, err = h.teams.ListByAlias(ctx, nil)
	} else {
		teams, err = h.teams.FindAll(ctx, ids)
	}
	aliases := map[string]*string{}
	for _, t := range teams {
		aliases[t.TeamID] = t.TeamAlias
	}
	return aliases, err
}

// activityContext 는 행을 응답으로 바꿀 때 붙이는 다른 모듈의 값이다
type activityContext struct {
	deployments map[string]inference.Deployment
	keys        map[string]row // 키 해시 → KeyMetadata
}

func (h *Handlers) activityContext(ctx context.Context, rows []limits.ActivityRow) (activityContext, error) {
	all, err := h.deployments.All(ctx)
	if err != nil {
		return activityContext{}, err
	}
	deployments := map[string]inference.Deployment{}
	for _, d := range all {
		deployments[d.ID] = d
	}
	var tokens []string
	for _, a := range rows {
		if a.KeyToken != nil && !slices.Contains(tokens, *a.KeyToken) {
			tokens = append(tokens, *a.KeyToken)
		}
	}
	keys, err := h.keyMetadata(ctx, tokens)
	return activityContext{deployments: deployments, keys: keys}, err
}

// keyMetadata 는 litellm KeyMetadata 다(N 3.4): 활성 키, 없으면 지운 키, 그다음 소유자 이메일
func (h *Handlers) keyMetadata(ctx context.Context, tokens []string) (map[string]row, error) {
	active, err := h.keys.FindAll(ctx, tokens)
	if err != nil {
		return nil, err
	}
	found := map[string]auth.Key{}
	for _, k := range active {
		found[k.Token] = k
	}
	meta := map[string]row{}
	var userIDs []string
	for _, t := range tokens {
		k, exists := found[t]
		if !exists {
			deleted, err := h.keys.FindDeleted(ctx, t)
			if err != nil && !errors.Is(err, auth.ErrKeyNotFound) {
				return nil, err
			}
			k = deleted.Key // 없으면 빈 값이라 모든 칸이 null 이 된다
		}
		meta[t] = row{"key_alias": k.KeyAlias, "team_id": k.TeamID, "user_id": k.UserID, "user_email": nil, "key_exists": exists}
		if k.UserID != nil {
			userIDs = append(userIDs, *k.UserID)
		}
	}
	users, err := h.users.FindAll(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	emails := map[string]*string{}
	for _, u := range users {
		emails[u.UserID] = u.UserEmail
	}
	for _, m := range meta {
		if id, ok := m["user_id"].(*string); ok && id != nil {
			m["user_email"] = emails[*id]
		}
	}
	return meta, nil
}

func (c activityContext) upstreamModel(a limits.ActivityRow) string {
	if a.DeploymentID != nil {
		if d, ok := c.deployments[*a.DeploymentID]; ok {
			return d.UpstreamModel
		}
	}
	return a.Model
}

func (c activityContext) provider(a limits.ActivityRow) string {
	if a.DeploymentID != nil {
		if d, ok := c.deployments[*a.DeploymentID]; ok {
			return d.Provider
		}
	}
	return "unknown"
}

// matchesModel 은 model 쿼리다. litellm 은 실제 모델 이름으로 거르고, 우리 화면은 모델 그룹 이름도 보내므로 둘 다 받는다
func (c activityContext) matchesModel(a limits.ActivityRow, model string) bool {
	return model == "" || a.Model == model || c.upstreamModel(a) == model
}

// metricEntry 는 litellm MetricWithMetadata 를 만들며 더한다
type metricEntry struct {
	metrics limits.Metrics
	keys    map[string]*limits.Metrics
}

func (e *metricEntry) add(a limits.ActivityRow) {
	e.metrics.Add(a.Metrics)
	if a.KeyToken == nil {
		return
	}
	if e.keys == nil {
		e.keys = map[string]*limits.Metrics{}
	}
	if e.keys[*a.KeyToken] == nil {
		e.keys[*a.KeyToken] = &limits.Metrics{}
	}
	e.keys[*a.KeyToken].Add(a.Metrics)
}

func (e *metricEntry) render(c activityContext, topKeys map[string]bool, metadata row) row {
	breakdown := row{}
	for k, m := range e.keys {
		if topKeys[k] {
			breakdown[k] = row{"metrics": metricsRow(*m), "metadata": c.keys[k]}
		}
	}
	return row{"metrics": metricsRow(e.metrics), "metadata": metadata, "api_key_breakdown": breakdown}
}

type entries map[string]*metricEntry

func (es entries) at(k string) *metricEntry {
	if es[k] == nil {
		es[k] = &metricEntry{}
	}
	return es[k]
}

func (es entries) render(c activityContext, topKeys map[string]bool, metadata func(string) row) row {
	out := row{}
	for k, e := range es {
		out[k] = e.render(c, topKeys, metadata(k))
	}
	return out
}

func emptyMetadata(string) row { return row{} }

// topKeys 는 기간 spend 상위 limit 개 키다(litellm api_key_limit)
func topKeys(rows []limits.ActivityRow, limit int) map[string]bool {
	spend := map[string]float64{}
	for _, a := range rows {
		if a.KeyToken != nil {
			spend[*a.KeyToken] += a.Metrics.Spend
		}
	}
	keys := make([]string, 0, len(spend))
	for k := range spend {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(spend[b], spend[a]), cmp.Compare(a, b)) })
	top := map[string]bool{}
	for _, k := range keys[:min(limit, len(keys))] {
		top[k] = true
	}
	return top
}

// aggregatedResponse 는 SpendAnalyticsPaginatedResponse 다(N 3.3). 날짜 내림차순, 0 채우기 없음
func aggregatedResponse(rows []limits.ActivityRow, c activityContext, entity string, aliases map[string]*string, keyLimit int) row {
	type day struct {
		total                                       limits.Metrics
		models, groups, providers, apiKeys, teamsBy entries
	}
	days := map[string]*day{}
	var total limits.Metrics
	allKeys := map[string]bool{}
	teamKeys := map[string]map[string]bool{}
	for _, a := range rows {
		d := days[a.Date]
		if d == nil {
			d = &day{models: entries{}, groups: entries{}, providers: entries{}, apiKeys: entries{}, teamsBy: entries{}}
			days[a.Date] = d
		}
		d.total.Add(a.Metrics)
		total.Add(a.Metrics)
		d.models.at(c.upstreamModel(a)).add(a)
		d.groups.at(a.Model).add(a)
		d.providers.at(c.provider(a)).add(a)
		team := "Unassigned"
		if a.TeamID != nil && *a.TeamID != "" {
			team = *a.TeamID
		}
		if entity == entityTeam {
			d.teamsBy.at(team).add(a)
		}
		if a.KeyToken != nil {
			d.apiKeys.at(*a.KeyToken).add(a)
			allKeys[*a.KeyToken] = true
			if teamKeys[team] == nil {
				teamKeys[team] = map[string]bool{}
			}
			teamKeys[team][*a.KeyToken] = true
		}
	}
	top := topKeys(rows, keyLimit)
	teamMetadata := func(id string) row {
		if alias, ok := aliases[id]; ok {
			return row{"team_alias": alias}
		}
		return row{}
	}
	dates := make([]string, 0, len(days))
	for date := range days {
		dates = append(dates, date)
	}
	slices.Sort(dates)
	slices.Reverse(dates)
	results := make([]row, 0, len(dates))
	for _, date := range dates {
		d := days[date]
		apiKeys := row{}
		for k, e := range d.apiKeys {
			if top[k] {
				apiKeys[k] = row{"metrics": metricsRow(e.metrics), "metadata": c.keys[k]}
			}
		}
		results = append(results, row{"date": date, "metrics": metricsRow(d.total), "breakdown": row{
			"models": d.models.render(c, top, emptyMetadata), "model_groups": d.groups.render(c, top, emptyMetadata),
			"providers": d.providers.render(c, top, emptyMetadata), "endpoints": row{}, "mcp_servers": row{},
			"api_keys": apiKeys, "entities": d.teamsBy.render(c, top, teamMetadata),
		}})
	}
	var entityKeys any
	if entity == entityTeam {
		counts := row{}
		for team, keys := range teamKeys {
			counts[team] = len(keys)
		}
		entityKeys = counts
	}
	metadata := totalsRow(total)
	metadata["page"], metadata["total_pages"], metadata["has_more"] = 1, 1, false
	metadata["api_key_limit"], metadata["total_api_keys"], metadata["entity_total_api_keys"] = keyLimit, len(allKeys), entityKeys
	return row{"results": results, "metadata": metadata}
}

// totalsRow 는 DailySpendMetadata 의 total_* 칸이다. 화면이 null 을 견디지 못하므로 모든 칸을 숫자로 낸다(N 3.5)
func totalsRow(m limits.Metrics) row {
	out := row{}
	for k, v := range metricsRow(m) {
		out["total_"+k] = v
	}
	out["total_tokens"] = m.PromptTokens + m.CompletionTokens
	return out
}

// keySpendRow 는 KeySpendActivityRow 다. metrics 는 litellm KeySpendMetrics 의 9칸이다
func keySpendRow(token string, m limits.Metrics, c activityContext) row {
	return row{"api_key": token, "metadata": c.keys[token], "metrics": row{
		"spend": m.Spend, "prompt_tokens": m.PromptTokens, "completion_tokens": m.CompletionTokens,
		"total_tokens": m.PromptTokens + m.CompletionTokens, "api_requests": m.SuccessfulRequests + m.FailedRequests,
		"successful_requests": m.SuccessfulRequests, "failed_requests": m.FailedRequests,
		"cache_read_input_tokens": 0, "cache_creation_input_tokens": 0,
	}}
}

// byKey 는 기간 동안 키별 합계를 spend 내림차순(같으면 해시 순)으로 낸다
func byKey(rows []limits.ActivityRow, keep func(limits.ActivityRow) bool) ([]string, map[string]*limits.Metrics) {
	sums := map[string]*limits.Metrics{}
	for _, a := range rows {
		if a.KeyToken == nil || !keep(a) {
			continue
		}
		if sums[*a.KeyToken] == nil {
			sums[*a.KeyToken] = &limits.Metrics{}
		}
		sums[*a.KeyToken].Add(a.Metrics)
	}
	keys := make([]string, 0, len(sums))
	for k := range sums {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(sums[b].Spend, sums[a].Spend), cmp.Compare(a, b)) })
	return keys, sums
}

func keepAll(limits.ActivityRow) bool { return true }

// activity 는 엔티티·하위 경로 하나의 핸들러다
func (h *Handlers) activity(entity, view string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start, end, ok := activityRange(w, r, !(entity == entityUser && view == "aggregated"))
		if !ok {
			return
		}
		var errs validationError
		keyLimit := queryInt(r, "api_key_limit", 100, 1, 1000, &errs)
		offset := queryInt(r, "offset", 0, 0, 0, &errs)
		limitDefault := map[string]int{"keys": 50, "users": 50, "search": 100, "model_top_keys": 5}[view]
		limit := queryInt(r, "limit", limitDefault, 1, 100, &errs)
		q := r.URL.Query()
		if view == "search" && q.Get("search") == "" {
			errs = append(errs, FieldError{Type: "missing", Loc: []any{"query", "search"}, Msg: "Field required"})
		}
		if view == "model_top_keys" && q.Get("model_group") == "" {
			errs = append(errs, FieldError{Type: "missing", Loc: []any{"query", "model_group"}, Msg: "Field required"})
		}
		if len(errs) > 0 {
			errs.write(w)
			return
		}
		f := limits.ActivityFilter{Start: start, End: end}
		aliases, ok := h.activityScope(w, r, entity, &f)
		if !ok {
			return
		}
		rows, err := h.usage.Activity(r.Context(), f)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		c, err := h.activityContext(r.Context(), rows)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		model := q.Get("model")
		rows = slices.DeleteFunc(rows, func(a limits.ActivityRow) bool { return !c.matchesModel(a, model) })
		switch view {
		case "aggregated":
			writeJSON(w, aggregatedResponse(rows, c, entity, aliases, keyLimit))
		case "keys":
			keys, sums := byKey(rows, keepAll)
			page := make([]row, 0, limit)
			for _, k := range keys[min(offset, len(keys)):min(offset+limit, len(keys))] {
				page = append(page, keySpendRow(k, *sums[k], c))
			}
			writeJSON(w, row{"api_keys": page, "total_api_keys": len(keys), "offset": offset, "limit": limit})
		case "search":
			writeJSON(w, row{"api_keys": searchKeys(rows, c, userEmails(c), q.Get("search"), limit)})
		case "model_top_keys":
			group, byGroup := q.Get("model_group"), q.Get("by_model_group") != "false"
			keys, sums := byKey(rows, func(a limits.ActivityRow) bool {
				return (byGroup && a.Model == group) || (!byGroup && c.upstreamModel(a) == group)
			})
			page := make([]row, 0, limit)
			for _, k := range keys[:min(limit, len(keys))] {
				page = append(page, keySpendRow(k, *sums[k], c))
			}
			writeJSON(w, row{"model": group, "by_model_group": byGroup, "api_keys": page})
		case "users":
			h.writeUserActivity(w, r, rows, offset, limit)
		}
	}
}

// userEmails 는 키 소유자 이메일 검색용이다(키 메타데이터에 이미 들어 있다)
func userEmails(c activityContext) map[string]string {
	emails := map[string]string{}
	for token, m := range c.keys {
		if e, ok := m["user_email"].(*string); ok && e != nil {
			emails[token] = *e
		}
	}
	return emails
}

// searchKeys 는 해시·별명·소유자 id·이메일 부분 일치(대소문자 무시)다. 행의 metrics 는 전체 SpendMetrics 다(N 4.2)
func searchKeys(rows []limits.ActivityRow, c activityContext, emails map[string]string, search string, limit int) []row {
	needle := strings.ToLower(search)
	matches := func(token string) bool {
		m := c.keys[token]
		candidates := []string{token, emails[token]}
		for _, k := range []string{"key_alias", "user_id"} {
			if v, ok := m[k].(*string); ok && v != nil {
				candidates = append(candidates, *v)
			}
		}
		return slices.ContainsFunc(candidates, func(s string) bool { return s != "" && strings.Contains(strings.ToLower(s), needle) })
	}
	keys, sums := byKey(rows, func(a limits.ActivityRow) bool { return matches(*a.KeyToken) })
	out := make([]row, 0, min(limit, len(keys)))
	for _, k := range keys[:min(limit, len(keys))] {
		out = append(out, row{"api_key": k, "metrics": metricsRow(*sums[k]), "metadata": c.keys[k]})
	}
	return out
}

// writeUserActivity 는 /user/daily/activity/aggregated/users 다. spend 내림차순, user_id 오름차순, 사용자 없는 행은 끝
func (h *Handlers) writeUserActivity(w http.ResponseWriter, r *http.Request, rows []limits.ActivityRow, offset, limit int) {
	sums := map[string]*limits.Metrics{}
	for _, a := range rows {
		id := ""
		if a.UserID != nil {
			id = *a.UserID
		}
		if sums[id] == nil {
			sums[id] = &limits.Metrics{}
		}
		sums[id].Add(a.Metrics)
	}
	ids := make([]string, 0, len(sums))
	for id := range sums {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		if (a == "") != (b == "") {
			return cmp.Compare(b, a) // 빈 id 는 끝
		}
		return cmp.Or(cmp.Compare(sums[b].Spend, sums[a].Spend), cmp.Compare(a, b))
	})
	pageIDs := ids[min(offset, len(ids)):min(offset+limit, len(ids))]
	users, err := h.users.FindAll(r.Context(), pageIDs)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	byID := map[string]auth.User{}
	for _, u := range users {
		byID[u.UserID] = u
	}
	out := make([]row, 0, len(pageIDs))
	for _, id := range pageIDs {
		m, u := sums[id], byID[id]
		var userID any
		if id != "" {
			userID = id
		}
		out = append(out, row{"user_id": userID, "user_email": u.UserEmail, "user_alias": u.UserAlias, "spend": m.Spend,
			"prompt_tokens": m.PromptTokens, "completion_tokens": m.CompletionTokens, "total_tokens": m.PromptTokens + m.CompletionTokens,
			"api_requests": m.SuccessfulRequests + m.FailedRequests, "successful_requests": m.SuccessfulRequests, "failed_requests": m.FailedRequests})
	}
	writeJSON(w, row{"users": out, "total_users": len(ids), "offset": offset, "limit": limit})
}

// teamSpendByUser 는 팀 화면의 "팀 안 사용자별 사용액"이다(N 5장). 팀 전체를 볼 권한이 없으면 자기 줄만
func (h *Handlers) teamSpendByUser(w http.ResponseWriter, r *http.Request) {
	start, end, ok := activityRange(w, r, true)
	if !ok {
		return
	}
	if r.URL.Query().Get("team_ids") == "" {
		detailError(400, "Please provide team_ids").Write(w)
		return
	}
	f := limits.ActivityFilter{Start: start, End: end}
	aliases, ok := h.activityScope(w, r, entityTeam, &f)
	if !ok {
		return
	}
	rows, err := h.usage.Activity(r.Context(), f)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	type pair struct{ team, user string }
	sums := map[pair]*limits.Metrics{}
	for _, a := range rows {
		p := pair{deref(a.TeamID), deref(a.UserID)}
		if sums[p] == nil {
			sums[p] = &limits.Metrics{}
		}
		sums[p].Add(a.Metrics)
	}
	pairs := make([]pair, 0, len(sums))
	var userIDs []string
	for p := range sums {
		pairs = append(pairs, p)
		userIDs = append(userIDs, p.user)
	}
	slices.SortFunc(pairs, func(a, b pair) int {
		return cmp.Or(cmp.Compare(sums[b].Spend, sums[a].Spend), cmp.Compare(a.team, b.team), cmp.Compare(a.user, b.user))
	})
	users, err := h.users.FindAll(r.Context(), userIDs)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	byID := map[string]auth.User{}
	for _, u := range users {
		byID[u.UserID] = u
	}
	results := make([]row, 0, len(pairs))
	for _, p := range pairs {
		m, u := sums[p], byID[p.user]
		results = append(results, row{"team_id": p.team, "team_alias": aliases[p.team], "user_id": p.user, "user_email": u.UserEmail,
			"user_alias": u.UserAlias, "spend": m.Spend, "prompt_tokens": m.PromptTokens, "completion_tokens": m.CompletionTokens,
			"total_tokens": m.PromptTokens + m.CompletionTokens, "api_requests": m.SuccessfulRequests + m.FailedRequests,
			"successful_requests": m.SuccessfulRequests, "failed_requests": m.FailedRequests})
	}
	writeJSON(w, row{"start_date": start.Format(time.DateOnly), "end_date": end.Format(time.DateOnly), "results": results})
}

func writeJSON(w http.ResponseWriter, v any) { core.WriteJSON(w, http.StatusOK, v) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
