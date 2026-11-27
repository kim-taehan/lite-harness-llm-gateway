package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// Organization 은 팀들을 묶는 상위 단위다(litellm 조직). 허용 모델은 팀이 가질 수 있는 모델의 상한이고, 예산·한도는 limits 의 organization 종류에 있다.
// 조직은 전체 관리자만 관리한다(조직 관리자 역할 없음)
type Organization struct {
	OrganizationID    string
	OrganizationAlias string
	Models            []string
	Metadata          map[string]any
	CreatedBy         *string
	UpdatedBy         *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type NewOrganization struct {
	OrganizationAlias string
	Models            []string
	Metadata          map[string]any
	CreatedBy         string
}

// OrganizationPatch 는 바꿀 필드다. 보내지 않은 필드는 그대로 둔다
type OrganizationPatch struct {
	OrganizationAlias *string
	Models            core.Change[[]string]
	Metadata          core.Change[map[string]any]
	UpdatedBy         string
}

// OrganizationMember 는 조직 구성원이다. UserRole 은 화면에 보이는 정보일 뿐 권한에 쓰지 않는다
type OrganizationMember struct {
	UserID    string
	UserEmail *string
	UserRole  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

var (
	ErrOrganizationNotFound = errors.New("조직이 없다")
	// ErrOrganizationHasTeams 는 팀이 남아 있는 조직을 지우려 할 때다
	ErrOrganizationHasTeams = errors.New("조직에 팀이 남아 있다")
	ErrAlreadyOrgMember     = errors.New("이미 조직 구성원이다")
	ErrNotOrgMember         = errors.New("조직 구성원이 아니다")
)

// Organizations 는 조직 저장소다. 조직을 바꾸면 그 조직 팀 키의 검증 캐시를 지운다(조직 한도가 키 검증과 함께 읽히므로)
type Organizations struct {
	db    *core.DB
	cache *KeyCache
}

func NewOrganizations(db *core.DB, cache *KeyCache) *Organizations {
	return &Organizations{db: db, cache: cache}
}

const organizationColumns = "organization_id, organization_alias, models, metadata, created_by, updated_by, created_at, updated_at"

func scanOrganization(row pgx.Row) (Organization, error) {
	var o Organization
	err := row.Scan(&o.OrganizationID, &o.OrganizationAlias, &o.Models, &o.Metadata, &o.CreatedBy, &o.UpdatedBy, &o.CreatedAt, &o.UpdatedAt)
	return o, err
}

func (s *Organizations) Create(ctx context.Context, n NewOrganization) (Organization, error) {
	models := n.Models
	if models == nil {
		models = []string{}
	}
	return scanOrganization(s.db.Q(ctx).QueryRow(ctx, `
		INSERT INTO organization (organization_id, organization_alias, models, metadata, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $5) RETURNING `+organizationColumns,
		core.NewUUID(), n.OrganizationAlias, models, orEmptyMap(n.Metadata), nullableString(n.CreatedBy)))
}

// Find 는 조직 하나다. 없으면 ErrOrganizationNotFound
func (s *Organizations) Find(ctx context.Context, id string) (Organization, error) {
	o, err := scanOrganization(s.db.Q(ctx).QueryRow(ctx, "SELECT "+organizationColumns+" FROM organization WHERE organization_id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrOrganizationNotFound
	}
	return o, err
}

// List 는 조직들이다(만든 순서). 별칭·id 로 거를 수 있다(없으면 전부)
func (s *Organizations) List(ctx context.Context, id, alias *string) ([]Organization, error) {
	lq := &core.ListQuery{}
	if id != nil {
		lq.Where("organization_id = ?", *id)
	}
	if alias != nil {
		lq.Where("organization_alias ILIKE ?", core.LikeContains(*alias))
	}
	rows, err := s.db.Q(ctx).Query(ctx, "SELECT "+organizationColumns+" FROM organization "+lq.Clause()+" ORDER BY created_at, organization_id", lq.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Organization
	for rows.Next() {
		o, err := scanOrganization(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

// Update 는 보낸 필드만 바꾼다. 없으면 ErrOrganizationNotFound
func (s *Organizations) Update(ctx context.Context, id string, patch OrganizationPatch) (Organization, error) {
	set := core.NewUpdateSet(id)
	set.Set("updated_by", nullableString(patch.UpdatedBy))
	if patch.OrganizationAlias != nil {
		set.Set("organization_alias", *patch.OrganizationAlias)
	}
	if patch.Models.Set {
		set.Set("models", patch.Models.Or([]string{}))
	}
	if patch.Metadata.Set {
		set.Set("metadata", orEmptyMap(patch.Metadata.Or(nil)))
	}
	o, err := scanOrganization(s.db.Q(ctx).QueryRow(ctx, "UPDATE organization SET "+set.Assignments()+" WHERE organization_id = $1 RETURNING "+organizationColumns, set.Args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrOrganizationNotFound
	}
	return o, err
}

// Delete 는 조직을 지운다. 팀이 남아 있으면 ErrOrganizationHasTeams 이고 아무것도 지우지 않는다. 지운 id 를 돌려준다
func (s *Organizations) Delete(ctx context.Context, ids []string) ([]string, error) {
	var deleted []string
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		var teams int
		if err := s.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM team WHERE organization_id = ANY($1)", ids).Scan(&teams); err != nil {
			return err
		}
		if teams > 0 {
			return ErrOrganizationHasTeams
		}
		rows, err := s.db.Q(ctx).Query(ctx, "DELETE FROM organization WHERE organization_id = ANY($1) RETURNING organization_id", ids)
		if err != nil {
			return err
		}
		deleted, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return deleted, err
}

// Teams 는 조직별 팀 id 다(조직 화면의 팀 목록)
func (s *Organizations) Teams(ctx context.Context, ids []string) (map[string][]string, error) {
	rows, err := s.db.Q(ctx).Query(ctx, "SELECT organization_id, team_id FROM team WHERE organization_id = ANY($1) ORDER BY created_at, team_id", ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]string{}
	for rows.Next() {
		var org, team string
		if err := rows.Scan(&org, &team); err != nil {
			return nil, err
		}
		result[org] = append(result[org], team)
	}
	return result, rows.Err()
}

// Members 는 조직별 구성원이다
func (s *Organizations) Members(ctx context.Context, ids []string) (map[string][]OrganizationMember, error) {
	rows, err := s.db.Q(ctx).Query(ctx, `
		SELECT m.organization_id, m.user_id, u.user_email, m.user_role, m.created_at, m.updated_at
		FROM organization_member m JOIN app_user u USING (user_id)
		WHERE m.organization_id = ANY($1) ORDER BY m.created_at, m.user_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]OrganizationMember{}
	for rows.Next() {
		var org string
		var m OrganizationMember
		if err := rows.Scan(&org, &m.UserID, &m.UserEmail, &m.UserRole, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		result[org] = append(result[org], m)
	}
	return result, rows.Err()
}

// AddMember 는 구성원을 더한다. 조직이나 사용자가 없으면 ErrOrganizationNotFound·ErrUserNotFound, 이미 있으면 ErrAlreadyOrgMember
func (s *Organizations) AddMember(ctx context.Context, orgID, userID, role string) error {
	if _, err := s.Find(ctx, orgID); err != nil {
		return err
	}
	var exists bool
	if err := s.db.Q(ctx).QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM app_user WHERE user_id = $1)", userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}
	tag, err := s.db.Q(ctx).Exec(ctx, `
		INSERT INTO organization_member (organization_id, user_id, user_role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, orgID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyOrgMember
	}
	return nil
}

// UpdateMember 는 구성원의 역할 문자열을 바꾼다. 구성원이 아니면 ErrNotOrgMember
func (s *Organizations) UpdateMember(ctx context.Context, orgID, userID, role string) error {
	tag, err := s.db.Q(ctx).Exec(ctx, "UPDATE organization_member SET user_role = $3, updated_at = now() WHERE organization_id = $1 AND user_id = $2", orgID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotOrgMember
	}
	return nil
}

// RemoveMember 는 구성원을 뺀다. 구성원이 아니면 ErrNotOrgMember
func (s *Organizations) RemoveMember(ctx context.Context, orgID, userID string) error {
	tag, err := s.db.Q(ctx).Exec(ctx, "DELETE FROM organization_member WHERE organization_id = $1 AND user_id = $2", orgID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotOrgMember
	}
	return nil
}
