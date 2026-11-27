package core

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Version 은 build.sh 가 -ldflags 로 넣는다. 대시보드가 보는 응답(litellm_version)과 기동 로그에 쓴다
var Version = "dev"

// Settings 는 대시보드에서 바꾸는 설정 저장소다. 이름마다 바꾼 값만 JSON 객체로 둔다.
// 여러 모듈이 읽으므로(관리 API 가 쓰고, 로그인이 새 사용자 기본값을 읽는다) core 에 둔다
type Settings struct {
	db *DB
}

func NewSettings(db *DB) *Settings { return &Settings{db: db} }

// Get 은 저장된 값이다. 한 번도 바꾸지 않았으면 빈 맵이다
func (s *Settings) Get(ctx context.Context, name string) (map[string]any, error) {
	var value map[string]any
	err := s.db.Q(ctx).QueryRow(ctx, "SELECT value FROM setting WHERE name = $1", name).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{}, nil
	}
	return value, err
}

// Update 는 값을 읽고 change 로 고쳐 저장하는 일을 한 트랜잭션으로 한다. 행을 잠가서 두 관리자가 동시에 목록을 고쳐도
// 한쪽 변경이 사라지지 않는다. change 가 오류를 돌려주면 아무것도 저장하지 않는다
func (s *Settings) Update(ctx context.Context, name string, change func(current map[string]any) (map[string]any, error)) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		if _, err := s.db.Q(ctx).Exec(ctx, "INSERT INTO setting (name, value) VALUES ($1, '{}') ON CONFLICT (name) DO NOTHING", name); err != nil {
			return err
		}
		var current map[string]any
		if err := s.db.Q(ctx).QueryRow(ctx, "SELECT value FROM setting WHERE name = $1 FOR UPDATE", name).Scan(&current); err != nil {
			return err
		}
		updated, err := change(current)
		if err != nil {
			return err
		}
		_, err = s.db.Q(ctx).Exec(ctx, "UPDATE setting SET value = $2, updated_at = now() WHERE name = $1", name, updated)
		return err
	})
}

// Merge 는 주어진 키만 덮어쓰고 합친 결과를 돌려준다
func (s *Settings) Merge(ctx context.Context, name string, values map[string]any) (map[string]any, error) {
	var merged map[string]any
	err := s.db.Q(ctx).QueryRow(ctx, `
		INSERT INTO setting (name, value) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET value = setting.value || excluded.value, updated_at = now()
		RETURNING value`, name, values).Scan(&merged)
	return merged, err
}
