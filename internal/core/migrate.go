package core

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 스키마 마이그레이션(litellm 의 prisma migrate 와 같은 역할). 라이브러리 없이 직접 한다:
// 파일은 실행 파일에 넣고(embed), advisory lock 으로 여러 대가 동시에 기동해도 한 대만 적용하며,
// 적용한 파일의 해시를 남겨 적용 뒤에 고친 파일을 찾아낸다(Flyway 의 validate 와 같다).

//go:embed migrations/*.sql
var migrationFiles embed.FS

// 다른 애플리케이션과 겹치지 않을 상수. 값 자체에 뜻은 없다
const migrationLockID = 7_320_431_001

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

// Migrate 는 아직 적용하지 않은 마이그레이션을 번호 순으로 하나씩 각자의 트랜잭션에서 적용한다
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	migrations, err := loadMigrations(migrationFiles)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("마이그레이션 연결: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("마이그레이션 잠금: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migration (
		version    integer PRIMARY KEY,
		name       text        NOT NULL,
		checksum   text        NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("마이그레이션 기록 테이블: %w", err)
	}
	applied, err := appliedChecksums(ctx, conn.Conn())
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if checksum, ok := applied[m.version]; ok {
			if checksum != m.checksum {
				return fmt.Errorf("이미 적용한 마이그레이션 %s 가 바뀌었다. 적용한 파일은 고치지 않고 새 번호 파일을 더한다", m.name)
			}
			continue
		}
		if err := applyOne(ctx, conn, m); err != nil {
			return err
		}
		logger.Info("마이그레이션 적용", "file", m.name)
	}
	return nil
}

func applyOne(ctx context.Context, conn *pgxpool.Conn, m migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, m.sql); err != nil {
		return fmt.Errorf("마이그레이션 %s: %w", m.name, err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migration (version, name, checksum) VALUES ($1, $2, $3)", m.version, m.name, m.checksum); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func appliedChecksums(ctx context.Context, q Querier) (map[int]string, error) {
	rows, err := q.Query(ctx, "SELECT version, checksum FROM schema_migration")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int]string{}
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, err
		}
		result[version] = checksum
	}
	return result, rows.Err()
}

func loadMigrations(files fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return nil, err
	}
	var result []migration
	seen := map[int]string{}
	for _, entry := range entries {
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("마이그레이션 파일 이름이 규칙(0001_name.sql)과 다르다: %s", entry.Name())
		}
		version, _ := strconv.Atoi(match[1])
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("마이그레이션 번호가 겹친다: %s, %s", other, entry.Name())
		}
		seen[version] = entry.Name()
		content, err := fs.ReadFile(files, "migrations/"+entry.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(content)
		result = append(result, migration{version: version, name: entry.Name(), sql: string(content), checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].version < result[j].version })
	return result, nil
}
