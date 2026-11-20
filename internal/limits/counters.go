package limits

import (
	"sync"
	"time"
)

// CounterStore 는 인스턴스가 함께 보는 카운터다(설계 7.1). 메모리 구현은 인스턴스 1개 전용이고,
// 여러 대로 늘릴 때는 같은 인터페이스의 Redis 구현을 쓴다(Lua 로 검사와 증가를 한 번에)
type CounterStore interface {
	// IncrementIfBelow 는 window 단위 고정 윈도우 카운터를 amount 만큼 올린다. 올린 뒤 값이 limit 을 넘으면 올리지 않고 false 다
	IncrementIfBelow(key string, amount, limit int64, window time.Duration) (int64, bool)
	// Add 는 window 단위 카운터에 더한다(응답 뒤 토큰)
	Add(key string, amount int64, window time.Duration)
	// Get 은 지금 윈도우의 값이다
	Get(key string, window time.Duration) int64
	// AcquireSlot 은 동시성 슬롯을 lease 동안 잡는다. 인스턴스가 죽어도 lease 가 지나면 풀린다(설계 7.1)
	AcquireSlot(key string, limit int64, lease time.Duration) (release func(), ok bool)
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

func (m *MemoryCounters) IncrementIfBelow(key string, amount, limit int64, window time.Duration) (int64, bool) {
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

func (m *MemoryCounters) Add(key string, amount int64, window time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.current(key, window)
	c.value += amount
	m.fixed[key] = c
}

func (m *MemoryCounters) Get(key string, window time.Duration) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current(key, window).value
}

func (m *MemoryCounters) AcquireSlot(key string, limit int64, lease time.Duration) (func(), bool) {
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
		return nil, false
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
	}, true
}
