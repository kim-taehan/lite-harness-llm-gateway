package limits

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"llm-gateway/internal/core"
)

// Customer 는 고객(end user) 한 명의 기본 정보다. 예산·한도는 Policies 의 ScopeEndUser 에 있다
type Customer struct {
	UserID    string
	Alias     *string
	Blocked   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

var (
	ErrCustomerExists   = errors.New("이미 있는 고객이다")
	ErrCustomerNotFound = errors.New("고객이 없다")
)

// Customers 는 고객 저장소다
type Customers struct{ db *core.DB }

func NewCustomers(db *core.DB) *Customers { return &Customers{db: db} }

const customerColumns = "user_id, alias, blocked, created_at, updated_at"

func scanCustomer(row pgx.Row) (Customer, error) {
	var c Customer
	err := row.Scan(&c.UserID, &c.Alias, &c.Blocked, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// Find 는 고객 한 명이다. 없으면 ErrCustomerNotFound
func (s *Customers) Find(ctx context.Context, id string) (Customer, error) {
	c, err := scanCustomer(s.db.Q(ctx).QueryRow(ctx, "SELECT "+customerColumns+" FROM customer WHERE user_id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrCustomerNotFound
	}
	return c, err
}

// List 는 모든 고객이다(만든 순서)
func (s *Customers) List(ctx context.Context) ([]Customer, error) {
	rows, err := s.db.Q(ctx).Query(ctx, "SELECT "+customerColumns+" FROM customer ORDER BY created_at, user_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Customer
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// Create 는 고객을 만든다. 이미 있으면 ErrCustomerExists
func (s *Customers) Create(ctx context.Context, id string, alias *string, blocked bool) (Customer, error) {
	c, err := scanCustomer(s.db.Q(ctx).QueryRow(ctx,
		"INSERT INTO customer (user_id, alias, blocked) VALUES ($1, $2, $3) RETURNING "+customerColumns, id, alias, blocked))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Customer{}, ErrCustomerExists
	}
	return c, err
}

// Update 는 별칭과 차단을 바꾼다. 보내지 않은 필드는 그대로 둔다. 없으면 ErrCustomerNotFound
func (s *Customers) Update(ctx context.Context, id string, alias core.Change[string], blocked core.Change[bool]) (Customer, error) {
	current, err := s.Find(ctx, id)
	if err != nil {
		return Customer{}, err
	}
	next := alias.Apply(current.Alias)
	isBlocked := current.Blocked
	if blocked.Set && blocked.Value != nil {
		isBlocked = *blocked.Value
	}
	return scanCustomer(s.db.Q(ctx).QueryRow(ctx,
		"UPDATE customer SET alias = $2, blocked = $3, updated_at = now() WHERE user_id = $1 RETURNING "+customerColumns, id, next, isBlocked))
}

// SetBlocked 는 고객들의 차단을 바꾼다. 없는 고객은 만든다(litellm /customer/block 이 upsert 라서 아직 없는 id 도 미리 막을 수 있다)
func (s *Customers) SetBlocked(ctx context.Context, ids []string, blocked bool) ([]Customer, error) {
	result := make([]Customer, 0, len(ids))
	for _, id := range ids {
		c, err := scanCustomer(s.db.Q(ctx).QueryRow(ctx, `
			INSERT INTO customer (user_id, blocked) VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET blocked = EXCLUDED.blocked, updated_at = now()
			RETURNING `+customerColumns, id, blocked))
		if err != nil {
			return nil, fmt.Errorf("고객 차단 변경: %w", err)
		}
		result = append(result, c)
	}
	return result, nil
}

// Blocked 는 지금 차단된 고객들이다
func (s *Customers) Blocked(ctx context.Context) ([]Customer, error) {
	rows, err := s.db.Q(ctx).Query(ctx, "SELECT "+customerColumns+" FROM customer WHERE blocked ORDER BY user_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Customer
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// IsBlocked 는 요청마다 도는 검사용이다. 고객 행이 없으면 막히지 않았다
func (s *Customers) IsBlocked(ctx context.Context, id string) (bool, error) {
	var blocked bool
	err := s.db.Q(ctx).QueryRow(ctx, "SELECT blocked FROM customer WHERE user_id = $1", id).Scan(&blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return blocked, err
}

// Delete 는 고객과 그 한도를 지운다. 지운 고객 수를 돌려준다
func (s *Customers) Delete(ctx context.Context, ids []string) (int, error) {
	tag, err := s.db.Q(ctx).Exec(ctx, "DELETE FROM customer WHERE user_id = ANY($1)", ids)
	if err != nil {
		return 0, err
	}
	for _, table := range []string{"limit_policy", "limit_model_policy"} {
		if _, err := s.db.Q(ctx).Exec(ctx, "DELETE FROM "+table+" WHERE scope = 'end_user' AND target_id = ANY($1)", ids); err != nil {
			return 0, err
		}
	}
	return int(tag.RowsAffected()), nil
}
