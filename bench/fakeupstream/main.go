// 부하 시험용 가짜 업스트림. OpenAI 호환 /v1/chat/completions 를 일정한 지연으로 돌려준다.
// 진짜 모델은 응답이 수 초라 게이트웨이 자체의 오버헤드가 묻히므로, 두 게이트웨이가 같은 가짜 서버를 보게 한다.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

type settings struct {
	delay      time.Duration // 비스트림 응답 지연
	ttft       time.Duration // 스트림 첫 청크까지
	chunks     int
	chunkDelay time.Duration
}

func main() {
	listen := flag.String("listen", ":9100", "수신 주소")
	s := settings{}
	flag.DurationVar(&s.delay, "delay", 50*time.Millisecond, "비스트림 응답 지연")
	flag.DurationVar(&s.ttft, "ttft", 50*time.Millisecond, "스트림 첫 청크까지 걸리는 시간")
	flag.IntVar(&s.chunks, "chunks", 20, "스트림 내용 청크 수")
	flag.DurationVar(&s.chunkDelay, "chunk-delay", 5*time.Millisecond, "스트림 청크 사이 간격")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.chat)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") })
	log.Printf("가짜 업스트림 %s (지연 %s, 첫 청크 %s, 청크 %d개 × %s)", *listen, s.delay, s.ttft, s.chunks, s.chunkDelay)
	log.Fatal((&http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe())
}

// wait 는 d 만큼 기다린다. 클라이언트가 끊으면 거짓이다
func wait(r *http.Request, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

func (s settings) chat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Stream {
		s.stream(w, r, req.Model)
		return
	}
	if !wait(r, s.delay) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":%q,"object":"chat.completion","created":0,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"benchmark response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`, nextID(), req.Model)
}

var responseCount atomic.Uint64

// nextID 는 응답마다 다른 id 다. litellm 은 응답 id 를 사용량 기록의 기본 키로 써서, 같은 id 를 계속 주면 요청 수만큼 기록하지 않고 한 행으로 합쳐 버린다
func nextID() string { return fmt.Sprintf("chatcmpl-bench-%d", responseCount.Add(1)) }

func (s settings) stream(w http.ResponseWriter, r *http.Request, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if !wait(r, s.ttft) {
		return
	}
	id := nextID() // 한 응답의 모든 청크는 같은 id 다
	for i := 0; i < s.chunks; i++ {
		fmt.Fprintf(w, "data: {\"id\":%q,\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"tok \"}}]}\n\n", id, model)
		flusher.Flush()
		if i < s.chunks-1 && !wait(r, s.chunkDelay) {
			return
		}
	}
	fmt.Fprintf(w, "data: {\"id\":%q,\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", id, model)
	fmt.Fprintf(w, "data: {\"id\":%q,\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":%q,\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n", id, model, s.chunks, 10+s.chunks)
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}
