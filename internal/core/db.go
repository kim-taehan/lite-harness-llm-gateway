package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier 는 풀과 트랜잭션이 함께 가진 쿼리 메서드다. 저장소는 이것만 쓴다
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DB 는 커넥션 풀이다. 트랜잭션은 ctx 에 실어 나른다.
// 여러 모듈의 쓰기를 한 트랜잭션으로 묶을 때(예: 팀 삭제 + 그 팀 한도 삭제) 모듈 API 에 tx 를 인자로 넘기지 않아도 되게 하려는 것이다
type DB struct {
	pool *pgxpool.Pool
}

func NewDB(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

type txKey struct{}

// Q 는 ctx 에 진행 중인 트랜잭션이 있으면 그것을, 없으면 풀을 돌려준다
func (d *DB) Q(ctx context.Context) Querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return d.pool
}

// ErrRollback 을 fn 이 돌려주면 오류 없이 되돌린다. 예상되는 실패(검증 실패 등)로 쓰기를 취소할 때 쓴다
var ErrRollback = errors.New("rollback")

// InTx 는 fn 을 한 트랜잭션에서 실행한다. 이미 트랜잭션 안이면 그 트랜잭션에 참여한다.
// fn 이 오류를 돌려주면 되돌리고 그 오류를 그대로 돌려준다
func (d *DB) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("트랜잭션 시작: %w", err)
	}
	// 커밋한 뒤의 Rollback 은 아무 일도 하지 않는다. 취소된 ctx 에서도 되돌리도록 WithoutCancel 로 부른다
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
