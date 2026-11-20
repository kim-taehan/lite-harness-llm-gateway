package limits

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// UsageQuery 는 request_log 집계다. 사용량 테이블은 limits 가 가지므로 집계도 여기서만 한다(설계 3장)
type UsageQuery struct {
	db *core.DB
}

func NewUsageQuery(db *core.DB) *UsageQuery { return &UsageQuery{db: db} }

// Metrics 는 기간·모델 하나의 합계다(litellm SpendMetrics 의 우리가 가진 칸)
type Metrics struct {
	Spend               float64
	PromptTokens        int64
	CompletionTokens    int64
	SuccessfulRequests  int64
	FailedRequests      int64
	TotalResponseTimeMS int64
	TimedRequests       int64
}

// Add 는 o 를 더한다
func (m *Metrics) Add(o Metrics) {
	m.Spend += o.Spend
	m.PromptTokens += o.PromptTokens
	m.CompletionTokens += o.CompletionTokens
	m.SuccessfulRequests += o.SuccessfulRequests
	m.FailedRequests += o.FailedRequests
	m.TotalResponseTimeMS += o.TotalResponseTimeMS
	m.TimedRequests += o.TimedRequests
}

// DayUsage 는 하루(UTC 날짜)의 합계와 모델별 합계다
type DayUsage struct {
	Date    string // YYYY-MM-DD
	Total   Metrics
	ByModel map[string]Metrics
}

// Daily 는 start..end(양 끝 포함, UTC 날짜) 의 날짜별 사용량이다. userID 가 nil 이면 전체다
func (q *UsageQuery) Daily(ctx context.Context, userID *string, start, end time.Time) ([]DayUsage, error) {
	rows, err := q.db.Q(ctx).Query(ctx, `
		SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day, model,
		       coalesce(sum(cost), 0)::float8, sum(prompt_tokens), sum(completion_tokens),
		       count(*) FILTER (WHERE status = 'success'), count(*) FILTER (WHERE status <> 'success'),
		       coalesce(sum(latency_ms), 0), count(*)
		FROM request_log
		WHERE created_at >= $1 AND created_at < $2 AND ($3::text IS NULL OR user_id = $3)
		GROUP BY day, model ORDER BY day, model`, start, end.AddDate(0, 0, 1), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var days []DayUsage
	for rows.Next() {
		var day, model string
		var m Metrics
		if err := rows.Scan(&day, &model, &m.Spend, &m.PromptTokens, &m.CompletionTokens, &m.SuccessfulRequests, &m.FailedRequests,
			&m.TotalResponseTimeMS, &m.TimedRequests); err != nil {
			return nil, err
		}
		if len(days) == 0 || days[len(days)-1].Date != day {
			days = append(days, DayUsage{Date: day, ByModel: map[string]Metrics{}})
		}
		d := &days[len(days)-1]
		d.Total.Add(m)
		d.ByModel[model] = m
	}
	return days, rows.Err()
}

// LogRow 는 요청 하나의 기록이다
type LogRow struct {
	RequestID        string
	CreatedAt        time.Time
	KeyToken         *string
	UserID           *string
	TeamID           *string
	Model            string
	DeploymentID     *string
	PromptTokens     int64
	CompletionTokens int64
	Cost             float64
	Status           string
	LatencyMS        int64
	TTFTMS           *int64
}

// LogFilter 는 /spend/logs 의 조건이다. 날짜는 litellm 처럼 start 자정 ≤ 시각 ≤ end 자정이다(end 날짜 자체는 사실상 빠진다, K 9.3)
type LogFilter struct {
	UserID    *string
	KeyToken  *string
	RequestID *string
	Start     *time.Time
	End       *time.Time
}

// LogLimit 은 litellm 과 같은 상한이다. 이만큼이면 잘렸다고 알린다
const LogLimit = 10_000

func (q *UsageQuery) Logs(ctx context.Context, f LogFilter) ([]LogRow, error) {
	rows, err := q.db.Q(ctx).Query(ctx, `
		SELECT request_id, created_at, key_token, user_id, team_id, model, deployment_id, prompt_tokens, completion_tokens,
		       cost::float8, status, latency_ms, ttft_ms
		FROM request_log
		WHERE ($1::text IS NULL OR user_id = $1) AND ($2::text IS NULL OR key_token = $2) AND ($3::text IS NULL OR request_id = $3)
		  AND ($4::timestamptz IS NULL OR created_at >= $4) AND ($5::timestamptz IS NULL OR created_at <= $5)
		ORDER BY created_at DESC LIMIT $6`, f.UserID, f.KeyToken, f.RequestID, f.Start, f.End, LogLimit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (LogRow, error) {
		var r LogRow
		err := row.Scan(&r.RequestID, &r.CreatedAt, &r.KeyToken, &r.UserID, &r.TeamID, &r.Model, &r.DeploymentID, &r.PromptTokens,
			&r.CompletionTokens, &r.Cost, &r.Status, &r.LatencyMS, &r.TTFTMS)
		return r, err
	})
}
