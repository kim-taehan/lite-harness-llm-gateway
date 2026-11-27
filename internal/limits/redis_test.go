package limits

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Redis 카운터의 장애 대응: 막지 않고 메모리로 이어서 처리하고, 연속 실패하면 잠깐 건너뛰고, 돌아오면 다시 쓴다

type redisHarness struct {
	server *miniredis.Miniredis
	store  *RedisCounters
	clock  *time.Time
	logs   *bytes.Buffer
}

func (h redisHarness) advance(d time.Duration) { *h.clock = h.clock.Add(d) }

func newRedisHarness(t *testing.T) redisHarness {
	t.Helper()
	clock := contractStart
	logs := &bytes.Buffer{}
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1, DialTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisCounters(client, func() time.Time { return clock }, slog.New(slog.NewTextHandler(logs, nil)))
	return redisHarness{server: server, store: store, clock: &clock, logs: logs}
}

func TestRedisCounters_KeepCountingInMemoryWhenRedisIsDown(t *testing.T) {
	h := newRedisHarness(t)
	ctx := t.Context()
	h.server.Close() // Redis 가 죽었다

	for want := int64(1); want <= 2; want++ {
		if got, ok := h.store.IncrementIfBelow(ctx, "rpm:k", 1, 2, time.Minute); !ok || got != want {
			t.Fatalf("Redis 가 죽었는데 요청이 막혔다: %d 번째 = %d, %v", want, got, ok)
		}
	}
	if _, ok := h.store.IncrementIfBelow(ctx, "rpm:k", 1, 2, time.Minute); ok {
		t.Error("한도를 넘었는데 통과했다(메모리 카운터로 이어서 세야 한다)")
	}
	if release, _, ok := h.store.AcquireSlot(ctx, "conc:k", 1, time.Hour); !ok {
		t.Error("동시성 슬롯이 Redis 장애로 막혔다")
	} else {
		release()
	}
	if got := strings.Count(h.logs.String(), "Redis 카운터 장애"); got != 1 {
		t.Errorf("장애 경고가 %d 번 남았다. 장애가 시작될 때 한 번만 남겨야 한다:\n%s", got, h.logs.String())
	}
}

// FailClosed 면 브레이커가 열린 동안 Unavailable 이 참이고, 열린 시간이 지나면 시험 요청을 통과시켜 복구를 알아챌 수 있다.
// 기본(FailClosed 꺼짐)은 장애여도 늘 거짓이다
func TestRedisCounters_UnavailableOnlyWhenFailClosedAndBreakerOpen(t *testing.T) {
	h := newRedisHarness(t)
	ctx := t.Context()
	h.store.FailClosed = true
	if h.store.Unavailable() {
		t.Fatal("멀쩡한데 거부 상태다")
	}
	h.server.Close()
	for i := 0; i < breakerThreshold-1; i++ {
		h.store.Get(ctx, "tpm:k", time.Minute)
	}
	if h.store.Unavailable() {
		t.Error("브레이커가 열리기 전(연속 실패 문턱 미만)에는 거부하지 않는다")
	}
	h.store.Get(ctx, "tpm:k", time.Minute)
	if !h.store.Unavailable() {
		t.Error("브레이커가 열렸는데 거부하지 않는다")
	}
	h.advance(breakerOpenFor)
	if h.store.Unavailable() {
		t.Error("열린 시간이 지났는데 계속 거부한다. 시험 요청이 통과해야 복구를 알아챈다")
	}

	h.store.FailClosed = false
	for i := 0; i < breakerThreshold; i++ {
		h.store.Get(ctx, "tpm:k", time.Minute)
	}
	if h.store.Unavailable() {
		t.Error("FailClosed 가 꺼져 있으면 장애여도 거부하지 않는다(기본 동작)")
	}
}

// 연속으로 실패하면 열린 시간 동안 Redis 를 건드리지 않고, 시간이 지나 돌아오면 다시 Redis 로 센다
func TestRedisCounters_CircuitBreakerSkipsRedisThenRecovers(t *testing.T) {
	h := newRedisHarness(t)
	ctx := t.Context()
	addr := h.server.Addr()
	h.server.Close()
	for i := 0; i < breakerThreshold; i++ {
		h.store.Get(ctx, "tpm:k", time.Minute) // 연속 실패로 브레이커가 열린다
	}

	h.server.Restart() // Redis 가 돌아왔지만 아직 열린 시간 안이다
	if h.server.Addr() != addr {
		t.Fatalf("재시작한 주소가 다르다: %s", h.server.Addr())
	}
	before := h.server.CommandCount()
	h.store.Get(ctx, "tpm:k", time.Minute)
	h.store.IncrementIfBelow(ctx, "rpm:k", 1, 5, time.Minute)
	if got := h.server.CommandCount() - before; got != 0 {
		t.Errorf("브레이커가 열려 있는데 Redis 에 명령 %d 개가 갔다", got)
	}

	h.advance(breakerOpenFor + time.Second)
	if got, ok := h.store.IncrementIfBelow(ctx, "rpm:k", 1, 5, time.Minute); !ok || got != 1 {
		t.Fatalf("복구 뒤 첫 요청 = %d, %v (Redis 값 1 이어야 한다)", got, ok)
	}
	if h.server.CommandCount() == before {
		t.Error("열린 시간이 지났는데 Redis 를 다시 쓰지 않는다")
	}
	if !strings.Contains(h.logs.String(), "Redis 카운터가 돌아왔다") {
		t.Errorf("복구 로그가 없다:\n%s", h.logs.String())
	}
}

// 클라이언트가 끊어서 생긴 오류는 Redis 장애가 아니다. 이것 때문에 브레이커가 열려 모두가 메모리로 밀려나면 안 된다
func TestRedisCounters_ClientCancelIsNotAnOutage(t *testing.T) {
	h := newRedisHarness(t)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	for i := 0; i < breakerThreshold*2; i++ {
		h.store.IncrementIfBelow(cancelled, "rpm:k", 1, 10, time.Minute)
	}
	before := h.server.CommandCount()
	if got, ok := h.store.IncrementIfBelow(t.Context(), "rpm:k", 1, 10, time.Minute); !ok || got != 1 {
		t.Fatalf("끊긴 요청들 뒤 정상 요청 = %d, %v", got, ok)
	}
	if h.server.CommandCount() == before {
		t.Error("클라이언트가 끊은 요청 때문에 브레이커가 열렸다(정상 요청이 Redis 에 가지 않았다)")
	}
	if strings.Contains(h.logs.String(), "Redis 카운터 장애") {
		t.Errorf("클라이언트가 끊은 것을 장애로 기록했다:\n%s", h.logs.String())
	}
}

// 윈도우마다 새 키를 쓰므로 옛 키가 쌓이지 않게 모든 키에 만료가 있어야 한다
func TestRedisCounters_EveryKeyExpires(t *testing.T) {
	h := newRedisHarness(t)
	ctx := t.Context()
	h.store.IncrementIfBelow(ctx, "rpm:k", 1, 5, time.Minute)
	h.store.Add(ctx, "tpm:k", 3, time.Minute)
	h.store.AcquireSlot(ctx, "conc:k", 2, 10*time.Minute)

	keys := h.server.Keys()
	if len(keys) != 3 {
		t.Fatalf("키 = %v, 3 개여야 한다", keys)
	}
	for _, key := range keys {
		if ttl := h.server.TTL(key); ttl <= 0 {
			t.Errorf("%s 에 만료가 없다(TTL=%v). 쌓이면 Redis 메모리가 샌다", key, ttl)
		}
	}
}

// 기동 때 연결을 확인한다. 주소를 잘못 적은 채 조용히 메모리 카운터로 도는 것을 막으려는 것이다
func TestOpenRedisCounters_ChecksConnectionAtStartup(t *testing.T) {
	server := miniredis.RunT(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	clock := func() time.Time { return contractStart }

	store, err := OpenRedisCounters(t.Context(), "redis://"+server.Addr()+"/0", clock, logger)
	if err != nil {
		t.Fatalf("살아 있는 Redis 에 연결하지 못했다: %v", err)
	}
	if _, ok := store.IncrementIfBelow(t.Context(), "rpm:k", 1, 1, time.Minute); !ok {
		t.Error("열린 저장소가 동작하지 않는다")
	}
	if err := store.Close(); err != nil {
		t.Errorf("닫기: %v", err)
	}

	if _, err := OpenRedisCounters(t.Context(), "http://not-redis", clock, logger); err == nil || !strings.Contains(err.Error(), "GATEWAY_REDIS_URL") {
		t.Errorf("잘못된 주소 형식: %v (GATEWAY_REDIS_URL 을 알려 줘야 한다)", err)
	}
	dead := server.Addr()
	server.Close()
	if _, err := OpenRedisCounters(t.Context(), "redis://"+dead, clock, logger); err == nil {
		t.Error("닿지 않는 Redis 인데 기동이 허용됐다")
	}
}
