package auth

import (
	"context"
	"net/http"
)

// Event 는 신원과 관련된 사건이다(로그인, 로그아웃, 비밀번호 변경). 감사 로그가 받아 적는다.
// auth 는 감사 저장소를 모른다(의존 방향이 admin → auth 라서). 조립 지점이 EventSink 를 넣어 준다
type Event struct {
	// Action 은 login_success, login_failed, logout, password_changed, onboarding_claimed 중 하나다
	Action string
	// UserID 는 행위자다. 실패한 로그인은 사용자를 알 수 없어 시도한 아이디(길이를 줄인 값)다
	UserID string
	// Values 는 방법(method)·이유(reason)·접속 주소(ip) 같은 부가 정보다. 비밀번호는 절대 넣지 않는다
	Values map[string]any
}

// EventSink 는 사건을 받아 적는 곳이다. 기록이 실패해도 요청을 실패시키지 않으므로 오류를 돌려주지 않는다
type EventSink interface {
	Record(ctx context.Context, e Event)
}

// maxEventUserID 는 실패한 로그인의 아이디를 기록할 때의 길이 상한이다(공개 라우트라 아무 값이나 올 수 있다)
const maxEventUserID = 128

// record 는 사건을 남긴다. 요청이 끊겨도 남도록 취소와 무관한 ctx 를 쓰고, 접속 주소를 함께 적는다
func (l *Login) record(r *http.Request, action, userID string, values map[string]any) {
	if l.events == nil {
		return
	}
	if values == nil {
		values = map[string]any{}
	}
	values["ip"] = l.proxies.clientIP(r)
	if len(userID) > maxEventUserID {
		userID = userID[:maxEventUserID]
	}
	l.events.Record(context.WithoutCancel(r.Context()), Event{Action: action, UserID: userID, Values: values})
}
