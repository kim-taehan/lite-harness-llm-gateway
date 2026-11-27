package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func requestFrom(peer, forwardedFor string) *http.Request {
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr = peer
	if forwardedFor != "" {
		r.Header.Set("X-Forwarded-For", forwardedFor)
	}
	return r
}

func TestClientIP_UsesForwardedForOnlyFromTrustedProxies(t *testing.T) {
	proxies, err := ParseTrustedProxies("10.0.0.5, 172.16.0.0/12")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name         string
		trusted      TrustedProxies
		peer, header string
		want         string
	}{
		{"신뢰 목록이 비면 헤더를 무시한다", nil, "10.0.0.5:4000", "1.2.3.4", "10.0.0.5"},
		{"신뢰하는 앞단이 알려 준 클라이언트", proxies, "10.0.0.5:4000", "1.2.3.4", "1.2.3.4"},
		{"클라이언트가 앞에 끼워 넣은 위조 값은 무시한다", proxies, "10.0.0.5:4000", "9.9.9.9, 1.2.3.4", "1.2.3.4"},
		{"앞단이 둘이면 신뢰 대역을 건너뛴 오른쪽 주소", proxies, "10.0.0.5:4000", "1.2.3.4, 172.16.3.3", "1.2.3.4"},
		{"신뢰하지 않는 상대의 헤더는 위조일 수 있어 무시한다", proxies, "8.8.8.8:4000", "1.2.3.4", "8.8.8.8"},
		{"헤더가 없으면 직접 연결 주소", proxies, "10.0.0.5:4000", "", "10.0.0.5"},
		{"헤더 값이 IP 가 아니면 직접 연결 주소", proxies, "10.0.0.5:4000", "not-an-ip", "10.0.0.5"},
		{"전부 신뢰 대역이면 직접 연결 주소", proxies, "10.0.0.5:4000", "172.16.3.3", "10.0.0.5"},
		{"IPv6", proxies, "[::1]:4000", "2001:db8::1", "::1"},
	}
	for _, c := range cases {
		if got := c.trusted.clientIP(requestFrom(c.peer, c.header)); got != c.want {
			t.Errorf("%s: %q, 기대 %q", c.name, got, c.want)
		}
	}
}

func TestParseTrustedProxies_RejectsBadEntriesAndAcceptsEmpty(t *testing.T) {
	if p, err := ParseTrustedProxies(""); err != nil || len(p) != 0 {
		t.Errorf("빈 값 = %v, %v", p, err)
	}
	for _, bad := range []string{"not-an-ip", "10.0.0.5, nope", "10.0.0.0/99"} {
		if _, err := ParseTrustedProxies(bad); err == nil {
			t.Errorf("%q 가 받아들여졌다", bad)
		}
	}
}
