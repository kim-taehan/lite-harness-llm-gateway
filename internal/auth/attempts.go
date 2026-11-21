package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// 비밀번호 로그인 시도 제한(설계 6장). 같은 아이디·IP 로 창 안에서 너무 많이 틀리면 창이 지날 때까지 막는다.
// 아이디만으로 막지 않는 것은 남이 일부러 틀려 다른 사람 계정을 잠그지 못하게 하려는 것이다.
// 아이디를 바꿔 가며 보내는 시도는 아이디·IP 조합으로는 안 걸리므로 같은 IP 의 실패 총량도 따로 센다. 메모리라 인스턴스마다 따로 센다
const (
	maxFailedLogins      = 10
	maxFailedLoginsPerIP = 50
	failedLoginWindow    = 15 * time.Minute
)

// ErrTooManyAttempts 는 시도 제한에 걸렸다. 비밀번호가 맞아도 창이 지날 때까지 막는다
var ErrTooManyAttempts = errors.New("로그인 시도가 너무 많다")

type loginAttempts struct {
	now      func() time.Time
	mu       sync.Mutex
	failures map[string][]time.Time
	sweptAt  time.Time
}

func newLoginAttempts(now func() time.Time) *loginAttempts {
	return &loginAttempts{now: now, failures: map[string][]time.Time{}, sweptAt: now()}
}

func pairKey(login, ip string) string { return sha256Hex(strings.ToLower(login) + "|" + ip) }
func ipKey(ip string) string          { return sha256Hex("ip|" + ip) }

func (a *loginAttempts) blocked(login, ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.recent(pairKey(login, ip))) >= maxFailedLogins || len(a.recent(ipKey(ip))) >= maxFailedLoginsPerIP
}

func (a *loginAttempts) fail(login, ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweep()
	for _, key := range []string{pairKey(login, ip), ipKey(ip)} {
		a.failures[key] = append(a.recent(key), a.now())
	}
}

// succeed 는 그 아이디·IP 의 실패만 지운다. IP 총량은 남겨 둬서, 자기 계정에 한 번 성공하고 다른 아이디를 계속 찔러 보는 것을 막는다
func (a *loginAttempts) succeed(login, ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, pairKey(login, ip))
}

// sweep 은 한 번 쓰고 버린 아이디가 맵에 영원히 남지 않게 창마다 한 번 오래된 항목을 쓸어 낸다. 잠금을 잡은 채로 부른다
func (a *loginAttempts) sweep() {
	if a.now().Sub(a.sweptAt) < failedLoginWindow {
		return
	}
	a.sweptAt = a.now()
	for key := range a.failures {
		a.recent(key)
	}
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

// passwordGate 는 비밀번호를 확인하는 모든 길(데스크탑 로그인, 대시보드 로그인, 비밀번호 변경)이 거치는 문이다.
// 시도 제한과 확인을 한 곳에 둬서 어느 길도 제한을 빼먹지 못한다
type passwordGate struct {
	users    *Users
	attempts *loginAttempts
}

// attempt 는 제한에 걸렸으면 verify 를 부르지 않고 ErrTooManyAttempts 를 돌려준다. verify 가 ErrBadCredentials 면 실패로 센다
func (g passwordGate) attempt(login, ip string, verify func() (User, error)) (User, error) {
	if g.attempts.blocked(login, ip) {
		return User{}, ErrTooManyAttempts
	}
	user, err := verify()
	switch {
	case errors.Is(err, ErrBadCredentials):
		g.attempts.fail(login, ip)
	case err == nil:
		g.attempts.succeed(login, ip)
	}
	return user, err
}

// check 는 아이디(이메일)와 비밀번호를 확인한다
func (g passwordGate) check(ctx context.Context, login, password, ip string) (User, error) {
	return g.attempt(login, ip, func() (User, error) { return g.users.checkPassword(ctx, login, password) })
}
