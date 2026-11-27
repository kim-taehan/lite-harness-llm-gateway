package app

import (
	"testing"

	"llm-gateway/internal/auth"
)

// envWith 는 시험용 기본 환경(마스터 키·DB 주소)에 값을 더한 환경이다
func envWith(extra map[string]string) func(string) string {
	return func(name string) string {
		if v, ok := extra[name]; ok {
			return v
		}
		return testEnv(name)
	}
}

// 로그인 방식은 운영 설정으로 고른다(설계 6장). 비우면 SSO 설정이 있을 때만 SSO 를 켜고 비밀번호는 늘 켠다
func TestLoadConfig_LoginMethods(t *testing.T) {
	const issuer = "http://keycloak.test/realms/gateway"
	cases := []struct {
		name string
		env  map[string]string
		want auth.LoginMethods
	}{
		{"비우고 SSO 설정도 없으면 비밀번호만", nil, auth.LoginMethods{Password: true}},
		{"비우고 SSO 설정이 있으면 둘 다", map[string]string{"GATEWAY_SSO_ISSUER": issuer}, auth.LoginMethods{SSO: true, Password: true}},
		{"password 로 고르면 SSO 설정이 있어도 비밀번호만", map[string]string{"GATEWAY_LOGIN_METHODS": "password", "GATEWAY_SSO_ISSUER": issuer}, auth.LoginMethods{Password: true}},
		{"sso 로 고르면 SSO 만", map[string]string{"GATEWAY_LOGIN_METHODS": "sso", "GATEWAY_SSO_ISSUER": issuer}, auth.LoginMethods{SSO: true}},
		{"둘 다 고름(공백·순서 무관)", map[string]string{"GATEWAY_LOGIN_METHODS": "password, sso"}, auth.LoginMethods{SSO: true, Password: true}},
		{"공백뿐이면 안 고른 것과 같다", map[string]string{"GATEWAY_LOGIN_METHODS": "  "}, auth.LoginMethods{Password: true}},
	}
	for _, c := range cases {
		cfg, err := LoadConfig(envWith(c.env))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if cfg.Login != c.want {
			t.Errorf("%s: %+v, 기대 %+v", c.name, cfg.Login, c.want)
		}
	}
}

func TestLoadConfig_RejectsUnknownLoginMethod(t *testing.T) {
	for _, bad := range []string{"ldap", "sso,ldap", ","} {
		if _, err := LoadConfig(envWith(map[string]string{"GATEWAY_LOGIN_METHODS": bad})); err == nil {
			t.Errorf("GATEWAY_LOGIN_METHODS=%q 로 설정이 만들어졌다", bad)
		}
	}
}

// 카운터 저장소는 운영 설정으로 고른다(설계 7장). 기본은 메모리(인스턴스 1개)이고, Redis 면 주소가 있어야 한다
func TestLoadConfig_CounterStore(t *testing.T) {
	cfg, err := LoadConfig(envWith(nil))
	if err != nil || cfg.CounterStore != "memory" {
		t.Errorf("기본 = %q, %v (memory 여야 한다)", cfg.CounterStore, err)
	}
	cfg, err = LoadConfig(envWith(map[string]string{"GATEWAY_COUNTER_STORE": "redis", "GATEWAY_REDIS_URL": "redis://cache:6379/0"}))
	if err != nil || cfg.CounterStore != "redis" || cfg.RedisURL != "redis://cache:6379/0" {
		t.Errorf("redis = %q %q, %v", cfg.CounterStore, cfg.RedisURL, err)
	}
	cfg, err = LoadConfig(envWith(map[string]string{"GATEWAY_COUNTER_STORE": "redis", "GATEWAY_REDIS_URL": "redis://cache:6379/0", "GATEWAY_LIMITS_FAIL_CLOSED": "true"}))
	if err != nil || !cfg.LimitsFailClosed {
		t.Errorf("fail-closed = %v, %v", cfg.LimitsFailClosed, err)
	}
	if cfg, _ = LoadConfig(envWith(nil)); cfg.LimitsFailClosed {
		t.Error("기본이 fail-closed 다. 기본은 메모리로 이어서 처리해야 한다")
	}
	for name, env := range map[string]map[string]string{
		"redis 인데 주소가 없다":      {"GATEWAY_COUNTER_STORE": "redis"},
		"모르는 저장소":              {"GATEWAY_COUNTER_STORE": "memcached"},
		"메모리 저장소에 fail-closed": {"GATEWAY_LIMITS_FAIL_CLOSED": "true"},
	} {
		if _, err := LoadConfig(envWith(env)); err == nil {
			t.Errorf("%s: 설정이 만들어졌다", name)
		}
	}
}
