package admin

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

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
		allowed, err := h.canInvite(ctx, p, *req.UserID)
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

// canInvite 는 호출자가 대상 사용자가 속한 팀의 admin 인지다
func (h *Handlers) canInvite(ctx context.Context, p auth.Principal, target string) (bool, error) {
	_, adminTeams, err := h.teamsOf(ctx, p.UserID)
	if err != nil || len(adminTeams) == 0 {
		return false, err
	}
	targetTeams, err := h.users.TeamIDs(ctx, []string{target})
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(targetTeams[target], func(t string) bool { return slices.Contains(adminTeams, t) }), nil
}
