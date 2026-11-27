package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// 업스트림 키: 저장은 봉해서, 업스트림 호출에만 붙이고, 어떤 응답에도 싣지 않는다

// authRecordingUpstream 은 받은 Authorization 헤더를 기록하는 가짜 업스트림이다(fakeOllama 는 이 헤더가 오면 시험을 실패시킨다)
type authRecordingUpstream struct {
	mu   sync.Mutex
	seen []string
}

func (u *authRecordingUpstream) record(r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.seen = append(u.seen, r.Header.Get("Authorization"))
}

func (u *authRecordingUpstream) last(t *testing.T) string {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.seen) == 0 {
		t.Fatal("업스트림이 호출되지 않았다")
	}
	return u.seen[len(u.seen)-1]
}

func newAuthRecordingUpstream(t *testing.T) (*httptest.Server, *authRecordingUpstream) {
	t.Helper()
	log := &authRecordingUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func chatWithMasterKey(h http.Handler, model string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+testMasterKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUpstreamKey_SentToUpstreamStoredSealedAndNeverReturned(t *testing.T) {
	upstream, seen := newAuthRecordingUpstream(t)
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	created := api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "openai/x", "api_base": upstream.URL, "api_key": "up-secret-1"}})
	id := created["model_info"].(map[string]any)["id"].(string)

	if rec := chatWithMasterKey(h, "m1"); rec.Code != 200 {
		t.Fatalf("추론: %d %s", rec.Code, rec.Body)
	}
	if got := seen.last(t); got != "Bearer up-secret-1" {
		t.Errorf("업스트림이 받은 Authorization = %q", got)
	}

	// 어떤 응답에도 평문이 없다: 생성·상세·표
	for name, body := range map[string]any{
		"생성":   created,
		"상세":   api.ok("GET", "/v1/model/info?litellm_model_id="+id, nil),
		"표":    api.ok("GET", "/v2/model/info", nil),
		"그룹":   api.ok("GET", "/model_group/info", nil),
		"v1모델": api.ok("GET", "/v1/models", nil),
	} {
		if raw, _ := json.Marshal(body); bytes.Contains(raw, []byte("up-secret-1")) {
			t.Errorf("%s 응답에 업스트림 키가 들어 있다", name)
		}
	}
	// DB 에는 봉한 값만 있다
	var sealed []byte
	if err := pool.QueryRow(t.Context(), "SELECT api_key_encrypted FROM model_deployment WHERE id = $1", id).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if len(sealed) == 0 || bytes.Contains(sealed, []byte("up-secret-1")) {
		t.Errorf("DB 의 값이 봉해지지 않았다: %q", sealed)
	}
}

func TestUpstreamKey_UpdateKeepsReplacesAndClears(t *testing.T) {
	upstream, seen := newAuthRecordingUpstream(t)
	h, _, _ := testGateway(t)
	api := apiClient{t: t, h: h}
	created := api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "openai/x", "api_base": upstream.URL, "api_key": "up-secret-1"}})
	id := created["model_info"].(map[string]any)["id"].(string)
	update := func(params map[string]any) {
		t.Helper()
		api.ok("PATCH", "/model/"+id+"/update", map[string]any{"litellm_params": params})
	}
	authAfterChat := func() string {
		t.Helper()
		if rec := chatWithMasterKey(h, "m1"); rec.Code != 200 {
			t.Fatalf("추론: %d %s", rec.Code, rec.Body)
		}
		return seen.last(t)
	}

	update(map[string]any{"weight": 1}) // api_key 를 보내지 않은 수정은 키를 그대로 둔다
	if got := authAfterChat(); got != "Bearer up-secret-1" {
		t.Errorf("다른 값만 고쳤는데 키가 바뀌었다: %q", got)
	}
	update(map[string]any{"api_key": ""}) // 빈 문자열은 바꾸지 않는다
	if got := authAfterChat(); got != "Bearer up-secret-1" {
		t.Errorf("빈 문자열이 키를 바꿨다: %q", got)
	}
	update(map[string]any{"api_key": "up-secret-2"})
	if got := authAfterChat(); got != "Bearer up-secret-2" {
		t.Errorf("키를 바꿨는데 = %q", got)
	}
	update(map[string]any{"api_key": nil}) // null 은 지운다
	if got := authAfterChat(); got != "" {
		t.Errorf("키를 지웠는데 Authorization = %q", got)
	}
	if status, _ := api.do("PATCH", "/model/"+id+"/update", map[string]any{"litellm_params": map[string]any{"api_key": 5}}); status == 200 {
		t.Error("숫자 api_key 가 받아들여졌다")
	}
}

// 마스터 키를 바꾸면 저장된 업스트림 키를 풀 수 없다. 그 모델의 추론만 실패하고 관리 화면은 열려야 한다
func TestUpstreamKey_UnreadableKeyFailsOnlyInference(t *testing.T) {
	upstream, seen := newAuthRecordingUpstream(t)
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	created := api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "openai/x", "api_base": upstream.URL, "api_key": "up-secret-1"}})
	id := created["model_info"].(map[string]any)["id"].(string)
	if _, err := pool.Exec(t.Context(), "UPDATE model_deployment SET api_key_encrypted = '\\x00112233445566778899aabbccddeeff00112233445566778899' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}

	if rec := chatWithMasterKey(h, "m1"); rec.Code == 200 {
		t.Errorf("풀 수 없는 키인데 추론이 됐다")
	}
	if len(seen.seen) != 0 {
		t.Errorf("키를 못 풀었는데 업스트림이 호출됐다: %v", seen.seen)
	}
	if got := api.ok("GET", "/v2/model/info", nil); got["total_count"] != 1.0 {
		t.Errorf("관리 화면 모델 표가 열리지 않는다: %v", got)
	}
	api.ok("PATCH", "/model/"+id+"/update", map[string]any{"litellm_params": map[string]any{"api_key": "up-secret-new"}})
	if rec := chatWithMasterKey(h, "m1"); rec.Code != 200 {
		t.Errorf("키를 다시 넣었는데 추론이 안 된다: %d %s", rec.Code, rec.Body)
	}
}
