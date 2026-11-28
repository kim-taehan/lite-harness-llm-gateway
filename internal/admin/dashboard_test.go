package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// 대시보드 정적 파일: 경로 규칙은 대시보드의 nginx.conf 와 같다(대시보드 JS 가 이 경로를 박아 두었다)

func dashboardMux(t *testing.T) *http.ServeMux {
	t.Helper()
	assets := fstest.MapFS{
		"index.html":              {Data: []byte("<html>home</html>")},
		"api-keys.html":           {Data: []byte("<html>keys</html>")},
		"teams/index.html":        {Data: []byte("<html>teams</html>")},
		"login.txt":               {Data: []byte("rsc-payload")},
		"assets/logo.svg":         {Data: []byte("<svg/>")},
		"_next/static/app.js":     {Data: []byte("console.log(1)")},
		"_next/static/font.woff2": {Data: []byte("font")},
		"favicon.ico":             {Data: []byte("ico")},
	}
	mux := http.NewServeMux()
	for _, route := range dashboardRoutes(assets) {
		if !route.Access.IsPublic() {
			t.Errorf("%s 는 인증 없이 열려야 한다(로그인 화면도 이 파일이다)", route.Path)
		}
		mux.Handle(route.Pattern(), route.Handler)
	}
	return mux
}

func fetch(mux http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestDashboard_PagesFollowTheNginxRules(t *testing.T) {
	mux := dashboardMux(t)
	for target, want := range map[string]string{
		"/ui":                   "home",
		"/ui/":                  "home",
		"/ui/api-keys":          "keys",  // <페이지>.html
		"/ui/api-keys/":         "keys",  // 끝의 / 는 무시
		"/ui/teams":             "teams", // <페이지>/index.html
		"/ui/some/client/route": "home",  // 클라이언트 라우트는 index.html
		"/ui/index.html":        "home",
		"/ui/%2e%2e/api-keys":   "keys", // 경로를 벗어나는 시도는 정리돼 파일 밖으로 못 나간다
	} {
		rec := fetch(mux, target)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s = %d %q, %q 가 있어야 한다", target, rec.Code, rec.Body.String(), want)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s Content-Type = %q", target, ct)
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s 는 배포 때 바뀌므로 매번 확인해야 한다: %q", target, rec.Header().Get("Cache-Control"))
		}
	}
}

func TestDashboard_MissingRSCPayloadsAndAssetsAre404NeverHTML(t *testing.T) {
	mux := dashboardMux(t)
	if rec := fetch(mux, "/ui/login.txt"); rec.Code != 200 || rec.Body.String() != "rsc-payload" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("있는 .txt = %d %q", rec.Code, rec.Body.String())
	}
	// 없는 .txt 를 index.html 로 답하면 대시보드 라우터가 로그인 화면을 무한히 오간다
	for _, target := range []string{"/ui/missing.txt", "/ui/deep/missing.txt", "/ui/assets/missing.png", "/ui/litellm-runtime-config.json"} {
		if rec := fetch(mux, target); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<html>") {
			t.Errorf("%s = %d %q, HTML 이 아닌 404 여야 한다", target, rec.Code, rec.Body.String())
		}
	}
	if rec := fetch(mux, "/ui/assets/logo.svg"); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("/ui/assets 의 파일 = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestDashboard_HashedBundlesAreCachedForeverUnderBothPrefixes(t *testing.T) {
	mux := dashboardMux(t)
	for _, target := range []string{"/_next/static/app.js", "/litellm-asset-prefix/_next/static/app.js"} {
		rec := fetch(mux, target)
		if rec.Code != 200 || rec.Body.String() != "console.log(1)" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
			t.Errorf("%s = %d %q %q", target, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("%s Cache-Control = %q", target, rec.Header().Get("Cache-Control"))
		}
	}
	if rec := fetch(mux, "/_next/static/font.woff2"); rec.Header().Get("Content-Type") != "font/woff2" {
		t.Errorf("woff2 Content-Type = %q(시스템 mime 표가 없는 이미지에서도 같아야 한다)", rec.Header().Get("Content-Type"))
	}
	if rec := fetch(mux, "/favicon.ico"); rec.Code != 200 {
		t.Errorf("favicon = %d", rec.Code)
	}
	for _, target := range []string{"/_next/static/none.js", "/assets/none.png", "/_next/..%2findex.html", "/_next/static/..%2f..%2findex.html"} {
		if rec := fetch(mux, target); rec.Code == 200 {
			t.Errorf("%s 가 200 으로 답했다(없는 파일·경로 이탈은 404 여야 한다): %q", target, rec.Body.String())
		}
	}
}

func TestDashboard_NoAssetsMeansNoRoutes(t *testing.T) {
	if routes := dashboardRoutes(nil); len(routes) != 0 {
		t.Errorf("대시보드가 없는데 라우트가 %d개 등록된다", len(routes))
	}
}
