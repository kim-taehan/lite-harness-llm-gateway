package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// Project 는 팀 안에서 키를 용도별로 묶는 단위다(litellm 프로젝트). 예산·한도는 limits 모듈의 project 종류에 있다
type Project struct {
	ProjectID    string
	ProjectAlias *string
	Description  *string
	TeamID       string
	Models       []string
	Metadata     map[string]any
	Blocked      bool
	CreatedBy    *string
	UpdatedBy    *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type NewProject struct {
	ProjectAlias *string
	Description  *string
	TeamID       string
	Models       []string
	Metadata     map[string]any
	Blocked      bool
	CreatedBy    string
}

// ProjectPatch 는 바꿀 필드다. 보내지 않은 필드는 그대로 둔다
type ProjectPatch struct {
	ProjectAlias core.Change[string]
	Description  core.Change[string]
	TeamID       *string
	Models       core.Change[[]string]
	Metadata     core.Change[map[string]any]
	Blocked      core.Change[bool]
	UpdatedBy    string
}

var (
	ErrProjectNotFound = errors.New("프로젝트가 없다")
	// ErrProjectHasKeys 는 키가 남아 있는 프로젝트를 지우거나 다른 팀으로 옮기려 할 때다
	ErrProjectHasKeys = errors.New("프로젝트에 키가 남아 있다")
)

// Projects 는 프로젝트 저장소다. 막거나 모델을 바꾸면 그 프로젝트 키의 검증 캐시를 지운다
type Projects struct {
	db    *core.DB
	cache *KeyCache
}

func NewProjects(db *core.DB, cache *KeyCache) *Projects { return &Projects{db: db, cache: cache} }

const projectColumns = "project_id, project_alias, description, team_id, models, metadata, blocked, created_by, updated_by, created_at, updated_at"

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ProjectID, &p.ProjectAlias, &p.Description, &p.TeamID, &p.Models, &p.Metadata, &p.Blocked,
		&p.CreatedBy, &p.UpdatedBy, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// Create 는 프로젝트를 만든다. 팀이 없으면 ErrTeamNotFound
func (s *Projects) Create(ctx context.Context, n NewProject) (Project, error) {
	models := n.Models
	if models == nil {
		models = []string{}
	}
	p, err := scanProject(s.db.Q(ctx).QueryRow(ctx, `
		INSERT INTO project (project_id, project_alias, description, team_id, models, metadata, blocked, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8) RETURNING `+projectColumns,
		core.NewUUID(), n.ProjectAlias, n.Description, n.TeamID, models, orEmptyMap(n.Metadata), n.Blocked, nullableString(n.CreatedBy)))
	if isForeignKeyViolation(err) {
		return Project{}, ErrTeamNotFound
	}
	return p, err
}

// Find 는 프로젝트 하나다. 없으면 ErrProjectNotFound
func (s *Projects) Find(ctx context.Context, id string) (Project, error) {
	p, err := scanProject(s.db.Q(ctx).QueryRow(ctx, "SELECT "+projectColumns+" FROM project WHERE project_id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrProjectNotFound
	}
	return p, err
}

// List 는 프로젝트들이다. teamIDs 가 nil 이면 전부, 아니면 그 팀들의 것만이다(만든 순서)
func (s *Projects) List(ctx context.Context, teamIDs []string) ([]Project, error) {
	query, args := "SELECT "+projectColumns+" FROM project ORDER BY created_at, project_id", []any{}
	if teamIDs != nil {
		query, args = "SELECT "+projectColumns+" FROM project WHERE team_id = ANY($1) ORDER BY created_at, project_id", []any{teamIDs}
	}
	rows, err := s.db.Q(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// IDsOfTeams 는 팀들의 프로젝트 id 다(팀을 지울 때 프로젝트의 한도 행을 함께 치우려고)
func (s *Projects) IDsOfTeams(ctx context.Context, teamIDs []string) ([]string, error) {
	rows, err := s.db.Q(ctx).Query(ctx, "SELECT project_id FROM project WHERE team_id = ANY($1)", teamIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Update 는 보낸 필드만 바꾼다. 실패: ErrProjectNotFound, ErrTeamNotFound(옮길 팀이 없다), ErrProjectHasKeys(키가 있는 프로젝트의 팀을 바꿈)
func (s *Projects) Update(ctx context.Context, id string, patch ProjectPatch) (Project, error) {
	var updated Project
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		current, err := s.Find(ctx, id)
		if err != nil {
			return err
		}
		if patch.TeamID != nil && *patch.TeamID != current.TeamID {
			// 키의 팀이 프로젝트의 팀과 달라지지 않게, 키가 있으면 팀을 옮기지 못한다
			var keys int
			if err := s.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM api_key WHERE project_id = $1", id).Scan(&keys); err != nil {
				return err
			}
			if keys > 0 {
				return ErrProjectHasKeys
			}
		}
		set := core.NewUpdateSet(id)
		set.Set("updated_by", nullableString(patch.UpdatedBy))
		if patch.ProjectAlias.Set {
			set.Set("project_alias", patch.ProjectAlias.Value)
		}
		if patch.Description.Set {
			set.Set("description", patch.Description.Value)
		}
		if patch.TeamID != nil {
			set.Set("team_id", *patch.TeamID)
		}
		if patch.Models.Set {
			set.Set("models", patch.Models.Or([]string{}))
		}
		if patch.Metadata.Set {
			set.Set("metadata", orEmptyMap(patch.Metadata.Or(nil)))
		}
		if patch.Blocked.Set && patch.Blocked.Value != nil {
			set.Set("blocked", *patch.Blocked.Value)
		}
		updated, err = scanProject(s.db.Q(ctx).QueryRow(ctx,
			"UPDATE project SET "+set.Assignments()+" WHERE project_id = $1 RETURNING "+projectColumns, set.Args...))
		if isForeignKeyViolation(err) {
			return ErrTeamNotFound
		}
		return err
	})
	if err == nil && (patch.Blocked.Set || patch.Models.Set) {
		s.cache.forgetProject(id)
	}
	return updated, err
}

// Delete 는 프로젝트를 지운다. 키가 남아 있으면 ErrProjectHasKeys 이고 아무것도 지우지 않는다. 없는 id 는 건너뛰고 지운 id 를 돌려준다
func (s *Projects) Delete(ctx context.Context, ids []string) ([]string, error) {
	var deleted []string
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		var keys int
		if err := s.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM api_key WHERE project_id = ANY($1)", ids).Scan(&keys); err != nil {
			return err
		}
		if keys > 0 {
			return ErrProjectHasKeys
		}
		rows, err := s.db.Q(ctx).Query(ctx, "DELETE FROM project WHERE project_id = ANY($1) RETURNING project_id", ids)
		if err != nil {
			return err
		}
		deleted, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return deleted, err
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
