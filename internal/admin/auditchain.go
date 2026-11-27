package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// 감사 기록 해시 체인(변조 방지). 기록마다 row_hash = HMAC(키, [앞 기록의 해시, 이 기록의 내용]) 이다.
// 내용을 고치면 그 기록의 해시가, 중간 기록을 지우면 다음 기록의 prev_hash 가 맞지 않는다. 키가 마스터 키에서 나오므로
// DB 만 고칠 수 있는 사람은 해시를 다시 맞출 수 없다. 끝부분을 지우는 것은 체인만으로 알 수 없어 검증 결과의 head 를 밖에 적어 둔다

// auditChainLock 은 체인에 한 줄씩 차례로 붙이려고 잡는 advisory lock 번호다(여러 대가 동시에 써도 순서가 하나다)
const auditChainLock = 7241058633

// chainedEntry 는 해시에 들어가는 기록의 내용이다. JSON 값은 DB 가 저장한 표현(jsonb::text)을 써서 넣을 때와 검증할 때가 같다
type chainedEntry struct {
	seq                                                  int64
	id, changedBy, changedByKey, action, table, objectID string
	updatedAt                                            time.Time
	before, values                                       string
	prevHash, rowHash                                    *string
}

const chainedColumns = "seq, id, updated_at, changed_by, changed_by_api_key, action, table_name, object_id, " +
	"COALESCE(before_value::text, 'null'), COALESCE(updated_values::text, 'null'), prev_hash, row_hash"

func scanChained(row pgx.Row) (chainedEntry, error) {
	var e chainedEntry
	err := row.Scan(&e.seq, &e.id, &e.updatedAt, &e.changedBy, &e.changedByKey, &e.action, &e.table, &e.objectID, &e.before, &e.values, &e.prevHash, &e.rowHash)
	return e, err
}

// chainHash 는 앞 해시와 내용으로 이 기록의 해시를 만든다. 필드를 JSON 배열로 묶어 경계가 모호하지 않게 한다
func chainHash(key []byte, prev string, e chainedEntry) string {
	fields, _ := json.Marshal([]string{prev, e.id, e.updatedAt.UTC().Format(time.RFC3339Nano), e.changedBy, e.changedByKey,
		e.action, e.table, e.objectID, e.before, e.values}) // 문자열 배열이라 실패하지 않는다
	mac := hmac.New(sha256.New, key)
	mac.Write(fields)
	return hex.EncodeToString(mac.Sum(nil))
}

// appendChained 는 기록 한 줄을 넣고 체인에 잇는다. 한 트랜잭션이고, 잠금으로 다른 기록과 순서가 섞이지 않는다
func (a *auditLog) appendChained(ctx context.Context, changedBy, keyToken, action, table, objectID string, before, values any) error {
	return a.db.InTx(ctx, func(ctx context.Context) error {
		q := a.db.Q(ctx)
		if _, err := q.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", auditChainLock); err != nil {
			return err
		}
		prev := ""
		err := q.QueryRow(ctx, "SELECT row_hash FROM audit_log WHERE row_hash IS NOT NULL ORDER BY seq DESC LIMIT 1").Scan(&prev)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		entry, err := scanChained(q.QueryRow(ctx, `
			INSERT INTO audit_log (id, updated_at, changed_by, changed_by_api_key, action, table_name, object_id, before_value, updated_values)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb) RETURNING `+chainedColumns,
			core.NewUUID(), a.now().UTC(), changedBy, keyToken, action, table, objectID, before, values))
		if err != nil {
			return err
		}
		_, err = q.Exec(ctx, "UPDATE audit_log SET prev_hash = $2, row_hash = $3 WHERE seq = $1", entry.seq, prev, chainHash(a.chainKey, prev, entry))
		return err
	})
}

// chainReport 는 검증 결과다. head 는 마지막으로 검증한 기록이다: 밖(운영 일지 등)에 적어 두면 끝부분을 지운 것도 알 수 있다
type chainReport struct {
	Valid     bool  `json:"valid"`
	Checked   int64 `json:"checked"`
	Unchained int64 `json:"unchained_before_chain"` // 체인을 시작하기 전(해시 기능 전)의 기록 수
	Invalid   *row  `json:"first_invalid"`
	Head      *row  `json:"head"`
}

// verify 는 체인을 처음부터 끝까지 확인한다. 보관 기간 삭제로 앞부분이 지워졌을 수 있어 남은 첫 기록의 prev_hash 를 기준점으로 믿는다.
// 처음 어긋난 곳에서 멈춘다
func (a *auditLog) verify(ctx context.Context) (chainReport, error) {
	report := chainReport{Valid: true}
	rows, err := a.db.Q(ctx).Query(ctx, "SELECT "+chainedColumns+" FROM audit_log ORDER BY seq")
	if err != nil {
		return report, err
	}
	defer rows.Close()
	var last *chainedEntry
	for rows.Next() {
		e, err := scanChained(rows)
		if err != nil {
			return report, err
		}
		reason := ""
		switch {
		case e.rowHash == nil && last == nil:
			report.Unchained++ // 체인 전의 기록이다
			continue
		case e.rowHash == nil:
			reason = "unhashed row inside the chain" // 체인이 시작된 뒤 해시 없이 끼워 넣은 기록
		case last != nil && derefOr(e.prevHash, "") != *last.rowHash:
			reason = "broken link (a record before this one was removed or replaced)"
		case chainHash(a.chainKey, derefOr(e.prevHash, ""), e) != *e.rowHash:
			reason = "content changed"
		}
		if reason != "" {
			report.Valid = false
			report.Invalid = &row{"id": e.id, "seq": e.seq, "updated_at": e.updatedAt.UTC(), "reason": reason}
			return report, nil
		}
		report.Checked++
		last = &e
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	if last != nil {
		report.Head = &row{"id": last.id, "seq": last.seq, "updated_at": last.updatedAt.UTC(), "row_hash": *last.rowHash}
	}
	return report, nil
}

// verifyAudit 는 GET /audit/verify 다(우리만의 API. litellm 에 없다)
func (h *Handlers) verifyAudit(w http.ResponseWriter, r *http.Request) {
	report, err := h.audit.verify(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, report)
}
