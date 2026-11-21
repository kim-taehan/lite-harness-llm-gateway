package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
		RETURNING `+invitationColumns, newUUID(), userID, now.Add(invitationTTL), by))
}

func (u *Users) findInvitation(ctx context.Context, id string) (Invitation, error) {
	return scanInvitation(u.db.Q(ctx).QueryRow(ctx, "SELECT "+invitationColumns+" FROM invitation WHERE id = $1", id))
}

// acceptInvitation 은 아직 쓰지 않은 링크만 쓴 것으로 바꾼다. 이미 썼으면 false 다(동시에 두 번 눌러도 한 번만 된다)
func (u *Users) acceptInvitation(ctx context.Context, id string, now time.Time) (bool, error) {
	tag, err := u.db.Q(ctx).Exec(ctx,
		"UPDATE invitation SET is_accepted = true, accepted_at = $2, updated_at = $2 WHERE id = $1 AND NOT is_accepted", id, now)
	return err == nil && tag.RowsAffected() == 1, err
}

// newUUID 는 uuid4 문자열이다
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand 는 실패하지 않는다(Go 1.24+)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
