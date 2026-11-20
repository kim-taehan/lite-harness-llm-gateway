package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"llm-gateway/internal/core"
)

const testMasterKey = "sk-test-master"

func guarded(t *testing.T, access core.Access) http.Handler {
	t.Helper()
	key, err := NewMasterKey(testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		core.WriteJSON(w, http.StatusOK, p.UserID)
	}
	return NewAuthenticator(key).Guard(core.Route{Method: http.MethodGet, Path: "/x", Access: access, Handler: handler})
}

func call(h http.Handler, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("본문이 JSON 이 아니다: %q", rec.Body.String())
	}
	return body.Error
}

func TestKeyMissing_ReturnsLitellmAuthError(t *testing.T) {
	rec := call(guarded(t, core.Roles(core.RoleProxyAdmin)), nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태 = %d, 401 이어야 한다", rec.Code)
	}
	want := map[string]any{
		"message": "Authentication Error, No api key passed in.",
		"type":    "auth_error",
		"param":   "None",
		"code":    "401",
	}
	got := errorBody(t, rec)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("error.%s = %v, 기대 %v", k, got[k], v)
		}
	}
}

func TestWrongKey_Is401(t *testing.T) {
	rec := call(guarded(t, core.Roles(core.RoleProxyAdmin)), map[string]string{"Authorization": "Bearer sk-wrong"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태 = %d", rec.Code)
	}
	if got := errorBody(t, rec)["message"]; got != "Authentication Error, Invalid proxy server token passed." {
		t.Errorf("message = %v", got)
	}
}

func TestEmptyBearer_IsMalformed(t *testing.T) {
	rec := call(guarded(t, core.Roles(core.RoleProxyAdmin)), map[string]string{"Authorization": "Bearer "})

	if got := errorBody(t, rec)["message"]; got != "Authentication Error, "+reasonMalformedKey {
		t.Errorf("message = %v", got)
	}
}

func TestMasterKey_AcceptedByBearerAndXApiKey_AsProxyAdmin(t *testing.T) {
	h := guarded(t, core.Roles(core.RoleProxyAdmin))
	for _, header := range []map[string]string{
		{"Authorization": "Bearer " + testMasterKey},
		{"x-api-key": testMasterKey},
	} {
		rec := call(h, header)
		if rec.Code != http.StatusOK {
			t.Fatalf("%v: 상태 = %d", header, rec.Code)
		}
		if rec.Body.String() != "\"default_user_id\"\n" {
			t.Errorf("%v: 신원 = %s", header, rec.Body.String())
		}
	}
}

func TestRoleNotAllowed_RejectedWith401LikeLitellm(t *testing.T) {
	rec := call(guarded(t, core.Roles(core.RoleInternalUser)), map[string]string{"Authorization": "Bearer " + testMasterKey})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("상태 = %d", rec.Code)
	}
	if got := errorBody(t, rec)["type"]; got != "auth_error" {
		t.Errorf("type = %v", got)
	}
}

func TestPublicRoute_NeedsNoKey(t *testing.T) {
	if rec := call(guarded(t, core.Public()), nil); rec.Code != http.StatusOK {
		t.Fatalf("상태 = %d", rec.Code)
	}
}

func TestMasterKey_NeverPrintsValue(t *testing.T) {
	key, _ := NewMasterKey(testMasterKey)
	if s := key.String(); s != "MasterKey(****)" {
		t.Errorf("String() = %q", s)
	}
	if _, err := NewMasterKey(""); err != ErrNoMasterKey {
		t.Errorf("빈 키는 ErrNoMasterKey 여야 한다: %v", err)
	}
}
