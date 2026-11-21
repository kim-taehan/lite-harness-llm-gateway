package limits

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// 대시보드 사용량·로그 화면용 조회(N 문서). litellm 은 일별 집계 표를 미리 쌓지만 우리는 request_log 를 그때그때 묶는다.
// 키 별명·팀 이름·제공자 같은 다른 모듈의 값은 여기서 조인하지 않는다. admin 이 각 모듈 API 로 붙인다(설계 3장 모듈 경계)

// ActivityRow 는 (UTC 날짜, 모델 그룹, 배포, 키, 팀, 사용자) 하나의 합계다. 사용량 화면의 모든 분해를 이것으로 만든다
type ActivityRow struct {
	Date         string // YYYY-MM-DD
	Model        string // 모델 그룹 이름
	DeploymentID *string
	KeyToken     *string
	TeamID       *string
	UserID       *string
	Metrics      Metrics
}

// ActivityFilter 는 조회 범위다. 슬라이스가 nil 이면 거르지 않고, 비어 있으면 아무것도 맞지 않는다
type ActivityFilter struct {
	Start, End     time.Time // UTC 날짜, 양 끝 포함
	UserID         *string
	TeamIDs        []string
	ExcludeTeamIDs []string
	KeyTokens      []string
}

// Activity 는 기간의 묶음 합계다. litellm 처럼 응답 시간은 성공한 요청만 센다(N 2.1)
func (q *UsageQuery) Activity(ctx context.Context, f ActivityFilter) ([]ActivityRow, error) {
	rows, err := q.db.Q(ctx).Query(ctx, `
		SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), model, deployment_id, key_token, team_id, user_id,
		       coalesce(sum(cost), 0)::float8, sum(prompt_tokens), sum(completion_tokens),
		       count(*) FILTER (WHERE status = 'success'), count(*) FILTER (WHERE status <> 'success'),
		       coalesce(sum(latency_ms) FILTER (WHERE status = 'success'), 0), count(*) FILTER (WHERE status = 'success')
		FROM request_log
		WHERE created_at >= $1 AND created_at < $2
		  AND ($3::text IS NULL OR user_id = $3)
		  AND ($4::text[] IS NULL OR team_id = ANY($4))
		  AND ($5::text[] IS NULL OR team_id IS NULL OR NOT (team_id = ANY($5)))
		  AND ($6::text[] IS NULL OR key_token = ANY($6))
		GROUP BY 1, 2, 3, 4, 5, 6`,
		f.Start, f.End.AddDate(0, 0, 1), f.UserID, f.TeamIDs, f.ExcludeTeamIDs, f.KeyTokens)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ActivityRow, error) {
		var a ActivityRow
		m := &a.Metrics
		err := row.Scan(&a.Date, &a.Model, &a.DeploymentID, &a.KeyToken, &a.TeamID, &a.UserID, &m.Spend, &m.PromptTokens, &m.CompletionTokens,
			&m.SuccessfulRequests, &m.FailedRequests, &m.TotalResponseTimeMS, &m.TimedRequests)
		return a, err
	})
}

// LogScope 는 비관리자가 볼 수 있는 로그다: 자기 요청 또는 열람할 수 있는 팀의 요청(N 6.4)
type LogScope struct {
	UserID  string
	TeamIDs []string
}

// LogQuery 는 /spend/logs/ui 의 조건이다. nil 은 거르지 않는다
type LogQuery struct {
	Start, End    *time.Time // 양 끝 포함
	RequestID     *string
	UserID        *string
	TeamID        *string
	KeyToken      *string
	KeyTokens     []string // 키 별명 필터를 admin 이 키 해시로 바꿔 준다
	DeploymentIDs []string // 모델 필터를 admin 이 배포 id 로 바꿔 준다
	Model         *string  // 모델 그룹 이름
	Failed        *bool
	HTTPStatus    *int
	Search        *string
	Scope         *LogScope
	SortBy        string // created_at·cost·total_tokens·latency_ms·ttft_ms·ended_at
	Desc          bool
	Offset, Limit int
}

// MaxLogTotal 은 litellm 처럼 개수를 이만큼에서 자른다(N 6.2)
const MaxLogTotal = 10_000

var logSortColumns = map[string]string{
	"created_at": "created_at", "cost": "cost", "total_tokens": "(prompt_tokens + completion_tokens)",
	"latency_ms": "latency_ms", "ttft_ms": "ttft_ms", "ended_at": "(created_at + latency_ms * interval '1 millisecond')", "model": "model",
}

const logColumns = `request_id, created_at, key_token, user_id, team_id, model, deployment_id, prompt_tokens, completion_tokens,
	cost::float8, status, latency_ms, ttft_ms, http_status`

func scanLog(row pgx.CollectableRow) (LogRow, error) {
	var r LogRow
	err := row.Scan(&r.RequestID, &r.CreatedAt, &r.KeyToken, &r.UserID, &r.TeamID, &r.Model, &r.DeploymentID, &r.PromptTokens,
		&r.CompletionTokens, &r.Cost, &r.Status, &r.LatencyMS, &r.TTFTMS, &r.HTTPStatus)
	return r, err
}

// LogRows 는 조건에 맞는 요청 행을 정렬해 Offset 부터 Limit 개 읽는다
func (q *UsageQuery) LogRows(ctx context.Context, lq LogQuery) ([]LogRow, error) {
	where, args := logWhere(lq)
	order, ok := logSortColumns[lq.SortBy]
	if !ok {
		order = "created_at"
	}
	dir := "ASC"
	if lq.Desc {
		dir = "DESC"
	}
	args = append(args, lq.Limit, lq.Offset)
	rows, err := q.db.Q(ctx).Query(ctx, "SELECT "+logColumns+" FROM request_log "+where+
		" ORDER BY "+order+" "+dir+" NULLS LAST, request_id "+dir+
		" LIMIT $"+strconv.Itoa(len(args)-1)+" OFFSET $"+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanLog)
}

// LogCount 는 조건에 맞는 행 수다. litellm 처럼 MaxLogTotal 에서 자른다
func (q *UsageQuery) LogCount(ctx context.Context, lq LogQuery) (int, error) {
	where, args := logWhere(lq)
	var total int
	err := q.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM (SELECT 1 FROM request_log "+where+" LIMIT "+strconv.Itoa(MaxLogTotal)+") t", args...).Scan(&total)
	return total, err
}

// LogPage 는 한 쪽의 행과 전체 개수(MaxLogTotal 에서 자름), 다음 쪽이 있는지다
func (q *UsageQuery) LogPage(ctx context.Context, lq LogQuery) ([]LogRow, int, bool, error) {
	total, err := q.LogCount(ctx, lq)
	if err != nil {
		return nil, 0, false, err
	}
	probe := lq
	probe.Limit = lq.Limit + 1 // 하나 더 읽어 다음 쪽이 있는지 안다
	logs, err := q.LogRows(ctx, probe)
	if err != nil {
		return nil, 0, false, err
	}
	hasMore := len(logs) > lq.Limit
	if hasMore {
		logs = logs[:lq.Limit]
	}
	return logs, total, hasMore, nil
}

func logWhere(lq LogQuery) (string, []any) {
	var failed *string
	if lq.Failed != nil {
		s := "success"
		if *lq.Failed {
			s = "failure"
		}
		failed = &s
	}
	var scopeUser *string
	var scopeTeams []string
	if lq.Scope != nil {
		scopeUser, scopeTeams = &lq.Scope.UserID, lq.Scope.TeamIDs
		if scopeTeams == nil {
			scopeTeams = []string{}
		}
	}
	return `WHERE ($1::timestamptz IS NULL OR created_at >= $1) AND ($2::timestamptz IS NULL OR created_at <= $2)
		  AND ($3::text IS NULL OR request_id = $3) AND ($4::text IS NULL OR user_id = $4) AND ($5::text IS NULL OR team_id = $5)
		  AND ($6::text IS NULL OR key_token = $6) AND ($7::text[] IS NULL OR deployment_id = ANY($7)) AND ($8::text IS NULL OR model = $8)
		  AND ($9::text IS NULL OR (status = 'success') = ($9 = 'success')) AND ($10::int IS NULL OR http_status = $10)
		  AND ($11::text IS NULL OR $11 IN (request_id, key_token, team_id, user_id, deployment_id))
		  AND ($12::text IS NULL OR user_id = $12 OR team_id = ANY($13)) AND ($14::text[] IS NULL OR key_token = ANY($14))`,
		[]any{lq.Start, lq.End, lq.RequestID, lq.UserID, lq.TeamID, lq.KeyToken, lq.DeploymentIDs, lq.Model, failed, lq.HTTPStatus,
			lq.Search, scopeUser, scopeTeams, lq.KeyTokens}
}

// LogUsers 는 기간 안 요청의 사용자 id 다(로그 화면 사용자 필터). litellm 처럼 최근 MaxLogTotal 행 안에서 고르고 오름차순으로 쪽을 나눈다
func (q *UsageQuery) LogUsers(ctx context.Context, start, end time.Time, search *string, scope *LogScope, offset, limit int) ([]string, bool, error) {
	where, args := logWhere(LogQuery{Start: &start, End: &end, Scope: scope})
	args = append(args, search, limit+1, offset)
	n := len(args)
	rows, err := q.db.Q(ctx).Query(ctx, `
		SELECT DISTINCT user_id FROM (SELECT user_id FROM request_log `+where+` AND user_id IS NOT NULL AND user_id <> ''
		                              ORDER BY created_at DESC LIMIT `+strconv.Itoa(MaxLogTotal)+`) recent
		WHERE ($`+strconv.Itoa(n-2)+`::text IS NULL OR user_id ILIKE '%' || $`+strconv.Itoa(n-2)+` || '%')
		ORDER BY user_id LIMIT $`+strconv.Itoa(n-1)+` OFFSET $`+strconv.Itoa(n), args...)
	if err != nil {
		return nil, false, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, false, err
	}
	if len(ids) > limit {
		return ids[:limit], true, nil
	}
	return ids, false, nil
}
