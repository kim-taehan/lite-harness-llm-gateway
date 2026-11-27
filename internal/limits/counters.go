package limits

import (
	"context"
	"sync"
	"time"
)

// CounterStore 는 인스턴스가 함께 보는 카운터다(설계 7.1). 메모리 구현은 인스턴스 1개 전용이고,
// 여러 대로 늘릴 때는 같은 인터페이스의 Redis 구현을 쓴다(Lua 로 검사와 증가를 한 번에).
// ctx 는 Redis 호출을 요청과 함께 취소하려고 받는다. 메모리 구현은 쓰지 않는다
type CounterStore interface {
	// IncrementIfBelow 는 window 단위 고정 윈도우 카운터를 amount 만큼 올린다. 올린 뒤 값이 limit 을 넘으면 올리지 않고 false 다
	IncrementIfBelow(ctx context.Context, key string, amount, limit int64, window time.Duration) (int64, bool)
	// Add 는 window 단위 카운터에 더한다(응답 뒤 토큰)
	Add(ctx context.Context, key string, amount int64, window time.Duration)
	// Get 은 지금 윈도우의 값이다
	Get(ctx context.Context, key string, window time.Duration) int64
	// AcquireSlot 은 동시성 슬롯을 lease 동안 잡는다. 인스턴스가 죽어도 lease 가 지나면 풀린다(설계 7.1).
	// held 는 잡은 뒤(실패면 잡혀 있던) 슬롯 수다. release 는 요청이 끊긴 뒤에도 불리므로 구현이 ctx 취소와 무관하게 반납해야 한다
	AcquireSlot(ctx context.Context, key string, limit int64, lease time.Duration) (release func(), held int64, ok bool)
}

// MemoryCounters 는 프로세스 안 카운터다
type MemoryCounters struct {
	now   func() time.Time
	mu    sync.Mutex
	fixed map[string]windowCount
	slots map[string]map[int64]time.Time // 슬롯 id → 만료 시각
	next  int64
}

type windowCount struct {
	start time.Time
	value int64
}

func NewMemoryCounters(now func() time.Time) *MemoryCounters {
	return &MemoryCounters{now: now, fixed: map[string]windowCount{}, slots: map[string]map[int64]time.Time{}}
}

func (m *MemoryCounters) current(key string, window time.Duration) windowCount {
	start := m.now().Truncate(window)
	c := m.fixed[key]
	if !c.start.Equal(start) {
		c = windowCount{start: start}
	}
	return c
}

func (m *MemoryCounters) IncrementIfBelow(_ context.Context, key string, amount, limit int64, window time.Duration) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.current(key, window)
	if c.value+amount > limit {
		return c.value, false
	}
	c.value += amount
	m.fixed[key] = c
	return c.value, true
}

func (m *MemoryCounters) Add(_ context.Context, key string, amount int64, window time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.current(key, window)
	c.value += amount
	m.fixed[key] = c
}

func (m *MemoryCounters) Get(_ context.Context, key string, window time.Duration) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current(key, window).value
}

func (m *MemoryCounters) AcquireSlot(_ context.Context, key string, limit int64, lease time.Duration) (func(), int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	held := m.slots[key]
	if held == nil {
		held = map[int64]time.Time{}
		m.slots[key] = held
	}
	for id, expires := range held {
		if !now.Before(expires) {
			delete(held, id)
		}
	}
	if int64(len(held)) >= limit {
		return nil, int64(len(held)), false
	}
	m.next++
	id := m.next
	held[id] = now.Add(lease)
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			delete(m.slots[key], id)
		})
	}, int64(len(held)), true
}
