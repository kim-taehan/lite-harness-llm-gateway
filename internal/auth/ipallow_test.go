package auth

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

func TestNormalizeIPEntry(t *testing.T) {
	valid := map[string]string{
		"203.0.113.9":        "203.0.113.9",
		"  203.0.113.9 ":     "203.0.113.9",
		"2001:db8::1":        "2001:db8::1",
		"::ffff:203.0.113.9": "203.0.113.9", // IPv4 를 IPv6 모양으로 쓴 것도 같은 주소다
		"10.1.0.0/16":        "10.1.0.0/16",
		"10.1.0.5/16":        "10.1.0.0/16", // 호스트 비트는 지워 같은 대역이 두 번 들어가지 않게 한다
		"2001:db8:abcd::/48": "2001:db8:abcd::/48",
		"203.0.113.9/32":     "203.0.113.9/32",
	}
	for input, want := range valid {
		if got, err := normalizeIPEntry(input); err != nil || got != want {
			t.Errorf("%q = %q, %v (기대 %q)", input, got, err, want)
		}
	}
	for _, bad := range []string{"", "  ", "not-an-ip", "10.0.0.0/33", "10.0.0.256", "10.0.0.1-10.0.0.9", "0.0.0.0/0x"} {
		if _, err := normalizeIPEntry(bad); !errors.Is(err, ErrIPEntryInvalid) {
			t.Errorf("%q 가 받아들여졌다: %v", bad, err)
		}
	}
}

func newAllowlist(t *testing.T, clock func() time.Time) (*IPAllowlist, *core.Settings) {
	t.Helper()
	db, _ := dbtest.Open(t)
	settings := core.NewSettings(db)
	return NewIPAllowlist(settings, nil, clock, false, slog.New(slog.NewTextHandler(io.Discard, nil))), settings
}

func serveFrom(a *IPAllowlist, remote, path string) int {
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rec, req)
	return rec.Code
}

// 다른 인스턴스가 바꾼 목록은 캐시 시간(ipAllowlistTTL) 안에 반영된다. 이 인스턴스가 바꾼 것은 바로 반영된다
func TestIPAllowlist_OtherInstanceChangesAppearAfterCacheTTL(t *testing.T) {
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	a, settings := newAllowlist(t, clock)
	b := NewIPAllowlist(settings, nil, clock, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := t.Context()

	if code := serveFrom(b, "198.51.100.7:1000", "/v1/models"); code != http.StatusNoContent {
		t.Fatalf("빈 목록인데 막혔다: %d", code)
	}
	if _, err := a.Add(ctx, "203.0.113.0/24"); err != nil {
		t.Fatal(err)
	}
	if code := serveFrom(a, "198.51.100.7:1000", "/v1/models"); code != http.StatusForbidden {
		t.Errorf("바꾼 인스턴스에는 바로 반영돼야 한다: %d", code)
	}
	if code := serveFrom(b, "198.51.100.7:1000", "/v1/models"); code != http.StatusNoContent {
		t.Errorf("다른 인스턴스는 캐시가 남아 있는 동안 옛 목록이다: %d", code)
	}
	now = now.Add(ipAllowlistTTL + time.Second)
	if code := serveFrom(b, "198.51.100.7:1000", "/v1/models"); code != http.StatusForbidden {
		t.Errorf("캐시 시간이 지났는데 반영되지 않았다: %d", code)
	}
}

// 목록을 읽지 못하면 닫힌 채로 동작한다. 읽지 못해서 허용 목록이 통째로 풀리면 안 된다. 헬스 체크는 늘 열려 있다
func TestIPAllowlist_FailsClosedWhenListCannotBeRead(t *testing.T) {
	db, pool := dbtest.Open(t)
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	settings := core.NewSettings(db)
	seed := NewIPAllowlist(settings, nil, clock, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := seed.Add(t.Context(), "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	fresh := NewIPAllowlist(settings, nil, clock, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pool.Close() // DB 가 끊겼다

	if code := serveFrom(fresh, "203.0.113.9:1000", "/v1/models"); code != http.StatusServiceUnavailable {
		t.Errorf("목록을 한 번도 읽지 못했는데 요청이 통과했다(또는 거절 코드가 다르다): %d", code)
	}
	if code := serveFrom(fresh, "203.0.113.9:1000", "/health/liveness"); code != http.StatusNoContent {
		t.Errorf("DB 가 끊겨도 헬스 체크는 열려 있어야 한다: %d", code)
	}
}

func TestIPAllowlist_KeepsLastKnownListWhenDBFailsAfterLoading(t *testing.T) {
	db, pool := dbtest.Open(t)
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	a := NewIPAllowlist(core.NewSettings(db), nil, clock, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := a.Add(t.Context(), "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	now = now.Add(ipAllowlistTTL + time.Second) // 캐시가 만료돼 다시 읽으려다 실패한다

	if code := serveFrom(a, "203.0.113.9:1000", "/v1/models"); code != http.StatusNoContent {
		t.Errorf("마지막으로 읽은 목록으로 허용 IP 가 통과해야 한다: %d", code)
	}
	if code := serveFrom(a, "198.51.100.7:1000", "/v1/models"); code != http.StatusForbidden {
		t.Errorf("마지막으로 읽은 목록으로 목록 밖 IP 는 계속 막혀야 한다: %d", code)
	}
}

func TestIPAllowlist_AddAndRemoveReportConflicts(t *testing.T) {
	a, _ := newAllowlist(t, time.Now)
	ctx := t.Context()
	if got, err := a.Add(ctx, "10.1.0.5/16"); err != nil || got != "10.1.0.0/16" {
		t.Fatalf("Add = %q, %v", got, err)
	}
	if _, err := a.Add(ctx, "10.1.0.0/16"); !errors.Is(err, ErrIPEntryExists) {
		t.Errorf("같은 대역을 다른 모양으로 또 넣었다: %v", err)
	}
	if _, err := a.Remove(ctx, "192.0.2.1"); !errors.Is(err, ErrIPEntryNotFound) {
		t.Errorf("없는 항목 삭제: %v", err)
	}
	if got, err := a.Remove(ctx, "10.1.0.99/16"); err != nil || got != "10.1.0.0/16" {
		t.Errorf("정규화한 모양으로 삭제해야 한다: %q, %v", got, err)
	}
	if list, _ := a.List(ctx); !slices.Equal(list, []string{}) {
		t.Errorf("목록 = %v", list)
	}
}

// 두 관리자가 동시에 항목을 더해도 어느 쪽도 사라지지 않는다(읽고-고치고-쓰기를 행 잠금으로 직렬화한다)
func TestIPAllowlist_ConcurrentAddsDoNotLoseEntries(t *testing.T) {
	a, _ := newAllowlist(t, time.Now)
	const adds = 20
	errs := make(chan error, adds)
	for i := 0; i < adds; i++ {
		go func() {
			_, err := a.Add(t.Context(), "10.0.0."+strconv.Itoa(i+1))
			errs <- err
		}()
	}
	for i := 0; i < adds; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	list, err := a.List(t.Context())
	if err != nil || len(list) != adds {
		t.Errorf("동시에 %d 개를 더했는데 목록에 %d 개가 남았다 (%v)", adds, len(list), err)
	}
}
