package limits

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"llm-gateway/internal/core"
)

const (
	// redisOpTimeout 은 Redis 호출 하나의 최대 시간이다. 요청 경로에서 부르므로 짧게 잡고, 넘으면 장애로 센다
	redisOpTimeout = 300 * time.Millisecond
	// 연속으로 이만큼 실패하면 breakerOpenFor 동안 Redis 를 건드리지 않고 메모리 카운터로 처리한다(서킷 브레이커)
	breakerThreshold = 3
	breakerOpenFor   = 5 * time.Second
)

// 검사와 증가를 한 번에 하는 스크립트들(설계 7.1). 윈도우 번호가 키에 들어 있어 시간은 스크립트가 보지 않는다
var (
	incrementIfBelowScript = redis.NewScript(`
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local amount = tonumber(ARGV[1])
if current + amount > tonumber(ARGV[2]) then return {current, 0} end
current = redis.call('INCRBY', KEYS[1], amount)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return {current, 1}`)

	addScript = redis.NewScript(`
local current = redis.call('INCRBY', KEYS[1], ARGV[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return current`)

	// 만료 시각(점수)이 지난 슬롯을 먼저 지우고 센다. 만료 시각이 지금과 같으면 만료다(메모리 구현과 같다)
	acquireSlotScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local held = redis.call('ZCARD', KEYS[1])
if held >= tonumber(ARGV[2]) then return {held, 0} end
redis.call('ZADD', KEYS[1], tonumber(ARGV[1]) + tonumber(ARGV[3]), ARGV[4])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return {held + 1, 1}`)
)

// RedisCounters 는 여러 인스턴스가 함께 보는 카운터다(설계 7.1). Redis 가 실패하면 이 인스턴스의 메모리 카운터로 이어서 처리한다.
// 그동안은 한도가 인스턴스마다 따로 세어지지만 요청을 막지는 않는다(litellm 의 기본과 같다). 진실의 원천은 PostgreSQL 이고
// 예산은 DB 의 spend 로 검사하므로 카운터가 비어도(Redis 재시작) RPM·TPM·동시성만 잠깐 느슨해진다
type RedisCounters struct {
	client   *redis.Client
	now      func() time.Time
	logger   *slog.Logger
	fallback *MemoryCounters
	breaker  *breaker
}

func NewRedisCounters(client *redis.Client, now func() time.Time, logger *slog.Logger) *RedisCounters {
	return &RedisCounters{client: client, now: now, logger: logger, fallback: NewMemoryCounters(now), breaker: &breaker{now: now}}
}

// OpenRedisCounters 는 Redis 주소(redis:// 또는 rediss://)로 연결해 카운터를 만든다. 기동 때 한 번 연결을 확인한다:
// 주소를 잘못 적은 채 조용히 메모리 카운터로 도는 것을 막으려고, 닿지 않으면 기동을 멈춘다. 운영 중 장애는 메모리로 이어서 처리한다
func OpenRedisCounters(ctx context.Context, url string, now func() time.Time, logger *slog.Logger) (*RedisCounters, error) {
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("GATEWAY_REDIS_URL 을 읽을 수 없다: %w", err)
	}
	options.DialTimeout = time.Second
	options.ReadTimeout, options.WriteTimeout = redisOpTimeout, redisOpTimeout
	options.MaxRetries = -1 // 재시도는 서킷 브레이커가 맡는다. 요청 경로에서 기다리지 않는다
	client := redis.NewClient(options)
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close() // 연결 실패를 알리는 중이라 닫기 오류는 더 알릴 것이 없다
		return nil, fmt.Errorf("Redis 에 닿지 않는다: %w", err)
	}
	return NewRedisCounters(client, now, logger), nil
}

// Close 는 Redis 연결을 닫는다
func (r *RedisCounters) Close() error { return r.client.Close() }

// windowKey 는 고정 윈도우의 키와 만료 시간이다. 윈도우 번호가 키에 있어 윈도우가 바뀌면 새 키를 쓰고, 옛 키는 만료로 사라진다
func (r *RedisCounters) windowKey(key string, window time.Duration) (string, time.Duration) {
	return fmt.Sprintf("gw:fixed:%s:%d", key, r.now().UnixMilli()/window.Milliseconds()), 2 * window
}

func (r *RedisCounters) IncrementIfBelow(ctx context.Context, key string, amount, limit int64, window time.Duration) (int64, bool) {
	if r.breaker.allow() {
		value, ok, err := r.incrementIfBelow(ctx, key, amount, limit, window)
		if r.settle(ctx, err) {
			return value, ok
		}
	}
	return r.fallback.IncrementIfBelow(ctx, key, amount, limit, window)
}

func (r *RedisCounters) incrementIfBelow(ctx context.Context, key string, amount, limit int64, window time.Duration) (int64, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	redisKey, ttl := r.windowKey(key, window)
	result, err := incrementIfBelowScript.Run(ctx, r.client, []string{redisKey}, amount, limit, ttl.Milliseconds()).Int64Slice()
	if err != nil {
		return 0, false, err
	}
	return result[0], result[1] == 1, nil
}

func (r *RedisCounters) Add(ctx context.Context, key string, amount int64, window time.Duration) {
	if r.breaker.allow() {
		if r.settle(ctx, r.add(ctx, key, amount, window)) {
			return
		}
	}
	r.fallback.Add(ctx, key, amount, window)
}

func (r *RedisCounters) add(ctx context.Context, key string, amount int64, window time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	redisKey, ttl := r.windowKey(key, window)
	return addScript.Run(ctx, r.client, []string{redisKey}, amount, ttl.Milliseconds()).Err()
}

func (r *RedisCounters) Get(ctx context.Context, key string, window time.Duration) int64 {
	if r.breaker.allow() {
		value, err := r.get(ctx, key, window)
		if r.settle(ctx, err) {
			return value
		}
	}
	return r.fallback.Get(ctx, key, window)
}

func (r *RedisCounters) get(ctx context.Context, key string, window time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	redisKey, _ := r.windowKey(key, window)
	value, err := r.client.Get(ctx, redisKey).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil // 아직 쓴 적 없는 윈도우다
	}
	return value, err
}

func (r *RedisCounters) AcquireSlot(ctx context.Context, key string, limit int64, lease time.Duration) (func(), int64, bool) {
	if r.breaker.allow() {
		release, held, ok, err := r.acquireSlot(ctx, key, limit, lease)
		if r.settle(ctx, err) {
			return release, held, ok
		}
	}
	return r.fallback.AcquireSlot(ctx, key, limit, lease)
}

func (r *RedisCounters) acquireSlot(ctx context.Context, key string, limit int64, lease time.Duration) (func(), int64, bool, error) {
	opCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	defer cancel()
	slotKey, member := "gw:slots:"+key, core.NewUUID()
	result, err := acquireSlotScript.Run(opCtx, r.client, []string{slotKey}, r.now().UnixMilli(), limit, lease.Milliseconds(), member).Int64Slice()
	if err != nil {
		return nil, 0, false, err
	}
	if result[1] != 1 {
		return nil, result[0], false, nil
	}
	return r.releaser(context.WithoutCancel(ctx), slotKey, member), result[0], true, nil
}

// releaser 는 슬롯을 반납하는 함수다. 요청이 끊긴 뒤에도 불리므로 취소와 무관한 ctx 를 쓴다.
// 반납이 실패해도 임대가 지나면 슬롯이 풀리므로 오류는 로그만 남긴다
func (r *RedisCounters) releaser(ctx context.Context, slotKey, member string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(ctx, redisOpTimeout)
			defer cancel()
			if err := r.client.ZRem(ctx, slotKey, member).Err(); err != nil {
				r.logger.Warn("동시성 슬롯 반납 실패(임대가 지나면 풀린다)", "err", err)
			}
		})
	}
}

// settle 은 Redis 호출의 결과를 서킷 브레이커에 알리고, 그 결과를 써도 되는지 돌려준다.
// 클라이언트가 끊어서 생긴 오류는 Redis 의 잘못이 아니라서 장애로 세지 않는다
func (r *RedisCounters) settle(ctx context.Context, err error) bool {
	switch {
	case err == nil:
		if r.breaker.success() {
			r.logger.Info("Redis 카운터가 돌아왔다: 다시 Redis 로 센다")
		}
		return true
	case ctx.Err() != nil:
		return false
	default:
		if r.breaker.failure() {
			r.logger.Warn("Redis 카운터 장애: 메모리 카운터로 이어서 처리한다(한도를 이 인스턴스만 센다)",
				"err", err, "retry_after", breakerOpenFor.String())
		}
		return false
	}
}

// breaker 는 Redis 가 연속으로 실패하면 잠깐 건너뛰게 하는 서킷 브레이커다. 열린 시간이 지나면 한 번 시도해 보고,
// 실패하면 바로 다시 연다(연속 실패 수가 이미 문턱에 있기 때문이다)
type breaker struct {
	now       func() time.Time
	mu        sync.Mutex
	failures  int
	openUntil time.Time
	degraded  bool
}

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.now().Before(b.openUntil)
}

// success 는 장애에서 돌아온 순간이면 참이다(로그를 한 번만 남기려고)
func (b *breaker) success() (recovered bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	recovered, b.degraded = b.degraded, false
	return recovered
}

// failure 는 이번 실패로 장애 상태가 시작된 순간이면 참이다(로그를 한 번만 남기려고)
func (b *breaker) failure() (degradedNow bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures < breakerThreshold {
		return false
	}
	b.openUntil = b.now().Add(breakerOpenFor)
	degradedNow, b.degraded = !b.degraded, true
	return degradedNow
}
