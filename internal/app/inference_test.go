package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

// 추론 경로(설계 4장)를 가짜 업스트림으로 확인한다: 중계, 모델 이름 되돌리기, usage 청크 걸러 내기, 사용량 기록

// fakeOllama 는 OpenAI 호환 /v1/chat/completions 를 흉내 낸다. 받은 요청 본문을 남긴다
func fakeOllama(t *testing.T) (*httptest.Server, *[]map[string]any) {
	var received []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		received = append(received, body)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("클라이언트 Authorization 이 업스트림으로 갔다")
		}
		if stream, _ := body["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, word := range []string{"Hel", "lo"} {
				fmt.Fprintf(w, "data: {\"id\":\"c1\",\"model\":\"%s\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"%s\"}}]}\n\n", body["model"], word)
			}
			fmt.Fprintf(w, "data: {\"id\":\"c1\",\"model\":\"%s\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\n", body["model"])
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"c2","model":"%s","choices":[{"index":0,"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`, body["model"])
	}))
	t.Cleanup(srv.Close)
	return srv, &received
}

func TestChat_RelaysStreamRecordsUsageAndHidesUnrequestedUsageChunk(t *testing.T) {
	upstream, received := fakeOllama(t)
	cfg, _ := LoadConfig(testEnv)
	db, pool := dbtest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	id := newIdentity(db)
	h := Handler(cfg, &core.Lifecycle{}, db, id, testLogin(t, cfg, id), startUsage(t, db, logger), logger)
	api := apiClient{t: t, h: h}

	api.ok("POST", "/model/new", map[string]any{"model_name": "chat-small",
		"litellm_params": map[string]any{"model": "ollama/tiny:1b", "api_base": upstream.URL},
		"model_info":     map[string]any{"input_cost_per_token": 0.001, "output_cost_per_token": 0.002}})
	key := api.ok("POST", "/key/generate", map[string]any{"models": []string{"chat-small"}})["key"].(string)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"chat-small","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" || rec.Header().Get("x-litellm-call-id") == "" {
		t.Fatalf("%d %v\n%s", rec.Code, rec.Header(), body)
	}
	if strings.Contains(body, "tiny:1b") || !strings.Contains(body, `"model":"chat-small"`) {
		t.Errorf("응답 model 을 요청 이름으로 되돌리지 않았다:\n%s", body)
	}
	if strings.Contains(body, "usage") {
		t.Errorf("클라이언트가 켜지 않은 usage 청크가 나갔다:\n%s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Errorf("[DONE] 이 없다:\n%s", body)
	}
	sent := (*received)[0]
	if sent["model"] != "tiny:1b" || sent["stream_options"].(map[string]any)["include_usage"] != true {
		t.Errorf("업스트림 요청 = %v (모델 태그, include_usage 주입)", sent)
	}

	// 배치가 쓰일 때까지 기다린다(시험의 배치 주기는 20ms)
	var prompt, completion int
	var costValue float64
	var source, status string
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := pool.QueryRow(t.Context(), "SELECT prompt_tokens, completion_tokens, cost::float8, usage_source, status FROM request_log").Scan(&prompt, &completion, &costValue, &source, &status)
		if err == nil || time.Now().After(deadline) {
			break
		}
		<-time.After(20 * time.Millisecond)
	}
	if prompt != 7 || completion != 2 || source != "provider" || status != "success" {
		t.Errorf("request_log = %d %d %s %s", prompt, completion, source, status)
	}
	if want := 7*0.001 + 2*0.002; fmt.Sprintf("%.6f", costValue) != fmt.Sprintf("%.6f", want) {
		t.Errorf("cost = %v, 기대 %v", costValue, want)
	}
	info := api.ok("GET", "/key/info?key="+key, nil)["info"].(map[string]any)
	if fmt.Sprintf("%.6f", info["spend"]) != fmt.Sprintf("%.6f", 7*0.001+2*0.002) {
		t.Errorf("키 spend = %v", info["spend"])
	}
}

func TestChat_NonStreamRewritesModelAndKeepsUsage(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "chat-small", "litellm_params": map[string]any{"model": "ollama/tiny:1b", "api_base": upstream.URL}})

	status, body := api.do("POST", "/v1/chat/completions", map[string]any{"model": "chat-small", "messages": []any{map[string]any{"role": "user", "content": "x"}}})
	obj := body.(map[string]any)
	if status != 200 || obj["model"] != "chat-small" || obj["usage"] == nil {
		t.Errorf("%d %v", status, obj)
	}
}

func TestChat_UnknownModelAndUnreachableUpstream(t *testing.T) {
	api := newAPI(t)
	status, body := api.do("POST", "/v1/chat/completions", map[string]any{"model": "nope", "messages": []any{}})
	if status != 400 || !strings.Contains(errField(body, "message").(string), "Invalid model name passed in model=nope") {
		t.Errorf("없는 모델: %d %v", status, body)
	}

	api.ok("POST", "/model/new", map[string]any{"model_name": "down", "litellm_params": map[string]any{"model": "ollama/x", "api_base": "http://127.0.0.1:1"}})
	status, body = api.do("POST", "/v1/chat/completions", map[string]any{"model": "down", "messages": []any{}})
	if status != 503 {
		t.Errorf("닿지 않는 업스트림: %d %v", status, body)
	}
}

func TestModels_ShowsOnlyAllowedModelsAndGroupInfo(t *testing.T) {
	api := newAPI(t)
	for _, name := range []string{"a", "b"} {
		api.ok("POST", "/model/new", map[string]any{"model_name": name, "litellm_params": map[string]any{"model": "ollama/" + name, "api_base": "http://127.0.0.1:1"},
			"model_info": map[string]any{"max_input_tokens": 8192, "max_output_tokens": 1024}})
	}
	key := api.ok("POST", "/key/generate", map[string]any{"models": []string{"b"}})["key"].(string)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	api.h.ServeHTTP(rec, req)
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"id":"b"`)) || bytes.Contains(rec.Body.Bytes(), []byte(`"id":"a"`)) {
		t.Errorf("/v1/models = %s", rec.Body.String())
	}

	info := api.ok("GET", "/model_group/info", nil)["data"].([]any)
	if len(info) != 2 || info[0].(map[string]any)["max_input_tokens"] != 8192.0 {
		t.Errorf("/model_group/info = %v", info)
	}
}
