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
	ScopeKey  Scope = "key"
	ScopeTeam Scope = "team"
	ScopeUser Scope = "user"
)

// Policy 는 대상 하나의 한도 설정이다. nil 은 "정하지 않음"이다
type Policy struct {
	MaxBudget           *float64
	SoftBudget          *float64
	Spend               float64
	TPMLimit            *int64
	RPMLimit            *int64
	MaxParallelRequests *int64
	BudgetDuration      *string
	BudgetResetAt       *time.Time
}

// Patch 는 바꿀 한도 필드다. 보내지 않은 필드는 그대로 둔다
type Patch struct {
	MaxBudget           core.Change[float64]
	SoftBudget          core.Change[float64]
	TPMLimit            core.Change[int64]
	RPMLimit            core.Change[int64]
	MaxParallelRequests core.Change[int64]
	BudgetDuration      core.Change[string]
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
		SELECT target_id, max_budget, soft_budget, spend, tpm_limit, rpm_limit, max_parallel_requests, budget_duration, budget_reset_at
		FROM limit_policy WHERE scope = $1 AND target_id = ANY($2)`, scope, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var pol Policy
		if err := rows.Scan(&id, &pol.MaxBudget, &pol.SoftBudget, &pol.Spend, &pol.TPMLimit, &pol.RPMLimit,
			&pol.MaxParallelRequests, &pol.BudgetDuration, &pol.BudgetResetAt); err != nil {
			return nil, err
		}
		result[id] = pol
	}
	return result, rows.Err()
}

// FindSubject 는 대상 종류마다 id 하나씩의 한도를 쿼리 한 번으로 읽는다(요청마다 도는 Gate 용). 한도를 정하지 않은 종류는 결과에 없다
func (p *Policies) FindSubject(ctx context.Context, ids map[Scope]string) (map[Scope]Policy, error) {
	scopes, targets := make([]string, 0, len(ids)), make([]string, 0, len(ids))
	for scope, id := range ids {
		scopes, targets = append(scopes, string(scope)), append(targets, id)
	}
	rows, err := p.db.Q(ctx).Query(ctx, `
		SELECT l.scope, l.max_budget, l.soft_budget, l.spend, l.tpm_limit, l.rpm_limit, l.max_parallel_requests, l.budget_duration, l.budget_reset_at
		FROM limit_policy l JOIN unnest($1::text[], $2::text[]) AS want(scope, target_id) USING (scope, target_id)`, scopes, targets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[Scope]Policy{}
	for rows.Next() {
		var scope Scope
		var pol Policy
		if err := rows.Scan(&scope, &pol.MaxBudget, &pol.SoftBudget, &pol.Spend, &pol.TPMLimit, &pol.RPMLimit,
			&pol.MaxParallelRequests, &pol.BudgetDuration, &pol.BudgetResetAt); err != nil {
			return nil, err
		}
		result[scope] = pol
	}
	return result, rows.Err()
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
		INSERT INTO limit_policy (scope, target_id, max_budget, soft_budget, tpm_limit, rpm_limit, max_parallel_requests, budget_duration, budget_reset_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (scope, target_id) DO UPDATE SET
			max_budget = EXCLUDED.max_budget, soft_budget = EXCLUDED.soft_budget,
			tpm_limit = EXCLUDED.tpm_limit, rpm_limit = EXCLUDED.rpm_limit,
			max_parallel_requests = EXCLUDED.max_parallel_requests,
			budget_duration = EXCLUDED.budget_duration, budget_reset_at = EXCLUDED.budget_reset_at,
			updated_at = now()`,
		scope, id, next.MaxBudget, next.SoftBudget, next.TPMLimit, next.RPMLimit, next.MaxParallelRequests, next.BudgetDuration, next.BudgetResetAt)
	if err != nil {
		return Policy{}, fmt.Errorf("한도 저장: %w", err)
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
	_, err := p.db.Q(ctx).Exec(ctx, "UPDATE limit_policy SET target_id = $3 WHERE scope = $1 AND target_id = $2", scope, from, to)
	return err
}

func (p *Policies) Delete(ctx context.Context, scope Scope, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := p.db.Q(ctx).Exec(ctx, "DELETE FROM limit_policy WHERE scope = $1 AND target_id = ANY($2)", scope, ids)
	return err
}

func (p *Policies) merge(current Policy, patch Patch) Policy {
	next := current
	next.MaxBudget = patch.MaxBudget.Apply(current.MaxBudget)
	next.SoftBudget = patch.SoftBudget.Apply(current.SoftBudget)
	next.TPMLimit = patch.TPMLimit.Apply(current.TPMLimit)
	next.RPMLimit = patch.RPMLimit.Apply(current.RPMLimit)
	next.MaxParallelRequests = patch.MaxParallelRequests.Apply(current.MaxParallelRequests)
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
	return patch.MaxBudget.Set || patch.SoftBudget.Set || patch.TPMLimit.Set || patch.RPMLimit.Set ||
		patch.MaxParallelRequests.Set || patch.BudgetDuration.Set
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
	return ""
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
