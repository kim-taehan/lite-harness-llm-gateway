package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies 는 X-Forwarded-For 를 믿을 앞단(리버스 프록시)의 주소 대역이다. 비어 있으면 헤더를 쓰지 않는다.
// 앞단 뒤에서는 모든 요청의 출발지가 앞단 주소라서, 이 목록 없이는 로그인 시도 제한이 사용자 구분 없이 전체에 걸린다(설계 6장)
type TrustedProxies []netip.Prefix

// ParseTrustedProxies 는 쉼표로 구분한 IP 또는 CIDR 을 읽는다. 잘못된 값이 있으면 기동을 막도록 오류를 돌려준다
func ParseTrustedProxies(list string) (TrustedProxies, error) {
	var proxies TrustedProxies
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			proxies = append(proxies, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("GATEWAY_TRUSTED_PROXIES 의 %q 는 IP 도 CIDR 도 아니다", entry)
		}
		proxies = append(proxies, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return proxies, nil
}

func (p TrustedProxies) contains(addr netip.Addr) bool {
	for _, prefix := range p {
		if prefix.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}

// clientIP 는 시도 제한의 키가 되는 클라이언트 주소다. 요청을 직접 보낸 상대가 신뢰하는 앞단일 때만 X-Forwarded-For 를 읽고,
// 오른쪽(가장 가까운 앞단이 덧붙인 쪽)부터 신뢰 대역을 건너뛴 첫 주소를 쓴다. 클라이언트가 헤더 앞쪽에 끼워 넣은 값은 믿지 않기 위해서다.
// 읽을 수 없거나 전부 신뢰 대역이면 직접 연결 주소를 쓴다
func (p TrustedProxies) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !p.contains(peer) {
		return host
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return host
		}
		if !p.contains(hop) {
			return hop.Unmap().String()
		}
	}
	return host
}
