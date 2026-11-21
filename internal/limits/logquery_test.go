package limits

import (
	"slices"
	"testing"
	"time"

	"llm-gateway/internal/core/dbtest"
)

func newLogQuery(t *testing.T, rows map[string]string) *UsageQuery {
	t.Helper()
	db, pool := dbtest.Open(t)
	for requestID, userID := range rows {
		status := "success"
		if requestID == "r-fail" {
			status = "error"
		}
		_, err := pool.Exec(t.Context(), `INSERT INTO request_log (request_id, created_at, user_id, model, usage_source, status, http_status, latency_ms)
			VALUES ($1, $2, $3, 'm', 'provider', $4, 200, 10)`, requestID, fixedNow, userID, status)
		if err != nil {
			t.Fatal(err)
		}
	}
	return NewUsageQuery(db)
}

func TestLogUsers_SearchTreatsWildcardsAsText(t *testing.T) {
	q := newLogQuery(t, map[string]string{"r1": "a_b", "r2": "axb", "r3": "100%"})
	start, end := fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour)
	for search, want := range map[string][]string{"a_b": {"a_b"}, "%": {"100%"}, "b": {"a_b", "axb"}} {
		got, _, err := q.LogUsers(t.Context(), start, end, &search, nil, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("search %q = %v, want %v", search, got, want)
		}
	}
}

func TestLogRows_FiltersCombine(t *testing.T) {
	q := newLogQuery(t, map[string]string{"r-ok": "u1", "r-fail": "u1", "r-other": "u2"})
	failed, user, search := true, "u1", "r-ok"
	cases := map[string]LogQuery{
		"실패만":                {Failed: &failed, Limit: 10},
		"성공만(사용자 u1)":        {Failed: new(bool), UserID: &user, Limit: 10},
		"검색은 요청 id 와 일치":     {Search: &search, Limit: 10},
		"빈 키 목록은 아무것도 안 맞는다": {KeyTokens: []string{}, Limit: 10},
	}
	want := map[string]int{"실패만": 1, "성공만(사용자 u1)": 1, "검색은 요청 id 와 일치": 1, "빈 키 목록은 아무것도 안 맞는다": 0}
	for name, lq := range cases {
		rows, err := q.LogRows(t.Context(), lq)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != want[name] {
			t.Errorf("%s: %d 행, want %d", name, len(rows), want[name])
		}
	}
}
