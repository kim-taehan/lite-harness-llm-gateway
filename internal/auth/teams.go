package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// Teams 는 팀과 팀원 관리다. admin 모듈의 관리 API 가 부른다
type Teams struct {
	db    *core.DB
	users *Users
	cache *KeyCache
}

func NewTeams(db *core.DB, users *Users, cache *KeyCache) *Teams {
	return &Teams{db: db, users: users, cache: cache}
}

const teamColumns = "team_id, team_alias, models, metadata, blocked, member_permissions, created_at, updated_at"

func scanTeam(row pgx.Row) (Team, error) {
	var t Team
	err := row.Scan(&t.TeamID, &t.TeamAlias, &t.Models, &t.Metadata, &t.Blocked, &t.MemberPermissions, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// Create 는 팀을 만든다. litellm 처럼 호출자(creatorUserID)를 팀 admin 으로 함께 넣는다(proxy_admin 도 마찬가지).
// 실패: ErrTeamIDReserved, ErrTeamIDTaken, *EmailOwnerMismatchError
func (t *Teams) Create(ctx context.Context, input NewTeam, creatorUserID string) (Team, error) {
	teamID := core.NewUUID()
	if input.TeamID != nil && *input.TeamID != "" {
		teamID = *input.TeamID
	}
	if teamID == dashboardTeamID {
		return Team{}, ErrTeamIDReserved
	}
	var created Team
	err := t.db.InTx(ctx, func(ctx context.Context) error {
		models := input.Models
		if models == nil {
			models = []string{}
		}
		tag, err := t.db.Q(ctx).Exec(ctx, `
			INSERT INTO team (team_id, team_alias, models, metadata, blocked) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (team_id) DO NOTHING`, teamID, input.TeamAlias, models, input.Metadata, input.Blocked)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTeamIDTaken
		}
		for _, ref := range withCreator(input.Members, creatorUserID) {
			user, err := t.users.resolveMember(ctx, ref)
			if err != nil {
				return err
			}
			if err := t.addMember(ctx, teamID, user.UserID, ref.Role); err != nil {
				return err
			}
		}
		created, err = t.Find(ctx, teamID)
		return err
	})
	return created, err
}

func withCreator(roster []MemberRef, creatorUserID string) []MemberRef {
	if creatorUserID == "" {
		return roster
	}
	for _, ref := range roster {
		if ref.UserID != nil && *ref.UserID == creatorUserID {
			return roster
		}
	}
	return append(roster, MemberRef{UserID: &creatorUserID, Role: TeamRoleAdmin})
}

// Find 는 팀 하나다. 없으면 ErrTeamNotFound
func (t *Teams) Find(ctx context.Context, teamID string) (Team, error) {
	team, err := scanTeam(t.db.Q(ctx).QueryRow(ctx, "SELECT "+teamColumns+" FROM team WHERE team_id = $1", teamID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, ErrTeamNotFound
	}
	return team, err
}

func (t *Teams) FindAll(ctx context.Context, teamIDs []string) ([]Team, error) {
	return t.list(ctx, "SELECT "+teamColumns+" FROM team WHERE team_id = ANY($1)", teamIDs)
}

// Members 는 팀별 팀원이다. 이메일·별칭은 사용자 행에서 읽는다(litellm 처럼 추가 시점 사본을 두지 않는다)
func (t *Teams) Members(ctx context.Context, teamIDs []string) (map[string][]TeamMember, error) {
	rows, err := t.db.Q(ctx).Query(ctx, `
		SELECT m.team_id, m.user_id, u.user_email, u.user_alias, m.role
		FROM team_member m JOIN app_user u ON u.user_id = m.user_id
		WHERE m.team_id = ANY($1) ORDER BY m.created_at, m.user_id`, teamIDs)
	if err != nil {
		return nil, err
	}
	members, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (TeamMember, error) {
		var m TeamMember
		err := row.Scan(&m.TeamID, &m.UserID, &m.UserEmail, &m.UserAlias, &m.Role)
		return m, err
	})
	if err != nil {
		return nil, err
	}
	result := map[string][]TeamMember{}
	for _, m := range members {
		result[m.TeamID] = append(result[m.TeamID], m)
	}
	return result, nil
}

// Membership 은 사용자 한 명의 한 팀 소속이다. 권한 판정에 필요한 것(역할, 팀의 멤버 권한 목록)을 한 번에 담는다
type Membership struct {
	TeamID string
	Role   TeamRole
	// MemberPermissions 는 그 팀 일반 멤버가 부를 수 있는 키 라우트다. nil 이면 litellm 기본을 쓴다
	MemberPermissions []string
}

// MembershipIn 은 사용자가 그 팀의 멤버인지와 소속 정보다. 팀 전체 명단을 읽지 않는다
func (t *Teams) MembershipIn(ctx context.Context, teamID, userID string) (Membership, bool, error) {
	m := Membership{TeamID: teamID}
	err := t.db.Q(ctx).QueryRow(ctx, `
		SELECT m.role, t.member_permissions FROM team_member m JOIN team t USING (team_id)
		WHERE m.team_id = $1 AND m.user_id = $2`, teamID, userID).Scan(&m.Role, &m.MemberPermissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, false, nil
	}
	return m, err == nil, err
}

// MembershipsOf 는 사용자가 속한 모든 팀의 소속 정보다(가입 순서). 여러 팀을 판정할 때 쿼리 하나로 끝낸다
func (t *Teams) MembershipsOf(ctx context.Context, userID string) ([]Membership, error) {
	rows, err := t.db.Q(ctx).Query(ctx, `
		SELECT m.team_id, m.role, t.member_permissions FROM team_member m JOIN team t USING (team_id)
		WHERE m.user_id = $1 ORDER BY m.created_at, m.team_id`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Membership, error) {
		var m Membership
		err := row.Scan(&m.TeamID, &m.Role, &m.MemberPermissions)
		return m, err
	})
}

// Update 는 보낸 필드만 바꾼다. 없으면 ErrTeamNotFound
func (t *Teams) Update(ctx context.Context, teamID string, patch TeamPatch) (Team, error) {
	set := core.NewUpdateSet(teamID)
	if patch.TeamAlias.Set {
		set.Set("team_alias", patch.TeamAlias.Value)
	}
	if patch.Models.Set {
		set.Set("models", patch.Models.Or([]string{}))
	}
	if patch.Metadata.Set {
		set.Set("metadata", patch.Metadata.Value)
	}
	if patch.Blocked.Set {
		set.Set("blocked", patch.Blocked.Or(false))
	}
	if patch.MemberPermissions.Set {
		set.Set("member_permissions", patch.MemberPermissions.Value)
	}
	sql := "UPDATE team SET " + set.Assignments() + " WHERE team_id = $1 RETURNING " + teamColumns
	team, err := scanTeam(t.db.Q(ctx).QueryRow(ctx, sql, set.Args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, ErrTeamNotFound
	}
	if err == nil && (patch.Blocked.Set || patch.Models.Set) {
		// 막힌 팀의 키는 바로 401 이어야 하고(litellm 도 차단 직후 캐시를 지운다), 팀 허용 모델도 키 검증에 쓰인다
		t.cache.forgetTeam(teamID)
	}
	return team, err
}

// Delete 는 팀을 지운다. litellm 처럼 팀 키는 사본(deleted_api_key)으로 옮긴 뒤 지우고, 팀원 행은 외래 키로 함께 지워진다.
// 하나라도 없으면 아무것도 지우지 않고 *NotFoundError 다
func (t *Teams) Delete(ctx context.Context, teamIDs []string, deletedBy string) error {
	err := t.db.InTx(ctx, func(ctx context.Context) error {
		found, err := t.FindAll(ctx, teamIDs)
		if err != nil {
			return err
		}
		if missing := firstMissing(teamIDs, found, func(team Team) string { return team.TeamID }); missing != "" {
			return &NotFoundError{ID: missing}
		}
		if _, err := archiveAndDelete(ctx, t.db.Q(ctx), "team_id = ANY($1)", deletedBy, teamIDs); err != nil {
			return err
		}
		_, err = t.db.Q(ctx).Exec(ctx, "DELETE FROM team WHERE team_id = ANY($1)", teamIDs)
		return err
	})
	if err == nil {
		t.cache.forgetTeam(teamIDs...)
	}
	return err
}

// AddMembers 는 litellm member_add 다. 없는 사용자는 만든다. 멤버 하나를 넣는데 이미 있으면 오류,
// 여럿을 넣는데 모두 이미 있으면 오류, 일부만 있으면 그것만 건너뛴다. 새로 넣은 사용자를 돌려준다.
// 실패: ErrTeamNotFound, *AlreadyMemberError, *EmailOwnerMismatchError
func (t *Teams) AddMembers(ctx context.Context, teamID string, refs []MemberRef, single bool) (Team, []User, error) {
	var team Team
	var added []User
	err := t.db.InTx(ctx, func(ctx context.Context) error {
		var err error
		if team, err = t.Find(ctx, teamID); err != nil {
			return err
		}
		members, err := t.Members(ctx, []string{teamID})
		if err != nil {
			return err
		}
		existing := members[teamID]
		var fresh []MemberRef
		for _, ref := range refs {
			if !containsMember(existing, ref) {
				fresh = append(fresh, ref)
			}
		}
		if len(fresh) == 0 {
			return &AlreadyMemberError{Single: single, Member: refs[0], Existing: existing}
		}
		for _, ref := range fresh {
			user, err := t.users.resolveMember(ctx, ref)
			if err != nil {
				return err
			}
			if err := t.addMember(ctx, teamID, user.UserID, ref.Role); err != nil {
				return err
			}
			added = append(added, user)
		}
		return nil
	})
	return team, added, err
}

// RemoveMember 는 팀원을 뺀다. litellm 처럼 그 사용자가 이 팀에 가진 키도 사본으로 옮긴 뒤 지운다.
// 실패: ErrTeamNotFound, ErrNotMember
func (t *Teams) RemoveMember(ctx context.Context, teamID string, userID, userEmail *string, deletedBy string) (Team, error) {
	var team Team
	var removedKeys []string
	err := t.db.InTx(ctx, func(ctx context.Context) error {
		var err error
		if team, err = t.Find(ctx, teamID); err != nil {
			return err
		}
		member, err := t.findMember(ctx, teamID, userID, userEmail)
		if err != nil {
			return err
		}
		if _, err := t.db.Q(ctx).Exec(ctx, "DELETE FROM team_member WHERE team_id = $1 AND user_id = $2", teamID, member.UserID); err != nil {
			return err
		}
		removedKeys, err = archiveAndDelete(ctx, t.db.Q(ctx), "team_id = $1 AND user_id = $2", deletedBy, teamID, member.UserID)
		return err
	})
	if err == nil {
		t.cache.forget(removedKeys...)
	}
	return team, err
}

// UpdateMember 는 팀원 역할을 바꾸고 찾은 user_id 를 돌려준다. role 이 nil 이면 바꾸지 않는다.
// 실패: ErrTeamNotFound, ErrNotMember
func (t *Teams) UpdateMember(ctx context.Context, teamID string, userID, userEmail *string, role *TeamRole) (string, error) {
	var resolved string
	err := t.db.InTx(ctx, func(ctx context.Context) error {
		if _, err := t.Find(ctx, teamID); err != nil {
			return err
		}
		member, err := t.findMember(ctx, teamID, userID, userEmail)
		if err != nil {
			return err
		}
		resolved = member.UserID
		if role == nil || *role == member.Role {
			return nil
		}
		_, err = t.db.Q(ctx).Exec(ctx, "UPDATE team_member SET role = $3 WHERE team_id = $1 AND user_id = $2", teamID, member.UserID, *role)
		return err
	})
	return resolved, err
}

// Page 는 /v2/team/list 의 한 쪽이다
func (t *Teams) Page(ctx context.Context, q TeamQuery) (core.Page[Team], error) {
	var lq core.ListQuery
	if q.MemberUserID != nil {
		lq.Where("team_id IN (SELECT team_id FROM team_member WHERE user_id = ?)", *q.MemberUserID)
	}
	if q.TeamID != nil {
		lq.Where("team_id = ?", *q.TeamID)
	}
	if q.TeamAliasContains != nil {
		lq.Where("team_alias ILIKE ?", core.LikeContains(*q.TeamAliasContains))
	}
	if q.Search != nil {
		if q.SearchTeamIDPrefix {
			lq.Where("(team_id LIKE ? OR team_alias ILIKE ?)", core.LikePrefix(*q.Search), core.LikeContains(*q.Search))
		} else {
			lq.Where("(team_id = ? OR team_alias ILIKE ?)", *q.Search, core.LikeContains(*q.Search))
		}
	}
	var total int64
	if err := t.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM team "+lq.Clause(), lq.Args...).Scan(&total); err != nil {
		return core.Page[Team]{}, err
	}
	sql := "SELECT " + teamColumns + " FROM team " + lq.Clause() +
		orderBy(q.SortBy, []string{"team_id", "team_alias", "created_at"}, q.SortOrder, "team_id") + lq.Page(q.Page, q.PageSize)
	items, err := t.list(ctx, sql, lq.Args...)
	return core.Page[Team]{Items: items, Total: total}, err
}

// ListByAlias 는 v1 /team/list 다: 별칭 오름차순(null 은 빈 문자열로 본다)
func (t *Teams) ListByAlias(ctx context.Context, memberUserID *string) ([]Team, error) {
	if memberUserID == nil {
		return t.list(ctx, "SELECT "+teamColumns+" FROM team ORDER BY coalesce(team_alias, ''), team_id")
	}
	return t.list(ctx, "SELECT "+teamColumns+" FROM team WHERE team_id IN (SELECT team_id FROM team_member WHERE user_id = $1) ORDER BY coalesce(team_alias, ''), team_id", *memberUserID)
}

func (t *Teams) list(ctx context.Context, sql string, args ...any) ([]Team, error) {
	rows, err := t.db.Q(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Team, error) { return scanTeam(row) })
}

func (t *Teams) addMember(ctx context.Context, teamID, userID string, role TeamRole) error {
	_, err := t.db.Q(ctx).Exec(ctx,
		"INSERT INTO team_member (team_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", teamID, userID, role)
	return err
}

func (t *Teams) findMember(ctx context.Context, teamID string, userID, userEmail *string) (TeamMember, error) {
	members, err := t.Members(ctx, []string{teamID})
	if err != nil {
		return TeamMember{}, err
	}
	for _, m := range members[teamID] {
		if matches(m, MemberRef{UserID: userID, UserEmail: userEmail}) {
			return m, nil
		}
	}
	return TeamMember{}, ErrNotMember
}

func containsMember(members []TeamMember, ref MemberRef) bool {
	for _, m := range members {
		if matches(m, ref) {
			return true
		}
	}
	return false
}

// user_id 가 있으면 그것으로, 없으면 이메일(대소문자 무시)로 같은 사람인지 본다
func matches(m TeamMember, ref MemberRef) bool {
	if ref.UserID != nil {
		return m.UserID == *ref.UserID
	}
	return ref.UserEmail != nil && m.UserEmail != nil && strings.EqualFold(*m.UserEmail, *ref.UserEmail)
}

func firstMissing[T any](ids []string, found []T, idOf func(T) string) string {
	have := map[string]bool{}
	for _, f := range found {
		have[idOf(f)] = true
	}
	for _, id := range ids {
		if !have[id] {
			return id
		}
	}
	return ""
}
