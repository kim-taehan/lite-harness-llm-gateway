package core_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

func TestMigrate_CreatesSchemaAndIsIdempotent(t *testing.T) {
	_, pool := dbtest.Open(t) // Open 이 한 번 적용했다
	ctx := t.Context()

	if err := core.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("두 번째 적용이 실패했다: %v", err)
	}
	for _, table := range []string{"team", "app_user", "team_member", "limit_policy"} {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil || !exists {
			t.Errorf("테이블 %s 가 없다 (%v)", table, err)
		}
	}
	var applied int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migration").Scan(&applied); err != nil || applied != 1 {
		t.Errorf("적용 기록 = %d (%v), 1 이어야 한다", applied, err)
	}
}

func TestMigrate_RefusesChangedAppliedFile(t *testing.T) {
	_, pool := dbtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, "UPDATE schema_migration SET checksum = 'tampered' WHERE version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := core.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("적용 뒤 바뀐 마이그레이션을 받아들였다")
	}
}

func TestInTx_RollsBackOnError(t *testing.T) {
	db, pool := dbtest.Open(t)
	ctx := t.Context()

	err := db.InTx(ctx, func(ctx context.Context) error {
		if _, err := db.Q(ctx).Exec(ctx, "INSERT INTO team (team_id) VALUES ('t1')"); err != nil {
			return err
		}
		return core.ErrRollback
	})
	if err != core.ErrRollback {
		t.Fatalf("err = %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM team").Scan(&n)
	if n != 0 {
		t.Errorf("되돌려지지 않았다: team 행 %d 개", n)
	}
}
