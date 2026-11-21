package admin

import (
	"net/http"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
)

// 모델 허브 관리(M 5장). 공개할 모델 그룹과 허브 위 링크를 core.Settings 에 둔다. 공개 목록 자체는 inference 가 낸다

func (h *Handlers) makeModelGroupsPublic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelGroups []string `json:"model_groups"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.ModelGroups == nil {
		validationError{missing(raw, "model_groups")}.write(w)
		return
	}
	if _, err := h.settings.Merge(r.Context(), inference.ModelHubSetting, map[string]any{"public_model_groups": req.ModelGroups}); err != nil {
		h.internalError(w, r, err)
		return
	}
	p, _ := auth.PrincipalFrom(r.Context())
	core.WriteJSON(w, http.StatusOK, row{"message": "Successfully updated public model groups", "public_model_groups": req.ModelGroups, "updated_by": p.UserID})
}

func (h *Handlers) updateUsefulLinks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UsefulLinks map[string]any `json:"useful_links"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.UsefulLinks == nil {
		validationError{missing(raw, "useful_links")}.write(w)
		return
	}
	if _, err := h.settings.Merge(r.Context(), inference.ModelHubSetting, map[string]any{"useful_links": req.UsefulLinks}); err != nil {
		h.internalError(w, r, err)
		return
	}
	p, _ := auth.PrincipalFrom(r.Context())
	core.WriteJSON(w, http.StatusOK, row{"message": "Successfully updated useful links", "useful_links": req.UsefulLinks, "updated_by": p.UserID})
}

// modelHubInfo 는 공개 허브 머리말이다(로그인 없이 본다). docs_title 은 litellm 기본값이다
func (h *Handlers) modelHubInfo(w http.ResponseWriter, r *http.Request) {
	stored, err := h.settings.Get(r.Context(), inference.ModelHubSetting)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	links, ok := stored["useful_links"].(map[string]any)
	if !ok {
		links = map[string]any{}
	}
	core.WriteJSON(w, http.StatusOK, row{"docs_title": "LiteLLM API", "custom_docs_description": nil, "litellm_version": core.Version, "useful_links": links})
}
