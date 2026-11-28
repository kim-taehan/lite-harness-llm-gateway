package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"llm-gateway/internal/core"
)

// Principal 은 요청 하나 동안 다른 모듈에 넘기는 신원이다
type Principal struct {
	UserID string    // 키에 사용자가 없으면 빈 문자열
	Role   core.Role // 사용자 행이 없는 키는 빈 문자열(litellm 의 role=None)
	// 가상 키로 들어왔을 때만 있다
	KeyToken string
	KeyAlias string // 예산 초과 문구에 쓴다
	TeamID   string
	Master   bool
	// 허용 모델. 비어 있으면 모두 허용(litellm 과 같다). 마스터 키는 둘 다 비어 있다
	KeyModels  []string
	TeamModels []string
	// 키가 프로젝트에 속하면 그 프로젝트와 허용 모델이다
	ProjectID     string
	ProjectModels []string
	// OrganizationID 는 키의 팀이 속한 조직이다. 조직 한도가 요청마다 걸린다
	OrganizationID string
	// 키·팀의 metadata 에서 온 가드레일 설정이다(guardrails.go). 마스터 키는 모두 비어 있다
	Guardrails              []string
	DisableGlobalGuardrails bool
	OptedOutGuardrails      []string
	// 대시보드 세션 키로 들어왔을 때만 있다(litellm login_method). 로그아웃은 이것이 있어야 한다
	UIMethod string
	// 관리자가 정한 비밀번호로 대시보드에 들어온 세션이다. 비밀번호를 바꿀 때까지 resetAllowedRoutes 만 부른다
	PasswordResetRequired bool
}

// resetAllowedRoutes 는 비밀번호를 바꿔야 하는 세션이 부를 수 있는 경로다(litellm login_utils.py:60)
var resetAllowedRoutes = []string{"/user/password/change", "/session/logout", "/management/v1/users/me"}

const passwordResetMessage = "This account's password must be changed before the session can be used: " +
	"it was either found in a known data breach or set by an admin. " +
	"Change it via POST /user/password/change (UI: /ui/change-password), then log in again."

// IsAdmin 은 모든 관리 자원을 보는 역할이다(쓰기는 proxy_admin 만)
func (p Principal) IsAdmin() bool { return p.Role == core.RoleProxyAdmin }

// CanViewAll 은 litellm 의 user_api_key_has_admin_view 다(proxy_admin, proxy_admin_viewer)
func (p Principal) CanViewAll() bool {
	return p.Role == core.RoleProxyAdmin || p.Role == core.RoleProxyAdminViewer
}

type principalKey struct{}

// PrincipalFrom 은 권한 검사를 통과한 요청의 신원이다. 공개 라우트에는 없다
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// litellm 과 같은 메시지(auth/user_api_key_auth.py, docs/litellm-inventory/J_key_contract.md 5.2)
const (
	reasonNoKey        = "No api key passed in."
	reasonMalformedKey = "Malformed API Key passed in. Ensure Key has `Bearer ` prefix."
	reasonKeyBlocked   = "Key is blocked. Update via `/key/unblock` if you're an admin."
)

// litellm 은 마스터 키 호출자를 user_id "default_user_id" 의 proxy_admin 으로 본다
var masterPrincipal = Principal{UserID: "default_user_id", Role: core.RoleProxyAdmin, Master: true}

// Authenticator 는 요청 헤더의 키로 신원을 정한다: 마스터 키, 또는 DB 의 가상 키
type Authenticator struct {
	masterKey MasterKey
	keys      *Keys
	users     *Users
	teams     *Teams
	projects  *Projects
	cache     *KeyCache
	logins    *loginKeys
	jwt       *jwtAuth // nil 이면 JWT 직접 인증이 꺼져 있다(EnableJWT)
	now       func() time.Time
	// denials·proxies 는 거절된 요청을 감사 기록에 남길 때만 쓴다(RecordDenials). nil 이면 남기지 않는다
	denials EventSink
	proxies TrustedProxies
}

func NewAuthenticator(masterKey MasterKey, keys *Keys, users *Users, teams *Teams, cache *KeyCache, now func() time.Time) *Authenticator {
	return &Authenticator{masterKey: masterKey, keys: keys, users: users, teams: teams, projects: NewProjects(keys.db, cache), cache: cache,
		logins: newLoginKeys(masterKey), now: now}
}

// Authenticate 는 신원을 돌려준다. 거절은 litellm 과 같은 오류 본문(*core.ProxyError)이고, 그 밖의 error 는 예상 밖 실패(DB 등)다.
// 검사 순서는 litellm 과 같다: 형식 → 키 있음 → 차단 → 만료 → 팀 차단(J 5.1). 모델 접근 검사는 추론 경로(M3)에서 한다
func (a *Authenticator) Authenticate(ctx context.Context, header http.Header) (Principal, error) {
	key, present := extractKey(header)
	switch {
	case !present:
		return Principal{}, core.AuthError(reasonNoKey)
	case key == "":
		return Principal{}, core.AuthError(reasonMalformedKey)
	case a.masterKey.Matches(key):
		return masterPrincipal, nil
	case a.jwt != nil && looksLikeJWT(key):
		return a.authenticateJWT(ctx, key)
	case strings.HasPrefix(key, loginKeyPrefix):
		principal, err := a.authenticateLogin(ctx, key)
		if !errors.Is(err, errBadLoginKey) {
			return principal, err
		}
		fallthrough // 풀 수 없는 로그인 키는 litellm 처럼 가상 키 형식 오류로 답한다
	case !strings.HasPrefix(key, "sk-"):
		// 해시를 그대로 키로 쓰는 것을 막는다
		return Principal{}, core.ProxyError{Status: 401, Type: "auth_error", Param: "None",
			Message: "LiteLLM Virtual Key expected. Received=" + maskKey(key) + ", expected to start with 'sk-'."}
	}
	resolved, err := a.resolve(ctx, hashKey(key))
	if errors.Is(err, ErrKeyNotFound) {
		return Principal{}, core.ProxyError{Status: 401, Type: "token_not_found_in_db", Param: "key",
			Message: "Authentication Error, Invalid API key provided."}
	}
	if err != nil {
		return Principal{}, fmt.Errorf("키 조회: %w", err)
	}
	k := resolved.key
	if k.Blocked != nil && *k.Blocked {
		return Principal{}, core.AuthError(reasonKeyBlocked)
	}
	if now := a.now().UTC(); k.Expires != nil && k.Expires.Before(now) {
		return Principal{}, expiredKeyError(k.Token, *k.Expires, now)
	}
	if resolved.teamBlocked {
		return Principal{}, teamBlockedError(deref(k.TeamID))
	}
	if resolved.projectBlocked {
		return Principal{}, projectBlockedError(deref(k.ProjectID))
	}
	p := Principal{UserID: deref(k.UserID), Role: resolved.role, KeyToken: k.Token, KeyAlias: deref(k.KeyAlias), TeamID: deref(k.TeamID),
		KeyModels: k.Models, TeamModels: resolved.teamModels, ProjectID: deref(k.ProjectID), ProjectModels: resolved.projectModels,
		OrganizationID: resolved.organizationID}
	return withGuardrailSettings(p, k.Metadata, resolved.teamMetadata), nil
}

// projectBlockedError 는 막힌 프로젝트의 키가 부른 요청의 오류다(litellm auth_checks 의 문구)
func projectBlockedError(projectID string) core.ProxyError {
	return core.AuthError("Project=" + projectID + " is blocked. Update via `/project/update` if you're an admin.")
}

// expiredKeyError 는 만료된 키(또는 폐기한 세션)의 litellm 오류다. 대시보드는 이 문구("Authentication Error - Expired Key")를
// 보면 로그아웃한다. 문구를 바꾸지 않는다. keyID 는 화면에 보이는 끝 4자만 쓴다
func expiredKeyError(keyID string, expiry, now time.Time) core.ProxyError {
	return core.ProxyError{Status: 401, Type: "expired_key", Param: "sk-..." + keyID[len(keyID)-4:],
		Message: "Authentication Error - Expired Key. Key Expiry time " + pyDatetime(expiry) + " and current time " + pyDatetime(now)}
}

// teamBlockedError 는 막힌 팀의 키가 부른 요청의 litellm 오류다
func teamBlockedError(teamID string) core.ProxyError {
	return core.AuthError("Team=" + teamID + " is blocked. Update via `/team/unblock` if you're an admin.")
}

// authenticateLogin 은 SSO·비밀번호 로그인 키다(K 4장). 요청마다 사용자·팀 행을 다시 읽어 지금 권한을 쓴다.
// 풀 수 없는 키는 errBadLoginKey 다
func (a *Authenticator) authenticateLogin(ctx context.Context, key string) (Principal, error) {
	claims, err := a.logins.open(key)
	if err != nil {
		return Principal{}, errBadLoginKey
	}
	now := a.now().UTC()
	if claims.Expires.Before(now) {
		return Principal{}, expiredKeyError(key, claims.Expires, now)
	}
	user, err := a.users.Find(ctx, claims.UserID)
	if errors.Is(err, ErrUserNotFound) {
		return Principal{}, core.ProxyError{Status: 401, Type: "auth_error", Param: "user_id",
			Message: "Authentication Error, user '" + claims.UserID + "' no longer exists."}
	}
	if err != nil {
		return Principal{}, fmt.Errorf("로그인 키 사용자 조회: %w", err)
	}
	if claims.UIMethod != "" && user.UISessionsRevokedAt != nil && !claims.IssuedAt.After(*user.UISessionsRevokedAt) {
		// 로그아웃한 세션이다. 만료와 같은 문구로 답해 대시보드가 로그인 화면으로 보내게 한다
		return Principal{}, expiredKeyError(key, *user.UISessionsRevokedAt, now)
	}
	p := Principal{UserID: user.UserID, Role: user.EffectiveRole(), KeyToken: hashKey(key), UIMethod: claims.UIMethod,
		PasswordResetRequired: claims.UIMethod == uiMethodPassword && user.PasswordResetRequired}
	if claims.TeamID == "" {
		p.KeyModels = user.Models // 팀이 없으면 사용자 허용 모델(litellm 과 같다)
		return p, nil
	}
	return a.withLoginTeam(ctx, p, user.UserID, claims.TeamID)
}

// withLoginTeam 은 로그인 키가 고른 팀을 신원에 담는다. 팀원이 아니거나 팀이 막혔으면 거절한다
func (a *Authenticator) withLoginTeam(ctx context.Context, p Principal, userID, teamID string) (Principal, error) {
	team, err := a.teams.Find(ctx, teamID)
	if err != nil && !errors.Is(err, ErrTeamNotFound) {
		return Principal{}, fmt.Errorf("로그인 키 팀 조회: %w", err)
	}
	teamIDs, terr := a.users.TeamIDs(ctx, []string{userID})
	if terr != nil {
		return Principal{}, fmt.Errorf("로그인 키 팀 조회: %w", terr)
	}
	if err != nil || !containsString(teamIDs[userID], teamID) {
		return Principal{}, core.ProxyError{Status: 403, Type: "auth_error", Param: "None",
			Message: "Team '" + teamID + "' is not in your team memberships."}
	}
	if team.Blocked {
		return Principal{}, teamBlockedError(team.TeamID)
	}
	p.TeamID, p.TeamModels, p.OrganizationID = team.TeamID, team.Models, deref(team.OrganizationID)
	return withGuardrailSettings(p, nil, team.Metadata), nil
}

// resolve 는 키와 그 소유자 역할·팀 차단 여부를 캐시나 DB 에서 읽는다
func (a *Authenticator) resolve(ctx context.Context, token string) (resolvedKey, error) {
	if r, ok := a.cache.entries.get(token); ok {
		return r, nil
	}
	key, err := a.keys.Find(ctx, token)
	cacheable := true
	if errors.Is(err, ErrKeyNotFound) {
		// 재발급 전 해시가 유예 기간 안이면 새 키로 인증한다. 유예가 끝나는 때 바로 막히도록 캐시하지 않는다
		key, err = a.keys.FindDuringGrace(ctx, token, a.now().UTC())
		cacheable = false
	}
	if err != nil {
		return resolvedKey{}, err
	}
	r := resolvedKey{key: key}
	if key.UserID != nil {
		// litellm: 역할은 키 소유자의 사용자 행에서 온다. 행은 있는데 역할이 비면 internal_user, 행이 없으면 역할 없음
		user, err := a.users.Find(ctx, *key.UserID)
		switch {
		case err == nil:
			r.role = user.EffectiveRole()
		case !errors.Is(err, ErrUserNotFound):
			return resolvedKey{}, err
		}
	}
	if key.TeamID != nil {
		team, err := a.teams.Find(ctx, *key.TeamID)
		if err != nil && !errors.Is(err, ErrTeamNotFound) {
			return resolvedKey{}, err
		}
		r.teamBlocked = err == nil && team.Blocked
		r.teamModels = team.Models
		r.teamMetadata = team.Metadata
		r.organizationID = deref(team.OrganizationID)
	}
	if key.ProjectID != nil {
		project, err := a.projects.Find(ctx, *key.ProjectID)
		if err != nil && !errors.Is(err, ErrProjectNotFound) {
			return resolvedKey{}, err
		}
		r.projectBlocked = err == nil && project.Blocked
		r.projectModels = project.Models
	}
	if cacheable {
		a.cache.entries.put(token, r)
	}
	return r, nil
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
		principal, err := a.Authenticate(r.Context(), r.Header)
		var rejected core.ProxyError
		switch {
		case errors.As(err, &rejected):
			rejected.Write(w)
			return
		case err != nil:
			core.ProxyError{Status: 500, Message: "Internal Server Error", Type: "internal_server_error"}.Write(w)
			return
		}
		if principal.PasswordResetRequired && !slices.Contains(resetAllowedRoutes, route.Path) {
			core.ProxyError{Status: 403, Type: "auth_error", Param: "None", Message: passwordResetMessage}.Write(w)
			return
		}
		if denial, denied := routeDenied(r, route.Access, principal); denied {
			a.recordDenial(r, principal, denial.Status)
			denial.Write(w)
			return
		}
		route.Handler(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}

// routeDenied 는 litellm 라우트 단계 판정이다(J 6.1): proxy_admin 은 모두 통과, proxy_admin_viewer 의 쓰기는 403,
// 허용 목록에 없는 역할은 401
func routeDenied(r *http.Request, access core.Access, p Principal) (core.ProxyError, bool) {
	if p.Role == core.RoleProxyAdmin {
		return core.ProxyError{}, false
	}
	// litellm RC:977-1095: proxy_admin_viewer 는 GET 을 모두 허용하고, 쓰기는 핸들러가 판정하는 라우트(/team/update 등)에서도 막는다
	if p.Role == core.RoleProxyAdminViewer && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		return core.ProxyError{}, false
	}
	if p.Role == core.RoleProxyAdminViewer {
		// 문구는 litellm 원문을 확인하지 못했다(J 6.1 4번은 상태만 확인)
		return core.ProxyError{Status: 403, Type: "auth_error", Param: "None",
			Message: "user not allowed to access this route, role= proxy_admin_viewer. Trying to access: " + r.URL.Path}, true
	}
	if access.Allows(p.Role) {
		return core.ProxyError{}, false
	}
	role := string(p.Role)
	if role == "" {
		role = "unknown"
	}
	return core.AuthError("Only proxy admin can be used to generate, delete, update info for new keys/users/teams. " +
		"Route=" + r.URL.Path + ". Your role=" + role + ". Your user_id=" + maskUserID(p.UserID)), true
}

// maskUserID 는 litellm RouteChecks._mask_user_id 처럼 앞 일부만 남긴다(미확인: 정확한 자릿수)
func maskUserID(id string) string {
	if id == "" {
		return "unknown"
	}
	if len(id) <= 4 {
		return "****"
	}
	return id[:4] + "****"
}

// pyDatetime 은 파이썬 str(datetime) 모양이다(예: 2026-11-20 10:00:00+00:00, 마이크로초가 있으면 .123456)
func pyDatetime(t time.Time) string {
	t = t.UTC()
	s := t.Format("2006-01-02 15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	return s + "+00:00"
}
