package auth

import (
	"strings"
	"sync"
	"time"
)

// 비밀번호 로그인 시도 제한(설계 6장). 같은 아이디·IP 로 창 안에서 너무 많이 틀리면 창이 지날 때까지 막는다.
// 아이디만으로 막지 않는 것은 남이 일부러 틀려 다른 사람 계정을 잠그지 못하게 하려는 것이다. 메모리라 인스턴스마다 따로 센다
const (
	maxFailedLogins   = 10
	failedLoginWindow = 15 * time.Minute
)

type loginAttempts struct {
	now      func() time.Time
	mu       sync.Mutex
	failures map[string][]time.Time
}

func newLoginAttempts(now func() time.Time) *loginAttempts {
	return &loginAttempts{now: now, failures: map[string][]time.Time{}}
}

func attemptKey(login, ip string) string { return sha256Hex(strings.ToLower(login) + "|" + ip) }

// blocked 는 지금 이 조합이 막혔는지다
func (a *loginAttempts) blocked(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.recent(key)) >= maxFailedLogins
}

func (a *loginAttempts) fail(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[key] = append(a.recent(key), a.now())
}

func (a *loginAttempts) succeed(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, key)
}

// recent 는 창 안의 실패만 남긴다. 잠금을 잡은 채로 부른다
func (a *loginAttempts) recent(key string) []time.Time {
	now := a.now()
	kept := a.failures[key][:0]
	for _, t := range a.failures[key] {
		if now.Sub(t) < failedLoginWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(a.failures, key)
		return nil
	}
	a.failures[key] = kept
	return kept
}
