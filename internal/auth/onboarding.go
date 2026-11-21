package auth

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"llm-gateway/internal/core"
)

// 초대 온보딩과 본인 비밀번호 변경(L 6.2·6.3·6.5). 비밀번호 로그인을 넣은 빌드에서만 등록한다

// onboardingTTL 은 초대 링크를 연 뒤 비밀번호를 정할 수 있는 시간이다(litellm 과 같다)
const onboardingTTL = 15 * time.Minute

func (l *Login) onboardingRoutes() []core.Route {
	public := core.Public()
	return []core.Route{
		{Method: http.MethodGet, Path: "/onboarding/get_token", Access: public, Handler: l.onboardingToken},
		// 본문과 함께 온보딩 JWT 를 Authorization 으로 받아 핸들러가 직접 확인한다(litellm 도 키 인증을 거치지 않는다)
		{Method: http.MethodPost, Path: "/onboarding/claim_token", Access: public, Handler: l.claimOnboarding},
		{Method: http.MethodPost, Path: "/user/password/change", Access: core.Authenticated(), Handler: l.changePassword},
	}
}

func invitationError(w http.ResponseWriter, message string) {
	core.DetailMessage(http.StatusUnauthorized, message).Write(w)
}

// usableInvitation 은 링크가 있고, 만료되지 않았고, 쓰지 않았는지 본다. litellm 처럼 만료는 날짜만 비교한다
func (l *Login) usableInvitation(w http.ResponseWriter, r *http.Request, id string) (Invitation, bool) {
	inv, err := l.users.findInvitation(r.Context(), id)
	switch {
	case errors.Is(err, ErrInvitationNotFound):
		invitationError(w, "Invitation link does not exist in db.")
	case err != nil:
		l.internalError(w, err)
	case inv.ExpiresAt.UTC().Format(time.DateOnly) < l.now().UTC().Format(time.DateOnly):
		invitationError(w, "Invitation link has expired.")
	case inv.IsAccepted || inv.AcceptedAt != nil:
		invitationError(w, "Invitation link has already been used.")
	default:
		return inv, true
	}
	return Invitation{}, false
}

func (l *Login) internalError(w http.ResponseWriter, err error) {
	l.logger.Error("온보딩 처리 실패", "err", err)
	core.DetailMessage(http.StatusInternalServerError, "Internal Server Error").Write(w)
}

// onboardingToken 은 초대 링크를 연 화면이 부른다. 바깥 JWT 의 key 클레임에 15분짜리 온보딩 JWT 를 넣는다(litellm 과 같은 두 겹)
func (l *Login) onboardingToken(w http.ResponseWriter, r *http.Request) {
	inv, ok := l.usableInvitation(w, r, r.URL.Query().Get("invite_link"))
	if !ok {
		return
	}
	user, err := l.users.Find(r.Context(), inv.UserID)
	if errors.Is(err, ErrUserNotFound) {
		invitationError(w, "User does not exist in db.")
		return
	}
	if err != nil {
		l.internalError(w, err)
		return
	}
	inner, err := signUIToken(l.master, map[string]any{"token_type": "litellm_onboarding", "invitation_link": inv.ID,
		"user_id": user.UserID, "exp": l.now().Add(onboardingTTL).Unix()})
	if err != nil {
		l.internalError(w, err)
		return
	}
	outer, err := signUIToken(l.master, uiClaims(user, uiMethodPassword, inner))
	if err != nil {
		l.internalError(w, err)
		return
	}
	loginURL := l.publicURL + "/ui/onboarding?" + url.Values{"token": {outer}, "user_email": {deref(user.UserEmail)}}.Encode()
	core.WriteJSON(w, http.StatusOK, map[string]any{"login_url": loginURL, "token": outer, "user_email": user.UserEmail})
}

// claimOnboarding 은 초대받은 사용자가 비밀번호를 정한다. 링크를 쓴 것으로 바꾸고 대시보드 세션을 바로 연다
func (l *Login) claimOnboarding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InvitationLink string `json:"invitation_link"`
		UserID         string `json:"user_id"`
		Password       string `json:"password"`
	}
	if !core.DecodeJSONBody(w, r, &req) {
		return
	}
	inv, ok := l.usableInvitation(w, r, req.InvitationLink)
	if !ok {
		return
	}
	if inv.UserID != req.UserID {
		invitationError(w, "Invalid invitation link. The user id submitted does not match the user id this link is attached to. Got="+
			req.UserID+", Expected="+inv.UserID)
		return
	}
	bearer, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		bearer, found = strings.CutPrefix(r.Header.Get("Authorization"), "bearer ")
	}
	if !found || bearer == "" {
		invitationError(w, "Missing onboarding session for invitation link.")
		return
	}
	claims, err := verifyUIToken(l.master, bearer, l.now())
	if err != nil || claims["token_type"] != "litellm_onboarding" || claims["invitation_link"] != inv.ID || claims["user_id"] != inv.UserID {
		invitationError(w, "Invalid onboarding session for invitation link.")
		return
	}
	user, err := l.users.ClaimInvitation(r.Context(), inv, req.Password, l.now())
	var weak *PasswordPolicyError
	switch {
	case errors.As(err, &weak):
		core.ProxyError{Status: http.StatusBadRequest, Message: weak.Message, Type: "validation_error", Param: "password"}.Write(w)
		return
	case errors.Is(err, ErrInvitationUsed):
		invitationError(w, "Invitation link has already been used.")
		return
	case err != nil:
		l.internalError(w, err)
		return
	}
	// 비밀번호를 정하며 끊은 세션(폐기 시각)보다 뒤에 발급하므로 이 새 세션은 살아 있다
	token, ok := l.startUISession(w, r, user, uiMethodPassword)
	if !ok {
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"login_url": l.publicURL + "/ui?login=success", "token": token, "user_email": user.UserEmail,
		"user": map[string]any{"user_id": user.UserID, "user_email": user.UserEmail, "user_alias": user.UserAlias, "user_role": user.UserRole}})
}

// changePassword 는 본인 비밀번호 변경이다. litellm 처럼 비밀번호로 로그인한 대시보드 세션만 된다.
// 바꾸면 그 사용자의 대시보드 세션이 모두 끊긴다(litellm 은 지금 세션은 남긴다, 설계 9장)
func (l *Login) changePassword(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if p.UIMethod != uiMethodPassword {
		detail(w, http.StatusForbidden, "Passwords can only be changed from a dashboard session created by logging in with a password.")
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !core.DecodeJSONBody(w, r, &req) {
		return
	}
	badRequest := func(message string) { core.DetailMessage(http.StatusBadRequest, message).Write(w) }
	// 세션 토큰을 훔친 사람이 현재 비밀번호를 무작위로 넣어 보지 못하게 로그인과 같은 시도 제한을 건다
	_, err := l.gate.check(r.Context(), p.UserID, req.CurrentPassword, clientIP(r))
	switch {
	case errors.Is(err, ErrTooManyAttempts):
		core.ProxyError{Status: http.StatusTooManyRequests, Type: "auth_error", Param: "None", Message: "Too many failed attempts. Try again later.",
			Headers: map[string]string{"Retry-After": "900"}}.Write(w)
		return
	case errors.Is(err, ErrBadCredentials):
		badRequest("Current password is incorrect.")
		return
	case err != nil:
		l.internalError(w, err)
		return
	}
	if req.NewPassword == req.CurrentPassword {
		badRequest("New password must be different from the current password.")
		return
	}
	err = l.users.SetPassword(r.Context(), p.UserID, req.NewPassword, false, l.now())
	var weak *PasswordPolicyError
	switch {
	case errors.As(err, &weak):
		core.ProxyError{Status: http.StatusBadRequest, Message: weak.Message, Type: "validation_error", Param: "password"}.Write(w)
	case err != nil:
		l.internalError(w, err)
	default:
		core.WriteJSON(w, http.StatusOK, map[string]any{"user_id": p.UserID, "message": "Password updated successfully."})
	}
}
