package admin

import (
	"llm-gateway/internal/core"
)

// 만들지 않는 기능의 엔드포인트(M 문서 STUB). 수정하지 않은 대시보드의 모든 화면이 깨지지 않도록,
// 대시보드가 부르는 경로마다 화면이 견디는 응답(모양을 다 갖춘 빈 값 또는 JSON 오류)을 준다(설계 9장)

// stubRoutes 는 빈 응답 라우트다
func (h *Handlers) stubRoutes() []core.Route {
	return nil
}
