package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// Users 는 사용자 관리다. admin 모듈의 관리 API 가 부른다
type Users struct {
	db *core.DB
}

func NewUsers(db *core.DB) *Users { return &Users{db: db} }

const userColumns = "user_id, user_email, user_alias, user_role, models, metadata, sso_user_id, created_at, updated_at"

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.UserID, &u.UserEmail, &u.UserAlias, &u.UserRole, &u.Models, &u.Metadata, &u.SSOUserID, &u.CreatedAt, &u.UpdatedAt)
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
	return updated, err
}

// Delete 는 사용자를 지우고(팀원 행은 외래 키로 함께) 지운 수를 돌려준다. 하나라도 없으면 아무것도 지우지 않고 *NotFoundError 다
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
		tag, err := u.db.Q(ctx).Exec(ctx, "DELETE FROM app_user WHERE user_id = ANY($1)", userIDs)
		deleted = tag.RowsAffected()
		return err
	})
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
