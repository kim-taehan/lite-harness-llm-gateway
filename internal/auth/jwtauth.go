package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"llm-gateway/internal/core"
)

// JWTConfig 는 Keycloak 토큰(JWT)으로 API 를 직접 부르는 설정이다(환경 변수 GATEWAY_JWT_*). 고객사 환경마다 달라 환경 변수로 받는다.
// Issuer 가 비어 있으면 꺼져 있고, JWT 모양 토큰은 가상 키 형식 오류(401)로 거절한다
type JWTConfig struct {
	Issuer   string // GATEWAY_JWT_ISSUER, 토큰의 iss 와 정확히 같아야 한다. 예: http://keycloak:8080/realms/corp
	Audience string // GATEWAY_JWT_AUDIENCE, 토큰의 aud 에 있어야 한다(Keycloak 은 audience 매퍼가 필요하다)
	JWKSURL  string // GATEWAY_JWT_JWKS_URL, 비우면 발급자의 openid-configuration 에서 찾는다
}

func (c JWTConfig) Enabled() bool { return c.Issuer != "" }

// Validate 는 기동 때 설정 실수를 막는다. 발급자·대상이 없으면 아무 토큰이나 받게 되므로 기동하지 않는다
func (c JWTConfig) Validate() error {
	if !c.Enabled() {
		return nil
	}
	if c.Audience == "" {
		return errors.New("GATEWAY_JWT_AUDIENCE 가 필요하다(없으면 같은 발급자의 다른 앱용 토큰도 받게 된다)")
	}
	for _, raw := range []string{c.Issuer, c.JWKSURL} {
		if raw == "" {
			continue
		}
		if u, err := url.Parse(raw); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("주소 %q 는 http(s) 주소여야 한다", raw)
		}
	}
	return nil
}

// jwtAuth 는 Authenticator 가 JWT 를 처리하는 데 쓰는 것이다
type jwtAuth struct {
	cfg      JWTConfig
	verifier *jwtVerifier
	mapping  GroupMapping // 여러 팀이 걸리면 앞선 그룹(이름 순)의 팀이 이 요청의 팀이다

	memberMu   sync.Mutex
	memberSeen map[string]time.Time // 사용자·팀 쌍을 팀원으로 넣은 시각. 요청마다 쓰지 않으려는 캐시다
}

// 같은 사용자를 같은 팀의 팀원으로 다시 넣지 않는 시간
const jwtMembershipRefresh = 5 * time.Minute

// EnableJWT 는 JWT 직접 인증을 켠다. cfg 는 Validate 를 통과해야 한다
func (a *Authenticator) EnableJWT(cfg JWTConfig, mapping GroupMapping, client *http.Client, logger *slog.Logger) {
	a.jwt = &jwtAuth{cfg: cfg, verifier: newJWTVerifier(cfg, client, a.now, logger), mapping: mapping, memberSeen: map[string]time.Time{}}
}

// looksLikeJWT 는 가상 키·로그인 키가 아니고 JWT 모양(점 둘, eyJ 로 시작)인 값이다
func looksLikeJWT(key string) bool {
	return strings.HasPrefix(key, "eyJ") && strings.Count(key, ".") == 2 && !strings.HasPrefix(key, "sk-") && !strings.HasPrefix(key, loginKeyPrefix)
}

// authenticateJWT 는 검증한 토큰의 사용자·그룹으로 신원을 정한다. 요청마다 사용자·팀 행을 다시 읽어 지금 권한을 쓴다.
// 가상 키가 아니라서 KeyToken 이 비고, 키 단위 한도는 걸리지 않고 사용자·팀·조직 한도가 걸린다
func (a *Authenticator) authenticateJWT(ctx context.Context, token string) (Principal, error) {
	claims, err := a.jwt.verifier.verify(ctx, token)
	switch {
	case errors.Is(err, errJWKSUnavailable):
		return Principal{}, core.ProxyError{Status: http.StatusServiceUnavailable, Type: "auth_error", Param: "None",
			Message: "Unable to verify the token right now. Try again shortly."}
	case err != nil:
		a.jwt.verifier.warn("invalid", "JWT 거절", err)
		return Principal{}, core.AuthError("invalid or expired JWT.")
	}
	if claims.claims.Subject == "" || (claims.claims.Email == "" && claims.claims.PreferredUsername == "") {
		return Principal{}, core.AuthError("JWT has no subject and email or preferred_username.")
	}
	user, err := a.jwtUser(ctx, claims.claims)
	if err != nil {
		return Principal{}, err
	}
	tokenGroups, _ := groupsIn(claims.raw, a.jwt.mapping.claim())
	teamIDs, admin := a.jwt.mapping.granted(tokenGroups)
	p := Principal{UserID: user.UserID, Role: user.EffectiveRole()}
	if admin {
		p.Role = core.RoleProxyAdmin // 매핑에 명시한 그룹으로만 관리자가 된다. 사용자 행의 역할은 바꾸지 않는다
	}
	return a.withJWTTeam(ctx, p, user, teamIDs)
}

// jwtUser 는 토큰의 사용자를 찾고, 없거나 아직 SSO 에 묶이지 않았으면 SSO 로그인과 같은 규칙으로 만들거나 묶는다
func (a *Authenticator) jwtUser(ctx context.Context, c oidcClaims) (User, error) {
	userID := strings.ToLower(c.Email)
	if userID == "" {
		userID = c.PreferredUsername
	}
	user, err := a.users.Find(ctx, userID)
	if err == nil && user.SSOUserID != nil && *user.SSOUserID == c.Subject {
		return user, nil
	}
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return User{}, fmt.Errorf("JWT 사용자 조회: %w", err)
	}
	user, err = a.users.upsertSSOUser(ctx, c)
	switch {
	case errors.Is(err, errSSOSubjectMismatch):
		return User{}, core.ProxyError{Status: 403, Type: "auth_error", Param: "None",
			Message: "This email already belongs to a different SSO identity. Ask an admin to resolve the conflict."}
	case errors.Is(err, errSSOEmailUnverified):
		return User{}, core.ProxyError{Status: 403, Type: "auth_error", Param: "None",
			Message: "This account already exists. Verify your email at the identity provider before signing in with SSO."}
	case err != nil:
		return User{}, fmt.Errorf("JWT 사용자 저장: %w", err)
	}
	return user, nil
}

// withJWTTeam 은 매핑으로 정해진 팀 중 처음으로 있는 팀을 신원에 담는다. 매핑된 팀이 없으면 사용자 허용 모델을 쓴다.
// 막힌 팀이면 거절한다. 팀에 없는 사용자는 팀원으로 넣는다(대시보드·사용량에 팀으로 보이게)
func (a *Authenticator) withJWTTeam(ctx context.Context, p Principal, user User, teamIDs []string) (Principal, error) {
	for _, teamID := range teamIDs {
		team, err := a.teams.Find(ctx, teamID)
		if errors.Is(err, ErrTeamNotFound) {
			a.jwt.verifier.warn("team:"+teamID, "GATEWAY_JWT_GROUP_MAP 의 팀이 없다", err)
			continue
		}
		if err != nil {
			return Principal{}, fmt.Errorf("JWT 팀 조회: %w", err)
		}
		if team.Blocked {
			return Principal{}, teamBlockedError(team.TeamID)
		}
		if err := a.ensureJWTMember(ctx, team.TeamID, user.UserID); err != nil {
			return Principal{}, err
		}
		p.TeamID, p.TeamModels, p.OrganizationID = team.TeamID, team.Models, deref(team.OrganizationID)
		return withGuardrailSettings(p, nil, team.Metadata), nil
	}
	p.KeyModels = user.Models // 팀이 없으면 사용자 허용 모델(로그인 키와 같다)
	return p, nil
}

func (a *Authenticator) ensureJWTMember(ctx context.Context, teamID, userID string) error {
	a.jwt.memberMu.Lock()
	seen, ok := a.jwt.memberSeen[teamID+"\x00"+userID]
	a.jwt.memberMu.Unlock()
	now := a.now()
	if ok && now.Sub(seen) < jwtMembershipRefresh {
		return nil
	}
	if err := a.teams.addMember(ctx, teamID, userID, TeamRoleUser); err != nil {
		return fmt.Errorf("JWT 팀원 추가: %w", err)
	}
	a.jwt.memberMu.Lock()
	a.jwt.memberSeen[teamID+"\x00"+userID] = now
	a.jwt.memberMu.Unlock()
	return nil
}
