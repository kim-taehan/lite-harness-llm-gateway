package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// Users 는 사용자 관리다. admin 모듈의 관리 API 가 부른다
type Users struct {
	db    *core.DB
	cache *KeyCache
}

func NewUsers(db *core.DB, cache *KeyCache) *Users { return &Users{db: db, cache: cache} }

const userColumns = "user_id, user_email, user_alias, user_role, models, metadata, sso_user_id, ui_sessions_revoked_at, password_reset_required, created_at, updated_at"

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.UserID, &u.UserEmail, &u.UserAlias, &u.UserRole, &u.Models, &u.Metadata, &u.SSOUserID, &u.UISessionsRevokedAt,
		&u.PasswordResetRequired, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// Create 는 사용자를 만들고, TeamID 가 있으면 그 팀에 role=user 로 넣는다(litellm 과 같다). 소속 팀 id 를 함께 돌려준다.
// 실패: ErrUserIDTaken, *EmailTakenError, ErrTeamNotFound
func (u *Users) Create(ctx context.Context, input NewUser) (User, []string, error) {
	userID := newID()
	if input.UserID != nil && *input.UserID != "" {
		userID = *input.UserID
	}
	var created User
	var teamIDs []string
	err := u.db.InTx(ctx, func(ctx context.Context) error {
		if _, err := u.Find(ctx, userID); err == nil {
			return ErrUserIDTaken
		} else if !errors.Is(err, ErrUserNotFound) {
			return err
		}
		if input.UserEmail != nil {
			if owner, err := u.FindByEmail(ctx, *input.UserEmail); err == nil {
				return &EmailTakenError{Existing: deref(owner.UserEmail)}
			} else if !errors.Is(err, ErrUserNotFound) {
				return err
			}
		}
		var err error
		created, err = u.insert(ctx, userID, input.UserEmail, input.UserAlias, input.UserRole, input.Models, input.Metadata)
		if err != nil || input.TeamID == nil {
			return err
		}
		var exists bool
		if err := u.db.Q(ctx).QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM team WHERE team_id = $1)", *input.TeamID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrTeamNotFound
		}
		teamIDs = []string{*input.TeamID}
		_, err = u.db.Q(ctx).Exec(ctx, "INSERT INTO team_member (team_id, user_id, role) VALUES ($1, $2, 'user')", *input.TeamID, userID)
		return err
	})
	return created, teamIDs, err
}

// Find 는 사용자 하나다. 없으면 ErrUserNotFound
func (u *Users) Find(ctx context.Context, userID string) (User, error) {
	return u.one(ctx, "SELECT "+userColumns+" FROM app_user WHERE user_id = $1", userID)
}

// FindByEmail 은 이메일로 찾는다(대소문자 무시, litellm 과 같다). 없으면 ErrUserNotFound
func (u *Users) FindByEmail(ctx context.Context, email string) (User, error) {
	return u.one(ctx, "SELECT "+userColumns+" FROM app_user WHERE lower(user_email) = lower($1)", email)
}

func (u *Users) FindAll(ctx context.Context, userIDs []string) ([]User, error) {
	return u.list(ctx, "SELECT "+userColumns+" FROM app_user WHERE user_id = ANY($1)", userIDs)
}

// TeamIDs 는 사용자별 소속 팀 id 다(litellm 사용자 행의 teams 배열)
func (u *Users) TeamIDs(ctx context.Context, userIDs []string) (map[string][]string, error) {
	rows, err := u.db.Q(ctx).Query(ctx,
		"SELECT user_id, team_id FROM team_member WHERE user_id = ANY($1) ORDER BY created_at, team_id", userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]string{}
	for rows.Next() {
		var userID, teamID string
		if err := rows.Scan(&userID, &teamID); err != nil {
			return nil, err
		}
		result[userID] = append(result[userID], teamID)
	}
	return result, rows.Err()
}

// Update 는 보낸 필드만 바꾼다. 실패: ErrUserNotFound, *EmailTakenError
func (u *Users) Update(ctx context.Context, userID string, patch UserPatch) (User, error) {
	var updated User
	err := u.db.InTx(ctx, func(ctx context.Context) error {
		if _, err := u.Find(ctx, userID); err != nil {
			return err
		}
		if patch.UserEmail.Value != nil {
			owner, err := u.FindByEmail(ctx, *patch.UserEmail.Value)
			if err == nil && owner.UserID != userID {
				return &EmailTakenError{Existing: deref(owner.UserEmail)}
			}
			if err != nil && !errors.Is(err, ErrUserNotFound) {
				return err
			}
		}
		sets := []string{"updated_at = now()"}
		args := []any{userID}
		set := func(column string, value any) {
			args = append(args, value)
			sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
		}
		if patch.UserEmail.Set {
			set("user_email", patch.UserEmail.Value)
		}
		if patch.UserAlias.Set {
			set("user_alias", patch.UserAlias.Value)
		}
		if patch.UserRole.Set {
			set("user_role", patch.UserRole.Value)
		}
		if patch.Models.Set {
			set("models", patch.Models.Or([]string{}))
		}
		if patch.Metadata.Set {
			set("metadata", patch.Metadata.Value)
		}
		var err error
		updated, err = u.one(ctx, "UPDATE app_user SET "+strings.Join(sets, ", ")+" WHERE user_id = $1 RETURNING "+userColumns, args...)
		return err
	})
	if err == nil && patch.UserRole.Set {
		// 키의 역할은 소유자 사용자 행에서 온다. 역할이 바뀌면 그 사용자 키 캐시를 지운다
		u.cache.forgetUser(userID)
	}
	return updated, err
}

// Delete 는 사용자와 그 키를 지우고(팀원 행은 외래 키로 함께) 지운 수를 돌려준다. 하나라도 없으면 아무것도 지우지 않고 *NotFoundError 다
func (u *Users) Delete(ctx context.Context, userIDs []string) (int64, error) {
	var deleted int64
	err := u.db.InTx(ctx, func(ctx context.Context) error {
		found, err := u.FindAll(ctx, userIDs)
		if err != nil {
			return err
		}
		if missing := firstMissing(userIDs, found, func(user User) string { return user.UserID }); missing != "" {
			return &NotFoundError{ID: missing}
		}
		// litellm 은 사용자의 키를 사본 없이 지운다(H 4.3)
		if _, err := u.db.Q(ctx).Exec(ctx, "DELETE FROM api_key WHERE user_id = ANY($1)", userIDs); err != nil {
			return err
		}
		tag, err := u.db.Q(ctx).Exec(ctx, "DELETE FROM app_user WHERE user_id = ANY($1)", userIDs)
		deleted = tag.RowsAffected()
		return err
	})
	if err == nil {
		u.cache.forgetUser(userIDs...)
	}
	return deleted, err
}

// Page 는 /user/list 의 한 쪽이다
func (u *Users) Page(ctx context.Context, q UserQuery) (core.Page[User], error) {
	var lq listQuery
	if q.Role != nil {
		lq.where("user_role = ?", *q.Role)
	}
	switch len(q.UserIDs) {
	case 0:
	case 1:
		// litellm: user_ids 가 하나면 부분 일치(대소문자 무시), 여러 개면 정확히 일치
		lq.where("user_id ILIKE ?", likeContains(q.UserIDs[0]))
	default:
		lq.where("user_id = ANY(?)", q.UserIDs)
	}
	if len(q.SSOUserIDs) > 0 {
		lq.where("sso_user_id = ANY(?)", q.SSOUserIDs)
	}
	if q.UserEmailContains != nil {
		lq.where("user_email ILIKE ?", likeContains(*q.UserEmailContains))
	}
	if q.Search != nil {
		lq.where("(user_id ILIKE ? OR user_email ILIKE ?)", likeContains(*q.Search), likeContains(*q.Search))
	}
	if q.TeamID != nil {
		lq.where("user_id IN (SELECT user_id FROM team_member WHERE team_id = ?)", *q.TeamID)
	}
	var total int64
	if err := u.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM app_user "+lq.clause(), lq.args...).Scan(&total); err != nil {
		return core.Page[User]{}, err
	}
	sql := "SELECT " + userColumns + " FROM app_user " + lq.clause() +
		orderBy(q.SortBy, []string{"user_id", "user_email", "created_at", "user_alias", "user_role"}, q.SortOrder, "user_id") +
		lq.page(q.Page, q.PageSize)
	items, err := u.list(ctx, sql, lq.args...)
	return core.Page[User]{Items: items, Total: total}, err
}

// resolveMember 는 팀원 참조를 사용자로 푼다. litellm 처럼 없는 사용자는 internal_user 로 만든다
// (user_id 로 오면 그 id 로, 이메일로만 오면 새 uuid 로). 실패: *EmailOwnerMismatchError
func (u *Users) resolveMember(ctx context.Context, ref MemberRef) (User, error) {
	internalUser := core.RoleInternalUser
	if ref.UserID == nil {
		user, err := u.FindByEmail(ctx, deref(ref.UserEmail))
		if errors.Is(err, ErrUserNotFound) {
			return u.insert(ctx, newID(), ref.UserEmail, nil, &internalUser, nil, nil)
		}
		return user, err
	}
	if ref.UserEmail != nil {
		owner, err := u.FindByEmail(ctx, *ref.UserEmail)
		if err == nil && owner.UserID != *ref.UserID {
			return User{}, &EmailOwnerMismatchError{Email: *ref.UserEmail, UserID: *ref.UserID}
		}
		if err != nil && !errors.Is(err, ErrUserNotFound) {
			return User{}, err
		}
	}
	user, err := u.Find(ctx, *ref.UserID)
	if errors.Is(err, ErrUserNotFound) {
		return u.insert(ctx, *ref.UserID, ref.UserEmail, nil, &internalUser, nil, nil)
	}
	return user, err
}

func (u *Users) insert(ctx context.Context, userID string, email, alias *string, role *core.Role, models []string, metadata map[string]any) (User, error) {
	if models == nil {
		models = []string{}
	}
	return u.one(ctx, `
		INSERT INTO app_user (user_id, user_email, user_alias, user_role, models, metadata)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+userColumns, userID, email, alias, role, models, metadata)
}

func (u *Users) one(ctx context.Context, sql string, args ...any) (User, error) {
	user, err := scanUser(u.db.Q(ctx).QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	return user, err
}

func (u *Users) list(ctx context.Context, sql string, args ...any) ([]User, error) {
	rows, err := u.db.Q(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (User, error) { return scanUser(row) })
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SetPassword 는 비밀번호를 정한다(관리자 /user/update, 초대 온보딩, 본인 변경). 빈 문자열이면 비밀번호 로그인을 막는다.
// litellm 처럼 정책을 검사하고(*PasswordPolicyError), 그 사용자의 대시보드 세션을 모두 끊는다.
// byAdmin 이면 다음 로그인 때 본인이 바꾸게 한다(password_reset_required)
func (u *Users) SetPassword(ctx context.Context, userID, password string, byAdmin bool, at time.Time) error {
	var stored *string
	if password != "" {
		if err := checkPasswordPolicy(password); err != nil {
			return err
		}
		hashed, err := hashPassword(password)
		if err != nil {
			return err
		}
		stored = &hashed
	}
	tag, err := u.db.Q(ctx).Exec(ctx, `
		UPDATE app_user SET password_hash = $2, password_reset_required = $3, ui_sessions_revoked_at = $4, updated_at = now()
		WHERE user_id = $1`, userID, stored, byAdmin && stored != nil, at.UTC().Truncate(time.Microsecond))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return err
}

// revokeUISessions 는 그 사용자의 지금까지 대시보드 세션을 모두 끊는다(로그아웃). 로그인 키는 DB 에 없어서 시각으로 가른다
func (u *Users) revokeUISessions(ctx context.Context, userID string, at time.Time) error {
	_, err := u.db.Q(ctx).Exec(ctx, "UPDATE app_user SET ui_sessions_revoked_at = $2 WHERE user_id = $1", userID, at)
	return err
}

// ensureProxyAdmin 은 대시보드 관리자 로그인(admin + 마스터 키)의 사용자 행을 proxy_admin 으로 맞춘다. 없으면 만든다(litellm 과 같다)
func (u *Users) ensureProxyAdmin(ctx context.Context, userID string) (User, error) {
	user, err := u.one(ctx, `
		INSERT INTO app_user (user_id, user_role) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET user_role = excluded.user_role, updated_at = now()
		RETURNING `+userColumns, userID, core.RoleProxyAdmin)
	if err == nil {
		u.cache.forgetUser(userID) // 역할이 바뀌었을 수 있다
	}
	return user, err
}

// ErrBadCredentials 는 아이디나 비밀번호가 틀렸다. 어느 쪽이 틀렸는지 알리지 않는다
var ErrBadCredentials = errors.New("아이디 또는 비밀번호가 틀렸다")

// checkPassword 는 이메일(대소문자 무시) 또는 user_id 와 비밀번호로 사용자를 찾는다
func (u *Users) checkPassword(ctx context.Context, login, password string) (User, error) {
	var hash *string
	var userID string
	err := u.db.Q(ctx).QueryRow(ctx,
		"SELECT user_id, password_hash FROM app_user WHERE lower(user_email) = lower($1) OR user_id = $1 ORDER BY (user_id = $1) DESC LIMIT 1", login).
		Scan(&userID, &hash)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && hash == nil) {
		// 사용자가 없어도 같은 시간이 걸리게 한 번 계산한다(아이디가 있는지 알아내는 것을 막는다)
		if _, err := verifyPasswordLimited(ctx, dummyPasswordHash(), password); err != nil {
			return User{}, err
		}
		return User{}, ErrBadCredentials
	}
	if err != nil {
		return User{}, err
	}
	matched, err := verifyPasswordLimited(ctx, *hash, password)
	if err != nil {
		return User{}, err
	}
	if !matched {
		return User{}, ErrBadCredentials
	}
	return u.Find(ctx, userID)
}

// dummyPasswordHash 는 시간 맞추기용이다. 어떤 비밀번호와도 맞지 않는다. 처음 쓸 때 한 번만 만든다(기동을 늦추지 않게)
var dummyPasswordHash = sync.OnceValue(func() string {
	h, _ := hashPassword(randomURLSafe(32))
	return h
})

// upsertSSOUser 는 SSO 콜백의 사용자다(K 2.4). user_id 는 이메일이고, 없으면 internal_user_viewer 로 만든다(litellm 기본).
// 있으면 이메일만 갱신하고 별칭은 비어 있을 때만 채운다. 역할은 바꾸지 않는다
func (u *Users) upsertSSOUser(ctx context.Context, c oidcClaims) (User, error) {
	email := strings.ToLower(c.Email)
	userID := email
	if userID == "" {
		userID = c.PreferredUsername
	}
	// litellm 기본은 별칭이 Keycloak sub(UUID)가 된다(K 2.4). 화면에 UUID 가 보이지 않게 이름 클레임을 쓴다(설계 9장 차이)
	alias := c.Name
	if alias == "" {
		alias = c.PreferredUsername
	}
	var result User
	err := u.db.InTx(ctx, func(ctx context.Context) error {
		existing, err := u.Find(ctx, userID)
		if errors.Is(err, ErrUserNotFound) {
			role, models, err := u.newUserDefaults(ctx)
			if err != nil {
				return err
			}
			result, err = u.one(ctx, `
				INSERT INTO app_user (user_id, user_email, user_alias, user_role, models, sso_user_id, metadata)
				VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+userColumns,
				userID, nullIfEmpty(email), nullIfEmpty(alias), role, models, c.Subject, map[string]any{"auth_provider": "generic"})
			return err
		}
		if err != nil {
			return err
		}
		result, err = u.one(ctx, `
			UPDATE app_user SET user_email = coalesce($2, user_email), user_alias = coalesce(user_alias, $3),
			       sso_user_id = coalesce(sso_user_id, $4), updated_at = now()
			WHERE user_id = $1 RETURNING `+userColumns, existing.UserID, nullIfEmpty(email), nullIfEmpty(alias), c.Subject)
		return err
	})
	return result, err
}

// newUserDefaults 는 SSO 로 처음 들어온 사용자의 역할·모델이다. 대시보드 "Default User Settings"
// (litellm default_internal_user_params)에 값이 있으면 그것을, 없으면 litellm 기본(internal_user_viewer, 모델 없음)을 쓴다
func (u *Users) newUserDefaults(ctx context.Context) (core.Role, []string, error) {
	stored, err := core.NewSettings(u.db).Get(ctx, "default_internal_user_params")
	if err != nil {
		return "", nil, err
	}
	role := core.RoleInternalUserViewer
	if r, ok := stored["user_role"].(string); ok && r != "" {
		role = core.Role(r)
	}
	models := []string{}
	if list, ok := stored["models"].([]any); ok {
		for _, m := range list {
			if s, ok := m.(string); ok {
				models = append(models, s)
			}
		}
	}
	return role, models, nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
