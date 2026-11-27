package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"llm-gateway/internal/core"
)

// 대시보드 로그인(L 문서). 대시보드는 token 쿠키 안 JWT 의 key 클레임을 모든 API 호출의 Bearer 로 쓴다.
// 그 key 는 데스크탑과 같은 자기 완결 로그인 키다. litellm 은 로그인마다 DB 에 가상 키를 하나씩 만들지만 우리는 만들지 않는다(설계 9장)

const (
	uiAdminUsername      = "admin"           // litellm UI_USERNAME 기본. 비밀번호는 마스터 키(UI_PASSWORD 가 없을 때와 같다)
	uiAdminUserID        = "default_user_id" // litellm PROXY_ADMIN_ID 기본
	invalidUICredentials = "Invalid credentials used to access UI. Check 'UI_USERNAME' and 'UI_PASSWORD', or the password set for your user"
)

func (l *Login) dashboardRoutes() []core.Route {
	public, signedIn := core.Public(), core.Authenticated()
	return []core.Route{
		{Method: http.MethodGet, Path: "/.well-known/litellm-ui-config", Access: public, Handler: l.uiConfig},
		{Method: http.MethodGet, Path: "/litellm/.well-known/litellm-ui-config", Access: public, Handler: l.uiConfig},
		{Method: http.MethodPost, Path: "/v2/login", Access: public, Handler: l.loginJSON},
		{Method: http.MethodPost, Path: "/login", Access: public, Handler: l.loginForm},
		{Method: http.MethodGet, Path: "/management/v1/users/me", Access: signedIn, Handler: l.currentUser},
		{Method: http.MethodGet, Path: "/sso/get/ui_settings", Access: signedIn, Handler: l.ssoUISettings},
		{Method: http.MethodPost, Path: "/session/logout", Access: signedIn, Handler: l.logout},
	}
}

// uiConfig 는 대시보드가 부팅 때 가장 먼저 부른다(L 2.2). proxy_base_url 을 null 로 두면 대시보드는 자기 출처로 API 를 부른다
// (대시보드를 nginx 가 같은 출처로 내고 API 를 우리에게 넘기는 배치, 설계 9장)
func (l *Login) uiConfig(w http.ResponseWriter, _ *http.Request) {
	core.WriteJSON(w, http.StatusOK, map[string]any{
		"server_root_path": "", "proxy_base_url": nil, "gateway_base_url": nil,
		"auto_redirect_to_sso": false, "admin_ui_disabled": false, "sso_configured": l.methods.SSO,
		"hide_default_credentials_hint": true, "is_control_plane": false, "mcp_stdio_enabled": false, "workers": []any{},
	})
}

func (l *Login) loginJSON(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	// 깨진 본문은 빈 자격 증명으로 본다(litellm 은 "None" 문자열로 바꿔 401). 크기는 제한한다(공개 라우트다)
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
	token, ok := l.signInUI(w, r, body.Username, body.Password)
	if !ok {
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"redirect_url": l.publicURL + "/ui?login=success", "token": token})
}

// loginForm 은 서버가 내는 HTML 로그인 폼(SSO 를 쓰지 않을 때의 /sso/key/generate)이 보낸다. 대시보드는 /v2/login 을 쓴다
func (l *Login) loginForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := l.signInUI(w, r, r.PostFormValue("username"), r.PostFormValue("password")); ok {
		http.Redirect(w, r, l.publicURL+"/ui?login=success", http.StatusSeeOther)
	}
}

// signInUI 는 관리자(admin + 마스터 키) 또는 DB 사용자 비밀번호로 로그인하고 token 쿠키를 심는다.
// DB 사용자 비밀번호는 로그인 방식에 password 가 있을 때만 된다. 관리자 로그인은 sso 만 골라도 된다(대시보드를 처음 여는 길)
func (l *Login) signInUI(w http.ResponseWriter, r *http.Request, username, password string) (string, bool) {
	ctx := r.Context()
	user, err := l.gate.attempt(username, l.proxies.clientIP(r), func() (User, error) {
		switch {
		case username == uiAdminUsername && l.master.Matches(password):
			return l.users.ensureProxyAdmin(ctx, uiAdminUserID)
		case l.methods.Password:
			return l.users.checkPassword(ctx, username, password)
		}
		return User{}, ErrBadCredentials
	})
	switch {
	case errors.Is(err, ErrTooManyAttempts):
		core.ProxyError{Status: http.StatusTooManyRequests, Type: "auth_error", Param: "None", Message: "Too many failed sign-in attempts. Try again later.",
			Headers: map[string]string{"Retry-After": "900"}}.Write(w)
		return "", false
	case errors.Is(err, ErrBadCredentials):
		// 시도 제한에 걸린 요청(위 분기)은 남기지 않는다. 공격자가 감사 기록을 불리지 못하게 제한이 걸리기 전까지만 남는다
		l.record(r, "login_failed", username, map[string]any{"method": uiMethodPassword, "reason": "bad_credentials"})
		core.ProxyError{Status: http.StatusUnauthorized, Type: "auth_error", Param: "invalid_credentials", Message: invalidUICredentials}.Write(w)
		return "", false
	case err != nil:
		l.logger.Error("대시보드 로그인 실패", "err", err)
		core.ProxyError{Status: http.StatusInternalServerError, Type: "auth_error", Param: "None", Message: "Failed to sign in"}.Write(w)
		return "", false
	}
	return l.startUISession(w, r, user, uiMethodPassword)
}

// startUISession 은 대시보드 세션 키와 그것을 담은 JWT 를 만들어 token 쿠키로 심는다(비밀번호·SSO 공통)
func (l *Login) startUISession(w http.ResponseWriter, r *http.Request, user User, method string) (string, bool) {
	token, err := l.uiToken(user, method)
	if err != nil {
		l.logger.Error("대시보드 로그인 실패", "err", err)
		core.ProxyError{Status: http.StatusInternalServerError, Type: "auth_error", Param: "None", Message: "Failed to sign in"}.Write(w)
		return "", false
	}
	// HttpOnly 가 아니다. 대시보드가 document.cookie 로 읽는다(L 3.7)
	http.SetCookie(w, &http.Cookie{Name: "token", Value: token, Path: "/", SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	l.record(r, "login_success", user.UserID, map[string]any{"method": method})
	return token, true
}

// uiToken 은 L 3.6 의 클레임이다. premium_user 는 늘 true 다(유료 기능을 라이선스로 잠그지 않는다, 설계 9장).
// 발급 시각은 DB(마이크로초)와 같은 정밀도로 자른다. 로그아웃 시각과 정확히 비교하기 위해서다
func (l *Login) uiToken(user User, method string) (string, error) {
	now := l.now().UTC().Truncate(time.Microsecond)
	expires := now.Add(loginKeyTTL)
	key, err := l.keys.issue(loginClaims{UserID: user.UserID, Expires: expires, UIMethod: method, IssuedAt: now})
	if err != nil {
		return "", err
	}
	claims := uiClaims(user, method, key)
	claims["exp"] = expires.Unix()
	return signUIToken(l.master, claims)
}

// uiClaims 는 litellm ReturnedUITokenObject 다(exp 는 부르는 쪽이 정한다)
func uiClaims(user User, method, key string) map[string]any {
	return map[string]any{
		"user_id": user.UserID, "key": key, "user_email": user.UserEmail, "user_role": user.EffectiveRole(), "login_method": method,
		"premium_user": true, "auth_header_name": "Authorization", "disabled_non_admin_personal_key_creation": false,
		"server_root_path": "", "password_reset_required": method == uiMethodPassword && user.PasswordResetRequired,
	}
}

// webSignIn 은 대시보드 SSO 버튼이다(L 5.1). return_to 는 litellm 처럼 무시한다(대시보드가 보내는 절대 URL 은 원본도 버린다).
// SSO 를 쓰지 않으면 litellm 처럼 /login 으로 보내는 로그인 폼을 준다
func (l *Login) webSignIn(w http.ResponseWriter, r *http.Request) {
	if l.oidc == nil {
		writePage(w, http.StatusOK, "signin", page{Title: "LiteLLM Login", Action: l.publicURL + "/login", Password: true})
		return
	}
	state := randomURLSafe(16) // CLI 의 litellm-session-token: 접두어가 없어 콜백이 대시보드 흐름으로 가른다
	target, verifier := l.oidc.authorizeURL(state)
	for name, value := range map[string]string{"litellm_oauth_state": state, "litellm_pkce_verifier": verifier} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(cliFlowTTL.Seconds()),
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// webCallback 은 대시보드 SSO 의 끝이다(L 5.2). 세션 JWT 는 쿠키로만 간다. 대시보드가 /ui/ 에서 그 쿠키를 읽는다
func (l *Login) webCallback(w http.ResponseWriter, r *http.Request) {
	verifier, err := r.Cookie("litellm_pkce_verifier")
	if err != nil {
		core.ProxyError{Status: 400, Type: "auth_error", Param: "state", Message: "Missing PKCE verifier cookie. Start the SSO login again."}.Write(w)
		return
	}
	user, ok := l.ssoUser(w, r, verifier.Value)
	if !ok {
		return
	}
	for _, name := range []string{"litellm_oauth_state", "litellm_pkce_verifier"} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
	}
	if _, ok := l.startUISession(w, r, user, uiMethodSSO); ok {
		http.Redirect(w, r, l.publicURL+"/ui/?login=success", http.StatusSeeOther)
	}
}

// currentUser 는 대시보드가 로그인 직후 부른다. 실패하면 대시보드가 바로 로그아웃시킨다(L 4.3)
func (l *Login) currentUser(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	var email *string
	user, err := l.users.Find(r.Context(), p.UserID)
	switch {
	case err == nil:
		email = user.UserEmail
	case !errors.Is(err, ErrUserNotFound):
		l.logger.Error("현재 사용자 읽기 실패", "err", err)
		core.ProxyError{Status: http.StatusInternalServerError, Message: "Internal Server Error", Type: "internal_server_error"}.Write(w)
		return
	}
	var role any
	if p.Role != "" {
		role = p.Role
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"user_id": p.UserID, "user_role": role, "user_email": email})
}

// ssoUISettings 의 PROXY_LOGOUT_URL 이 비면 대시보드는 로그아웃 뒤 로그인 화면으로 간다(L 7.2)
func (l *Login) ssoUISettings(w http.ResponseWriter, _ *http.Request) {
	core.WriteJSON(w, http.StatusOK, map[string]any{
		"PROXY_BASE_URL": nil, "PROXY_LOGOUT_URL": nil, "LITELLM_UI_API_DOC_BASE_URL": nil, "DEFAULT_TEAM_DISABLED": false,
		"SSO_ENABLED": l.methods.SSO, "NUM_SPEND_LOGS_ROWS": 0, "DISABLE_EXPENSIVE_DB_QUERIES": false,
	})
}

// logout 은 그 사용자의 대시보드 세션을 모두 끊는다. litellm 은 이 세션 하나만 지운다(세션 키가 DB 에 없어서 다르다, 설계 9장)
func (l *Login) logout(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if p.UIMethod == "" {
		core.DetailMessage(http.StatusForbidden, "Only UI session tokens can be revoked through this endpoint.").Write(w)
		return
	}
	if err := l.users.revokeUISessions(r.Context(), p.UserID, l.now().UTC().Truncate(time.Microsecond)); err != nil {
		l.logger.Error("세션 폐기 실패", "err", err)
		core.DetailMessage(http.StatusInternalServerError, "Failed to revoke session").Write(w)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "token", Value: "", Path: "/", MaxAge: -1})
	l.record(r, "logout", p.UserID, map[string]any{"method": p.UIMethod})
	core.WriteJSON(w, http.StatusOK, map[string]any{"message": "Session revoked."})
}
