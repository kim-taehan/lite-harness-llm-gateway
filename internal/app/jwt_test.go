package app

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

// Keycloak 토큰(JWT)으로 API 를 직접 부른다. 가짜 Keycloak 이 JWKS 를 내놓고 시험이 서명한 토큰을 보낸다

const (
	jwtTestIssuer   = "http://keycloak.test/realms/corp"
	jwtTestAudience = "llm-gateway"
)

type fakeKeycloak struct {
	key *rsa.PrivateKey
	srv *httptest.Server
}

func newFakeKeycloak(t *testing.T) *fakeKeycloak {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwk := map[string]any{"kty": "RSA", "kid": "kc-1", "use": "sig", "alg": "RS256",
		"n": b64url(key.PublicKey.N.Bytes()), "e": b64url(big.NewInt(int64(key.PublicKey.E)).Bytes())}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwk}})
	}))
	t.Cleanup(srv.Close)
	return &fakeKeycloak{key: key, srv: srv}
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func jwtSegment(t *testing.T, v map[string]any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b64url(data)
}

// token 은 서명한 토큰이다. claims 로 기본 내용을 바꾸고, nil 값은 그 클레임을 뺀다
func (k *fakeKeycloak) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload := map[string]any{"iss": jwtTestIssuer, "aud": jwtTestAudience, "sub": "kc-kim", "email": "kim@corp.test", "email_verified": true,
		"name": "Kim", "preferred_username": "kim", "exp": time.Now().Add(5 * time.Minute).Unix()}
	for name, v := range claims {
		if v == nil {
			delete(payload, name)
			continue
		}
		payload[name] = v
	}
	input := jwtSegment(t, map[string]any{"alg": "RS256", "kid": "kc-1"}) + "." + jwtSegment(t, payload)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + b64url(sig)
}

// jwtGateway 는 JWT 직접 인증을 켠 게이트웨이다. 그룹 eng 는 팀 research, 그룹 ops 는 관리자다
func jwtGateway(t *testing.T, keycloak *fakeKeycloak) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	cfg, err := LoadConfig(testEnv)
	if err != nil {
		t.Fatal(err)
	}
	cfg.JWT = auth.JWTConfig{Issuer: jwtTestIssuer, Audience: jwtTestAudience, JWKSURL: keycloak.srv.URL,
		Groups: map[string]auth.GroupGrant{"eng": {TeamID: "research"}, "ops": {Admin: true}}}
	db, pool := dbtest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := newIdentity(db)
	return buildHandler(cfg, parts{lifecycle: &core.Lifecycle{}, db: db, id: id, login: testLogin(t, cfg, id), usage: startUsage(t, db, logger), logger: logger}), pool
}

// call 은 임의의 Bearer 값으로 부른다
func call(h http.Handler, method, path, bearer string, body any) (int, map[string]any) {
	var reader io.Reader = bytes.NewReader(nil)
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

func modelIDs(decoded map[string]any) []string {
	var ids []string
	for _, m := range decoded["data"].([]any) {
		ids = append(ids, m.(map[string]any)["id"].(string))
	}
	return ids
}

func TestJWT_DirectCallCreatesTheUserAndMapsGroupsToTeamAndAdminRole(t *testing.T) {
	upstream, _ := fakeOllama(t)
	keycloak := newFakeKeycloak(t)
	h, pool := jwtGateway(t, keycloak)
	api := apiClient{t: t, h: h}
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	api.ok("POST", "/model/new", map[string]any{"model_name": "m2", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	api.ok("POST", "/team/new", map[string]any{"team_id": "research", "models": []string{"m1"}})

	eng := keycloak.token(t, map[string]any{"groups": []string{"eng", "unmapped"}})
	if status, body := call(h, "GET", "/v1/models", eng, nil); status != 200 || strings.Join(modelIDs(body), ",") != "m1" {
		t.Fatalf("팀 모델만 보여야 한다: %d %v", status, body)
	}
	chat := map[string]any{"model": "m1", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	if status, body := call(h, "POST", "/v1/chat/completions", eng, chat); status != 200 {
		t.Fatalf("JWT 로 호출 = %d %v", status, body)
	}
	chat["model"] = "m2"
	if status, _ := call(h, "POST", "/v1/chat/completions", eng, chat); status != 403 {
		t.Errorf("팀에 없는 모델 = %d, 403 이어야 한다", status)
	}

	// 처음 온 사용자가 이메일로 만들어지고 팀원이 된다
	user := api.ok("GET", "/user/info?user_id=kim@corp.test", nil)
	if info, _ := user["user_info"].(map[string]any); info["user_email"] != "kim@corp.test" {
		t.Errorf("사용자가 만들어지지 않았다: %v", user)
	}
	team := api.ok("GET", "/team/info?team_id=research", nil)
	isMember := false
	members, _ := team["team_info"].(map[string]any)["members_with_roles"].([]any)
	for _, m := range members {
		if member, _ := m.(map[string]any); member["user_id"] == "kim@corp.test" && member["role"] == "user" {
			isMember = true
		}
	}
	if !isMember {
		t.Errorf("토큰 사용자가 팀원이 아니다: %v", members)
	}
	// 사용량에 사용자·팀이 남는다(키가 없어도)
	var userID, teamID string
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := pool.QueryRow(t.Context(), "SELECT user_id, team_id FROM request_log WHERE model = 'm1' LIMIT 1").Scan(&userID, &teamID)
		if err == nil || time.Now().After(deadline) {
			break
		}
		<-time.After(20 * time.Millisecond)
	}
	if userID != "kim@corp.test" || teamID != "research" {
		t.Errorf("사용량 기록의 사용자·팀 = %q·%q", userID, teamID)
	}

	// 관리자는 매핑한 그룹으로만 된다
	newTeam := map[string]any{"team_id": "other"}
	if status, _ := call(h, "POST", "/team/new", eng, newTeam); status != 401 && status != 403 {
		t.Errorf("그룹 eng 사용자가 팀을 만들었다: %d", status)
	}
	ops := keycloak.token(t, map[string]any{"sub": "kc-ops", "email": "ops@corp.test", "groups": []string{"ops"}})
	if status, body := call(h, "POST", "/team/new", ops, newTeam); status != 200 {
		t.Errorf("그룹 ops 사용자는 관리자다: %d %v", status, body)
	}
	// 사용자 행의 역할은 바뀌지 않는다(그룹을 빼면 바로 관리자가 아니다)
	noGroup := keycloak.token(t, map[string]any{"sub": "kc-ops", "email": "ops@corp.test"})
	if status, _ := call(h, "POST", "/team/new", noGroup, map[string]any{"team_id": "another"}); status != 401 && status != 403 {
		t.Errorf("그룹이 빠진 토큰이 관리자로 받아졌다: %d", status)
	}
}

func TestJWT_TeamMembershipFollowsTheTokenNotTheOldMembership(t *testing.T) {
	upstream, _ := fakeOllama(t)
	keycloak := newFakeKeycloak(t)
	h, _ := jwtGateway(t, keycloak)
	api := apiClient{t: t, h: h}
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	api.ok("POST", "/model/new", map[string]any{"model_name": "m2", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	api.ok("POST", "/team/new", map[string]any{"team_id": "research", "models": []string{"m1"}})

	if _, body := call(h, "GET", "/v1/models", keycloak.token(t, map[string]any{"groups": []string{"eng"}}), nil); strings.Join(modelIDs(body), ",") != "m1" {
		t.Fatalf("eng 토큰 = %v", body)
	}
	// Keycloak 에서 그룹을 뺀 다음 토큰은 팀 제한을 받지 않는다(이전에 팀원이 된 기록과 상관없이 토큰이 기준이다)
	_, body := call(h, "GET", "/v1/models", keycloak.token(t, nil), nil)
	if got := strings.Join(modelIDs(body), ","); got != "m1,m2" {
		t.Errorf("그룹이 빠진 토큰의 모델 = %q, 팀 제한이 없어야 한다", got)
	}
}

func TestJWT_BlockedTeamStopsTheRequestsOfItsTokenUsers(t *testing.T) {
	keycloak := newFakeKeycloak(t)
	h, _ := jwtGateway(t, keycloak)
	api := apiClient{t: t, h: h}
	api.ok("POST", "/team/new", map[string]any{"team_id": "research"})
	eng := keycloak.token(t, map[string]any{"groups": []string{"eng"}})
	if status, _ := call(h, "GET", "/v1/models", eng, nil); status != 200 {
		t.Fatalf("차단 전 = %d", status)
	}
	api.ok("POST", "/team/block", map[string]any{"team_id": "research"})
	if status, body := call(h, "GET", "/v1/models", eng, nil); status < 400 {
		t.Errorf("막은 팀의 토큰 사용자가 통과했다: %d %v", status, body)
	}
}

func TestJWT_RejectsForgedWrongAudienceExpiredAndUnboundIdentities(t *testing.T) {
	keycloak := newFakeKeycloak(t)
	h, _ := jwtGateway(t, keycloak)
	api := apiClient{t: t, h: h}

	attacker, _ := rsa.GenerateKey(rand.Reader, 2048)
	forged := (&fakeKeycloak{key: attacker}).token(t, nil)
	hsInput := jwtSegment(t, map[string]any{"alg": "HS256", "kid": "kc-1"}) + "." + jwtSegment(t, map[string]any{"iss": jwtTestIssuer, "aud": jwtTestAudience, "sub": "x", "email": "x@corp.test", "exp": time.Now().Add(time.Hour).Unix()})
	mac := hmac.New(sha256.New, keycloak.key.PublicKey.N.Bytes())
	mac.Write([]byte(hsInput))

	for name, token := range map[string]string{
		"다른 키로 서명":   forged,
		"HS256 혼동":   hsInput + "." + b64url(mac.Sum(nil)),
		"대상이 다름":     keycloak.token(t, map[string]any{"aud": "other-app"}),
		"발급자가 다름":    keycloak.token(t, map[string]any{"iss": "http://evil.test/realms/corp"}),
		"만료":         keycloak.token(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}),
		"사용자 식별자 없음": keycloak.token(t, map[string]any{"email": nil, "preferred_username": nil}),
	} {
		if status, _ := call(h, "GET", "/v1/models", token, nil); status != 401 {
			t.Errorf("%s: %d, 401 이어야 한다", name, status)
		}
	}

	// 같은 이메일이 다른 SSO 주체에 묶여 있으면 남의 계정에 들어올 수 없다
	if status, _ := call(h, "GET", "/v1/models", keycloak.token(t, nil), nil); status != 200 {
		t.Fatalf("정상 토큰 = %d", status)
	}
	if status, body := call(h, "GET", "/v1/models", keycloak.token(t, map[string]any{"sub": "someone-else"}), nil); status != 403 {
		t.Errorf("같은 이메일, 다른 주체 = %d %v, 403 이어야 한다", status, body)
	}
	// 관리자가 미리 만든 계정은 IdP 가 이메일을 확인하지 않았으면 묶이지 않는다
	api.ok("POST", "/user/new", map[string]any{"user_id": "pre@corp.test", "user_email": "pre@corp.test", "user_role": "internal_user"})
	unverified := keycloak.token(t, map[string]any{"sub": "kc-pre", "email": "pre@corp.test", "email_verified": false})
	if status, _ := call(h, "GET", "/v1/models", unverified, nil); status != 403 {
		t.Errorf("확인되지 않은 이메일로 기존 계정에 들어왔다: %d", status)
	}
}

func TestJWT_IsOffUnlessConfigured(t *testing.T) {
	keycloak := newFakeKeycloak(t)
	h, _ := testHandler(t) // GATEWAY_JWT_ISSUER 없음
	status, body := call(h, "GET", "/v1/models", keycloak.token(t, nil), nil)
	errBody, _ := body["error"].(map[string]any)
	message, _ := errBody["message"].(string)
	if status != 401 || !strings.Contains(message, "Virtual Key expected") {
		t.Errorf("꺼진 게이트웨이 = %d %v, 가상 키 형식 오류(401)여야 한다", status, body)
	}
}

func TestLoadJWT_RefusesMisconfiguration(t *testing.T) {
	env := func(pairs map[string]string) func(string) string {
		return func(name string) string { return pairs[name] }
	}
	for name, vars := range map[string]map[string]string{
		"대상 없음":           {"GATEWAY_JWT_ISSUER": "http://kc/realms/corp"},
		"발급자 없이 대상만":      {"GATEWAY_JWT_AUDIENCE": "gw"},
		"깨진 그룹 표":         {"GATEWAY_JWT_ISSUER": "http://kc/realms/corp", "GATEWAY_JWT_AUDIENCE": "gw", "GATEWAY_JWT_GROUP_MAP": `{"eng":`},
		"모르는 필드가 있는 그룹 표": {"GATEWAY_JWT_ISSUER": "http://kc/realms/corp", "GATEWAY_JWT_AUDIENCE": "gw", "GATEWAY_JWT_GROUP_MAP": `{"eng":{"team":"x"}}`},
		"아무것도 주지 않는 그룹":   {"GATEWAY_JWT_ISSUER": "http://kc/realms/corp", "GATEWAY_JWT_AUDIENCE": "gw", "GATEWAY_JWT_GROUP_MAP": `{"eng":{}}`},
	} {
		if _, err := loadJWT(env(vars)); err == nil {
			t.Errorf("%s: 기동을 멈춰야 한다", name)
		}
	}
	cfg, err := loadJWT(env(map[string]string{"GATEWAY_JWT_ISSUER": "http://kc/realms/corp", "GATEWAY_JWT_AUDIENCE": "gw",
		"GATEWAY_JWT_GROUP_MAP": `{"eng":{"team_id":"research"},"ops":{"admin":true}}`}))
	if err != nil || !cfg.Enabled() || cfg.Groups["eng"].TeamID != "research" || !cfg.Groups["ops"].Admin {
		t.Errorf("정상 설정 = %+v %v", cfg, err)
	}
	if cfg, err := loadJWT(env(nil)); err != nil || cfg.Enabled() {
		t.Errorf("설정이 없으면 꺼져 있어야 한다: %+v %v", cfg, err)
	}
}
