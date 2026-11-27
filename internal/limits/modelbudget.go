package limits

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// ModelBudget 은 대상 하나가 모델 하나에 쓰는 한도다(litellm model_max_budget 의 항목). 모델 이름은 요청한 이름과 정확히 같을 때만 걸린다
type ModelBudget struct {
	MaxBudget      *float64 `json:"max_budget"`
	BudgetDuration *string  `json:"budget_duration"`
	RPMLimit       *int64   `json:"rpm_limit"`
	TPMLimit       *int64   `json:"tpm_limit"`
}

// modelState 는 요청을 검사할 때 읽는 모델 한도와 지금까지 쓴 금액이다
type modelState struct {
	ModelBudget
	Spend float64
}

func validateModelBudgets(budgets map[string]ModelBudget) string {
	for model, b := range budgets {
		if strings.TrimSpace(model) == "" {
			return "model_max_budget keys must be non-empty model names"
		}
		if b.MaxBudget != nil && (*b.MaxBudget < 0 || math.IsInf(*b.MaxBudget, 0) || math.IsNaN(*b.MaxBudget)) {
			return fmt.Sprintf("model_max_budget[%q].max_budget must be a non-negative finite number. Received: %s", model, formatFloat(*b.MaxBudget))
		}
		if b.BudgetDuration != nil {
			if _, ok := nextReset(*b.BudgetDuration, time.Unix(0, 0)); !ok {
				return fmt.Sprintf("Invalid budget_duration for model_max_budget[%q]: %s", model, *b.BudgetDuration)
			}
		}
	}
	return ""
}

// loadModelBudgets 는 대상별 모델 한도다. 모델 한도가 없는 대상은 결과에 없다
func (p *Policies) loadModelBudgets(ctx context.Context, scope Scope, ids []string) (map[string]map[string]ModelBudget, error) {
	rows, err := p.db.Q(ctx).Query(ctx, `
		SELECT target_id, model, max_budget, budget_duration, rpm_limit, tpm_limit
		FROM limit_model_policy WHERE scope = $1 AND target_id = ANY($2)`, scope, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]map[string]ModelBudget{}
	for rows.Next() {
		var id, model string
		var b ModelBudget
		if err := rows.Scan(&id, &model, &b.MaxBudget, &b.BudgetDuration, &b.RPMLimit, &b.TPMLimit); err != nil {
			return nil, err
		}
		if result[id] == nil {
			result[id] = map[string]ModelBudget{}
		}
		result[id][model] = b
	}
	return result, rows.Err()
}

// saveModelBudgets 는 대상의 모델 한도를 desired 로 바꾼다(litellm 처럼 통째로 바꾼다: 목록에 없는 모델은 지운다).
// 이미 있는 모델의 사용액은 지키고, 주기가 바뀌었을 때만 다음 리셋 시각을 다시 계산한다
func (p *Policies) saveModelBudgets(ctx context.Context, scope Scope, id string, desired map[string]ModelBudget) error {
	q := p.db.Q(ctx)
	keep := make([]string, 0, len(desired))
	for model := range desired {
		keep = append(keep, model)
	}
	if _, err := q.Exec(ctx, "DELETE FROM limit_model_policy WHERE scope = $1 AND target_id = $2 AND NOT (model = ANY($3))", scope, id, keep); err != nil {
		return fmt.Errorf("모델 한도 정리: %w", err)
	}
	now := p.now().UTC()
	for model, b := range desired {
		var resetAt *time.Time
		if b.BudgetDuration != nil {
			if next, ok := nextReset(*b.BudgetDuration, now); ok {
				resetAt = &next
			}
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO limit_model_policy (scope, target_id, model, max_budget, budget_duration, budget_reset_at, tpm_limit, rpm_limit, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			ON CONFLICT (scope, target_id, model) DO UPDATE SET
				max_budget = EXCLUDED.max_budget,
				budget_reset_at = CASE WHEN limit_model_policy.budget_duration IS DISTINCT FROM EXCLUDED.budget_duration
					THEN EXCLUDED.budget_reset_at ELSE limit_model_policy.budget_reset_at END,
				budget_duration = EXCLUDED.budget_duration,
				tpm_limit = EXCLUDED.tpm_limit, rpm_limit = EXCLUDED.rpm_limit, updated_at = now()`,
			scope, id, model, b.MaxBudget, b.BudgetDuration, resetAt, b.TPMLimit, b.RPMLimit); err != nil {
			return fmt.Errorf("모델 한도 저장: %w", err)
		}
	}
	return nil
}

// findModelStates 는 요청 하나의 대상들이 이 모델에 건 한도를 쿼리 한 번으로 읽는다(요청마다 도는 Gate 용). 없는 대상은 결과에 없다
func (p *Policies) findModelStates(ctx context.Context, ids map[Scope]string, model string) (map[Scope]modelState, error) {
	scopes, targets := make([]string, 0, len(ids)), make([]string, 0, len(ids))
	for scope, id := range ids {
		scopes, targets = append(scopes, string(scope)), append(targets, id)
	}
	rows, err := p.db.Q(ctx).Query(ctx, `
		SELECT m.scope, m.max_budget, m.spend, m.rpm_limit, m.tpm_limit
		FROM limit_model_policy m JOIN unnest($1::text[], $2::text[]) AS want(scope, target_id) USING (scope, target_id)
		WHERE m.model = $3`, scopes, targets, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[Scope]modelState{}
	for rows.Next() {
		var scope Scope
		var s modelState
		if err := rows.Scan(&scope, &s.MaxBudget, &s.Spend, &s.RPMLimit, &s.TPMLimit); err != nil {
			return nil, err
		}
		result[scope] = s
	}
	return result, rows.Err()
}

// resetDueModelBudgets 는 ResetDueBudgets 와 같은 일을 모델 한도에 한다(같은 트랜잭션 안에서 부른다)
func (p *Policies) resetDueModelBudgets(ctx context.Context, now time.Time) (int, error) {
	rows, err := p.db.Q(ctx).Query(ctx, `
		SELECT scope, target_id, model, budget_duration FROM limit_model_policy
		WHERE budget_duration IS NOT NULL AND budget_reset_at <= $1
		FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return 0, err
	}
	type due struct {
		scope             Scope
		id, model, period string
	}
	var targets []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.scope, &d.id, &d.model, &d.period); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, d := range targets {
		next, ok := nextReset(d.period, now)
		if !ok {
			continue
		}
		if _, err := p.db.Q(ctx).Exec(ctx, `
			UPDATE limit_model_policy SET spend = 0, budget_reset_at = $4, updated_at = now() WHERE scope = $1 AND target_id = $2 AND model = $3`,
			d.scope, d.id, d.model, next); err != nil {
			return 0, fmt.Errorf("%s %s 모델 %s 리셋: %w", d.scope, d.id, d.model, err)
		}
		count++
	}
	return count, nil
}
