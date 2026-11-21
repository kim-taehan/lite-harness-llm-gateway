package app

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// M3 완료 기준(설계 4.2): 클라이언트가 끊으면 업스트림 호출을 취소하고, 받은 만큼을 추정치로 499 로 기록한다

// stallingUpstream 은 첫 청크만 보내고 클라이언트가 끊을 때까지 붙들고 있는 가짜 업스트림이다. 취소가 전해지면 cancelled 가 닫힌다
func stallingUpstream(t *testing.T) (srv *httptest.Server, cancelled <-chan struct{}) {
	done := make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"model\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello wor\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(done)
	}))
	t.Cleanup(srv.Close)
	return srv, done
}

func TestChat_ClientDisconnectCancelsUpstreamAndRecordsEstimate(t *testing.T) {
	upstream, upstreamCancelled := stallingUpstream(t)
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)

	gateway := httptest.NewServer(h)
	t.Cleanup(gateway.Close)
	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, "POST", gateway.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"m1","stream":true,"messages":[{"role":"user","content":"hello there"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(resp.Body).ReadString('\n') // 첫 청크가 흘러온 것을 보고 나서 끊는다
	if !strings.Contains(line, "Hello wor") {
		t.Fatalf("첫 청크가 중계되지 않았다: %q", line)
	}
	cancel()
	_ = resp.Body.Close()

	select {
	case <-upstreamCancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("클라이언트가 끊었는데 업스트림 호출이 취소되지 않았다")
	}
	var status, source string
	var httpStatus, completion int
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := pool.QueryRow(t.Context(), "SELECT status, usage_source, http_status, completion_tokens FROM request_log").Scan(&status, &source, &httpStatus, &completion)
		if err == nil || time.Now().After(deadline) {
			break
		}
		<-time.After(20 * time.Millisecond)
	}
	if status != "cancelled" || source != "estimate" || httpStatus != 499 || completion == 0 {
		t.Errorf("request_log = %s %s %d (완성 토큰 %d) — cancelled·estimate·499·받은 만큼의 추정이어야 한다", status, source, httpStatus, completion)
	}
}

func TestChat_UpstreamErrorBodyAndStatusAreRelayedAndRecorded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":{"message":"model is loading","type":"server_error"}}`)
	}))
	t.Cleanup(upstream.Close)
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "model is loading") {
		t.Errorf("업스트림 오류가 그대로 가야 한다: %d %s", rec.Code, rec.Body.String())
	}
	var status string
	var httpStatus int
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := pool.QueryRow(t.Context(), "SELECT status, http_status FROM request_log").Scan(&status, &httpStatus)
		if err == nil || time.Now().After(deadline) {
			break
		}
		<-time.After(20 * time.Millisecond)
	}
	if status != "error" || httpStatus != 503 {
		t.Errorf("request_log = %s %d", status, httpStatus)
	}
}
