package limits

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// budgetResetInterval 은 설계 3.3 의 예산 리셋 주기다
const budgetResetInterval = time.Minute

// ResetDueBudgets 는 리셋 시각이 지난 대상의 사용액을 0 으로 되돌리고 다음 리셋 시각을 지금부터 다시 계산한다(litellm 과 같다).
// 돌려주는 값은 리셋한 대상 수다. 여러 대가 동시에 돌아도 SKIP LOCKED 로 한 대만 같은 행을 잡는다
func (p *Policies) ResetDueBudgets(ctx context.Context) (int, error) {
	now := p.now().UTC()
	count := 0
	err := p.db.InTx(ctx, func(ctx context.Context) error {
		due, err := p.dueForReset(ctx, now)
		if err != nil {
			return err
		}
		for _, target := range due {
			next, ok := nextReset(target.duration, now)
			if !ok {
				continue // 저장 때 검증한 값이라 여기 올 일은 없다. 이상하면 그 대상만 건너뛴다
			}
			if _, err := p.db.Q(ctx).Exec(ctx, `
				UPDATE limit_policy SET spend = 0, budget_reset_at = $3, updated_at = now() WHERE scope = $1 AND target_id = $2`,
				target.scope, target.id, next); err != nil {
				return fmt.Errorf("%s %s 리셋: %w", target.scope, target.id, err)
			}
			count++
		}
		models, err := p.resetDueModelBudgets(ctx, now)
		count += models
		return err
	})
	return count, err
}

type resetTarget struct {
	scope    Scope
	id       string
	duration string
}

// dueForReset 는 리셋할 행을 잠그고 읽는다. 행을 다 읽어 닫은 뒤에야 갱신할 수 있어 목록으로 돌려준다
func (p *Policies) dueForReset(ctx context.Context, now time.Time) ([]resetTarget, error) {
	rows, err := p.db.Q(ctx).Query(ctx, `
		SELECT scope, target_id, budget_duration FROM limit_policy
		WHERE budget_duration IS NOT NULL AND budget_reset_at <= $1
		FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var due []resetTarget
	for rows.Next() {
		var t resetTarget
		if err := rows.Scan(&t.scope, &t.id, &t.duration); err != nil {
			return nil, err
		}
		due = append(due, t)
	}
	return due, rows.Err()
}

// RunBudgetResets 는 ctx 가 끝날 때까지 주기마다 예산을 리셋한다. 한 번 실패해도 오류만 남기고 다음 주기에 다시 한다
func (p *Policies) RunBudgetResets(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(budgetResetInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := p.ResetDueBudgets(ctx)
			if err != nil {
				logger.Error("예산 리셋 실패", "err", err)
			} else if count > 0 {
				logger.Info("예산 리셋", "count", count)
			}
		}
	}
}
