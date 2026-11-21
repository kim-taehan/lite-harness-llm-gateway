package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// 초대 링크(L 6.1). 관리자가 사용자에게 링크를 주면 그 사용자가 자기 비밀번호를 정한다. 7일 뒤 만료되고 한 번만 쓴다

// invitationTTL 은 litellm 과 같다
const invitationTTL = 7 * 24 * time.Hour

// Invitation 은 litellm InvitationModel 이다
type Invitation struct {
	ID         string
	UserID     string
	IsAccepted bool
	AcceptedAt *time.Time
	ExpiresAt  time.Time
	CreatedAt  time.Time
	CreatedBy  string
	UpdatedAt  time.Time
	UpdatedBy  string
}

var ErrInvitationNotFound = errors.New("초대 링크가 없다")

const invitationColumns = "id, user_id, is_accepted, accepted_at, expires_at, created_at, created_by, updated_at, updated_by"

func scanInvitation(row pgx.Row) (Invitation, error) {
	var i Invitation
	err := row.Scan(&i.ID, &i.UserID, &i.IsAccepted, &i.AcceptedAt, &i.ExpiresAt, &i.CreatedAt, &i.CreatedBy, &i.UpdatedAt, &i.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrInvitationNotFound
	}
	return i, err
}

// CreateInvitation 은 사용자의 초대 링크를 만든다. 사용자가 없으면 ErrUserNotFound
func (u *Users) CreateInvitation(ctx context.Context, userID, by string, now time.Time) (Invitation, error) {
	if _, err := u.Find(ctx, userID); err != nil {
		return Invitation{}, err
	}
	return scanInvitation(u.db.Q(ctx).QueryRow(ctx, `
		INSERT INTO invitation (id, user_id, expires_at, created_by, updated_by) VALUES ($1, $2, $3, $4, $4)
		RETURNING `+invitationColumns, core.NewUUID(), userID, now.Add(invitationTTL), by))
}

func (u *Users) findInvitation(ctx context.Context, id string) (Invitation, error) {
	return scanInvitation(u.db.Q(ctx).QueryRow(ctx, "SELECT "+invitationColumns+" FROM invitation WHERE id = $1", id))
}

// ErrInvitationUsed 는 이미 쓴 초대 링크다
var ErrInvitationUsed = errors.New("이미 쓴 초대 링크다")

// ClaimInvitation 은 초대받은 사용자가 비밀번호를 정한다. 링크를 쓴 것으로 바꾸고, 비밀번호를 정하고, 사용자를 돌려주는 것을
// 한 트랜잭션으로 한다. 링크는 아직 쓰지 않은 것만 쓴 것으로 바뀌므로 동시에 두 번 눌러도 한 번만 된다.
// 실패: ErrInvitationUsed, *PasswordPolicyError
func (u *Users) ClaimInvitation(ctx context.Context, inv Invitation, password string, now time.Time) (User, error) {
	var user User
	err := u.db.InTx(ctx, func(ctx context.Context) error {
		tag, err := u.db.Q(ctx).Exec(ctx,
			"UPDATE invitation SET is_accepted = true, accepted_at = $2, updated_at = $2 WHERE id = $1 AND NOT is_accepted", inv.ID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrInvitationUsed
		}
		if err := u.SetPassword(ctx, inv.UserID, password, false, now); err != nil {
			return err
		}
		user, err = u.Find(ctx, inv.UserID)
		return err
	})
	return user, err
}
