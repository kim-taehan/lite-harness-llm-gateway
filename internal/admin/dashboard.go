package admin

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"llm-gateway/internal/core"
)

// litellm 대시보드(Next.js 정적 빌드)를 실행 파일 안에서 직접 내준다. 별도 nginx 컨테이너가 필요 없고 API 와 같은 출처라 웹 SSO 도 된다.
// 파일은 빌드할 때 build.sh 가 uiassets/ 에 복사한다(저장소에는 넣지 않는다). 비어 있으면(index.html 이 없으면) 이 라우트를 등록하지 않는다.
// 경로 규칙은 대시보드 nginx.conf 를 그대로 옮긴 것이다(대시보드 JS 가 이 경로를 박아 두었다).

//go:embed all:uiassets
var embeddedDashboard embed.FS

// dashboardAssets 는 내장된 대시보드 파일이다. 없으면 nil 이다
func dashboardAssets() fs.FS {
	sub, err := fs.Sub(embeddedDashboard, "uiassets")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// DashboardEmbedded 는 이 실행 파일에 대시보드가 들어 있는지다(기동 로그용)
func DashboardEmbedded() bool { return dashboardAssets() != nil }

// 해시가 붙은 번들이라 오래 캐시해도 된다
const (
	cacheImmutable  = "public, max-age=31536000, immutable"
	cacheRevalidate = "no-cache"
)

// dashboardRoutes 는 대시보드 파일 라우트다. 모두 인증 없이 연다(로그인 화면도 이 파일들이다). assets 가 nil 이면 없다
func dashboardRoutes(assets fs.FS) []core.Route {
	if assets == nil {
		return nil
	}
	public := core.Public()
	route := func(path string, handler http.HandlerFunc) core.Route {
		return core.Route{Method: http.MethodGet, Path: path, Access: public, Handler: handler}
	}
	hashed := func(prefix string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			serveAsset(w, r, assets, prefix+r.PathValue("path"), cacheImmutable)
		}
	}
	return []core.Route{
		route("/ui", func(w http.ResponseWriter, r *http.Request) { serveAsset(w, r, assets, "index.html", cacheRevalidate) }),
		route("/ui/{page...}", func(w http.ResponseWriter, r *http.Request) { servePage(w, r, assets, r.PathValue("page")) }),
		// next.config 의 assetPrefix 가 /litellm-asset-prefix 라 HTML 이 그 경로로 번들을 부른다. 실제 파일은 _next/ 아래에 있다
		route("/litellm-asset-prefix/_next/{path...}", hashed("_next/")),
		route("/_next/{path...}", hashed("_next/")),
		route("/assets/{path...}", hashed("assets/")),
		route("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
			serveAsset(w, r, assets, "favicon.ico", "public, max-age=86400")
		}),
	}
}

// servePage 는 /ui/<페이지> 다. 페이지는 <페이지>.html, <페이지>/index.html 순으로 찾고, 클라이언트 라우트는 index.html 로 답한다.
// Next.js 가 내는 .txt(RSC 조각)와 /ui/assets/ 는 파일 그대로만 답한다: 없으면 404 여야 한다(HTML 로 답하면 클라이언트 라우터가 로그인 화면을 무한히 오간다)
func servePage(w http.ResponseWriter, r *http.Request, assets fs.FS, page string) {
	page = strings.Trim(path.Clean("/"+page), "/")
	switch {
	case page == "litellm-runtime-config.json":
		// 백엔드를 따로 두지 않으므로 대시보드는 자기 출처(이 서버)로 API 를 부른다. 파일이 없다는 뜻의 404 다
		http.NotFound(w, r)
	case strings.HasPrefix(page, "assets/"):
		serveAsset(w, r, assets, page, cacheRevalidate)
	case strings.HasSuffix(page, ".txt"):
		serveAsset(w, r, assets, page, cacheRevalidate)
	default:
		for _, candidate := range []string{page + ".html", page + "/index.html", "index.html"} {
			if page == "" && candidate != "index.html" {
				continue
			}
			if serveAsset(w, r, assets, candidate, cacheRevalidate) {
				return
			}
		}
		http.NotFound(w, r)
	}
}

// serveAsset 은 파일을 내준다. 없으면 404 를 쓰고 false 다
func serveAsset(w http.ResponseWriter, r *http.Request, assets fs.FS, name, cacheControl string) bool {
	if !fs.ValidPath(name) {
		http.NotFound(w, r)
		return false
	}
	content, err := fs.ReadFile(assets, name)
	if err != nil {
		if name != "index.html" && !strings.HasSuffix(name, ".html") {
			http.NotFound(w, r)
		}
		return false
	}
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
	return true
}

// contentType 은 시스템 mime 표가 없는 환경(scratch 이미지)에서도 같은 값이 나오도록 자주 쓰는 확장자를 직접 정한다
func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	}
	if m := mime.TypeByExtension(path.Ext(name)); m != "" {
		return m
	}
	return "application/octet-stream"
}
