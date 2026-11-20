package limits

import (
	"errors"
	"os"
	"testing"
	"time"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

var fixedNow = time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)

func newPolicies(t *testing.T) *Policies {
	db, _ := dbtest.Open(t)
	return NewPolicies(db, func() time.Time { return fixedNow })
}

func TestApply_SavesOnlySentFields_AndComputesReset(t *testing.T) {
	p := newPolicies(t)
	ctx := t.Context()

	if _, err := p.Apply(ctx, ScopeTeam, "t1", Patch{MaxBudget: core.To(10.0), TPMLimit: core.To(int64(1000)), BudgetDuration: core.To("30d")}); err != nil {
		t.Fatal(err)
	}
	// 두 번째 수정은 rpm 만 보낸다. 나머지는 그대로여야 한다
	if _, err := p.Apply(ctx, ScopeTeam, "t1", Patch{RPMLimit: core.To(int64(5))}); err != nil {
		t.Fatal(err)
	}
	got := mustFind(t, p, ScopeTeam, "t1")
	if *got.MaxBudget != 10 || *got.TPMLimit != 1000 || *got.RPMLimit != 5 || *got.BudgetDuration != "30d" {
		t.Errorf("한도 = %+v", got)
	}
	if want := fixedNow.AddDate(0, 0, 30); !got.BudgetResetAt.Equal(want) {
		t.Errorf("budget_reset_at = %v, 기대 %v", got.BudgetResetAt, want)
	}
}

func TestApply_NullClearsField(t *testing.T) {
	p := newPolicies(t)
	ctx := t.Context()
	_, _ = p.Apply(ctx, ScopeUser, "u1", Patch{MaxBudget: core.To(3.0)})

	if _, err := p.Apply(ctx, ScopeUser, "u1", Patch{MaxBudget: core.Change[float64]{Set: true}}); err != nil {
		t.Fatal(err)
	}
	if got := mustFind(t, p, ScopeUser, "u1"); got.MaxBudget != nil {
		t.Errorf("max_budget 가 지워지지 않았다: %v", *got.MaxBudget)
	}
}

func TestApply_RejectsLitellmInvalidValues(t *testing.T) {
	p := newPolicies(t)
	cases := map[string]Patch{
		"max_budget must be a non-negative finite number. Received: -1": {MaxBudget: core.To(-1.0)},
		"soft_budget (5) must be strictly lower than max_budget (5)":    {MaxBudget: core.To(5.0), SoftBudget: core.To(5.0)},
		"Invalid budget_duration: 3 days":                               {BudgetDuration: core.To("3 days")},
	}
	for want, patch := range cases {
		_, err := p.Apply(t.Context(), ScopeTeam, "t1", patch)
		var invalid *InvalidError
		if !errors.As(err, &invalid) || invalid.Message != want {
			t.Errorf("err = %v, 기대 %q", err, want)
		}
	}
	if found, _ := p.Find(t.Context(), ScopeTeam, []string{"t1"}); len(found) != 0 {
		t.Errorf("틀린 값이 저장됐다: %+v", found)
	}
}

func TestApply_WithoutChangesStoresNothing(t *testing.T) {
	p := newPolicies(t)
	if _, err := p.Apply(t.Context(), ScopeTeam, "t1", Patch{}); err != nil {
		t.Fatal(err)
	}
	if found, _ := p.Find(t.Context(), ScopeTeam, []string{"t1"}); len(found) != 0 {
		t.Errorf("한도를 정하지 않았는데 행이 생겼다")
	}
}

func mustFind(t *testing.T, p *Policies, scope Scope, id string) Policy {
	t.Helper()
	found, err := p.Find(t.Context(), scope, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	pol, ok := found[id]
	if !ok {
		t.Fatalf("%s/%s 한도가 없다", scope, id)
	}
	return pol
}
