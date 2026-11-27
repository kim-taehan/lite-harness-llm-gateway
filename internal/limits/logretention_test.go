package limits

import (
	"testing"
	"time"

	"llm-gateway/internal/core/dbtest"
)

// 보관 기간보다 오래된 기록만 지우고, 사용액(limit_policy.spend)은 건드리지 않는다
func TestPurgeRequestLog_DeletesOnlyOlderThanRetention(t *testing.T) {
	db, pool := dbtest.Open(t)
	ctx := t.Context()
	retention := NewLogRetention(db, func() time.Time { return fixedNow }, 90*24*time.Hour)

	insert := func(id string, at time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO request_log (request_id, created_at, model, usage_source, status, http_status, latency_ms)
			VALUES ($1, $2, 'm', 'provider', 'success', 200, 10)`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	insert("old", fixedNow.Add(-91*24*time.Hour))
	insert("edge", fixedNow.Add(-89*24*time.Hour))
	insert("new", fixedNow)
	if _, err := pool.Exec(ctx, "INSERT INTO limit_policy (scope, target_id, spend) VALUES ('key', 'k1', 5)"); err != nil {
		t.Fatal(err)
	}

	deleted, err := retention.Purge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("지운 기록 = %d, 1 이어야 한다", deleted)
	}
	var left int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM request_log WHERE request_id IN ('edge', 'new')").Scan(&left); err != nil || left != 2 {
		t.Errorf("남아야 할 기록 = %d (%v), 2 여야 한다", left, err)
	}
	var spend float64
	if err := pool.QueryRow(ctx, "SELECT spend FROM limit_policy WHERE target_id = 'k1'").Scan(&spend); err != nil || spend != 5 {
		t.Errorf("사용액 = %v (%v), 5 그대로여야 한다", spend, err)
	}
}
