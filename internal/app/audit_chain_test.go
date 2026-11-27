package app

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 감사 기록 해시 체인: 고치거나 지우거나 끼워 넣으면 /audit/verify 가 어디서 어긋났는지 알려 준다

// chainedGateway 는 감사 기록 n 줄을 만든 게이트웨이다(팀 생성 n 번)
func chainedGateway(t *testing.T, n int) (apiClient, *pgxpool.Pool) {
	t.Helper()
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	for i := 0; i < n; i++ {
		api.ok("POST", "/team/new", map[string]any{"team_id": fmt.Sprintf("t%d", i)})
	}
	return api, pool
}

// auditIDs 는 순번 순서의 감사 기록 id 다
func auditIDs(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), "SELECT id FROM audit_log ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func verifyChain(t *testing.T, api apiClient) map[string]any {
	t.Helper()
	return api.ok("GET", "/audit/verify", nil)
}

func TestAuditChain_ValidChainReportsCountAndHead(t *testing.T) {
	api, pool := chainedGateway(t, 4)
	report := verifyChain(t, api)
	ids := auditIDs(t, pool)
	head, _ := report["head"].(map[string]any)
	if report["valid"] != true || report["checked"] != float64(len(ids)) || report["unchained_before_chain"] != 0.0 || report["first_invalid"] != nil ||
		head["id"] != ids[len(ids)-1] || len(head["row_hash"].(string)) != 64 {
		t.Errorf("검증 = %v (기록 %d 줄)", report, len(ids))
	}
}

func TestAuditChain_DetectsChangedRemovedAndInsertedRecords(t *testing.T) {
	cases := map[string]struct {
		tamper func(t *testing.T, pool *pgxpool.Pool, ids []string) string // 처음 어긋나야 할 기록 id
		reason string
	}{
		"내용 변조": {func(t *testing.T, pool *pgxpool.Pool, ids []string) string {
			exec(t, pool, `UPDATE audit_log SET updated_values = '{"team_id":"forged"}' WHERE id = $1`, ids[1])
			return ids[1]
		}, "content changed"},
		"행위자 변조": {func(t *testing.T, pool *pgxpool.Pool, ids []string) string {
			exec(t, pool, `UPDATE audit_log SET changed_by = 'someone-else' WHERE id = $1`, ids[2])
			return ids[2]
		}, "content changed"},
		"해시까지 다른 기록 것으로 바꿈": {func(t *testing.T, pool *pgxpool.Pool, ids []string) string {
			exec(t, pool, `UPDATE audit_log SET row_hash = (SELECT row_hash FROM audit_log WHERE id = $2) WHERE id = $1`, ids[1], ids[2])
			return ids[1]
		}, "content changed"},
		"중간 삭제": {func(t *testing.T, pool *pgxpool.Pool, ids []string) string {
			exec(t, pool, `DELETE FROM audit_log WHERE id = $1`, ids[1])
			return ids[2]
		}, "broken link"},
		"해시 없이 끼워 넣기": {func(t *testing.T, pool *pgxpool.Pool, ids []string) string {
			exec(t, pool, `INSERT INTO audit_log (id, changed_by, action, table_name, object_id) VALUES ('forged', 'x', 'deleted', 'LiteLLM_TeamTable', 't0')`)
			return "forged"
		}, "unhashed row"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			api, pool := chainedGateway(t, 4)
			want := c.tamper(t, pool, auditIDs(t, pool))
			report := verifyChain(t, api)
			invalid, _ := report["first_invalid"].(map[string]any)
			if report["valid"] != false || invalid["id"] != want || !strings.Contains(invalid["reason"].(string), c.reason) {
				t.Errorf("검증 = %v, 기대: %s 에서 %q", report, want, c.reason)
			}
		})
	}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// 보관 기간 삭제는 가장 오래된 기록부터 지운다. 남은 첫 기록을 기준점으로 삼아 체인은 그대로 유효하다
func TestAuditChain_StaysValidAfterTheOldestRecordsArePurged(t *testing.T) {
	api, pool := chainedGateway(t, 5)
	ids := auditIDs(t, pool)
	exec(t, pool, `DELETE FROM audit_log WHERE id = ANY($1)`, ids[:2])
	report := verifyChain(t, api)
	if report["valid"] != true || report["checked"] != float64(len(ids)-2) {
		t.Errorf("앞부분을 지운 뒤 검증 = %v", report)
	}
}

// 여러 요청이 동시에 기록해도 체인은 한 줄로 이어진다(advisory lock)
func TestAuditChain_ConcurrentWritesKeepOneChain(t *testing.T) {
	h, _, _ := testGateway(t)
	api := apiClient{t: t, h: h}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() { api.ok("POST", "/tag/new", map[string]any{"name": fmt.Sprintf("tag-%d", i)}) })
	}
	wg.Wait()
	if report := verifyChain(t, api); report["valid"] != true || report["checked"] != 20.0 {
		t.Errorf("동시 기록 뒤 검증 = %v", report)
	}
}
