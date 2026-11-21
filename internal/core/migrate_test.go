package core_test

import (
	"context"
	"errors"
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
	var before int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migration").Scan(&before); err != nil || before == 0 {
		t.Fatalf("첫 적용 기록 = %d (%v)", before, err)
	}

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
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migration").Scan(&applied); err != nil || applied != before {
		t.Errorf("두 번째 적용 뒤 기록 = %d (%v), 그대로 %d 여야 한다", applied, err, before)
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
	failure := errors.New("검증 실패")

	err := db.InTx(ctx, func(ctx context.Context) error {
		if _, err := db.Q(ctx).Exec(ctx, "INSERT INTO team (team_id) VALUES ('t1')"); err != nil {
			return err
		}
		return failure
	})
	if err != failure {
		t.Fatalf("err = %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM team").Scan(&n)
	if n != 0 {
		t.Errorf("되돌려지지 않았다: team 행 %d 개", n)
	}
}
