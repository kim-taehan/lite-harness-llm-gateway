package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 전역 IP 허용 목록(설계 6장): 목록이 비어 있으면 모두 허용하고, 목록이 있으면 목록 밖 IP 의 요청을 403 으로 막는다.
// 헬스 체크(/health/liveness·liveliness·readiness)는 늘 열려 있고, 로그인 같은 공개 라우트도 막는다

// callFrom 은 직접 연결한 주소가 ip 인 요청을 마스터 키로 보낸다. header 가 같은 이름이면 덮어쓴다
func callFrom(h http.Handler, ip, method, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = net.JoinHostPort(ip, "40000")
	req.Header.Set("Authorization", "Bearer "+testMasterKey)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// apiClient 의 요청은 직접 연결 주소가 192.0.2.1 이라, 목록을 만들 때 관리자가 자기 주소부터 넣어야 잠기지 않는다
const apiClientAddr = "192.0.2.1"

func addAllowedIP(t *testing.T, api apiClient, ip string) map[string]any {
	t.Helper()
	return api.ok("POST", "/add/allowed_ip", map[string]any{"ip": ip})
}

func TestIPAllowlist_EmptyListAllowsEveryone(t *testing.T) {
	h := newAPI(t).h
	for _, ip := range []string{"198.51.100.7", "203.0.113.9", "2001:db8::1"} {
		if rec := callFrom(h, ip, "GET", "/v1/models", nil); rec.Code != http.StatusOK {
			t.Errorf("%s: 목록이 비었는데 %d", ip, rec.Code)
		}
	}
}

func TestIPAllowlist_BlocksOutsideTheListAndAllowsIPsAndCIDRs(t *testing.T) {
	api := newAPI(t)
	addAllowedIP(t, api, apiClientAddr)
	addAllowedIP(t, api, "203.0.113.0/24")
	addAllowedIP(t, api, "2001:db8::1")

	for _, ip := range []string{"203.0.113.9", "203.0.113.254", "2001:db8::1"} {
		if rec := callFrom(api.h, ip, "GET", "/v1/models", nil); rec.Code != http.StatusOK {
			t.Errorf("목록 안 %s: %d", ip, rec.Code)
		}
	}
	blocked := callFrom(api.h, "198.51.100.7", "GET", "/v1/models", nil)
	if blocked.Code != http.StatusForbidden || !strings.Contains(blocked.Body.String(), "Access forbidden: IP address 198.51.100.7 not allowed.") ||
		!strings.Contains(blocked.Body.String(), `"type":"auth_error"`) {
		t.Errorf("목록 밖 IP: %d %s", blocked.Code, blocked.Body.String())
	}
	if rec := callFrom(api.h, "203.0.114.1", "GET", "/v1/models", nil); rec.Code != http.StatusForbidden {
		t.Errorf("대역 바로 밖 IP: %d", rec.Code)
	}
	// 공개 라우트(키 없이 부르는 로그인 화면 설정)도 목록 밖 IP 에는 막힌다
	if rec := callFrom(api.h, "198.51.100.7", "GET", "/get/ui_settings", map[string]string{"Authorization": ""}); rec.Code != http.StatusForbidden {
		t.Errorf("공개 라우트가 목록 밖 IP 에 열려 있다: %d", rec.Code)
	}
	// 헬스 체크는 목록 밖 IP(앞단 프로브)에도 열려 있다
	for _, path := range []string{"/health/liveness", "/health/liveliness", "/health/readiness"} {
		if rec := callFrom(api.h, "198.51.100.7", "GET", path, map[string]string{"Authorization": ""}); rec.Code != http.StatusOK {
			t.Errorf("%s 가 목록 밖 IP 에 막혔다: %d", path, rec.Code)
		}
	}
	// 같은 서버 안에서 직접 부르는 요청(앞단 헤더 없음)은 목록과 무관하게 허용한다(잠겼을 때 복구하는 길)
	if rec := callFrom(api.h, "127.0.0.1", "GET", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Errorf("직접 연결한 로컬 요청이 막혔다: %d", rec.Code)
	}
	if rec := callFrom(api.h, "::1", "GET", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Errorf("직접 연결한 로컬(IPv6) 요청이 막혔다: %d", rec.Code)
	}
	// 앞단(nginx)이 같은 서버에서 붙이는 요청에는 X-Forwarded-For 가 있다. 그 앞단을 신뢰하도록 설정하지 않았다면 로컬 예외로
	// 모두 통과시키면 안 된다. 안 그러면 앞단 뒤의 모든 클라이언트가 허용 목록을 우회한다
	if rec := callFrom(api.h, "127.0.0.1", "GET", "/v1/models", map[string]string{"X-Forwarded-For": "198.51.100.7"}); rec.Code != http.StatusForbidden {
		t.Errorf("앞단을 거친 요청이 로컬 예외로 통과했다: %d", rec.Code)
	}
}

// 앞단 뒤에서는 클라이언트 주소로 판정한다. 신뢰하는 앞단이 알려 준 주소만 쓴다(GATEWAY_TRUSTED_PROXIES)
func TestIPAllowlist_UsesForwardedForOnlyFromTrustedProxies(t *testing.T) {
	h := gatewayBehindProxy(t, apiClientAddr)
	api := apiClient{t: t, h: h}
	addAllowedIP(t, api, "203.0.113.9") // 목록이 비어 있을 때 넣는다. 이후 apiClientAddr(앞단) 자신은 목록에 없다

	if rec := callFrom(h, apiClientAddr, "GET", "/v1/models", map[string]string{"X-Forwarded-For": "203.0.113.9"}); rec.Code != http.StatusOK {
		t.Errorf("앞단이 알려 준 허용 IP: %d", rec.Code)
	}
	if rec := callFrom(h, apiClientAddr, "GET", "/v1/models", map[string]string{"X-Forwarded-For": "198.51.100.7"}); rec.Code != http.StatusForbidden {
		t.Errorf("앞단이 알려 준 목록 밖 IP: %d", rec.Code)
	}
	// 클라이언트가 헤더 앞에 허용 IP 를 끼워 넣어도 앞단이 덧붙인 실제 주소(오른쪽)로 판정한다
	if rec := callFrom(h, apiClientAddr, "GET", "/v1/models", map[string]string{"X-Forwarded-For": "203.0.113.9, 198.51.100.7"}); rec.Code != http.StatusForbidden {
		t.Errorf("위조한 앞쪽 값이 통과했다: %d", rec.Code)
	}
	// 신뢰하지 않는 상대가 보낸 헤더는 무시한다
	if rec := callFrom(h, "198.51.100.7", "GET", "/v1/models", map[string]string{"X-Forwarded-For": "203.0.113.9"}); rec.Code != http.StatusForbidden {
		t.Errorf("신뢰하지 않는 상대의 헤더가 통과했다: %d", rec.Code)
	}
}

func TestIPAllowlist_ManagementAPIFollowsLitellmContract(t *testing.T) {
	api := newAPI(t)
	if got := api.ok("GET", "/get/allowed_ips", nil)["data"]; got == nil || len(got.([]any)) != 0 {
		t.Fatalf("처음 목록 = %v", got)
	}
	added := addAllowedIP(t, api, apiClientAddr)
	if added["status"] != "success" || added["message"] != "IP "+apiClientAddr+" address added successfully" {
		t.Errorf("추가 응답 = %v", added)
	}
	// 대역은 호스트 비트를 지운 모양으로 저장한다
	if msg := addAllowedIP(t, api, "10.1.0.5/16")["message"]; msg != "IP 10.1.0.0/16 address added successfully" {
		t.Errorf("대역 추가 응답 = %v", msg)
	}
	if status, body := api.do("POST", "/add/allowed_ip", map[string]any{"ip": "10.1.0.0/16"}); status != 400 || body.(map[string]any)["detail"] != "IP address already exists" {
		t.Errorf("중복 추가: %d %v", status, body)
	}
	if status, body := api.do("POST", "/add/allowed_ip", map[string]any{"ip": "not-an-ip"}); status != 400 || !strings.Contains(body.(map[string]any)["detail"].(string), "not-an-ip") {
		t.Errorf("잘못된 IP: %d %v", status, body)
	}
	if status, _ := api.do("POST", "/add/allowed_ip", map[string]any{}); status == 200 {
		t.Error("ip 없는 추가가 받아들여졌다")
	}
	list := api.ok("GET", "/get/allowed_ips", nil)["data"].([]any)
	if len(list) != 2 || list[0] != apiClientAddr || list[1] != "10.1.0.0/16" {
		t.Errorf("목록 = %v", list)
	}
	if status, body := api.do("POST", "/delete/allowed_ip", map[string]any{"ip": "198.51.100.7"}); status != 404 || body.(map[string]any)["detail"] != "IP address not found" {
		t.Errorf("없는 항목 삭제: %d %v", status, body)
	}
	deleted := api.ok("POST", "/delete/allowed_ip", map[string]any{"ip": "10.1.0.0/16"})
	if deleted["status"] != "success" || deleted["message"] != "IP 10.1.0.0/16 deleted successfully" {
		t.Errorf("삭제 응답 = %v", deleted)
	}

	// 관리자만 본다: 목록에는 내부 대역이 들어 있다
	api.ok("POST", "/user/new", map[string]any{"user_id": "kim", "user_role": "internal_user"})
	key := api.ok("POST", "/key/generate", map[string]any{"user_id": "kim"})["key"].(string)
	asUser := map[string]string{"Authorization": "Bearer " + key}
	// 관리자 전용 라우트에 허용 목록에 없는 역할이 오면 litellm 처럼 401 이다(라우트 단계 판정)
	for _, c := range []struct{ method, path string }{{"GET", "/get/allowed_ips"}, {"POST", "/add/allowed_ip"}, {"POST", "/delete/allowed_ip"}} {
		if rec := callFrom(api.h, apiClientAddr, c.method, c.path, asUser); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Only proxy admin") {
			t.Errorf("일반 사용자가 %s %s: %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// 비상 해제(GATEWAY_ALLOWED_IPS_DISABLE=true)로 다시 띄우면 목록을 무시한다. 목록에서 자기 IP 를 빼고 저장해 잠겼을 때 쓴다
func TestIPAllowlist_EmergencyDisableIgnoresTheList(t *testing.T) {
	h := gatewayWithEnv(t, map[string]string{"GATEWAY_ALLOWED_IPS_DISABLE": "true"})
	api := apiClient{t: t, h: h}
	addAllowedIP(t, api, "203.0.113.9")

	if rec := callFrom(h, "198.51.100.7", "GET", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Errorf("비상 해제인데 목록 밖 IP 가 막혔다: %d", rec.Code)
	}
	if list := api.ok("GET", "/get/allowed_ips", nil)["data"].([]any); len(list) != 1 {
		t.Errorf("비상 해제여도 저장된 목록은 그대로 보이고 고칠 수 있어야 한다: %v", list)
	}
}

// 허용 목록 변경은 보안 설정 변경이라 감사 기록에 남는다(litellm 도 general_settings 변경으로 남긴다)
func TestIPAllowlist_ChangesAreAudited(t *testing.T) {
	api := newAPI(t)
	addAllowedIP(t, api, apiClientAddr)
	addAllowedIP(t, api, "203.0.113.0/24")
	api.ok("POST", "/delete/allowed_ip", map[string]any{"ip": "203.0.113.0/24"})
	api.do("POST", "/delete/allowed_ip", map[string]any{"ip": "198.51.100.7"}) // 실패한 요청은 남기지 않는다

	logs := auditLogs(t, api, "table_name=LiteLLM_Config&sort_order=asc")
	if len(logs) != 3 {
		t.Fatalf("감사 기록 %d 건, 3 건이어야 한다: %v", len(logs), logs)
	}
	var actions []string
	for _, entry := range logs {
		e := entry.(map[string]any)
		actions = append(actions, e["action"].(string))
		if e["object_id"] != "general_settings" {
			t.Errorf("대상 = %v", e["object_id"])
		}
	}
	if strings.Join(actions, ",") != "updated,updated,deleted" {
		t.Errorf("동작 = %v", actions)
	}
	if values, _ := logs[1].(map[string]any)["updated_values"].(map[string]any); values["ip"] != "203.0.113.0/24" {
		t.Errorf("기록에 어떤 IP 였는지 남아야 한다: %v", values)
	}
}
