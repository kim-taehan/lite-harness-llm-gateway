package limits

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// UsageEvent 는 요청 하나의 사용량이다. 응답이 끝나거나 끊긴 뒤 추론 경로가 넣는다
type UsageEvent struct {
	RequestID        string
	CreatedAt        time.Time
	KeyToken         string
	UserID           string
	TeamID           string
	ProjectID        string   // 키가 속한 프로젝트. 프로젝트 사용액에 더한다
	OrganizationID   string   // 키의 팀이 속한 조직. 조직 사용액에 더한다
	EndUserID        string   // 요청이 밝힌 고객. 사용액을 고객 한도에 더하는 데만 쓰고 request_log 에는 남기지 않는다
	Tags             []string // 요청이 붙인 태그. 마찬가지로 등록한 태그의 사용액에만 더한다
	Provider         string   // 요청을 처리한 배포의 provider. 예산을 정한 프로바이더의 사용액에만 더한다
	Model            string
	DeploymentID     string
	PromptTokens     int
	CompletionTokens int
	Estimated        bool // 업스트림 usage 를 받기 전에 끊겨 받은 청크로 어림했다(설계 4.2)
	Cost             float64
	Status           string // success · error · cancelled
	HTTPStatus       int
	ErrorCode        string
	LatencyMS        int
	TTFTMS           *int
}

// UsageOptions 는 배치 설정이다(설계 11.1 GATEWAY_USAGE_*)
type UsageOptions struct {
	BatchSize     int
	FlushInterval time.Duration
	QueueCapacity int
}

// UsageRecorder 는 사용량을 채널에 받아 배치로 쓴다. Record 는 막히지 않는다:
// 채널이 가득 차면 경고 로그를 남기고 버린다(설계 2장 큐 초과). 비정상 종료 시 최대 한 배치 주기만큼 잃을 수 있다(허용한 오차)
type UsageRecorder struct {
	db      *core.DB
	logger  *slog.Logger
	opts    UsageOptions
	events  chan UsageEvent
	dropped atomic.Int64
	once    sync.Once
	done    chan struct{}
}

func NewUsageRecorder(db *core.DB, logger *slog.Logger, opts UsageOptions) *UsageRecorder {
	return &UsageRecorder{db: db, logger: logger, opts: opts, events: make(chan UsageEvent, opts.QueueCapacity), done: make(chan struct{})}
}

// Record 는 채널에 넣고 바로 돌아온다
func (u *UsageRecorder) Record(e UsageEvent) {
	select {
	case u.events <- e:
	default:
		n := u.dropped.Add(1)
		u.logger.Warn("사용량 채널이 가득 차 기록을 버렸다", "request_id", e.RequestID, "dropped_total", n)
	}
}

// Run 은 배치 쓰기 goroutine 이다. Close 로 채널을 닫으면 남은 것을 마지막 배치로 쓰고 끝난다.
// ctx 는 DB 쓰기에만 쓴다. 정상 종료 중에도 마지막 배치를 쓰도록 조립 지점은 Run 이 끝난 뒤에 ctx 를 취소한다
func (u *UsageRecorder) Run(ctx context.Context) {
	defer close(u.done)
	ticker := time.NewTicker(u.opts.FlushInterval)
	defer ticker.Stop()
	batch := make([]UsageEvent, 0, u.opts.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := u.write(ctx, batch); err != nil {
			u.logger.Error("사용량 배치 쓰기 실패", "count", len(batch), "err", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case e, open := <-u.events:
			if !open {
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= u.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Queued 는 아직 쓰지 않은 채 채널에 쌓인 건수다(지표용)
func (u *UsageRecorder) Queued() int { return len(u.events) }

// Dropped 는 채널이 가득 차 버린 누적 건수다(지표용)
func (u *UsageRecorder) Dropped() int64 { return u.dropped.Load() }

// Close 는 새 기록을 받지 않고 남은 배치를 다 쓸 때까지 기다린다(설계 3.3 정상 종료 3번)
func (u *UsageRecorder) Close() {
	u.once.Do(func() { close(u.events) })
	<-u.done
}

// write 는 request_log 를 넣고 키·팀·사용자 spend 를 더한다. 한 트랜잭션이다.
// spend 는 spend = spend + ? 로 더해 여러 인스턴스가 같은 행을 갱신해도 맞는다(설계 7장)
func (u *UsageRecorder) write(ctx context.Context, batch []UsageEvent) error {
	return u.db.InTx(ctx, func(ctx context.Context) error {
		q := u.db.Q(ctx)
		rows := make([][]any, len(batch))
		spend := map[[2]string]float64{}
		modelSpend := map[[3]string]float64{}
		for i, e := range batch {
			source := "provider"
			if e.Estimated {
				source = "estimate"
			}
			rows[i] = []any{e.RequestID, e.CreatedAt, nullable(e.KeyToken), nullable(e.UserID), nullable(e.TeamID), e.Model, nullable(e.DeploymentID),
				e.PromptTokens, e.CompletionTokens, source, e.Cost, e.Status, e.HTTPStatus, nullable(e.ErrorCode), e.LatencyMS, e.TTFTMS}
			if e.Cost > 0 {
				for scope, id := range map[Scope]string{ScopeKey: e.KeyToken, ScopeTeam: e.TeamID, ScopeUser: e.UserID,
					ScopeTeamMember: MemberTarget(e.TeamID, e.UserID), ScopeEndUser: e.EndUserID, ScopeProject: e.ProjectID, ScopeOrganization: e.OrganizationID} {
					if id != "" {
						spend[[2]string{string(scope), id}] += e.Cost
						modelSpend[[3]string{string(scope), id, e.Model}] += e.Cost
					}
				}
				if e.Provider != "" {
					spend[[2]string{string(ScopeProvider), e.Provider}] += e.Cost
				}
				for _, tag := range e.Tags {
					spend[[2]string{string(ScopeTag), tag}] += e.Cost
					modelSpend[[3]string{string(ScopeTag), tag, e.Model}] += e.Cost
				}
			}
		}
		tx, ok := q.(pgx.Tx)
		if !ok {
			return fmt.Errorf("트랜잭션 밖에서 불렸다")
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"request_log"}, []string{"request_id", "created_at", "key_token", "user_id", "team_id", "model",
			"deployment_id", "prompt_tokens", "completion_tokens", "usage_source", "cost", "status", "http_status", "error_code", "latency_ms", "ttft_ms"},
			pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("request_log: %w", err)
		}
		for target, amount := range spend {
			// 고객과 태그는 호출자가 요청에 적는 값이라, 등록한 것만 행을 만든다(아무 값이나 보내 행을 늘리지 못하게)
			if _, err := q.Exec(ctx, `
				INSERT INTO limit_policy (scope, target_id, spend)
				SELECT $1::text, $2::text, $3::numeric
				WHERE ($1::text <> 'end_user' OR EXISTS (SELECT 1 FROM customer WHERE user_id = $2::text))
				  AND ($1::text <> 'tag' OR EXISTS (SELECT 1 FROM tag WHERE name = $2::text))
				  AND ($1::text <> 'provider' OR EXISTS (SELECT 1 FROM limit_policy WHERE scope = 'provider' AND target_id = $2::text))
				ON CONFLICT (scope, target_id) DO UPDATE SET spend = limit_policy.spend + EXCLUDED.spend, updated_at = now()`,
				target[0], target[1], amount); err != nil {
				return fmt.Errorf("spend: %w", err)
			}
		}
		// 모델별 한도가 이미 있는 (대상, 모델)에만 더한다. 없으면 갱신되는 행이 없다
		for target, amount := range modelSpend {
			if _, err := q.Exec(ctx, `
				UPDATE limit_model_policy SET spend = spend + $4, updated_at = now() WHERE scope = $1 AND target_id = $2 AND model = $3`,
				target[0], target[1], target[2], amount); err != nil {
				return fmt.Errorf("모델별 spend: %w", err)
			}
		}
		return nil
	})
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
