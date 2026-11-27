package admin

import (
	"context"
	"net/http"
	"sync"
	"time"

	"llm-gateway/internal/auth"
)

// 거절된 요청 기록(access_denied): 인증은 됐지만 권한이 없어 거절된 요청을 감사 기록에 남긴다. 누가 권한 밖의 일을 시도했는지가
// 사고 조사의 실마리다. 라우트 단계 거절은 auth.Guard 가, 관리 핸들러 안의 거절(팀 admin 이 남의 팀을 고치려 함 등)은 audited 가 알린다.
// 같은 행위자·같은 경로는 deniedWindow 에 한 번만 남긴다: 권한 없는 키 하나로 같은 요청을 반복해 기록을 불리지 못하게 한다

// deniedWindow 는 같은 거절을 다시 남기기까지의 간격이다
const deniedWindow = time.Minute

// maxDeniedKeys 는 간격을 기억하는 (행위자, 경로) 수의 상한이다. 넘으면 비운다(메모리를 묶지 않게. 비우면 잠깐 더 자주 남을 뿐이다)
const maxDeniedKeys = 10000

// deniedThrottle 은 같은 거절을 간격 안에 한 번만 통과시킨다. 인스턴스마다 따로 센다(여러 대면 대수만큼 더 남을 수 있다)
type deniedThrottle struct {
	now  func() time.Time
	mu   sync.Mutex
	seen map[string]time.Time
}

func newDeniedThrottle(now func() time.Time) *deniedThrottle {
	return &deniedThrottle{now: now, seen: map[string]time.Time{}}
}

// allow 는 이 거절을 지금 남길지다
func (d *deniedThrottle) allow(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if last, ok := d.seen[key]; ok && now.Sub(last) < deniedWindow {
		return false
	}
	if len(d.seen) >= maxDeniedKeys {
		d.seen = map[string]time.Time{}
	}
	d.seen[key] = now
	return true
}

// deniedKey 는 간격을 세는 단위(행위자, 메서드, 경로)다
func deniedKey(actor, method, path string) string { return actor + " " + method + " " + path }

// recordHandlerDenial 은 관리 핸들러가 401·403 으로 거절한 요청을 남긴다(audited 가 부른다). 대상 id 는 요청이 가리킨 첫 대상이다
func (h *Handlers) recordHandlerDenial(r *http.Request, action, table string, status int, ids []string) {
	p := principal(r.Context())
	actor := p.UserID
	if actor == "" && len(p.KeyToken) >= 12 {
		actor = "key:" + p.KeyToken[:12]
	}
	if actor == "" || !h.denials.allow(deniedKey(actor, r.Method, r.URL.Path)) {
		return
	}
	objectID := r.URL.Path
	if len(ids) > 0 {
		objectID = ids[0]
	}
	values := map[string]any{"method": r.Method, "path": r.URL.Path, "status": status, "role": string(p.Role), "attempted": action}
	if len(ids) > 1 {
		values["targets"] = len(ids)
	}
	if err := h.audit.recordAs(context.WithoutCancel(r.Context()), actor, p.KeyToken, "access_denied", table, objectID, nil, values); err != nil {
		h.logger.Error("감사 기록 실패", "action", "access_denied", "table", table, "err", err)
	}
}

// deniedStatus 는 거절(권한 없음)로 보는 상태 코드인지다. 400·404·422 같은 요청 오류는 거절로 보지 않는다
func deniedStatus(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// throttledDenial 은 authAudit 가 auth 의 거절 사건을 간격에 맞춰 거른다
func (a authAudit) throttledDenial(e auth.Event) bool {
	method, _ := e.Values["method"].(string)
	path, _ := e.Values["path"].(string)
	return a.denials.allow(deniedKey(e.UserID, method, path))
}
