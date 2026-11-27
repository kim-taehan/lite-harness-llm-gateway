package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	permit     *limits.Permit
}

// upstreamUsage 는 두 모양을 함께 읽는다: Chat Completions·Embeddings(prompt/completion), Responses(input/output)
type upstreamUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
}

func (u upstreamUsage) tokens() (prompt, completion int) {
	return u.PromptTokens + u.InputTokens, u.CompletionTokens + u.OutputTokens
}

// upstreamAPI 는 중계할 OpenAI API 하나다
type upstreamAPI struct {
	path string
	// injectUsage 면 스트림에 stream_options.include_usage 를 켠다(Chat Completions). Responses 는 completed 이벤트에 늘 usage 가 있다
	injectUsage bool
}

var (
	apiChat       = upstreamAPI{path: "chat/completions", injectUsage: true}
	apiResponses  = upstreamAPI{path: "responses"}
	apiEmbeddings = upstreamAPI{path: "embeddings"}
)

// relayRequest 는 중계할 요청 하나다. body["model"] 은 업스트림 모델 이름으로 바뀌어 있고, requested 는 클라이언트가 부른 이름이다
type relayRequest struct {
	api        upstreamAPI
	deployment Deployment
	body       map[string]any
	requested  string
	stream     bool
	permit     *limits.Permit
}

// relay 는 업스트림에 보내고 응답을 그대로 흘려 준다. 클라이언트가 끊으면 r.Context() 가 취소되어 업스트림 요청도 끊긴다(설계 4.2).
// 끝나면(끊겨도) 사용량을 기록한다
func (h *Handlers) relay(w http.ResponseWriter, r *http.Request, req relayRequest) {
	d, body, api, permit := req.deployment, req.body, req.api, req.permit
	p, _ := auth.PrincipalFrom(r.Context())
	x := &exchange{requestID: core.NewUUID(), started: h.now(), principal: p, deployment: d, requested: req.requested, status: "success", httpStatus: 200, permit: permit}
	// litellm 과 같은 이름의 요청 ID 헤더(설계 11.3)
	w.Header().Set("x-litellm-call-id", x.requestID)
	for name, value := range permit.Headers() {
		w.Header().Set(name, value)
	}
	defer func() { h.record(r, x) }()

	clientWantsUsage := requestUsageChunk(body, api, req.stream)
	payload, err := json.Marshal(body)
	if err != nil {
		x.fail(500, "internal_error")
		h.internalError(w, r, err)
		return
	}
	resp, err := h.send(r.Context(), d, api.path, payload)
	if err != nil {
		if r.Context().Err() != nil {
			x.cancel()
			return
		}
		h.logger.Warn("업스트림 연결 실패", "deployment", d.ID, "err", err)
		x.fail(503, "upstream_unavailable")
		core.ProxyError{Status: 503, Type: "server_error", Param: nil,
			Message: "Upstream unavailable for model=" + req.requested + ". The model server did not respond."}.Write(w)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 || !req.stream || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		h.relayWhole(w, resp, x)
		return
	}
	h.relayStream(w, r, resp, x, clientWantsUsage)
}

// requestUsageChunk 는 스트림이면 업스트림이 마지막에 usage 청크를 주도록 켠다(body 를 고친다).
// 클라이언트가 켜지 않았으면 그 청크는 걸러 내야 하므로 클라이언트가 원했는지를 돌려준다(설계 4.4)
func requestUsageChunk(body map[string]any, api upstreamAPI, stream bool) (clientWantsUsage bool) {
	if !stream || !api.injectUsage {
		return true
	}
	options, _ := body["stream_options"].(map[string]any)
	if options == nil {
		options = map[string]any{}
	}
	clientWantsUsage, _ = options["include_usage"].(bool)
	options["include_usage"] = true
	body["stream_options"] = options
	return clientWantsUsage
}

// send 는 업스트림을 부르는 단 하나의 지점이다(추론 중계와 헬스 체크가 같이 쓴다). 주소 조립·헤더·(뒤에 붙일) 업스트림 인증이
// 여기에만 있다. 클라이언트의 Authorization 은 업스트림에 넘기지 않는다(설계 5장 4번). 배포에 저장한 업스트림 키가 있으면 그것만 붙인다
func (h *Handlers) send(ctx context.Context, d Deployment, path string, payload []byte) (*http.Response, error) {
	key, err := h.deployments.upstreamKey(d)
	if err != nil {
		return nil, fmt.Errorf("배포 %s 의 업스트림 키: %w", d.ID, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint(path), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return h.upstream.Do(req)
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
		Choices  []json.RawMessage `json:"choices"`
		Usage    *upstreamUsage    `json:"usage"`
		Delta    json.RawMessage   `json:"delta"` // Responses 의 *.delta 이벤트
		Response *struct {
			Usage *upstreamUsage `json:"usage"`
		} `json:"response"` // Responses 의 response.completed 이벤트
	}
	if json.Unmarshal(payload, &chunk) == nil {
		if chunk.Response != nil && chunk.Response.Usage != nil {
			x.usage = chunk.Response.Usage
		}
		if len(chunk.Choices) > 0 || len(chunk.Delta) > 0 {
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
	if x.permit != nil {
		event.EndUserID = x.permit.EndUserID()
	}
	if x.usage != nil {
		event.PromptTokens, event.CompletionTokens = x.usage.tokens()
	} else if x.chunks > 0 {
		// usage 를 받기 전에 끊겼다. 내용 청크 하나를 토큰 하나로 어림한다(Ollama 는 청크마다 토큰 하나를 보낸다)
		event.CompletionTokens, event.Estimated = x.chunks, true
	}
	if x.firstToken != nil {
		ms := int(x.firstToken.Sub(x.started).Milliseconds())
		event.TTFTMS = &ms
	}
	event.Cost = cost(x.deployment, event.PromptTokens, event.CompletionTokens)
	// 요청이 끊긴 뒤에도 토큰은 정산해야 한다
	x.permit.AddTokens(context.WithoutCancel(r.Context()), event.PromptTokens+event.CompletionTokens)
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
	changed := false
	if _, has := obj["model"]; has {
		obj["model"] = model
		changed = true
	}
	// Responses 스트림 이벤트는 response 객체 안에 model 이 있다
	if inner, ok := obj["response"].(map[string]any); ok {
		if _, has := inner["model"]; has {
			inner["model"] = model
			changed = true
		}
	}
	if !changed {
		return data
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}
