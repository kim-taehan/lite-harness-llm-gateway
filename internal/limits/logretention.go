package limits

import (
	"context"
	"log/slog"
	"time"

	"llm-gateway/internal/core"
)

// logRetentionInterval 은 설계 3.3 의 보관 기간 삭제 주기다
const logRetentionInterval = 24 * time.Hour

// LogRetention 은 보관 기간이 지난 request_log 를 지운다. 사용액은 limit_policy 에 따로 쌓여 있어 지워도 예산에 영향이 없다
type LogRetention struct {
	db   *core.DB
	now  func() time.Time
	keep time.Duration
}

func NewLogRetention(db *core.DB, now func() time.Time, keep time.Duration) *LogRetention {
	return &LogRetention{db: db, now: now, keep: keep}
}

// Purge 는 지운 기록 수를 돌려준다. 여러 대가 같이 돌아도 같은 조건의 삭제라 결과가 같다(락이 필요 없다)
func (r *LogRetention) Purge(ctx context.Context) (int64, error) {
	tag, err := r.db.Q(ctx).Exec(ctx, "DELETE FROM request_log WHERE created_at < $1", r.now().Add(-r.keep))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Run 은 기동 때 한 번, 이후 하루마다 지운다. ctx 가 끝나면 돌아온다. 실패해도 오류만 남기고 다음 주기에 다시 한다
func (r *LogRetention) Run(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(logRetentionInterval)
	defer ticker.Stop()
	for {
		deleted, err := r.Purge(ctx)
		if err != nil {
			logger.Error("요청 기록 보관 삭제 실패", "err", err)
		} else if deleted > 0 {
			logger.Info("요청 기록 보관 삭제", "deleted", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
