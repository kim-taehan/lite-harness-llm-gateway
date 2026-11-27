package limits

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"llm-gateway/internal/core"
)

// Gate 는 요청 전에 예산·RPM·TPM·동시성을 한 번에 검사하고 동시성 슬롯을 잡는다(설계 4장 3·4단계, 3.1 LimitGate)
type Gate struct {
	policies *Policies
	counters CounterStore
	now      func() time.Time
}

func NewGate(policies *Policies, counters CounterStore, now func() time.Time) *Gate {
	return &Gate{policies: policies, counters: counters, now: now}
}

// Subject 는 한도를 거는 대상들이다. 빈 id 는 건너뛴다
type Subject struct {
	KeyToken string
	KeyAlias string
	TeamID   string
	UserID   string
	// EndUserID 는 요청이 밝힌 고객(end user)이다. 호출자가 정하는 값이라 속일 수 있다(litellm 도 같다). 비면 고객 한도는 보지 않는다
	EndUserID string
	// Tags 는 요청이 붙인 태그다. 등록한 태그에만 한도가 걸리고 호출자가 정하는 값이라 속일 수 있다. 중복과 개수는 부르는 쪽이 줄인다
	Tags  []string
	Model string // 요청한 모델 이름. 모델별 한도(model_max_budget)를 이 이름으로 찾는다. 비면 모델별 한도는 보지 않는다
}

// Permit 은 통과한 요청의 슬롯이다. 응답이 끝나거나 끊기면 Release 한다(설계 4.2: defer)
type Permit struct {
	release []func()
	gate    *Gate
	subject Subject
	limited map[target]bool // TPM 한도가 있는 대상(응답 뒤 토큰을 더할 곳)
	daily   map[target]bool // TPD 한도가 있는 대상(응답 뒤 토큰을 더할 곳)
	// modelTPM 은 모델별 TPM 이 걸린 카운터 키다(응답 뒤 토큰을 더할 곳)
	modelTPM []string
	headers  map[string]string
}

// dailyWindow 는 하루 토큰 한도의 창이다. 윈도우가 에포크 기준으로 잘려 UTC 자정에 바뀐다(litellm 도 UTC 날짜 기준)
const dailyWindow = 24 * time.Hour

// report 는 litellm v3 리미터처럼 x-ratelimit-{api_key|team|user}-{limit|remaining}-{requests|tokens|max_parallel_requests} 헤더를 모은다.
// 값은 검사 시점의 스냅샷이다(응답 뒤 토큰 정산은 반영하지 않는다)
func (p *Permit) report(scope Scope, kind string, limit, used int64) {
	if scope == ScopeTag {
		return // 태그는 요청 하나에 여럿이라 헤더 이름이 겹친다. litellm 도 태그 헤더는 없다
	}
	if p.headers == nil {
		p.headers = map[string]string{}
	}
	prefix := "x-ratelimit-" + descriptorNames[scope]
	p.headers[prefix+"-limit-"+kind] = strconv.FormatInt(limit, 10)
	p.headers[prefix+"-remaining-"+kind] = strconv.FormatInt(max(0, limit-used), 10)
}

// EndUserID 는 이 요청이 밝힌 고객이다(사용량 기록에 남긴다). 없으면 비어 있다
func (p *Permit) EndUserID() string { return p.subject.EndUserID }

// Tags 는 이 요청이 붙인 태그다(사용량 기록에 남긴다)
func (p *Permit) Tags() []string { return p.subject.Tags }

// Headers 는 통과한 요청의 응답에 실을 x-ratelimit-* 헤더다. 한도가 없으면 비어 있다
func (p *Permit) Headers() map[string]string { return p.headers }

func (p *Permit) Release() {
	for _, r := range p.release {
		r()
	}
}

// AddTokens 는 응답 뒤 실제 토큰을 TPM·TPD 카운터에 더한다. 요청이 끊긴 뒤에도 불리므로 호출하는 쪽이 취소와 무관한 ctx 를 넘긴다
func (p *Permit) AddTokens(ctx context.Context, tokens int) {
	for _, t := range p.subject.targets() {
		if p.limited[t] {
			p.gate.counters.Add(ctx, counterKey("tpm", t.scope, t.id), int64(tokens), time.Minute)
		}
		if p.daily[t] {
			p.gate.counters.Add(ctx, counterKey("tpd", t.scope, t.id), int64(tokens), dailyWindow)
		}
	}
	for _, key := range p.modelTPM {
		p.gate.counters.Add(ctx, key, int64(tokens), time.Minute)
	}
}

// targets 는 이 요청에 한도가 걸릴 수 있는 대상들이다. 검사 순서(키, 팀, 사용자, 팀원, 고객, 태그들)대로이고 빈 id 는 뺀다
func (s Subject) targets() []target {
	var result []target
	for _, t := range []target{{ScopeKey, s.KeyToken}, {ScopeTeam, s.TeamID}, {ScopeUser, s.UserID},
		{ScopeTeamMember, MemberTarget(s.TeamID, s.UserID)}, {ScopeEndUser, s.EndUserID}} {
		if t.id != "" {
			result = append(result, t)
		}
	}
	for _, tag := range s.Tags {
		result = append(result, target{ScopeTag, tag})
	}
	return result
}

// 동시성 슬롯 임대 시간. 긴 스트림이 이보다 오래 가면 슬롯이 먼저 풀린다(연장은 Redis 구현 때 붙인다)
const slotLease = 10 * time.Minute

// Admit 은 통과하면 Permit, 막으면 litellm 모양 오류(core.ProxyError)다
func (g *Gate) Admit(ctx context.Context, s Subject) (*Permit, error) {
	if s.EndUserID != "" {
		blocked, err := NewCustomers(g.policies.db).IsBlocked(ctx, s.EndUserID)
		if err != nil {
			return nil, err
		}
		if blocked {
			return nil, endUserBlocked(s.EndUserID)
		}
	}
	wanted := s.targets()
	policies, err := g.policies.FindSubject(ctx, wanted)
	if err != nil {
		return nil, err
	}
	for _, t := range wanted {
		if p, ok := policies[t]; ok && p.MaxBudget != nil && p.Spend >= *p.MaxBudget {
			return nil, budgetExceeded(t, s, p)
		}
	}
	var models map[target]modelState
	if s.Model != "" {
		if models, err = g.policies.findModelStates(ctx, wanted, s.Model); err != nil {
			return nil, err
		}
	}
	for _, t := range wanted {
		if m, ok := models[t]; ok && m.MaxBudget != nil && m.Spend >= *m.MaxBudget {
			return nil, modelBudgetExceeded(t, s, m)
		}
	}
	if g.countersDown() && (usesCounters(policies) || modelUsesCounters(models)) {
		return nil, countersUnavailable()
	}
	permit := &Permit{gate: g, subject: s, limited: map[target]bool{}, daily: map[target]bool{}}
	for _, t := range wanted {
		p, ok := policies[t]
		if !ok {
			continue
		}
		scope, id := t.scope, t.id
		if p.TPMLimit != nil {
			permit.limited[t] = true
			used := g.counters.Get(ctx, counterKey("tpm", scope, id), time.Minute)
			if used >= *p.TPMLimit {
				permit.Release()
				return nil, g.rateLimited(scope, id, "tokens", *p.TPMLimit)
			}
			permit.report(scope, "tokens", *p.TPMLimit, used)
		}
		if p.TPDLimit != nil {
			permit.daily[t] = true
			// litellm 과 같게 이미 쓴 양만 본다. 마지막 요청은 한도를 넘어서 통과할 수 있다
			used := g.counters.Get(ctx, counterKey("tpd", scope, id), dailyWindow)
			if used >= *p.TPDLimit {
				permit.Release()
				return nil, g.dailyTokensExceeded(scope, used, *p.TPDLimit)
			}
		}
		if p.RPMLimit != nil {
			used, ok := g.counters.IncrementIfBelow(ctx, counterKey("rpm", scope, id), 1, *p.RPMLimit, time.Minute)
			if !ok {
				permit.Release()
				return nil, g.rateLimited(scope, id, "requests", *p.RPMLimit)
			}
			permit.report(scope, "requests", *p.RPMLimit, used)
		}
		if p.MaxParallelRequests != nil {
			release, held, ok := g.counters.AcquireSlot(ctx, counterKey("conc", scope, id), *p.MaxParallelRequests, slotLease)
			if !ok {
				permit.Release()
				return nil, g.rateLimited(scope, id, "max_parallel_requests", *p.MaxParallelRequests)
			}
			permit.release = append(permit.release, release)
			permit.report(scope, "max_parallel_requests", *p.MaxParallelRequests, held)
		}
	}
	for _, t := range wanted {
		if m, ok := models[t]; ok {
			if err := g.admitModel(ctx, permit, t, s.Model, m); err != nil {
				permit.Release()
				return nil, err
			}
		}
	}
	return permit, nil
}

// admitModel 은 대상이 이 모델에 건 RPM·TPM 을 검사한다. 카운터는 (대상, 모델) 단위이고 한도 값은 요청의 한도와 따로 센다
func (g *Gate) admitModel(ctx context.Context, permit *Permit, t target, model string, m modelState) error {
	counterID := t.id + "|" + model
	if m.TPMLimit != nil {
		key := counterKey("mtpm", t.scope, counterID)
		permit.modelTPM = append(permit.modelTPM, key)
		if g.counters.Get(ctx, key, time.Minute) >= *m.TPMLimit {
			return forModel(g.rateLimited(t.scope, t.id, "tokens", *m.TPMLimit), model)
		}
	}
	if m.RPMLimit != nil {
		if _, ok := g.counters.IncrementIfBelow(ctx, counterKey("mrpm", t.scope, counterID), 1, *m.RPMLimit, time.Minute); !ok {
			return forModel(g.rateLimited(t.scope, t.id, "requests", *m.RPMLimit), model)
		}
	}
	return nil
}

// forModel 은 429 문구에 어느 모델의 한도인지 덧붙인다
func forModel(e core.ProxyError, model string) core.ProxyError {
	e.Message += " Model: " + model
	return e
}

func modelUsesCounters(models map[target]modelState) bool {
	for _, m := range models {
		if m.RPMLimit != nil || m.TPMLimit != nil {
			return true
		}
	}
	return false
}

// modelBudgetExceeded 는 모델별 예산을 넘었을 때의 오류다. 상태 코드와 말머리는 대상 예산(budgetExceeded)과 같다.
// 모델 예산의 정확한 litellm 문구는 확인하지 못해 모델 이름만 덧붙였다
func modelBudgetExceeded(t target, s Subject, m modelState) core.ProxyError {
	return core.ProxyError{Status: http.StatusUnprocessableEntity, Type: "budget_exceeded", Param: nil,
		Message: fmt.Sprintf("Budget has been exceeded! %s Model=%s Current cost: %s, Max budget: %s",
			subjectLabel(t, s), s.Model, formatFloat(m.Spend), formatFloat(*m.MaxBudget))}
}

// outageAware 는 장애를 알리는 카운터 저장소(Redis)다. 장애 때 거부하도록 정했으면 Unavailable 이 참이다
type outageAware interface{ Unavailable() bool }

func (g *Gate) countersDown() bool {
	store, ok := g.counters.(outageAware)
	return ok && store.Unavailable()
}

// usesCounters 는 이 요청의 한도 중 카운터가 필요한 것(RPM·TPM·TPD·동시성)이 있는지다. 예산만 있으면 DB 로 검사하므로 카운터가 필요 없다
func usesCounters(policies map[target]Policy) bool {
	for _, p := range policies {
		if p.TPMLimit != nil || p.TPDLimit != nil || p.RPMLimit != nil || p.MaxParallelRequests != nil {
			return true
		}
	}
	return false
}

// countersUnavailable 은 한도 카운터를 못 쓸 때(Redis 장애, 거부로 설정) 주는 503 이다. 서킷 브레이커가 5초 뒤 다시 시험하므로 retry-after 도 5초다
func countersUnavailable() core.ProxyError {
	return core.ProxyError{Status: http.StatusServiceUnavailable, Type: "service_unavailable", Param: nil,
		Message: "Rate limit counters are temporarily unavailable. Retry shortly.",
		Headers: map[string]string{"retry-after": "5"}}
}

func counterKey(kind string, scope Scope, id string) string {
	return "gw:" + kind + ":" + string(scope) + ":" + id
}

// litellm 의 descriptor 이름(parallel_request_limiter_v3.py): 키 api_key, 팀 team, 사용자 user
var descriptorNames = map[Scope]string{ScopeKey: "api_key", ScopeTeam: "team", ScopeUser: "user", ScopeTeamMember: "team_member", ScopeEndUser: "end_user", ScopeTag: "tag"}

// gatedScopes 는 요청마다 검사하는 대상 종류다(소프트 예산 지표가 종류별로 센다)
var gatedScopes = []Scope{ScopeKey, ScopeTeam, ScopeUser, ScopeTeamMember, ScopeEndUser, ScopeTag}

// rateLimited 는 litellm v3 리미터의 429 문구와 retry-after 다. 본문 모양(type)은 litellm 원문에서 확인하지 못했다
func (g *Gate) rateLimited(scope Scope, id, kind string, limit int64) core.ProxyError {
	reset := g.now().UTC().Truncate(time.Minute).Add(time.Minute).Format("2006-01-02 15:04:05 UTC")
	return core.ProxyError{Status: http.StatusTooManyRequests, Type: "rate_limit_error", Param: nil,
		Message: fmt.Sprintf("Rate limit exceeded for %s: %s. Limit type: %s. Current limit: %d, Remaining: 0. Limit resets at: %s",
			descriptorNames[scope], id, kind, limit, reset),
		Headers: map[string]string{"retry-after": strconv.Itoa(60), "rate_limit_type": kind, "reset_at": reset}}
}

// endUserBlocked 는 차단한 고객의 요청에 주는 400 이다(litellm blocked_user_list 훅의 문구)
func endUserBlocked(id string) core.ProxyError {
	return core.ProxyError{Status: http.StatusBadRequest, Type: "bad_request_error", Param: nil,
		Message: "User blocked from making LLM API Calls. User=" + id}
}

// dailyTokensExceeded 는 litellm daily_token_limiter 의 429 다. retry-after 는 다음 UTC 자정까지 남은 초다
func (g *Gate) dailyTokensExceeded(scope Scope, used, limit int64) core.ProxyError {
	now := g.now().UTC()
	retryAfter := int(now.Truncate(dailyWindow).Add(dailyWindow).Sub(now).Seconds())
	return core.ProxyError{Status: http.StatusTooManyRequests, Type: "rate_limit_error", Param: nil,
		Message: fmt.Sprintf("Daily token limit reached for %s. Used %d of %d tokens today (UTC). Resets at 00:00 UTC.", descriptorNames[scope], used, limit),
		Headers: map[string]string{"retry-after": strconv.Itoa(retryAfter), "rate_limit_type": "tokens"}}
}

// budgetExceeded 는 litellm BudgetExceededError 다. 이 포크의 기본 상태 코드는 422 다(J 5.3)
func budgetExceeded(t target, s Subject, p Policy) core.ProxyError {
	return core.ProxyError{Status: http.StatusUnprocessableEntity, Type: "budget_exceeded", Param: nil,
		Message: fmt.Sprintf("Budget has been exceeded! %s Current cost: %s, Max budget: %s", subjectLabel(t, s), formatFloat(p.Spend), formatFloat(*p.MaxBudget))}
}

// subjectLabel 은 오류 문구에서 대상을 가리키는 말이다. 태그는 어느 태그인지 알려야 해서 이름을 쓴다
func subjectLabel(t target, s Subject) string {
	if t.scope == ScopeTag {
		return "Tag=" + t.id
	}
	return map[Scope]string{ScopeKey: "Key=" + orDefault(s.KeyAlias, "key"), ScopeTeam: "Team=" + s.TeamID, ScopeUser: "User=" + s.UserID,
		ScopeTeamMember: "User=" + s.UserID + " in Team=" + s.TeamID, ScopeEndUser: "End User=" + s.EndUserID}[t.scope]
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
