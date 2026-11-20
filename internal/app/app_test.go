package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"llm-gateway/internal/core"
)

func testHandler(t *testing.T) (http.Handler, *core.Lifecycle) {
	t.Helper()
	cfg, err := LoadConfig(func(name string) string {
		if name == "GATEWAY_MASTER_KEY" {
			return "sk-test"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := &core.Lifecycle{}
	return Handler(cfg, lifecycle), lifecycle
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestLiveness_AnswersLikeLitellmOnBothPaths(t *testing.T) {
	h, _ := testHandler(t)
	for _, path := range []string{"/health/liveliness", "/health/liveness"} {
		rec := get(h, path)
		if rec.Code != http.StatusOK || rec.Body.String() != "\"I'm alive!\"\n" {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type = %q", path, ct)
		}
	}
}

func TestLiveness_503WhileShuttingDown(t *testing.T) {
	h, lifecycle := testHandler(t)
	lifecycle.BeginShutdown()

	rec := get(h, "/health/liveness")
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "{\"status\":\"shutting_down\"}\n" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestUndeclaredPath_FastAPIStyle404(t *testing.T) {
	h, _ := testHandler(t)
	rec := get(h, "/nowhere")
	if rec.Code != http.StatusNotFound || rec.Body.String() != "{\"detail\":\"Not Found\"}\n" {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestStartupRefusedWithoutMasterKey(t *testing.T) {
	if _, err := LoadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("마스터 키 없이 설정이 만들어졌다")
	}
}

func TestEveryRouteDeclaresAccess(t *testing.T) {
	for _, r := range routes(&core.Lifecycle{}) {
		if r.Handler == nil || r.Method == "" || r.Path == "" {
			t.Errorf("빈 라우트: %+v", r)
		}
		if !r.Access.IsPublic() && !anyRoleAllowed(r.Access) {
			t.Errorf("%s: 누구도 부를 수 없는 라우트다(권한 선언 누락)", r.Pattern())
		}
	}
}

func anyRoleAllowed(a core.Access) bool {
	for _, role := range []core.Role{core.RoleProxyAdmin, core.RoleProxyAdminViewer, core.RoleInternalUser, core.RoleInternalUserViewer, core.RoleTeam} {
		if a.Allows(role) {
			return true
		}
	}
	return false
}
