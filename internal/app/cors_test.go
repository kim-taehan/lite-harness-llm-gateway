package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 대시보드를 다른 호스트에서 낼 때 브라우저가 보내는 CORS 요청(litellm 기본과 같음)

func TestCORS_DefaultAllowsAnyOriginWithoutCredentials(t *testing.T) {
	h := newAPI(t).h
	pre := httptest.NewRequest(http.MethodOptions, "/v2/login", nil)
	pre.Header.Set("Origin", "http://ui.corp.local:3000")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	pre.Header.Set("Access-Control-Request-Headers", "content-type,authorization")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, pre)
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" ||
		rec.Header().Get("Access-Control-Allow-Headers") != "content-type,authorization" || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("preflight = %d %v", rec.Code, rec.Header())
	}
	get := httptest.NewRequest(http.MethodGet, "/litellm/.well-known/litellm-ui-config", nil)
	get.Header.Set("Origin", "http://ui.corp.local:3000")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("본 요청 = %d %v", rec.Code, rec.Header())
	}
}

func TestCORS_ListedOriginsEchoWithCredentials(t *testing.T) {
	cfg := loadCORS(func(k string) string {
		if k == "GATEWAY_CORS_ORIGINS" {
			return "http://ui.corp.local:3000, http://other.local"
		}
		return ""
	})
	h := withCORS(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	call := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/x", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "GET")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call("http://ui.corp.local:3000"); rec.Header().Get("Access-Control-Allow-Origin") != "http://ui.corp.local:3000" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("허용 출처 = %v", rec.Header())
	}
	if rec := call("http://evil.local"); rec.Code != 400 {
		t.Errorf("허용 안 된 출처: %d", rec.Code)
	}
}
