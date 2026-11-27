package admin

import (
	"context"
	"errors"
	"net/http"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// teamAdminMayInvite 는 팀 admin 이 이 사용자에게 초대 링크를 만들 수 있는지다: 자기 팀 사용자여야 하고 관리자 역할이 아니어야 한다.
// 링크로 비밀번호를 새로 정하면 그 계정으로 로그인하게 되므로, 관리자 계정을 허용하면 팀 admin 이 관리자가 된다.
// litellm 은 대상의 역할을 보지 않는다(우리가 다르게 한다. 설계 6장)
func (h *Handlers) teamAdminMayInvite(ctx context.Context, caller, target string) (bool, error) {
	sharesTeam, err := h.sharesAdminTeam(ctx, caller, target)
	if err != nil || !sharesTeam {
		return false, err
	}
	user, err := h.users.Find(ctx, target)
	if err != nil {
		return false, err
	}
	role := user.EffectiveRole()
	return role != core.RoleProxyAdmin && role != core.RoleProxyAdminViewer, nil
}

// POST /invitation/new (L 6.1). 링크 주소(/ui/onboarding?invitation_id=…)는 대시보드가 만든다.
// proxy_admin 은 누구나, 팀 admin 은 자기 팀 사용자만 초대한다(litellm 과 같다. 조직은 없다)
func (h *Handlers) createInvitation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID *string `json:"user_id"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.UserID == nil {
		validationError{missing(raw, "user_id")}.write(w)
		return
	}
	ctx, p := r.Context(), principal(r.Context())
	if !p.IsAdmin() {
		allowed, err := h.teamAdminMayInvite(ctx, p.UserID, *req.UserID)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		if !allowed {
			proxyError(400, "Admin-only endpoint. Not allowed to access this., your role="+string(p.Role)).Write(w)
			return
		}
	}
	by := p.UserID
	if by == "" {
		by = "default_user_id" // litellm: 호출자 user_id 가 없으면 proxy admin 이름
	}
	inv, err := h.users.CreateInvitation(ctx, *req.UserID, by, h.now().UTC())
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		proxyError(400, "User id does not exist in 'LiteLLM_UserTable'. Fix this by creating user via `/user/new`.").Write(w)
	case err != nil:
		h.internalError(w, r, err)
	default:
		core.WriteJSON(w, http.StatusOK, row{"id": inv.ID, "user_id": inv.UserID, "is_accepted": inv.IsAccepted, "accepted_at": inv.AcceptedAt,
			"expires_at": inv.ExpiresAt.UTC(), "created_at": inv.CreatedAt.UTC(), "created_by": inv.CreatedBy,
			"updated_at": inv.UpdatedAt.UTC(), "updated_by": inv.UpdatedBy})
	}
}
