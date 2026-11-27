package limits

import (
	"testing"
	"time"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

// 리셋 시각이 지난 대상만 사용액을 0 으로 되돌리고, 다음 리셋 시각을 지금부터 다시 계산한다
func TestResetDueBudgets_ResetsOnlyDueAndSchedulesNext(t *testing.T) {
	db, _ := dbtest.Open(t)
	now := fixedNow
	p := NewPolicies(db, func() time.Time { return now })
	ctx := t.Context()

	for _, id := range []string{"due", "later", "no-duration"} {
		if _, err := p.Apply(ctx, ScopeKey, id, Patch{MaxBudget: core.To(10.0)}); err != nil {
			t.Fatal(err)
		}
		if _, err := p.SetSpend(ctx, ScopeKey, id, 7); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"due", "later"} {
		if _, err := p.Apply(ctx, ScopeKey, id, Patch{BudgetDuration: core.To("30d")}); err != nil {
			t.Fatal(err)
		}
	}

	// 30일이 지난 뒤에는 "due" 와 "later" 둘 다 지났으므로, "later" 의 리셋 시각을 미래로 돌려 놓는다
	now = fixedNow.AddDate(0, 0, 31)
	if _, err := db.Q(ctx).Exec(ctx, "UPDATE limit_policy SET budget_reset_at = $1 WHERE target_id = 'later'", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	count, err := p.ResetDueBudgets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("리셋한 대상 = %d, 1 이어야 한다", count)
	}
	due := mustFind(t, p, ScopeKey, "due")
	if due.Spend != 0 {
		t.Errorf("due 의 사용액 = %v, 0 이어야 한다", due.Spend)
	}
	if want := now.AddDate(0, 0, 30); !due.BudgetResetAt.Equal(want) {
		t.Errorf("due 의 다음 리셋 = %v, 기대 %v", due.BudgetResetAt, want)
	}
	for _, id := range []string{"later", "no-duration"} {
		if got := mustFind(t, p, ScopeKey, id); got.Spend != 7 {
			t.Errorf("%s 의 사용액 = %v, 7 그대로여야 한다", id, got.Spend)
		}
	}
}
