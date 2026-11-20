package limits

import (
	"errors"
	"strings"
	"testing"
	"time"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

func newGate(t *testing.T) (*Gate, *Policies, *time.Time) {
	db, _ := dbtest.Open(t)
	now := time.Date(2026, 11, 21, 9, 0, 30, 0, time.UTC)
	clock := func() time.Time { return now }
	policies := NewPolicies(db, clock)
	return NewGate(policies, NewMemoryCounters(clock), clock), policies, &now
}

func rejection(t *testing.T, err error) core.ProxyError {
	t.Helper()
	var rejected core.ProxyError
	if !errors.As(err, &rejected) {
		t.Fatalf("거절이 아니다: %v", err)
	}
	return rejected
}

func TestGate_RPMIsFixedWindowLikeLitellm(t *testing.T) {
	gate, policies, now := newGate(t)
	ctx := t.Context()
	_, _ = policies.Apply(ctx, ScopeKey, "k1", Patch{RPMLimit: core.To(int64(2))})
	s := Subject{KeyToken: "k1"}

	for i := 0; i < 2; i++ {
		if _, err := gate.Admit(ctx, s); err != nil {
			t.Fatalf("%d 번째: %v", i+1, err)
		}
	}
	_, err := gate.Admit(ctx, s)
	r := rejection(t, err)
	if r.Status != 429 || r.Headers["retry-after"] != "60" ||
		r.Message != "Rate limit exceeded for api_key: k1. Limit type: requests. Current limit: 2, Remaining: 0. Limit resets at: 2026-11-21 09:01:00 UTC" {
		t.Errorf("%+v", r)
	}
	*now = now.Add(time.Minute) // 다음 윈도우
	if _, err := gate.Admit(ctx, s); err != nil {
		t.Errorf("새 윈도우인데 막혔다: %v", err)
	}
}

func TestGate_MaxParallelReleasesSlot(t *testing.T) {
	gate, policies, _ := newGate(t)
	ctx := t.Context()
	_, _ = policies.Apply(ctx, ScopeTeam, "t1", Patch{MaxParallelRequests: core.To(int64(1))})
	s := Subject{TeamID: "t1"}

	first, err := gate.Admit(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Admit(ctx, s); rejection(t, err).Status != 429 {
		t.Fatal("동시 두 번째가 통과했다")
	}
	first.Release()
	if _, err := gate.Admit(ctx, s); err != nil {
		t.Errorf("반납 뒤에도 막혔다: %v", err)
	}
}

func TestGate_BudgetExceededIs422(t *testing.T) {
	gate, policies, _ := newGate(t)
	ctx := t.Context()
	_, _ = policies.Apply(ctx, ScopeUser, "u1", Patch{MaxBudget: core.To(1.0)})
	_, _ = policies.SetSpend(ctx, ScopeUser, "u1", 1.5)

	r := rejection(t, func() error { _, err := gate.Admit(ctx, Subject{UserID: "u1"}); return err }())
	if r.Status != 422 || r.Type != "budget_exceeded" || !strings.HasPrefix(r.Message, "Budget has been exceeded! User=u1 Current cost: 1.5, Max budget: 1") {
		t.Errorf("%+v", r)
	}
}

func TestGate_TPMCountsTokensAfterResponse(t *testing.T) {
	gate, policies, _ := newGate(t)
	ctx := t.Context()
	_, _ = policies.Apply(ctx, ScopeKey, "k1", Patch{TPMLimit: core.To(int64(100))})
	s := Subject{KeyToken: "k1"}

	permit, err := gate.Admit(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	permit.AddTokens(100)
	permit.Release()
	if r := rejection(t, func() error { _, err := gate.Admit(ctx, s); return err }()); !strings.Contains(r.Message, "Limit type: tokens") {
		t.Errorf("%+v", r)
	}
}
