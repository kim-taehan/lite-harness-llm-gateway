package limits

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// CounterStore 계약: 메모리와 Redis 구현이 같은 시험을 통과해야 한다. 한도 검사의 의미가 구현마다 달라지면 안 된다

type counterFixture struct {
	store CounterStore
	clock *time.Time
}

func (f counterFixture) advance(d time.Duration) { *f.clock = f.clock.Add(d) }

var contractStart = time.Date(2026, 11, 21, 9, 0, 30, 0, time.UTC)

func memoryFixture(t *testing.T) counterFixture {
	t.Helper()
	clock := contractStart
	return counterFixture{store: NewMemoryCounters(func() time.Time { return clock }), clock: &clock}
}

func redisFixture(t *testing.T) counterFixture {
	t.Helper()
	clock := contractStart
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisCounters(client, func() time.Time { return clock }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return counterFixture{store: store, clock: &clock}
}

func TestCounterStoreContract(t *testing.T) {
	for name, newFixture := range map[string]func(*testing.T) counterFixture{"memory": memoryFixture, "redis": redisFixture} {
		t.Run(name, func(t *testing.T) {
			for _, c := range []struct {
				name string
				run  func(*testing.T, counterFixture)
			}{
				{"한도까지만 올리고 넘으면 올리지 않는다", contractIncrementStopsAtLimit},
				{"한 번에 여러 개를 올릴 때도 넘으면 통째로 거절한다", contractIncrementByAmount},
				{"윈도우가 바뀌면 처음부터 센다", contractWindowRollover},
				{"Add 와 Get 은 같은 윈도우의 값을 본다", contractAddAndGet},
				{"키마다 따로 센다", contractKeysAreIndependent},
				{"동시성 슬롯은 한도까지만 잡고 반납하면 다시 잡힌다", contractSlotsRespectLimit},
				{"반납을 두 번 불러도 한 칸만 비운다", contractReleaseIsIdempotent},
				{"임대가 지난 슬롯은 저절로 비워진다", contractSlotLeaseExpires},
				{"요청 ctx 가 취소된 뒤에도 반납된다", contractReleaseSurvivesCancel},
			} {
				t.Run(c.name, func(t *testing.T) { c.run(t, newFixture(t)) })
			}
		})
	}
}

func contractIncrementStopsAtLimit(t *testing.T, f counterFixture) {
	ctx := t.Context()
	for want := int64(1); want <= 2; want++ {
		if got, ok := f.store.IncrementIfBelow(ctx, "rpm:k", 1, 2, time.Minute); !ok || got != want {
			t.Fatalf("%d 번째: %d, %v", want, got, ok)
		}
	}
	if got, ok := f.store.IncrementIfBelow(ctx, "rpm:k", 1, 2, time.Minute); ok || got != 2 {
		t.Errorf("한도를 넘었는데 = %d, %v (값은 2 그대로여야 한다)", got, ok)
	}
	if got := f.store.Get(ctx, "rpm:k", time.Minute); got != 2 {
		t.Errorf("거절된 시도가 값을 올렸다: %d", got)
	}
}

func contractIncrementByAmount(t *testing.T, f counterFixture) {
	ctx := t.Context()
	if got, ok := f.store.IncrementIfBelow(ctx, "tok:k", 3, 5, time.Minute); !ok || got != 3 {
		t.Fatalf("첫 번째: %d, %v", got, ok)
	}
	if got, ok := f.store.IncrementIfBelow(ctx, "tok:k", 3, 5, time.Minute); ok || got != 3 {
		t.Errorf("3+3 > 5 인데 = %d, %v", got, ok)
	}
}

func contractWindowRollover(t *testing.T, f counterFixture) {
	ctx := t.Context()
	f.store.IncrementIfBelow(ctx, "rpm:k", 1, 1, time.Minute)
	if _, ok := f.store.IncrementIfBelow(ctx, "rpm:k", 1, 1, time.Minute); ok {
		t.Fatal("같은 윈도우에서 한도를 넘었는데 통과했다")
	}
	f.advance(time.Minute) // 09:00:30 → 09:01:30, 다음 윈도우
	if got, ok := f.store.IncrementIfBelow(ctx, "rpm:k", 1, 1, time.Minute); !ok || got != 1 {
		t.Errorf("새 윈도우인데 = %d, %v", got, ok)
	}
}

func contractAddAndGet(t *testing.T, f counterFixture) {
	ctx := t.Context()
	f.store.Add(ctx, "tpm:k", 7, time.Minute)
	f.store.Add(ctx, "tpm:k", 5, time.Minute)
	if got := f.store.Get(ctx, "tpm:k", time.Minute); got != 12 {
		t.Errorf("Get = %d, 12 여야 한다", got)
	}
	f.advance(time.Minute)
	if got := f.store.Get(ctx, "tpm:k", time.Minute); got != 0 {
		t.Errorf("새 윈도우의 Get = %d, 0 이어야 한다", got)
	}
	if got := f.store.Get(ctx, "tpm:never-written", time.Minute); got != 0 {
		t.Errorf("쓴 적 없는 키 = %d", got)
	}
}

func contractKeysAreIndependent(t *testing.T, f counterFixture) {
	ctx := t.Context()
	f.store.IncrementIfBelow(ctx, "rpm:a", 1, 1, time.Minute)
	if got, ok := f.store.IncrementIfBelow(ctx, "rpm:b", 1, 1, time.Minute); !ok || got != 1 {
		t.Errorf("다른 키가 영향을 받았다: %d, %v", got, ok)
	}
}

func contractSlotsRespectLimit(t *testing.T, f counterFixture) {
	ctx := t.Context()
	var releases []func()
	for want := int64(1); want <= 2; want++ {
		release, held, ok := f.store.AcquireSlot(ctx, "conc:k", 2, time.Hour)
		if !ok || held != want {
			t.Fatalf("%d 번째 슬롯: held=%d ok=%v", want, held, ok)
		}
		releases = append(releases, release)
	}
	if _, held, ok := f.store.AcquireSlot(ctx, "conc:k", 2, time.Hour); ok || held != 2 {
		t.Errorf("한도를 넘었는데 잡혔다: held=%d ok=%v", held, ok)
	}
	releases[0]()
	if _, held, ok := f.store.AcquireSlot(ctx, "conc:k", 2, time.Hour); !ok || held != 2 {
		t.Errorf("반납했는데 다시 못 잡는다: held=%d ok=%v", held, ok)
	}
}

func contractReleaseIsIdempotent(t *testing.T, f counterFixture) {
	ctx := t.Context()
	first, _, _ := f.store.AcquireSlot(ctx, "conc:k", 2, time.Hour)
	f.store.AcquireSlot(ctx, "conc:k", 2, time.Hour)
	first()
	first() // 두 번 불러도 다른 슬롯을 비우면 안 된다
	if _, held, ok := f.store.AcquireSlot(ctx, "conc:k", 2, time.Hour); !ok || held != 2 {
		t.Fatalf("한 칸이 비었어야 한다: held=%d ok=%v", held, ok)
	}
	if _, held, ok := f.store.AcquireSlot(ctx, "conc:k", 2, time.Hour); ok {
		t.Errorf("반납을 두 번 불러 두 칸이 비었다: held=%d", held)
	}
}

func contractSlotLeaseExpires(t *testing.T, f counterFixture) {
	ctx := t.Context()
	if _, _, ok := f.store.AcquireSlot(ctx, "conc:k", 1, 10*time.Second); !ok {
		t.Fatal("첫 슬롯을 못 잡았다")
	}
	if _, _, ok := f.store.AcquireSlot(ctx, "conc:k", 1, 10*time.Second); ok {
		t.Fatal("한도 1 인데 두 번째가 잡혔다")
	}
	f.advance(11 * time.Second) // 반납 없이 인스턴스가 죽은 경우
	if _, _, ok := f.store.AcquireSlot(ctx, "conc:k", 1, 10*time.Second); !ok {
		t.Error("임대가 지났는데 슬롯이 안 비워진다")
	}
}

func contractReleaseSurvivesCancel(t *testing.T, f counterFixture) {
	ctx, cancel := context.WithCancel(t.Context())
	release, _, ok := f.store.AcquireSlot(ctx, "conc:k", 1, time.Hour)
	if !ok {
		t.Fatal("슬롯을 못 잡았다")
	}
	cancel() // 클라이언트가 끊었다
	release()
	if _, _, ok := f.store.AcquireSlot(t.Context(), "conc:k", 1, time.Hour); !ok {
		t.Error("요청이 취소된 뒤 반납이 안 돼 슬롯이 영원히 잡혀 있다")
	}
}
