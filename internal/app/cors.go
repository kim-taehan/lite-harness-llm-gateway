package app

import (
	"net/http"
	"slices"
	"strings"
)

// CORS 는 litellm(Starlette CORSMiddleware)과 같게 한다. 대시보드를 다른 호스트에서 낼 때(LITELLM_BACKEND_URL 로 API 를 직접 부름)
// 브라우저가 우리 API 를 부를 수 있어야 하기 때문이다. 같은 출처로 낼 때는 영향이 없다.
// 기본은 litellm 과 같다: 모든 출처 허용, 이때 자격 증명(쿠키)은 끈다(litellm _get_cors_config)

// CORSConfig 는 GATEWAY_CORS_ORIGINS(쉼표, 기본 *)와 GATEWAY_CORS_ALLOW_CREDENTIALS(기본: 출처가 * 가 아닐 때만 참)다
type CORSConfig struct {
	Origins     []string
	Credentials bool
}

func loadCORS(getenv func(string) string) CORSConfig {
	var origins []string
	for _, o := range strings.Split(getenv("GATEWAY_CORS_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		origins = []string{"*"}
	}
	credentials := !slices.Contains(origins, "*")
	if v := strings.TrimSpace(getenv("GATEWAY_CORS_ALLOW_CREDENTIALS")); v != "" {
		credentials = strings.EqualFold(v, "true")
	}
	return CORSConfig{Origins: origins, Credentials: credentials}
}

const corsMethods = "DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT"

func withCORS(cfg CORSConfig, next http.Handler) http.Handler {
	allowAll := slices.Contains(cfg.Origins, "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		allowed := allowAll || slices.Contains(cfg.Origins, origin)
		preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
		if preflight {
			if !allowed {
				http.Error(w, "Disallowed CORS origin", http.StatusBadRequest)
				return
			}
			setAllowOrigin(w.Header(), cfg, allowAll, origin)
			w.Header().Set("Access-Control-Allow-Methods", corsMethods)
			if h := r.Header.Get("Access-Control-Request-Headers"); h != "" {
				w.Header().Set("Access-Control-Allow-Headers", h) // litellm 은 allow_headers=["*"] 라 요청한 헤더를 그대로 허용한다
			}
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusOK)
			return
		}
		if allowed {
			setAllowOrigin(w.Header(), cfg, allowAll, origin)
		}
		next.ServeHTTP(w, r)
	})
}

// setAllowOrigin 은 Starlette 와 같다: 모든 출처이고 자격 증명이 없으면 *, 아니면 요청 출처를 되돌려 준다
func setAllowOrigin(h http.Header, cfg CORSConfig, allowAll bool, origin string) {
	if allowAll && !cfg.Credentials {
		h.Set("Access-Control-Allow-Origin", "*")
	} else {
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
	}
	if cfg.Credentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
}
