// Package limits 는 한도와 사용량을 맡는다: 키·팀·사용자의 예산·RPM·TPM·동시성 설정(limit_policy),
// 요청 검사(Gate)와 사용량 기록·조회.
package limits

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"llm-gateway/internal/core"
)

// Scope 는 한도를 거는 대상의 종류다
type Scope string

const (
	ScopeKey        Scope = "key"
	ScopeTeam       Scope = "team"
	ScopeUser       Scope = "user"
	ScopeTeamMember Scope = "team_member"
	// ScopeEndUser 는 고객(end user)이다. 대상 id 는 요청이 보낸 user 값이다
	ScopeEndUser Scope = "end_user"
	// ScopeTag 는 태그다. 대상 id 는 태그 이름이고, 요청 하나에 여러 개가 붙을 수 있다
	ScopeTag Scope = "tag"
	// ScopeProvider 는 프로바이더다. 대상 id 는 배포의 provider 이름이고, 요청의 한도 검사(Gate)가 아니라 배포를 고르는 단계가 본다
	ScopeProvider Scope = "provider"
)

// target 은 한도를 거는 대상 하나다. 요청 하나가 대상 여러 개(태그가 여럿)에 걸릴 수 있어 종류만으로는 가를 수 없다
type target struct {
	scope Scope
	id    string
}

// MemberTarget 은 팀원 한도의 대상 id 다("팀id:사용자id", litellm descriptor 와 같다). 팀이나 사용자가 없으면 빈 문자열이다
func MemberTarget(teamID, userID string) string {
	if teamID == "" || userID == "" {
		return ""
	}
	return teamID + ":" + userID
}

// Policy 는 대상 하나의 한도 설정이다. nil 은 "정하지 않음"이다
type Policy struct {
	MaxBudget           *float64
	SoftBudget          *float64
	Spend               float64
	TPMLimit            *int64
	TPDLimit            *int64
	RPMLimit            *int64
	MaxParallelRequests *int64
	BudgetDuration      *string
	BudgetResetAt       *time.Time
	// ModelBudgets 는 모델별 한도다(litellm model_max_budget). Find 가 채우고 Gate 는 요청의 모델 하나만 따로 읽는다
	ModelBudgets map[string]ModelBudget
}

// HasLimits 는 정해 둔 한도(예산·TPM·TPD·RPM·병렬·기간)가 하나라도 있는지다. 사용액만 있으면 아니다
func (p Policy) HasLimits() bool {
	return p.MaxBudget != nil || p.SoftBudget != nil || p.TPMLimit != nil || p.TPDLimit != nil || p.RPMLimit != nil || p.MaxParallelRequests != nil || p.BudgetDuration != nil || len(p.ModelBudgets) > 0
}

// Patch 는 바꿀 한도 필드다. 보내지 않은 필드는 그대로 둔다
type Patch struct {
	MaxBudget           core.Change[float64]
	SoftBudget          core.Change[float64]
	TPMLimit            core.Change[int64]
	TPDLimit            core.Change[int64]
	RPMLimit            core.Change[int64]
	MaxParallelRequests core.Change[int64]
	BudgetDuration      core.Change[string]
	// ModelBudgets 를 보내면 모델별 한도를 통째로 바꾼다. null 이나 {} 면 모두 지운다
	ModelBudgets core.Change[map[string]ModelBudget]
}

// InvalidError 는 litellm 이 400 으로 거절하는 한도 값이다. Message 는 litellm 문구다
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

// Policies 는 한도 설정의 저장·조회다. 요청을 막는 검사는 Gate 가 한다
type Policies struct {
	db  *core.DB
	now func() time.Time
}

func NewPolicies(db *core.DB, now func() time.Time) *Policies {
	return &Policies{db: db, now: now}
}

// Find 는 대상별 한도다. 한도를 정하지 않은 대상은 결과에 없다
func (p *Policies) Find(ctx context.Context, scope Scope, ids []string) (map[string]Policy, error) {
	result := map[string]Policy{}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := p.db.Q(ctx).Query(ctx, `
		SELECT target_id, max_budget, soft_budget, spend, tpm_limit, tpd_limit, rpm_limit, max_parallel_requests, budget_duration, budget_reset_at
		FROM limit_policy WHERE scope = $1 AND target_id = ANY($2)`, scope, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var pol Policy
		if err := rows.Scan(&id, &pol.MaxBudget, &pol.SoftBudget, &pol.Spend, &pol.TPMLimit, &pol.TPDLimit, &pol.RPMLimit,
			&pol.MaxParallelRequests, &pol.BudgetDuration, &pol.BudgetResetAt); err != nil {
			return nil, err
		}
		result[id] = pol
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // 같은 연결로 다음 쿼리를 보내려면 먼저 닫는다
	budgets, err := p.loadModelBudgets(ctx, scope, ids)
	if err != nil {
		return nil, err
	}
	for id, byModel := range budgets {
		if pol, ok := result[id]; ok {
			pol.ModelBudgets = byModel
			result[id] = pol
		}
	}
	return result, nil
}

// FindSubject 는 대상들의 한도를 쿼리 한 번으로 읽는다(요청마다 도는 Gate 용). 한도를 정하지 않은 대상은 결과에 없다
func (p *Policies) FindSubject(ctx context.Context, wanted []target) (map[target]Policy, error) {
	scopes, ids := make([]string, 0, len(wanted)), make([]string, 0, len(wanted))
	for _, t := range wanted {
		scopes, ids = append(scopes, string(t.scope)), append(ids, t.id)
	}
	rows, err := p.db.Q(ctx).Query(ctx, `
		SELECT l.scope, l.target_id, l.max_budget, l.soft_budget, l.spend, l.tpm_limit, l.tpd_limit, l.rpm_limit, l.max_parallel_requests, l.budget_duration, l.budget_reset_at
		FROM limit_policy l JOIN unnest($1::text[], $2::text[]) AS want(scope, target_id) USING (scope, target_id)`, scopes, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[target]Policy{}
	for rows.Next() {
		var t target
		var pol Policy
		if err := rows.Scan(&t.scope, &t.id, &pol.MaxBudget, &pol.SoftBudget, &pol.Spend, &pol.TPMLimit, &pol.TPDLimit, &pol.RPMLimit,
			&pol.MaxParallelRequests, &pol.BudgetDuration, &pol.BudgetResetAt); err != nil {
			return nil, err
		}
		result[t] = pol
	}
	return result, rows.Err()
}

// SoftBudgetExceeded 는 사용액이 소프트 예산 이상인 대상 수를 종류별로 센다(litellm 의 소프트 예산 알림 조건과 같다). 없는 종류는 0 이다
func (p *Policies) SoftBudgetExceeded(ctx context.Context) (map[Scope]int64, error) {
	counts := map[Scope]int64{}
	for _, scope := range gatedScopes {
		counts[scope] = 0
	}
	rows, err := p.db.Q(ctx).Query(ctx, `
		SELECT scope, count(*) FROM limit_policy WHERE soft_budget IS NOT NULL AND spend >= soft_budget GROUP BY scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var scope Scope
		var n int64
		if err := rows.Scan(&scope, &n); err != nil {
			return nil, err
		}
		counts[scope] = n
	}
	return counts, rows.Err()
}

// Apply 는 patch 를 적용해 저장한다. 값이 틀리면 *InvalidError 이고 아무것도 저장하지 않는다
func (p *Policies) Apply(ctx context.Context, scope Scope, id string, patch Patch) (Policy, error) {
	found, err := p.Find(ctx, scope, []string{id})
	if err != nil {
		return Policy{}, err
	}
	current := found[id]
	next := p.merge(current, patch)
	if msg := validate(next); msg != "" {
		return Policy{}, &InvalidError{Message: msg}
	}
	if !patch.changes() {
		return next, nil
	}
	_, err = p.db.Q(ctx).Exec(ctx, `
		INSERT INTO limit_policy (scope, target_id, max_budget, soft_budget, tpm_limit, tpd_limit, rpm_limit, max_parallel_requests, budget_duration, budget_reset_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
		ON CONFLICT (scope, target_id) DO UPDATE SET
			max_budget = EXCLUDED.max_budget, soft_budget = EXCLUDED.soft_budget,
			tpm_limit = EXCLUDED.tpm_limit, tpd_limit = EXCLUDED.tpd_limit, rpm_limit = EXCLUDED.rpm_limit,
			max_parallel_requests = EXCLUDED.max_parallel_requests,
			budget_duration = EXCLUDED.budget_duration, budget_reset_at = EXCLUDED.budget_reset_at,
			updated_at = now()`,
		scope, id, next.MaxBudget, next.SoftBudget, next.TPMLimit, next.TPDLimit, next.RPMLimit, next.MaxParallelRequests, next.BudgetDuration, next.BudgetResetAt)
	if err != nil {
		return Policy{}, fmt.Errorf("한도 저장: %w", err)
	}
	if patch.ModelBudgets.Set {
		if err := p.saveModelBudgets(ctx, scope, id, next.ModelBudgets); err != nil {
			return Policy{}, err
		}
	}
	return next, nil
}

// SetSpend 는 사용액을 value 로 맞추고 바뀌기 전 값을 돌려준다(/key/{key}/reset_spend)
func (p *Policies) SetSpend(ctx context.Context, scope Scope, id string, value float64) (float64, error) {
	var previous float64
	err := p.db.InTx(ctx, func(ctx context.Context) error {
		found, err := p.Find(ctx, scope, []string{id})
		if err != nil {
			return err
		}
		previous = found[id].Spend
		_, err = p.db.Q(ctx).Exec(ctx, `
			INSERT INTO limit_policy (scope, target_id, spend) VALUES ($1, $2, $3)
			ON CONFLICT (scope, target_id) DO UPDATE SET spend = EXCLUDED.spend, updated_at = now()`, scope, id, value)
		return err
	})
	return previous, err
}

// Retarget 은 대상 id 가 바뀔 때 한도·사용액을 옮긴다(키 재발급은 키 해시가 바뀐다)
func (p *Policies) Retarget(ctx context.Context, scope Scope, from, to string) error {
	if _, err := p.db.Q(ctx).Exec(ctx, "UPDATE limit_policy SET target_id = $3 WHERE scope = $1 AND target_id = $2", scope, from, to); err != nil {
		return err
	}
	_, err := p.db.Q(ctx).Exec(ctx, "UPDATE limit_model_policy SET target_id = $3 WHERE scope = $1 AND target_id = $2", scope, from, to)
	return err
}

func (p *Policies) Delete(ctx context.Context, scope Scope, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := p.db.Q(ctx).Exec(ctx, "DELETE FROM limit_policy WHERE scope = $1 AND target_id = ANY($2)", scope, ids); err != nil {
		return err
	}
	_, err := p.db.Q(ctx).Exec(ctx, "DELETE FROM limit_model_policy WHERE scope = $1 AND target_id = ANY($2)", scope, ids)
	return err
}

func (p *Policies) merge(current Policy, patch Patch) Policy {
	next := current
	next.MaxBudget = patch.MaxBudget.Apply(current.MaxBudget)
	next.SoftBudget = patch.SoftBudget.Apply(current.SoftBudget)
	next.TPMLimit = patch.TPMLimit.Apply(current.TPMLimit)
	next.TPDLimit = patch.TPDLimit.Apply(current.TPDLimit)
	next.RPMLimit = patch.RPMLimit.Apply(current.RPMLimit)
	next.MaxParallelRequests = patch.MaxParallelRequests.Apply(current.MaxParallelRequests)
	if patch.ModelBudgets.Set {
		next.ModelBudgets = patch.ModelBudgets.Or(nil)
	}
	if patch.BudgetDuration.Set {
		next.BudgetDuration = patch.BudgetDuration.Value
		next.BudgetResetAt = nil
		// litellm 은 budget_duration 을 정할 때 다음 리셋 시각을 지금부터 계산한다
		if d := next.BudgetDuration; d != nil {
			if reset, ok := nextReset(*d, p.now().UTC()); ok {
				next.BudgetResetAt = &reset
			}
		}
	}
	return next
}

func (patch Patch) changes() bool {
	return patch.MaxBudget.Set || patch.SoftBudget.Set || patch.TPMLimit.Set || patch.TPDLimit.Set || patch.RPMLimit.Set ||
		patch.MaxParallelRequests.Set || patch.BudgetDuration.Set || patch.ModelBudgets.Set
}

func validate(p Policy) string {
	for _, b := range []struct {
		name  string
		value *float64
	}{{"max_budget", p.MaxBudget}, {"soft_budget", p.SoftBudget}} {
		if b.value != nil && (*b.value < 0 || math.IsInf(*b.value, 0) || math.IsNaN(*b.value)) {
			return fmt.Sprintf("%s must be a non-negative finite number. Received: %s", b.name, formatFloat(*b.value))
		}
	}
	if p.SoftBudget != nil && p.MaxBudget != nil && *p.SoftBudget >= *p.MaxBudget {
		return fmt.Sprintf("soft_budget (%s) must be strictly lower than max_budget (%s)", formatFloat(*p.SoftBudget), formatFloat(*p.MaxBudget))
	}
	if p.BudgetDuration != nil {
		if _, ok := nextReset(*p.BudgetDuration, time.Unix(0, 0)); !ok {
			return "Invalid budget_duration: " + *p.BudgetDuration
		}
	}
	return validateModelBudgets(p.ModelBudgets)
}

// litellm 예산 주기 문자열("30s", "10m", "1h", "7d", "1w", "1mo")
var durationPattern = regexp.MustCompile(`^(\d+)(mo|s|m|h|d|w)$`)

func nextReset(duration string, from time.Time) (time.Time, bool) {
	match := durationPattern.FindStringSubmatch(duration)
	if match == nil {
		return time.Time{}, false
	}
	n, _ := strconv.Atoi(match[1])
	switch match[2] {
	case "s":
		return from.Add(time.Duration(n) * time.Second), true
	case "m":
		return from.Add(time.Duration(n) * time.Minute), true
	case "h":
		return from.Add(time.Duration(n) * time.Hour), true
	case "d":
		return from.AddDate(0, 0, n), true
	case "w":
		return from.AddDate(0, 0, 7*n), true
	default:
		return from.AddDate(0, n, 0), true
	}
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
