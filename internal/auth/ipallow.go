package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"llm-gateway/internal/core"
)

// 전역 IP 허용 목록(설계 6장). 목록이 비어 있으면 모두 허용하고, 목록이 있으면 목록 밖 IP 의 요청을 막는다.
// 목록은 대시보드에서 DB 로 관리한다(litellm 은 설정 파일 항목이고 유료 기능이다)

const (
	// ipAllowlistSetting 은 setting 테이블의 이름이다. litellm 의 general_settings 와 같은 이름이고 감사 기록의 대상 id 이기도 하다
	ipAllowlistSetting = "general_settings"
	ipAllowlistField   = "allowed_ips"
	// ipAllowlistTTL 은 목록 캐시 시간이다. 이 인스턴스가 바꾼 것은 바로 반영되고, 다른 인스턴스가 바꾼 것은 이 시간 안에 반영된다
	ipAllowlistTTL = 30 * time.Second
	// ipAllowlistRetry 는 목록을 읽지 못했을 때 다시 시도하기까지의 시간이다(DB 가 끊긴 동안 요청마다 두드리지 않게)
	ipAllowlistRetry = 5 * time.Second
)

var (
	// ErrIPEntryInvalid 는 IP 도 CIDR 도 아닌 항목이다
	ErrIPEntryInvalid = errors.New("올바른 IP 나 CIDR 가 아니다")
	// ErrIPEntryExists 는 이미 있는 항목이다(IP 와 같은 대역은 같은 항목이다)
	ErrIPEntryExists = errors.New("이미 있는 IP 다")
	// ErrIPEntryNotFound 는 목록에 없는 항목이다
	ErrIPEntryNotFound = errors.New("목록에 없는 IP 다")
)

// IPAllowlist 는 전역 IP 허용 목록이다. 요청을 막는 일(Middleware)과 목록을 고치는 일(List·Add·Remove)을 한다
type IPAllowlist struct {
	settings *core.Settings
	proxies  TrustedProxies
	now      func() time.Time
	disabled bool
	logger   *slog.Logger

	mu          sync.Mutex
	loaded      bool
	nextRefresh time.Time
	entries     []netip.Prefix
}

// NewIPAllowlist 의 disabled 는 비상 해제(GATEWAY_ALLOWED_IPS_DISABLE)다. 켜면 목록을 무시한다.
// proxies 는 클라이언트 주소를 정할 때 X-Forwarded-For 를 믿을 앞단이다(로그인 시도 제한과 같은 방식)
func NewIPAllowlist(settings *core.Settings, proxies TrustedProxies, now func() time.Time, disabled bool, logger *slog.Logger) *IPAllowlist {
	return &IPAllowlist{settings: settings, proxies: proxies, now: now, disabled: disabled, logger: logger}
}

// parseIPEntry 는 IP 또는 CIDR 을 대역으로 읽는다. 단일 IP 는 /32(IPv6 는 /128)로 본다
func parseIPEntry(entry string) (netip.Prefix, error) {
	entry = strings.TrimSpace(entry)
	if prefix, err := netip.ParsePrefix(entry); err == nil {
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%w: %q", ErrIPEntryInvalid, entry)
	}
	addr = addr.Unmap() // IPv4 를 IPv6 모양으로 쓴 것도 같은 주소다
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// normalizeIPEntry 는 저장하고 보여 줄 모양이다. 같은 항목이 모양만 달라 두 번 들어가지 않게 한다:
// CIDR 은 호스트 비트를 지우고(10.1.0.5/16 → 10.1.0.0/16), 단일 IP 는 주소만 적는다
func normalizeIPEntry(entry string) (string, error) {
	prefix, err := parseIPEntry(entry)
	if err != nil {
		return "", err
	}
	if strings.Contains(entry, "/") {
		return prefix.String(), nil
	}
	return prefix.Addr().String(), nil
}

func storedEntries(current map[string]any) []string {
	var entries []string
	list, _ := current[ipAllowlistField].([]any)
	for _, item := range list {
		if entry, ok := item.(string); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

// List 는 저장된 목록이다(캐시가 아니라 DB 의 값. 관리 화면이 보는 것)
func (a *IPAllowlist) List(ctx context.Context) ([]string, error) {
	current, err := a.settings.Get(ctx, ipAllowlistSetting)
	if err != nil {
		return nil, err
	}
	return append([]string{}, storedEntries(current)...), nil
}

// Add 는 항목을 더하고 저장한 모양을 돌려준다. 실패: ErrIPEntryInvalid, ErrIPEntryExists
func (a *IPAllowlist) Add(ctx context.Context, entry string) (string, error) {
	normalized, err := normalizeIPEntry(entry)
	if err != nil {
		return "", err
	}
	return normalized, a.change(ctx, func(list []string) ([]string, error) {
		if slices.Contains(list, normalized) {
			return nil, ErrIPEntryExists
		}
		return append(list, normalized), nil
	})
}

// Remove 는 항목을 지우고 지운 모양을 돌려준다. 실패: ErrIPEntryInvalid, ErrIPEntryNotFound
func (a *IPAllowlist) Remove(ctx context.Context, entry string) (string, error) {
	normalized, err := normalizeIPEntry(entry)
	if err != nil {
		return "", err
	}
	return normalized, a.change(ctx, func(list []string) ([]string, error) {
		if !slices.Contains(list, normalized) {
			return nil, ErrIPEntryNotFound
		}
		return slices.DeleteFunc(list, func(e string) bool { return e == normalized }), nil
	})
}

// change 는 목록을 한 트랜잭션으로 고치고, 저장한 목록을 이 인스턴스의 캐시에 바로 반영한다
func (a *IPAllowlist) change(ctx context.Context, edit func(list []string) ([]string, error)) error {
	var saved []string
	err := a.settings.Update(ctx, ipAllowlistSetting, func(current map[string]any) (map[string]any, error) {
		edited, err := edit(storedEntries(current))
		if err != nil {
			return nil, err
		}
		saved = edited
		next := map[string]any{}
		for k, v := range current {
			next[k] = v
		}
		next[ipAllowlistField] = edited
		return next, nil
	})
	if err != nil {
		return err
	}
	a.cache(saved)
	return nil
}

func (a *IPAllowlist) cache(list []string) {
	prefixes := a.parseStored(list)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries, a.loaded, a.nextRefresh = prefixes, true, a.now().Add(ipAllowlistTTL)
}

// parseStored 는 저장된 항목을 대역으로 읽는다. DB 를 손으로 고쳐 잘못된 값이 들어갔으면 그 항목만 건너뛰고 알린다
func (a *IPAllowlist) parseStored(list []string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(list))
	for _, entry := range list {
		prefix, err := parseIPEntry(entry)
		if err != nil {
			a.logger.Warn("IP 허용 목록의 잘못된 항목을 건너뛴다", "entry", entry)
			continue
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

// snapshot 은 캐시한 목록이다. 시간이 지나면 다시 읽는다. 읽지 못하면 마지막으로 읽은 목록을 계속 쓰고,
// 한 번도 읽지 못했으면 오류다(읽지 못해서 허용 목록이 통째로 풀리면 안 된다)
func (a *IPAllowlist) snapshot(ctx context.Context) ([]netip.Prefix, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loaded && a.now().Before(a.nextRefresh) {
		return a.entries, nil
	}
	current, err := a.settings.Get(ctx, ipAllowlistSetting)
	if err != nil {
		if !a.loaded {
			return nil, err
		}
		a.logger.Warn("IP 허용 목록을 다시 읽지 못해 마지막으로 읽은 목록을 쓴다", "err", err)
		a.nextRefresh = a.now().Add(ipAllowlistRetry)
		return a.entries, nil
	}
	a.entries, a.loaded, a.nextRefresh = a.parseStored(storedEntries(current)), true, a.now().Add(ipAllowlistTTL)
	return a.entries, nil
}

// isHealthProbe 는 앞단 프로브가 목록 밖 주소에서 부르는 헬스 체크다. 늘 열어 둔다
func isHealthProbe(path string) bool {
	return path == "/health/liveness" || path == "/health/liveliness" || path == "/health/readiness"
}

// isDirectLocal 은 같은 서버 안에서 앞단 없이 직접 부른 요청이다. 목록에서 자기 주소를 빼고 저장해 잠겼을 때 서버에서 고치는 길이다.
// 앞단이 붙이는 헤더가 하나라도 있으면 직접 호출이 아니다: 같은 서버의 앞단(nginx)을 거친 요청은 연결 주소가 늘 로컬이라,
// 이 예외를 적용하면 앞단 뒤의 모든 클라이언트가 허용 목록을 우회하게 된다
func isDirectLocal(r *http.Request) bool {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		if len(r.Header.Values(header)) > 0 {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

// Middleware 는 목록 밖 IP 의 요청을 403 으로 막는다. 헬스 체크는 거르지 않는다
func (a *IPAllowlist) Middleware(next http.Handler) http.Handler {
	if a.disabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isHealthProbe(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		entries, err := a.snapshot(r.Context())
		if err != nil {
			a.logger.Error("IP 허용 목록을 읽지 못해 요청을 거절했다", "err", err)
			core.ProxyError{Status: http.StatusServiceUnavailable, Type: "auth_error", Param: "None",
				Message: "The IP allow list is temporarily unavailable. Please retry shortly."}.Write(w)
			return
		}
		if len(entries) == 0 || isDirectLocal(r) {
			next.ServeHTTP(w, r)
			return
		}
		client := a.proxies.clientIP(r)
		if addr, err := netip.ParseAddr(client); err == nil && containsAddr(entries, addr.Unmap()) {
			next.ServeHTTP(w, r)
			return
		}
		core.ProxyError{Status: http.StatusForbidden, Type: "auth_error", Param: "None",
			Message: "Access forbidden: IP address " + client + " not allowed."}.Write(w)
	})
}

func containsAddr(entries []netip.Prefix, addr netip.Addr) bool {
	return slices.ContainsFunc(entries, func(prefix netip.Prefix) bool { return prefix.Contains(addr) })
}
