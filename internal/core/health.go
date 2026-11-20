package core

import (
	"net/http"
	"sync/atomic"
)

// Lifecycle 은 종료가 시작됐는지 알린다. 종료 신호를 받으면 새 요청을 받기 전에 먼저 켜서,
// 남은 요청을 처리하는 동안 liveness 가 503 을 주게 한다(litellm 과 같은 동작)
type Lifecycle struct {
	shuttingDown atomic.Bool
}

func (l *Lifecycle) BeginShutdown() { l.shuttingDown.Store(true) }

func (l *Lifecycle) ShuttingDown() bool { return l.shuttingDown.Load() }

// HealthRoutes 는 litellm 과 같은 liveness 경로다. 프로브는 키 없이 부르므로 litellm 처럼 공개한다
func HealthRoutes(l *Lifecycle) []Route {
	liveness := func(w http.ResponseWriter, _ *http.Request) {
		if l.ShuttingDown() {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "shutting_down"})
			return
		}
		// litellm 은 문자열 하나를 JSON 으로 돌려준다
		WriteJSON(w, http.StatusOK, "I'm alive!")
	}
	return []Route{
		// liveliness 는 litellm 의 옛 이름이다. 클라이언트·프로브 호환을 위해 둘 다 받는다
		{Method: http.MethodGet, Path: "/health/liveliness", Access: Public(), Handler: liveness},
		{Method: http.MethodGet, Path: "/health/liveness", Access: Public(), Handler: liveness},
	}
}
