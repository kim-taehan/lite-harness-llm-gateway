package app

import (
	"testing"
	"time"

	"llm-gateway/internal/admin"
	"llm-gateway/internal/core/dbtest"
)

// 감사 기록 보관 기간: 지난 기록만 나눠서 지우고, 지운 사실을 한 줄 남긴다

func TestAuditRetention_PurgesOnlyOlderRecordsInBatchesAndLeavesATrace(t *testing.T) {
	db, pool := dbtest.Open(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	retention := admin.NewAuditRetention(db, func() time.Time { return now }, []byte("test-chain-key"), 365*24*time.Hour)

	insert := func(id string, at time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, updated_at, changed_by, action, table_name, object_id)
			VALUES ($1, $2, 'admin', 'updated', 'LiteLLM_TeamTable', 't')`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	insert("edge", now.Add(-364*24*time.Hour))
	insert("new", now)
	// 한 번에 지우는 양(5000)을 넘는 오래된 기록: 여러 번에 나눠 모두 지워야 한다
	if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, updated_at, changed_by, action, table_name, object_id)
		SELECT 'old-' || n, $1, 'admin', 'updated', 'LiteLLM_TeamTable', 't' FROM generate_series(1, 12000) AS n`, now.Add(-400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	deleted, err := retention.Purge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 12000 {
		t.Errorf("지운 기록 = %d, 12000 이어야 한다", deleted)
	}
	var old, kept int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE id LIKE 'old-%'").Scan(&old)
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE id IN ('edge', 'new')").Scan(&kept)
	if old != 0 || kept != 2 {
		t.Errorf("오래된 기록 %d 개 남음(0), 기간 안 기록 %d 개 남음(2)", old, kept)
	}

	// 지운 사실이 한 줄 남는다: 행위자 system, 줄 수와 기준 시각
	var action, by, table string
	var values map[string]any
	if err := pool.QueryRow(ctx, "SELECT action, changed_by, table_name, updated_values FROM audit_log WHERE action = 'retention_purged'").Scan(&action, &by, &table, &values); err != nil {
		t.Fatalf("정리 기록이 없다: %v", err)
	}
	if by != "system" || table != "audit_log" || values["deleted"] != 12000.0 || values["older_than"] == nil {
		t.Errorf("정리 기록 = %s %s %v", by, table, values)
	}

	// 지울 것이 없으면 기록도 더하지 않는다(하루마다 도는 job 이 기록을 불리지 않게)
	if deleted, err := retention.Purge(ctx); err != nil || deleted != 0 {
		t.Fatalf("두 번째 정리 = %d, %v", deleted, err)
	}
	var traces int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE action = 'retention_purged'").Scan(&traces)
	if traces != 1 {
		t.Errorf("정리 기록 %d 줄, 1 줄이어야 한다", traces)
	}
}

func TestLoadConfig_AuditRetention(t *testing.T) {
	cases := []struct {
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{"", 365 * 24 * time.Hour, false},
		{"30", 30 * 24 * time.Hour, false},
		{" 90 ", 90 * 24 * time.Hour, false},
		{"0", 0, false}, // 영구 보관
		{"-1", 0, true},
		{"abc", 0, true},
		{"1.5", 0, true},
	}
	for _, c := range cases {
		cfg, err := LoadConfig(envWith(map[string]string{"GATEWAY_AUDIT_RETENTION_DAYS": c.raw}))
		if (err != nil) != c.wantErr || (err == nil && cfg.AuditRetention != c.want) {
			t.Errorf("%q: %v, %v (기대 %v, 오류 %v)", c.raw, cfg.AuditRetention, err, c.want, c.wantErr)
		}
	}
}
