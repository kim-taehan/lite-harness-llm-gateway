package admin

import (
	"context"
	"log/slog"
	"time"

	"llm-gateway/internal/core"
)

// auditRetentionInterval 은 보관 기간이 지난 감사 기록을 지우는 주기다
const auditRetentionInterval = 24 * time.Hour

// auditPurgeBatch 는 한 번에 지우는 최대 줄 수다. 오래 쌓인 기록을 처음 지울 때 한 문장이 테이블을 오래 잡지 않게 나눠 지운다
const auditPurgeBatch = 5000

// AuditRetention 은 보관 기간이 지난 audit_log 를 지운다. 사용량 기록(90일)과 달리 감사 기록은 점검·사고 조사에 쓰여서
// 기본 보관 기간이 길고 운영자가 정한다(GATEWAY_AUDIT_RETENTION_DAYS). 지운 사실도 감사 기록 한 줄로 남긴다
type AuditRetention struct {
	log  *auditLog
	keep time.Duration
}

// NewAuditRetention 의 keep 은 0 보다 커야 한다. 영구 보관(0)이면 이 job 을 만들지 않는다
func NewAuditRetention(db *core.DB, now func() time.Time, chainKey []byte, keep time.Duration) *AuditRetention {
	return &AuditRetention{log: &auditLog{db: db, now: now, chainKey: chainKey}, keep: keep}
}

// Purge 는 기준 시각(지금 − 보관 기간)보다 오래된 기록을 지우고 지운 줄 수를 돌려준다. 여러 대가 같이 돌아도 같은 조건의 삭제다.
// 지운 것이 있으면 그 사실(줄 수와 기준 시각)을 감사 기록에 남긴다. ctx 가 끝나면 그때까지 지운 만큼만 돌려준다
func (r *AuditRetention) Purge(ctx context.Context) (int64, error) {
	cutoff := r.log.now().UTC().Add(-r.keep)
	var total int64
	for ctx.Err() == nil {
		tag, err := r.log.db.Q(ctx).Exec(ctx, `
			DELETE FROM audit_log WHERE id IN (SELECT id FROM audit_log WHERE updated_at < $1 ORDER BY updated_at LIMIT $2)`, cutoff, auditPurgeBatch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < auditPurgeBatch {
			break
		}
	}
	if total > 0 {
		values := map[string]any{"deleted": total, "older_than": cutoff.Format(time.RFC3339)}
		// 정리 기록은 기준 시각보다 새로우므로 방금 지운 것에 섞이지 않는다
		if err := r.log.recordAs(context.WithoutCancel(ctx), "system", "", "retention_purged", "audit_log", "retention", nil, values); err != nil {
			return total, err
		}
	}
	return total, nil
}

// Run 은 기동 때 한 번, 이후 하루마다 지운다. ctx 가 끝나면 돌아온다. 실패해도 오류만 남기고 다음 주기에 다시 한다
func (r *AuditRetention) Run(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(auditRetentionInterval)
	defer ticker.Stop()
	for {
		deleted, err := r.Purge(ctx)
		if err != nil {
			logger.Error("감사 기록 보관 삭제 실패", "err", err)
		} else if deleted > 0 {
			logger.Info("감사 기록 보관 삭제", "deleted", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
