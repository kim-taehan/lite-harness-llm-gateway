package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

// 시험 요청의 직접 연결 주소다(httptest 기본값). 이 주소를 "앞단"으로 본다
const testPeerAddr = "192.0.2.1"

// gatewayBehindProxy 는 GATEWAY_TRUSTED_PROXIES 로 앞단을 신뢰하도록 설정한 게이트웨이다
func gatewayBehindProxy(t *testing.T, trusted string) http.Handler {
	t.Helper()
	return gatewayWithEnv(t, map[string]string{"GATEWAY_TRUSTED_PROXIES": trusted})
}

// gatewayWithEnv 는 시험용 기본 환경에 환경 변수를 더해 조립한 게이트웨이다
func gatewayWithEnv(t *testing.T, extra map[string]string) http.Handler {
	t.Helper()
	cfg, err := LoadConfig(envWith(extra))
	if err != nil {
		t.Fatal(err)
	}
	db, _ := dbtest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := newIdentity(db)
	return buildHandler(cfg, parts{lifecycle: &core.Lifecycle{}, db: db, id: id, login: testLogin(t, cfg, id), usage: startUsage(t, db, logger), logger: logger})
}

// passwordLoginAttempt 는 X-Forwarded-For 가 forwardedFor 인 요청이 비밀번호 로그인을 한 번 시도한 결과 코드다
func passwordLoginAttempt(b browser, action, forwardedFor, password string) int {
	return b.do("POST", action, url.Values{"username": {"park"}, "password": {password}}, map[string]string{"X-Forwarded-For": forwardedFor}).Code
}

func startPasswordLogin(t *testing.T, h http.Handler) (browser, string) {
	t.Helper()
	api := apiClient{t: t, h: h}
	api.ok("POST", "/user/new", map[string]any{"user_id": "park"})
	api.ok("POST", "/user/update", map[string]any{"user_id": "park", "password": "Right-pass-123"})
	b := browser{t: t, h: h}
	start := jsonOf(t, b.do("POST", "/sso/cli/start", nil, nil))
	return b, "/sso/cli/password/" + start["login_id"].(string)
}

// 앞단 뒤에서는 클라이언트 주소로 센다: 공격자가 막혀도 같은 앞단을 쓰는 다른 사람은 막히지 않는다
func TestLoginAttempts_TrustedProxyKeepsClientsApart(t *testing.T) {
	b, action := startPasswordLogin(t, gatewayBehindProxy(t, testPeerAddr))
	for i := 0; i < 10; i++ {
		if code := passwordLoginAttempt(b, action, "6.6.6.6", "wrong"); code != 401 {
			t.Fatalf("%d 번째 실패: %d", i+1, code)
		}
	}
	if code := passwordLoginAttempt(b, action, "6.6.6.6", "Right-pass-123"); code != 429 {
		t.Errorf("공격자 주소가 막히지 않았다: %d", code)
	}
	if code := passwordLoginAttempt(b, action, "7.7.7.7", "wrong"); code != 401 {
		t.Errorf("같은 앞단을 쓰는 다른 사람이 막혔다: %d", code)
	}
}

// 앞단을 신뢰하지 않으면(기본) 헤더는 무시한다. 직접 연결 주소가 같으면 같은 사람으로 세어 위조 헤더로 제한을 피할 수 없다
func TestLoginAttempts_ForwardedForIgnoredWithoutTrustedProxy(t *testing.T) {
	b, action := startPasswordLogin(t, gatewayBehindProxy(t, ""))
	for i := 0; i < 10; i++ {
		passwordLoginAttempt(b, action, "6.6.6.6", "wrong")
	}
	if code := passwordLoginAttempt(b, action, "7.7.7.7", "Right-pass-123"); code != 429 {
		t.Errorf("신뢰 설정이 없는데 헤더로 제한을 피했다: %d", code)
	}
}

func TestLoadConfig_RejectsBadTrustedProxies(t *testing.T) {
	_, err := LoadConfig(func(name string) string {
		if name == "GATEWAY_TRUSTED_PROXIES" {
			return "10.0.0.5, nope"
		}
		return testEnv(name)
	})
	if err == nil {
		t.Error("잘못된 GATEWAY_TRUSTED_PROXIES 로 설정이 만들어졌다")
	}
}
