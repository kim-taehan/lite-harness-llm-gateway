package core

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

// litellm 은 엔드포인트마다 오류 본문 모양이 다르다(docs/litellm-inventory/H_team_user_contract.md 2.3).
// 모양별로 타입을 두고, 본문을 만드는 곳은 이 파일 하나다.

// ProxyError 는 litellm ProxyException 모양 {"error":{message,type,param,code}} 이다. code 는 상태 코드 문자열이다
type ProxyError struct {
	Status  int
	Message string
	Type    string
	Param   any // litellm 은 null 또는 문자열 "None" 등을 쓴다
}

// AuthError 는 인증 실패다. litellm 은 관리 라우트 역할 거부도 같은 401 auth_error 로 돌려준다
func AuthError(reason string) ProxyError {
	return ProxyError{Status: http.StatusUnauthorized, Message: "Authentication Error, " + reason, Type: "auth_error", Param: "None"}
}

func (e ProxyError) Write(w http.ResponseWriter) {
	WriteJSON(w, e.Status, map[string]any{"error": map[string]any{
		"message": e.Message,
		"type":    e.Type,
		"param":   e.Param,
		"code":    strconv.Itoa(e.Status),
	}})
}

// DetailError 는 FastAPI HTTPException 기본 모양 {"detail": …} 이다
type DetailError struct {
	Status int
	Detail any
}

func (e DetailError) Write(w http.ResponseWriter) {
	WriteJSON(w, e.Status, map[string]any{"detail": e.Detail})
}

// WriteRouteNotFound 는 등록되지 않은 경로에 FastAPI 기본 404 와 같은 본문을 준다
func WriteRouteNotFound(w http.ResponseWriter) {
	DetailError{Status: http.StatusNotFound, Detail: "Not Found"}.Write(w)
}

// WriteJSON 은 값을 JSON 으로 쓴다. 쓰기 실패는 클라이언트가 끊은 경우라 로그만 남긴다
func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Debug("응답 쓰기 실패", "err", err)
	}
}
