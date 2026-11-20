package auth

import (
	"context"
	"net/http"
	"strings"

	"llm-gateway/internal/core"
)

// Principal 은 요청 하나 동안 다른 모듈에 넘기는 신원이다
type Principal struct {
	UserID string
	Role   core.Role
}

type principalKey struct{}

// PrincipalFrom 은 권한 검사를 통과한 요청의 신원이다. 공개 라우트에는 없다
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// litellm 과 같은 메시지(auth/user_api_key_auth.py). 응답에서는 앞에 "Authentication Error, " 가 붙는다
const (
	reasonNoKey        = "No api key passed in."
	reasonMalformedKey = "Malformed API Key passed in. Ensure Key has `Bearer ` prefix."
	reasonInvalidKey   = "Invalid proxy server token passed."
)

// litellm 은 마스터 키 호출자를 user_id "default_user_id" 의 proxy_admin 으로 본다
var masterPrincipal = Principal{UserID: "default_user_id", Role: core.RoleProxyAdmin}

// Authenticator 는 요청 헤더의 키로 신원을 정한다. 지금은 마스터 키만 안다(가상 키는 M2)
type Authenticator struct {
	masterKey MasterKey
}

func NewAuthenticator(masterKey MasterKey) *Authenticator {
	return &Authenticator{masterKey: masterKey}
}

// Authenticate 는 신원 또는 거절 이유를 돌려준다. 거절은 예상되는 결과라 error 가 아니라 이유 문자열이다
func (a *Authenticator) Authenticate(header http.Header) (Principal, string) {
	key, present := extractKey(header)
	switch {
	case !present:
		return Principal{}, reasonNoKey
	case key == "":
		return Principal{}, reasonMalformedKey
	case a.masterKey.Matches(key):
		return masterPrincipal, ""
	default:
		return Principal{}, reasonInvalidKey
	}
}

// litellm 은 Authorization 이 먼저이고 "Bearer "·"bearer " 접두어를 떼며, 접두어가 없으면 값 그대로 쓴다
func extractKey(header http.Header) (string, bool) {
	if values, ok := header["Authorization"]; ok && len(values) > 0 {
		key := strings.TrimPrefix(strings.TrimPrefix(values[0], "Bearer "), "bearer ")
		return strings.TrimSpace(key), true
	}
	if values, ok := header["X-Api-Key"]; ok && len(values) > 0 {
		return strings.TrimSpace(values[0]), true
	}
	return "", false
}

// Guard 는 라우트를 권한 검사로 감싼다. 앱은 모든 라우트를 이것으로 감싸 등록한다
func (a *Authenticator) Guard(route core.Route) http.Handler {
	if route.Access.IsPublic() {
		return route.Handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, reason := a.Authenticate(r.Header)
		if reason != "" {
			core.AuthError(reason).Write(w)
			return
		}
		if !route.Access.Allows(principal.Role) {
			core.AuthError(roleDenied(r, principal)).Write(w)
			return
		}
		route.Handler(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}

// litellm route_checks.py 의 관리 라우트 거부 메시지와 같은 모양
func roleDenied(r *http.Request, p Principal) string {
	return "Only proxy admin can be used to generate, delete, update info for new keys/users/teams. " +
		"Route=" + r.URL.Path + ". Your role=" + string(p.Role) + "."
}
