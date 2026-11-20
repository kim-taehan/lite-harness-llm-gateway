package inference

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// usageSink 는 사용량을 받는 곳이다(limits.UsageRecorder). 막히지 않아야 한다
type usageSink interface {
	Record(limits.UsageEvent)
}

// exchange 는 요청 하나를 중계하며 사용량을 모은다
type exchange struct {
	requestID  string
	started    time.Time
	principal  auth.Principal
	deployment Deployment
	requested  string
	firstToken *time.Time
	chunks     int // 내용이 있는 청크 수. usage 를 받기 전에 끊기면 이것으로 어림한다
	usage      *upstreamUsage
	status     string
	httpStatus int
	errorCode  string
}

type upstreamUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// relay 는 업스트림에 보내고 응답을 그대로 흘려 준다. 클라이언트가 끊으면 r.Context() 가 취소되어 업스트림 요청도 끊긴다(설계 4.2).
// 끝나면(끊겨도) 사용량을 기록한다
func (h *Handlers) relay(w http.ResponseWriter, r *http.Request, d Deployment, body map[string]any, requested string, stream bool) {
	p, _ := auth.PrincipalFrom(r.Context())
	x := &exchange{requestID: newRequestID(), started: h.now(), principal: p, deployment: d, requested: requested, status: "success", httpStatus: 200}
	// litellm 과 같은 이름의 요청 ID 헤더(설계 11.3)
	w.Header().Set("x-litellm-call-id", x.requestID)
	defer func() { h.record(r, x) }()

	// 스트림이면 업스트림이 마지막에 usage 청크를 주도록 켠다. 클라이언트가 켜지 않았으면 그 청크는 걸러 낸다(설계 4.4)
	clientWantsUsage := false
	if stream {
		options, _ := body["stream_options"].(map[string]any)
		if options == nil {
			options = map[string]any{}
		}
		clientWantsUsage, _ = options["include_usage"].(bool)
		options["include_usage"] = true
		body["stream_options"] = options
	}
	payload, err := json.Marshal(body)
	if err != nil {
		x.fail(500, "internal_error")
		h.internalError(w, r, err)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, d.chatURL(), bytes.NewReader(payload))
	if err != nil {
		x.fail(500, "internal_error")
		h.internalError(w, r, err)
		return
	}
	// 클라이언트의 Authorization 은 업스트림에 넘기지 않는다(설계 5장 4번)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.upstream.Do(req)
	if err != nil {
		if r.Context().Err() != nil {
			x.cancel()
			return
		}
		h.logger.Warn("업스트림 연결 실패", "deployment", d.ID, "err", err)
		x.fail(503, "upstream_unavailable")
		core.ProxyError{Status: 503, Type: "server_error", Param: nil,
			Message: "Upstream unavailable for model=" + requested + ". The model server did not respond."}.Write(w)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 || !stream || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		h.relayWhole(w, resp, x)
		return
	}
	h.relayStream(w, r, resp, x, clientWantsUsage)
}

func (x *exchange) fail(status int, code string) {
	x.status, x.httpStatus, x.errorCode = "error", status, code
}

// cancel 은 클라이언트가 끊은 요청이다. litellm 처럼 499 로 남긴다(설계 4.4)
func (x *exchange) cancel() {
	x.status, x.httpStatus = "cancelled", 499
}

// relayWhole 은 스트림이 아닌 응답이다. 성공이면 model 을 요청한 이름으로 되돌린다(설계 5장 1번)
func (h *Handlers) relayWhole(w http.ResponseWriter, resp *http.Response, x *exchange) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		x.fail(502, "upstream_error")
		core.ProxyError{Status: 502, Type: "server_error", Message: "Upstream response could not be read."}.Write(w)
		return
	}
	if resp.StatusCode >= 400 {
		x.fail(resp.StatusCode, "upstream_error")
	} else {
		now := h.now()
		x.firstToken = &now
		var parsed struct {
			Usage *upstreamUsage `json:"usage"`
		}
		if json.Unmarshal(data, &parsed) == nil {
			x.usage = parsed.Usage
		}
		data = withModel(data, x.requested)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(data)
}

// relayStream 은 SSE 를 줄 단위로 읽어 바로 쓰고 이벤트마다 내보낸다. 모아서 보내지 않는다(설계 4.4)
func (h *Handlers) relayStream(w http.ResponseWriter, r *http.Request, resp *http.Response, x *exchange, clientWantsUsage bool) {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no") // nginx 가 앞에 있어도 모으지 않게
	w.WriteHeader(http.StatusOK)
	flusher := http.NewResponseController(w)

	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	skipBlank := false
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			out, keep := h.streamLine(line, x, clientWantsUsage)
			switch {
			case !keep:
				skipBlank = true // 걸러 낸 이벤트의 끝 빈 줄도 보내지 않는다
			case skipBlank && len(bytes.TrimSpace(out)) == 0:
				skipBlank = false
			default:
				skipBlank = false
				if _, werr := w.Write(out); werr != nil {
					x.cancel()
					return // 클라이언트가 끊었다. defer 가 업스트림 본문을 닫는다
				}
				if len(bytes.TrimSpace(out)) == 0 {
					_ = flusher.Flush() // 이벤트 끝(빈 줄)
				}
			}
		}
		if err != nil {
			switch {
			case r.Context().Err() != nil:
				x.cancel()
			case !errors.Is(err, io.EOF):
				h.logger.Warn("업스트림 스트림이 중간에 끊겼다", "err", err)
				x.fail(502, "upstream_error")
			}
			_ = flusher.Flush()
			return
		}
	}
}

// streamLine 은 SSE 한 줄을 고친다: model 을 요청 이름으로, usage 를 기록한다. keep=false 면 보내지 않는다(클라이언트가 원하지 않은 usage 청크)
func (h *Handlers) streamLine(line []byte, x *exchange, clientWantsUsage bool) ([]byte, bool) {
	payload, ok := bytes.CutPrefix(line, []byte("data: "))
	if !ok || bytes.HasPrefix(payload, []byte("[DONE]")) {
		return line, true
	}
	payload = bytes.TrimRight(payload, "\r\n")
	var chunk struct {
		Choices []json.RawMessage `json:"choices"`
		Usage   *upstreamUsage    `json:"usage"`
	}
	if json.Unmarshal(payload, &chunk) == nil {
		if len(chunk.Choices) > 0 {
			x.chunks++
			if x.firstToken == nil {
				now := h.now()
				x.firstToken = &now
			}
		}
		if chunk.Usage != nil {
			x.usage = chunk.Usage
			if len(chunk.Choices) == 0 && !clientWantsUsage {
				return nil, false
			}
		}
	}
	return append(append([]byte("data: "), withModel(payload, x.requested)...), '\n'), true
}

// record 는 사용량을 채널에 넣는다. 끊긴 요청도 그때까지의 부분 사용량을 남긴다(설계 4.2)
func (h *Handlers) record(r *http.Request, x *exchange) {
	if r.Context().Err() != nil && x.status == "success" {
		x.cancel()
	}
	event := limits.UsageEvent{
		RequestID: x.requestID, CreatedAt: x.started, KeyToken: x.principal.KeyToken, UserID: x.principal.UserID, TeamID: x.principal.TeamID,
		Model: x.requested, DeploymentID: x.deployment.ID, Status: x.status, HTTPStatus: x.httpStatus, ErrorCode: x.errorCode,
		LatencyMS: int(h.now().Sub(x.started).Milliseconds()),
	}
	if x.usage != nil {
		event.PromptTokens, event.CompletionTokens = x.usage.PromptTokens, x.usage.CompletionTokens
	} else if x.chunks > 0 {
		// usage 를 받기 전에 끊겼다. 내용 청크 하나를 토큰 하나로 어림한다(Ollama 는 청크마다 토큰 하나를 보낸다)
		event.CompletionTokens, event.Estimated = x.chunks, true
	}
	if x.firstToken != nil {
		ms := int(x.firstToken.Sub(x.started).Milliseconds())
		event.TTFTMS = &ms
	}
	event.Cost = cost(x.deployment, event.PromptTokens, event.CompletionTokens)
	h.usage.Record(event)
}

// cost 는 배포에 넣은 단가로 계산한다. 단가가 없으면 0 이다(설계 7장: 가격표를 내려받지 않는다)
func cost(d Deployment, prompt, completion int) float64 {
	var total float64
	if d.InputCostPerToken != nil {
		total += *d.InputCostPerToken * float64(prompt)
	}
	if d.OutputCostPerToken != nil {
		total += *d.OutputCostPerToken * float64(completion)
	}
	return total
}

// withModel 은 JSON 객체의 model 을 바꾼다. 객체가 아니면 그대로 둔다
func withModel(data []byte, model string) []byte {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // 숫자를 float 로 바꿔 값이 달라지지 않게
	var obj map[string]any
	if err := decoder.Decode(&obj); err != nil {
		return data
	}
	if _, has := obj["model"]; !has {
		return data
	}
	obj["model"] = model
	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}

func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:])
}
