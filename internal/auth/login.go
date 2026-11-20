package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"llm-gateway/internal/core"
)

// LoginMethods 는 이 빌드에 들어간 로그인 방식이다. 빌드 시점에 정하고 실행 중에는 바꿀 수 없다(설계 6장)
type LoginMethods struct {
	SSO      bool
	Password bool
}

// ParseLoginMethods 는 build.sh 가 새긴 값("sso,password" 등)을 읽는다
func ParseLoginMethods(value string) (LoginMethods, error) {
	var m LoginMethods
	for _, part := range strings.Split(value, ",") {
		switch strings.TrimSpace(part) {
		case "sso":
			m.SSO = true
		case "password":
			m.Password = true
		default:
			return LoginMethods{}, fmt.Errorf("로그인 방식 %q 를 모른다(sso, password 만 된다)", part)
		}
	}
	return m, nil
}

func (m LoginMethods) String() string {
	var parts []string
	if m.SSO {
		parts = append(parts, "sso")
	}
	if m.Password {
		parts = append(parts, "password")
	}
	return strings.Join(parts, ",")
}

// Login 은 데스크탑(CLI) 로그인이다(K 2장). 브라우저에서 SSO 또는 아이디·비밀번호로 로그인하면 로그인 키를 발급해 폴링으로 넘긴다
type Login struct {
	methods   LoginMethods
	flows     *cliFlows
	attempts  *loginAttempts
	keys      *loginKeys
	master    MasterKey   // 대시보드 관리자 로그인과 token JWT 서명
	oidc      *oidcClient // SSO 를 넣지 않은 빌드면 nil
	users     *Users
	teams     *Teams
	publicURL string
	now       func() time.Time
	logger    *slog.Logger
}

// NewLogin 은 SSO 가 들어간 빌드면 Keycloak 설정을 읽는다. 설정이 없거나 닿지 않으면 기동을 멈춘다
func NewLogin(ctx context.Context, methods LoginMethods, master MasterKey, oidc OIDCConfig, users *Users, teams *Teams,
	client *http.Client, now func() time.Time, logger *slog.Logger) (*Login, error) {
	l := &Login{methods: methods, flows: newCLIFlows(now), attempts: newLoginAttempts(now), keys: newLoginKeys(master), master: master, users: users, teams: teams,
		publicURL: strings.TrimRight(oidc.PublicURL, "/"), now: now, logger: logger}
	if methods.SSO {
		if oidc.Issuer == "" || oidc.ClientID == "" || oidc.PublicURL == "" {
			return nil, errors.New("SSO 가 들어간 빌드다. GATEWAY_SSO_ISSUER, GATEWAY_SSO_CLIENT_ID, GATEWAY_PUBLIC_URL 이 필요하다")
		}
		discovered, err := discoverOIDC(ctx, oidc, client)
		if err != nil {
			return nil, err
		}
		l.oidc = discovered
	}
	return l, nil
}

// Routes 는 로그인 라우트다. 브라우저·데스크탑이 키 없이 부르므로 공개다. 빌드에 없는 방식의 라우트는 등록하지 않는다(404)
func (l *Login) Routes() []core.Route {
	public := core.Public()
	routes := []core.Route{
		{Method: http.MethodPost, Path: "/sso/cli/start", Access: public, Handler: l.start},
		{Method: http.MethodGet, Path: "/sso/key/generate", Access: public, Handler: l.signIn},
		{Method: http.MethodPost, Path: "/sso/cli/complete/{login_id}", Access: public, Handler: l.complete},
		{Method: http.MethodGet, Path: "/sso/cli/poll/{login_id}", Access: public, Handler: l.poll},
	}
	routes = append(routes, l.dashboardRoutes()...)
	if l.methods.SSO {
		routes = append(routes, core.Route{Method: http.MethodGet, Path: "/sso/callback", Access: public, Handler: l.callback})
	}
	if l.methods.Password {
		routes = append(routes, core.Route{Method: http.MethodPost, Path: "/sso/cli/password/{login_id}", Access: public, Handler: l.passwordSignIn})
	}
	return routes
}

func detail(w http.ResponseWriter, status int, message string) {
	core.DetailError{Status: status, Detail: message}.Write(w)
}

func (l *Login) start(w http.ResponseWriter, r *http.Request) {
	loginID, secret, code, ok := l.flows.start(sha256Hex(clientIP(r)))
	if !ok {
		detail(w, http.StatusTooManyRequests, "Too many CLI login attempts. Try again later.")
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"login_id": loginID, "poll_secret": secret, "user_code": code, "expires_in": int(cliFlowTTL.Seconds())})
}

// signIn 은 브라우저 첫 화면이다. SSO 만 있으면 Keycloak 으로 보내고, 비밀번호가 있으면 로그인 화면을 보인다(둘 다면 둘 중 고른다).
// source=litellm-cli 가 아니면 대시보드의 "Login with SSO" 다
func (l *Login) signIn(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("source") != "litellm-cli" {
		l.webSignIn(w, r)
		return
	}
	loginID := q.Get("key")
	if !l.flowExists(w, loginID) {
		return
	}
	if l.methods.SSO && (!l.methods.Password || q.Get("method") == "sso") {
		l.redirectToSSO(w, r, loginID)
		return
	}
	ssoURL := ""
	if l.methods.SSO {
		ssoURL = l.publicURL + "/sso/key/generate?" + url.Values{"source": {"litellm-cli"}, "key": {loginID}, "method": {"sso"}}.Encode()
	}
	writePage(w, http.StatusOK, "signin", page{Title: "LiteLLM CLI Login", Action: l.publicURL + "/sso/cli/password/" + loginID,
		SSO: l.methods.SSO, Password: l.methods.Password, SSOURL: ssoURL})
}

// redirectToSSO 는 Keycloak 로 보낸다. state 는 litellm 과 같은 litellm-session-token:<login_id> 이고, 같은 값을 쿠키에 묶어 콜백에서 맞춘다
func (l *Login) redirectToSSO(w http.ResponseWriter, r *http.Request, loginID string) {
	state := "litellm-session-token:" + loginID
	target, verifier := l.oidc.authorizeURL(state)
	l.flows.with(loginID, func(f *cliFlow) bool { f.pkceVerifier = verifier; return true })
	http.SetCookie(w, &http.Cookie{Name: "litellm_oauth_state", Value: state, Path: "/", MaxAge: int(cliFlowTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (l *Login) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		message := "OAuth error: " + e
		if d := q.Get("error_description"); d != "" {
			message += ", error_description: " + d
		}
		detail(w, http.StatusUnauthorized, message)
		return
	}
	state := q.Get("state")
	cookie, err := r.Cookie("litellm_oauth_state")
	if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		core.ProxyError{Status: 400, Type: "auth_error", Param: "state",
			Message: "Invalid OAuth state parameter — does not match the browser-bound state cookie."}.Write(w)
		return
	}
	loginID, isCLI := strings.CutPrefix(state, "litellm-session-token:")
	if !isCLI {
		l.webCallback(w, r)
		return
	}
	loginID, _, _ = strings.Cut(loginID, ":")
	var verifier string
	if !l.flows.with(loginID, func(f *cliFlow) bool { verifier = f.pkceVerifier; return false }) {
		flowNotFound(w)
		return
	}
	if user, ok := l.ssoUser(w, r, verifier); ok {
		l.finishSignIn(w, r, loginID, user)
	}
}

// ssoUser 는 콜백의 코드를 토큰으로 바꾸고 그 사용자를 저장한다(데스크탑·대시보드 공통)
func (l *Login) ssoUser(w http.ResponseWriter, r *http.Request, verifier string) (User, bool) {
	code := r.URL.Query().Get("code")
	if code == "" {
		core.ProxyError{Status: 400, Type: "auth_error", Param: "code", Message: "Missing authorization code in callback"}.Write(w)
		return User{}, false
	}
	claims, err := l.oidc.exchange(r.Context(), code, verifier)
	if err != nil {
		l.logger.Warn("SSO 코드 교환 실패", "err", err)
		core.ProxyError{Status: 401, Type: "auth_error", Param: "None", Message: err.Error()}.Write(w)
		return User{}, false
	}
	user, err := l.users.upsertSSOUser(r.Context(), claims)
	if err != nil {
		l.logger.Error("SSO 사용자 저장 실패", "err", err)
		detail(w, http.StatusInternalServerError, "Failed to retrieve user information from SSO")
		return User{}, false
	}
	return user, true
}

func (l *Login) passwordSignIn(w http.ResponseWriter, r *http.Request) {
	loginID := r.PathValue("login_id")
	if !l.flowExists(w, loginID) {
		return
	}
	username := r.PostFormValue("username")
	signinPage := page{Title: "LiteLLM CLI Login", Action: l.publicURL + "/sso/cli/password/" + loginID, Password: true}
	attempt := attemptKey(username, clientIP(r))
	if l.attempts.blocked(attempt) {
		signinPage.Error = "로그인 시도가 너무 많습니다. 잠시 후 다시 시도하세요."
		writePage(w, http.StatusTooManyRequests, "signin", signinPage)
		return
	}
	user, err := l.users.checkPassword(r.Context(), username, r.PostFormValue("password"))
	if errors.Is(err, ErrBadCredentials) {
		l.attempts.fail(attempt)
		signinPage.Error = "이메일(아이디) 또는 비밀번호가 맞지 않습니다."
		writePage(w, http.StatusUnauthorized, "signin", signinPage)
		return
	}
	if err != nil {
		l.logger.Error("비밀번호 로그인 실패", "err", err)
		detail(w, http.StatusInternalServerError, "Failed to sign in")
		return
	}
	l.attempts.succeed(attempt)
	l.finishSignIn(w, r, loginID, user)
}

// finishSignIn 은 로그인한 사용자의 팀을 세션에 담고 코드 확인 화면을 보인다(SSO·비밀번호 공통)
func (l *Login) finishSignIn(w http.ResponseWriter, r *http.Request, loginID string, user User) {
	teams, err := l.teamDetails(r.Context(), user.UserID)
	if err != nil {
		l.logger.Error("로그인 팀 읽기 실패", "err", err)
		detail(w, http.StatusInternalServerError, "Could not resolve team model grants for this login. Please try again")
		return
	}
	token := randomURLSafe(32)
	if !l.flows.with(loginID, func(f *cliFlow) bool {
		f.session = &cliSession{UserID: user.UserID, Teams: teams}
		f.browserTokenHash = sha256Hex(token)
		return true
	}) {
		flowNotFound(w)
		return
	}
	writePage(w, http.StatusOK, "complete", page{Title: "LiteLLM CLI Login", Action: l.publicURL + "/sso/cli/complete/" + loginID, Token: token})
}

func (l *Login) teamDetails(ctx context.Context, userID string) ([]teamDetail, error) {
	teamIDs, err := l.users.TeamIDs(ctx, []string{userID})
	if err != nil {
		return nil, err
	}
	teams, err := l.teams.FindAll(ctx, teamIDs[userID])
	if err != nil {
		return nil, err
	}
	byID := map[string]Team{}
	for _, t := range teams {
		byID[t.TeamID] = t
	}
	var details []teamDetail
	for _, id := range teamIDs[userID] { // 소속 순서를 지킨다. 팀 행이 있는 것만(litellm 과 같다)
		if t, ok := byID[id]; ok {
			details = append(details, teamDetail{TeamID: t.TeamID, TeamAlias: t.TeamAlias, TeamModels: orEmpty(t.Models)})
		}
	}
	return details, nil
}

func (l *Login) complete(w http.ResponseWriter, r *http.Request) {
	loginID := r.PathValue("login_id")
	code, token := r.PostFormValue("user_code"), r.PostFormValue("browser_complete_token")
	result := ""
	found := l.flows.with(loginID, func(f *cliFlow) bool {
		switch {
		case f.session == nil:
			result = "CLI login is not ready"
		case subtle.ConstantTimeCompare([]byte(sha256Hex(normalizeUserCode(code))), []byte(f.userCodeHash)) != 1 ||
			subtle.ConstantTimeCompare([]byte(sha256Hex(token)), []byte(f.browserTokenHash)) != 1:
			result = "Invalid verification code"
		default:
			f.codeVerified = true
			return true
		}
		return false
	})
	switch {
	case !found:
		flowNotFound(w)
	case result != "":
		detail(w, http.StatusBadRequest, result)
	default:
		writePage(w, http.StatusOK, "success", page{Title: "CLI Authentication Successful - LiteLLM"})
	}
}

// poll 은 데스크탑이 1.5초마다 부른다. litellm 처럼 세션을 먼저 찾고(400) 그다음 비밀을 본다(403). 키를 내주면 흐름을 지운다(일회성)
func (l *Login) poll(w http.ResponseWriter, r *http.Request) {
	loginID := r.PathValue("login_id")
	if !l.flowExists(w, loginID) {
		return
	}
	secret := r.Header.Get("x-litellm-cli-poll-secret")
	var session *cliSession
	secretOK, ready := false, false
	l.flows.with(loginID, func(f *cliFlow) bool {
		secretOK = subtle.ConstantTimeCompare([]byte(sha256Hex(secret)), []byte(f.pollSecretHash)) == 1
		ready = f.session != nil && f.codeVerified
		session = f.session
		return false
	})
	switch {
	case !secretOK:
		detail(w, http.StatusForbidden, "Invalid CLI polling secret")
		return
	case !ready:
		core.WriteJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		return
	}
	teamIDs := make([]string, len(session.Teams))
	for i, t := range session.Teams {
		teamIDs[i] = t.TeamID
	}
	teamID := r.URL.Query().Get("team_id")
	switch {
	case teamID == "" && len(session.Teams) >= 2:
		core.WriteJSON(w, http.StatusOK, map[string]any{"status": "ready", "user_id": session.UserID, "teams": teamIDs,
			"team_details": session.Teams, "requires_team_selection": true})
		return
	case teamID == "" && len(session.Teams) == 1:
		teamID = teamIDs[0]
	case teamID != "" && !containsString(teamIDs, teamID):
		detail(w, http.StatusForbidden, "User does not belong to team: "+teamID+". Available teams: "+pyStringList(teamIDs))
		return
	}
	key, err := l.keys.issue(loginClaims{UserID: session.UserID, TeamID: teamID, Expires: l.now().UTC().Add(loginKeyTTL)})
	if err != nil {
		detail(w, http.StatusInternalServerError, "Error checking session status: "+err.Error())
		return
	}
	l.flows.remove(loginID)
	var team any
	if teamID != "" {
		team = teamID
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"status": "ready", "key": key, "user_id": session.UserID, "team_id": team,
		"teams": teamIDs, "team_details": orEmptyDetails(session.Teams)})
}

// flowExists 는 litellm _get_cli_sso_flow_or_raise 의 400 세 가지다(K 5.1)
func (l *Login) flowExists(w http.ResponseWriter, loginID string) bool {
	switch {
	case strings.HasPrefix(loginID, "sk-"):
		detail(w, http.StatusBadRequest, "Your litellm CLI is out of date and uses a login flow this proxy no longer supports. Upgrade it with `pip install -U 'litellm[proxy]'` and run `lite login` again.")
	case !loginIDPattern.MatchString(loginID):
		detail(w, http.StatusBadRequest, "Invalid CLI login session id")
	case !l.flows.with(loginID, func(*cliFlow) bool { return false }):
		flowNotFound(w)
	default:
		return true
	}
	return false
}

func flowNotFound(w http.ResponseWriter) {
	detail(w, http.StatusBadRequest, "CLI login session not found or expired. Run `lite login` again. If this happens immediately after starting a login, "+
		"the proxy is likely running multiple replicas without a shared cache; configure a Redis cache so every replica can see the login session.")
}

// clientIP 는 속도 제한 키다. 프록시 뒤 XFF 는 아직 믿지 않는다(litellm 도 use_x_forwarded_for 를 켤 때만 쓴다)
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func pyStringList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = "'" + s + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func orEmptyDetails(d []teamDetail) []teamDetail {
	if d == nil {
		return []teamDetail{}
	}
	return d
}
