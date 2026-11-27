package admin

import (
	"errors"
	"net/http"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// 전역 IP 허용 목록 관리(대시보드 IP 허용 목록 화면). 요청·응답 모양은 litellm 과 같다:
// GET /get/allowed_ips → {"data": [...]}, POST /add/allowed_ip·/delete/allowed_ip {"ip": "..."} → {"message", "status"}.
// litellm 과 다른 점: 관리자만 부를 수 있고(목록에 내부 대역이 들어 있다), 잘못된 IP 는 거절하고, CIDR 도 받는다(설계 6장)

// auditConfigTable 은 설정 변경을 남기는 감사 테이블 이름이다(litellm 이 general_settings 변경에 쓰는 값)
const auditConfigTable = "LiteLLM_Config"

// configTarget 은 설정 변경의 감사 대상 id 다. litellm 처럼 바꾼 설정 이름(general_settings)이다
func configTarget(*http.Request, map[string]any, map[string]any) []string {
	return []string{"general_settings"}
}

func (h *Handlers) getAllowedIPs(w http.ResponseWriter, r *http.Request) {
	list, err := h.ipAllow.List(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"data": list})
}

func (h *Handlers) addAllowedIP(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.allowedIPEntry(w, r)
	if !ok {
		return
	}
	saved, err := h.ipAllow.Add(r.Context(), entry)
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, row{"message": "IP " + saved + " address added successfully", "status": "success"})
	case errors.Is(err, auth.ErrIPEntryInvalid):
		core.DetailError{Status: http.StatusBadRequest, Detail: err.Error()}.Write(w)
	case errors.Is(err, auth.ErrIPEntryExists):
		core.DetailError{Status: http.StatusBadRequest, Detail: "IP address already exists"}.Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) deleteAllowedIP(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.allowedIPEntry(w, r)
	if !ok {
		return
	}
	saved, err := h.ipAllow.Remove(r.Context(), entry)
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, row{"message": "IP " + saved + " deleted successfully", "status": "success"})
	case errors.Is(err, auth.ErrIPEntryInvalid):
		core.DetailError{Status: http.StatusBadRequest, Detail: err.Error()}.Write(w)
	case errors.Is(err, auth.ErrIPEntryNotFound):
		core.DetailError{Status: http.StatusNotFound, Detail: "IP address not found"}.Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// allowedIPEntry 는 요청 본문의 ip 다. 없으면 FastAPI 와 같은 422 를 쓴다
func (h *Handlers) allowedIPEntry(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		IP *string `json:"ip"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return "", false
	}
	if req.IP == nil {
		validationError{missing(raw, "ip")}.write(w)
		return "", false
	}
	return *req.IP, true
}
