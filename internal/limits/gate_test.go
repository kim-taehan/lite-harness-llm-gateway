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

func TestGate_PermitCarriesRatelimitHeadersLikeLitellm(t *testing.T) {
	gate, policies, _ := newGate(t)
	ctx := t.Context()
	_, _ = policies.Apply(ctx, ScopeKey, "k1", Patch{RPMLimit: core.To(int64(5)), TPMLimit: core.To(int64(1000)), MaxParallelRequests: core.To(int64(3))})
	_, _ = policies.Apply(ctx, ScopeTeam, "t1", Patch{RPMLimit: core.To(int64(10))})

	permit, err := gate.Admit(ctx, Subject{KeyToken: "k1", TeamID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	permit.AddTokens(ctx, 400)
	second, err := gate.Admit(ctx, Subject{KeyToken: "k1", TeamID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"x-ratelimit-api_key-limit-requests": "5", "x-ratelimit-api_key-remaining-requests": "3",
		"x-ratelimit-api_key-limit-tokens": "1000", "x-ratelimit-api_key-remaining-tokens": "600",
		"x-ratelimit-api_key-limit-max_parallel_requests": "3", "x-ratelimit-api_key-remaining-max_parallel_requests": "1",
		"x-ratelimit-team-limit-requests": "10", "x-ratelimit-team-remaining-requests": "8",
	}
	got := second.Headers()
	if len(got) != len(want) {
		t.Errorf("헤더 %d 개, want %d: %v", len(got), len(want), got)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
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

func TestGate_DailyTokenLimitResetsAtUTCMidnight(t *testing.T) {
	gate, policies, now := newGate(t) // 2026-11-21 09:00:30 UTC
	ctx := t.Context()
	_, _ = policies.Apply(ctx, ScopeKey, "k1", Patch{TPDLimit: core.To(int64(100))})
	s := Subject{KeyToken: "k1"}

	permit, err := gate.Admit(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	permit.AddTokens(ctx, 60)
	// 분이 바뀌어도(TPM 창) 하루 합은 이어진다. 아직 한도 아래라 통과하고, 이 요청이 한도를 넘긴다(litellm 과 같다)
	*now = now.Add(5 * time.Hour)
	permit, err = gate.Admit(ctx, s)
	if err != nil {
		t.Fatalf("한도 아래인데 막혔다: %v", err)
	}
	permit.AddTokens(ctx, 60)

	_, err = gate.Admit(ctx, s)
	r := rejection(t, err)
	// 14:00:30 → 다음 UTC 자정까지 9시간 59분 30초
	if r.Status != 429 || r.Headers["retry-after"] != "35970" ||
		r.Message != "Daily token limit reached for api_key. Used 120 of 100 tokens today (UTC). Resets at 00:00 UTC." {
		t.Errorf("%+v", r)
	}

	*now = time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	if _, err := gate.Admit(ctx, s); err != nil {
		t.Errorf("자정이 지났는데 막혔다: %v", err)
	}
}

func setModelSpend(t *testing.T, p *Policies, scope Scope, id, model string, spend float64) {
	t.Helper()
	if _, err := p.db.Q(t.Context()).Exec(t.Context(),
		"UPDATE limit_model_policy SET spend = $4 WHERE scope = $1 AND target_id = $2 AND model = $3", scope, id, model, spend); err != nil {
		t.Fatal(err)
	}
}

func TestGate_ModelLimitsApplyOnlyToThatModel(t *testing.T) {
	gate, policies, _ := newGate(t)
	ctx := t.Context()
	_, err := policies.Apply(ctx, ScopeKey, "k1", Patch{ModelBudgets: core.To(map[string]ModelBudget{
		"m1": {RPMLimit: ref(int64(1))}, "m2": {TPMLimit: ref(int64(10))}})})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := gate.Admit(ctx, Subject{KeyToken: "k1", Model: "m1"}); err != nil {
		t.Fatalf("m1 첫 요청: %v", err)
	}
	r := rejection(t, mustFail(gate.Admit(ctx, Subject{KeyToken: "k1", Model: "m1"})))
	if r.Status != 429 || !strings.HasSuffix(r.Message, " Model: m1") || !strings.Contains(r.Message, "Limit type: requests") {
		t.Errorf("m1 RPM 초과 = %+v", r)
	}
	for i := 0; i < 5; i++ {
		if _, err := gate.Admit(ctx, Subject{KeyToken: "k1", Model: "other"}); err != nil {
			t.Fatalf("한도 없는 모델이 막혔다: %v", err)
		}
	}

	permit, err := gate.Admit(ctx, Subject{KeyToken: "k1", Model: "m2"})
	if err != nil {
		t.Fatal(err)
	}
	permit.AddTokens(ctx, 10)
	r = rejection(t, mustFail(gate.Admit(ctx, Subject{KeyToken: "k1", Model: "m2"})))
	if r.Status != 429 || !strings.Contains(r.Message, "Limit type: tokens") || !strings.HasSuffix(r.Message, " Model: m2") {
		t.Errorf("m2 TPM 초과 = %+v", r)
	}
	// 모델을 안 알려 주면(Model 비움) 모델별 한도는 보지 않는다
	if _, err := gate.Admit(ctx, Subject{KeyToken: "k1"}); err != nil {
		t.Errorf("모델 없는 검사가 막혔다: %v", err)
	}
}

func mustFail(_ *Permit, err error) error { return err }

func ref[T any](v T) *T { return &v }

func TestGate_ModelBudgetBlocksAtMaxAndResetsAfterItsPeriod(t *testing.T) {
	gate, policies, now := newGate(t)
	ctx := t.Context()
	_, err := policies.Apply(ctx, ScopeTeam, "t1", Patch{ModelBudgets: core.To(map[string]ModelBudget{
		"m1": {MaxBudget: ref(1.0), BudgetDuration: ref("1d")}})})
	if err != nil {
		t.Fatal(err)
	}
	s := Subject{KeyToken: "k1", TeamID: "t1", Model: "m1"}
	if _, err := gate.Admit(ctx, s); err != nil {
		t.Fatal(err)
	}
	setModelSpend(t, policies, ScopeTeam, "t1", "m1", 1.0)
	r := rejection(t, mustFail(gate.Admit(ctx, s)))
	if r.Status != 422 || r.Message != "Budget has been exceeded! Team=t1 Model=m1 Current cost: 1, Max budget: 1" {
		t.Errorf("모델 예산 초과 = %+v", r)
	}
	if _, err := gate.Admit(ctx, Subject{KeyToken: "k1", TeamID: "t1", Model: "m2"}); err != nil {
		t.Errorf("다른 모델이 막혔다: %v", err)
	}

	if n, err := policies.ResetDueBudgets(ctx); err != nil || n != 0 {
		t.Fatalf("주기 전 리셋 = %d, %v", n, err)
	}
	*now = now.Add(25 * time.Hour)
	if n, err := policies.ResetDueBudgets(ctx); err != nil || n != 1 {
		t.Fatalf("주기 뒤 리셋 = %d, %v", n, err)
	}
	if _, err := gate.Admit(ctx, s); err != nil {
		t.Errorf("리셋 뒤에도 막혔다: %v", err)
	}
}

func TestPolicies_ModelBudgetsReplaceKeepSpendAndRejectBadValues(t *testing.T) {
	_, policies, _ := newGate(t)
	ctx := t.Context()
	apply := func(b map[string]ModelBudget) (Policy, error) {
		return policies.Apply(ctx, ScopeKey, "k1", Patch{ModelBudgets: core.To(b)})
	}
	if _, err := apply(map[string]ModelBudget{"m1": {MaxBudget: ref(5.0)}, "m2": {MaxBudget: ref(5.0)}}); err != nil {
		t.Fatal(err)
	}
	setModelSpend(t, policies, ScopeKey, "k1", "m1", 3)

	if _, err := apply(map[string]ModelBudget{"m1": {MaxBudget: ref(9.0)}}); err != nil { // m2 는 사라지고 m1 은 사용액을 지킨다
		t.Fatal(err)
	}
	found, _ := policies.Find(ctx, ScopeKey, []string{"k1"})
	if got := found["k1"].ModelBudgets; len(got) != 1 || *got["m1"].MaxBudget != 9 {
		t.Errorf("바꾼 뒤 = %+v", got)
	}
	var spend float64
	_ = policies.db.Q(ctx).QueryRow(ctx, "SELECT spend FROM limit_model_policy WHERE target_id = 'k1' AND model = 'm1'").Scan(&spend)
	if spend != 3 {
		t.Errorf("모델 한도를 고쳤더니 사용액이 %v 로 바뀌었다(3 이어야 한다)", spend)
	}

	for name, bad := range map[string]map[string]ModelBudget{
		"음수 예산":   {"m1": {MaxBudget: ref(-1.0)}},
		"틀린 주기":   {"m1": {BudgetDuration: ref("soon")}},
		"빈 모델 이름": {" ": {MaxBudget: ref(1.0)}},
	} {
		var invalid *InvalidError
		if _, err := apply(bad); !errors.As(err, &invalid) {
			t.Errorf("%s: 거절되지 않았다(%v)", name, err)
		}
	}
	if _, err := policies.Apply(ctx, ScopeKey, "k1", Patch{ModelBudgets: core.Change[map[string]ModelBudget]{Set: true}}); err != nil {
		t.Fatal(err)
	}
	if found, _ = policies.Find(ctx, ScopeKey, []string{"k1"}); len(found["k1"].ModelBudgets) != 0 {
		t.Errorf("null 로 지웠는데 남았다: %+v", found["k1"].ModelBudgets)
	}
}

// downCounters 는 장애 중인 카운터 저장소를 흉내 낸다(Redis 의 FailClosed 상태)
type downCounters struct{ *MemoryCounters }

func (downCounters) Unavailable() bool { return true }

func TestGate_RejectsWith503OnlyRequestsThatNeedCountersWhenStoreIsDown(t *testing.T) {
	db, _ := dbtest.Open(t)
	now := time.Date(2026, 11, 21, 9, 0, 30, 0, time.UTC)
	clock := func() time.Time { return now }
	policies := NewPolicies(db, clock)
	gate := NewGate(policies, downCounters{NewMemoryCounters(clock)}, clock)
	ctx := t.Context()
	_, _ = policies.Apply(ctx, ScopeKey, "limited", Patch{RPMLimit: core.To(int64(5))})
	_, _ = policies.Apply(ctx, ScopeKey, "budgeted", Patch{MaxBudget: core.To(100.0)})

	_, err := gate.Admit(ctx, Subject{KeyToken: "limited"})
	r := rejection(t, err)
	if r.Status != 503 || r.Headers["retry-after"] != "5" {
		t.Errorf("RPM 한도가 걸린 키 = %+v, 503 이어야 한다", r)
	}
	if _, err := gate.Admit(ctx, Subject{KeyToken: "budgeted"}); err != nil {
		t.Errorf("예산만 있는 키(카운터가 필요 없음)가 막혔다: %v", err)
	}
	if _, err := gate.Admit(ctx, Subject{KeyToken: "nolimit"}); err != nil {
		t.Errorf("한도가 없는 키가 막혔다: %v", err)
	}
}

func TestGate_DailyTokenLimitIsPerScope(t *testing.T) {
	gate, policies, _ := newGate(t)
	ctx := t.Context()
	_, _ = policies.Apply(ctx, ScopeTeam, "t1", Patch{TPDLimit: core.To(int64(10))})
	permit, _ := gate.Admit(ctx, Subject{KeyToken: "k1", TeamID: "t1"})
	permit.AddTokens(ctx, 10)

	if _, err := gate.Admit(ctx, Subject{KeyToken: "k2", TeamID: "t1"}); err == nil {
		t.Error("같은 팀의 다른 키는 팀 한도를 함께 쓴다")
	}
	if _, err := gate.Admit(ctx, Subject{KeyToken: "k3", TeamID: "t2"}); err != nil {
		t.Errorf("다른 팀이 막혔다: %v", err)
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
	permit.AddTokens(ctx, 100)
	permit.Release()
	if r := rejection(t, func() error { _, err := gate.Admit(ctx, s); return err }()); !strings.Contains(r.Message, "Limit type: tokens") {
		t.Errorf("%+v", r)
	}
}
