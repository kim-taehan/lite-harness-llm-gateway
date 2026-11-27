package limits

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"llm-gateway/internal/core"
)

// Tag 는 등록한 태그 하나다. 예산·한도는 Policies 의 ScopeTag 에 있다.
// Models 는 litellm 처럼 태그에 허용한 모델 목록으로 저장만 하고, 태그 기반 라우팅과 접근 판정에는 아직 쓰지 않는다
type Tag struct {
	Name        string
	Description *string
	Models      []string
	CreatedBy   *string
	UpdatedBy   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

var (
	ErrTagExists   = errors.New("이미 있는 태그다")
	ErrTagNotFound = errors.New("태그가 없다")
)

// Tags 는 태그 저장소다
type Tags struct{ db *core.DB }

func NewTags(db *core.DB) *Tags { return &Tags{db: db} }

const tagColumns = "name, description, models, created_by, updated_by, created_at, updated_at"

func scanTag(row pgx.Row) (Tag, error) {
	var t Tag
	err := row.Scan(&t.Name, &t.Description, &t.Models, &t.CreatedBy, &t.UpdatedBy, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// Find 는 이름으로 찾은 태그다. 없는 이름은 결과에 없다
func (s *Tags) Find(ctx context.Context, names []string) (map[string]Tag, error) {
	rows, err := s.db.Q(ctx).Query(ctx, "SELECT "+tagColumns+" FROM tag WHERE name = ANY($1)", names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]Tag{}
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		result[t.Name] = t
	}
	return result, rows.Err()
}

// List 는 모든 태그다(이름순)
func (s *Tags) List(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.Q(ctx).Query(ctx, "SELECT "+tagColumns+" FROM tag ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Tag
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// Create 는 태그를 만든다. 이미 있으면 ErrTagExists
func (s *Tags) Create(ctx context.Context, name string, description *string, models []string, by string) (Tag, error) {
	if models == nil {
		models = []string{}
	}
	t, err := scanTag(s.db.Q(ctx).QueryRow(ctx,
		"INSERT INTO tag (name, description, models, created_by, updated_by) VALUES ($1, $2, $3, $4, $4) RETURNING "+tagColumns,
		name, description, models, nullable(by)))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Tag{}, ErrTagExists
	}
	return t, err
}

// Update 는 설명과 허용 모델을 바꾼다. 보내지 않은 필드는 그대로 둔다. 없으면 ErrTagNotFound
func (s *Tags) Update(ctx context.Context, name string, description core.Change[string], models core.Change[[]string], by string) (Tag, error) {
	found, err := s.Find(ctx, []string{name})
	if err != nil {
		return Tag{}, err
	}
	current, ok := found[name]
	if !ok {
		return Tag{}, ErrTagNotFound
	}
	return scanTag(s.db.Q(ctx).QueryRow(ctx,
		"UPDATE tag SET description = $2, models = $3, updated_by = $4, updated_at = now() WHERE name = $1 RETURNING "+tagColumns,
		name, description.Apply(current.Description), models.Or(current.Models), nullable(by)))
}

// Delete 는 태그와 그 한도를 지운다. 없으면 ErrTagNotFound
func (s *Tags) Delete(ctx context.Context, name string) error {
	tag, err := s.db.Q(ctx).Exec(ctx, "DELETE FROM tag WHERE name = $1", name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTagNotFound
	}
	for _, table := range []string{"limit_policy", "limit_model_policy"} {
		if _, err := s.db.Q(ctx).Exec(ctx, "DELETE FROM "+table+" WHERE scope = 'tag' AND target_id = $1", name); err != nil {
			return err
		}
	}
	return nil
}
