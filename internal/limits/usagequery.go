package limits

import (
	"slices"
	"time"

	"llm-gateway/internal/core"
)

// UsageQuery 는 request_log 집계·조회다. 사용량 테이블은 limits 가 가지므로 집계도 여기서만 한다(설계 3장).
// 조회는 activity.go 의 Activity(묶음 합계)와 LogRows·LogCount·LogPage(요청 행) 두 갈래뿐이다
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

// Accumulate 는 key 의 합계에 v 를 더한다. 없으면 만든다. 묶음 합계를 맵에 쌓는 곳이 여럿이라 한 곳에 둔다
func Accumulate[K comparable](sums map[K]*Metrics, key K, v Metrics) {
	if sums[key] == nil {
		sums[key] = &Metrics{}
	}
	sums[key].Add(v)
}

// Tokens 는 프롬프트와 완성 토큰의 합이다
func (m Metrics) Tokens() int64 { return m.PromptTokens + m.CompletionTokens }

// Requests 는 성공과 실패를 합친 요청 수다(litellm api_requests)
func (m Metrics) Requests() int64 { return m.SuccessfulRequests + m.FailedRequests }

// DayUsage 는 하루(UTC 날짜)의 합계와 모델별 합계다
type DayUsage struct {
	Date    string // YYYY-MM-DD
	Total   Metrics
	ByModel map[string]Metrics
}

// FoldDays 는 묶음 합계를 날짜 오름차순의 하루 단위로 접는다
func FoldDays(rows []ActivityRow) []DayUsage {
	byDate := map[string]*DayUsage{}
	var dates []string
	for _, a := range rows {
		day := byDate[a.Date]
		if day == nil {
			day = &DayUsage{Date: a.Date, ByModel: map[string]Metrics{}}
			byDate[a.Date] = day
			dates = append(dates, a.Date)
		}
		day.Total.Add(a.Metrics)
		m := day.ByModel[a.Model]
		m.Add(a.Metrics)
		day.ByModel[a.Model] = m
	}
	slices.Sort(dates)
	days := make([]DayUsage, 0, len(dates))
	for _, date := range dates {
		days = append(days, *byDate[date])
	}
	return days
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
	HTTPStatus       int
}

// LogLimit 은 litellm /spend/logs 와 같은 상한이다. 이만큼이면 잘렸다고 알린다
const LogLimit = 10_000
