package inference

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// Handlers 는 OpenAI 표준 추론 라우트다. 클라이언트는 주소와 키만 바꾼다(설계 1장)
type Handlers struct {
	deployments *Deployments
	upstream    *http.Client // 연결·응답 헤더 타임아웃을 정한 클라이언트(설계 4장: connect 5s, read 300s). 스트림 전체 시간은 막지 않는다
	logger      *slog.Logger
}

func NewHandlers(deployments *Deployments, upstream *http.Client, logger *slog.Logger) *Handlers {
	return &Handlers{deployments: deployments, upstream: upstream, logger: logger}
}

// Routes 는 추론 라우트다. litellm 은 LLM API 라우트를 역할과 무관하게 연다(키만 맞으면). 모델 권한은 핸들러가 본다
func (h *Handlers) Routes() []core.Route {
	any := core.Authenticated()
	return []core.Route{
		{Method: http.MethodPost, Path: "/v1/chat/completions", Access: any, Handler: h.chat},
		{Method: http.MethodPost, Path: "/chat/completions", Access: any, Handler: h.chat},
		{Method: http.MethodGet, Path: "/v1/models", Access: any, Handler: h.models},
		{Method: http.MethodGet, Path: "/models", Access: any, Handler: h.models},
		{Method: http.MethodGet, Path: "/model_group/info", Access: any, Handler: h.modelGroupInfo},
	}
}

const maxRequestBody = 32 << 20 // 긴 대화 기록·이미지를 담은 요청도 받는다

func (h *Handlers) chat(w http.ResponseWriter, r *http.Request) {
	body, err := readJSONObject(w, r)
	if err != nil {
		core.ProxyError{Status: 400, Message: "Invalid JSON body: " + err.Error(), Type: "invalid_request_error", Param: nil}.Write(w)
		return
	}
	model, _ := body["model"].(string)
	if model == "" {
		core.ProxyError{Status: 400, Message: "model is required", Type: "invalid_request_error", Param: "model"}.Write(w)
		return
	}
	p, _ := auth.PrincipalFrom(r.Context())
	if denial, denied := modelDenied(p, model); denied {
		denial.Write(w)
		return
	}
	deployment, found, err := h.choose(r, model)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !found {
		// litellm 문구(proxy_server.py)
		core.ProxyError{Status: 400, Type: "invalid_request_error", Param: "model",
			Message: r.URL.Path + ": Invalid model name passed in model=" + model + ". Call `/v1/models` to view available models for your key."}.Write(w)
		return
	}
	body["model"] = deployment.UpstreamModel
	stream, _ := body["stream"].(bool)
	h.relay(w, r, deployment, body, model, stream)
}

func (h *Handlers) choose(r *http.Request, model string) (Deployment, bool, error) {
	all, err := h.deployments.Active(r.Context())
	if err != nil {
		return Deployment{}, false, err
	}
	var candidates []Deployment
	for _, d := range all {
		if d.ModelName == model {
			candidates = append(candidates, d)
		}
	}
	if len(candidates) == 0 {
		return Deployment{}, false, nil
	}
	return pick(candidates), true, nil
}

// relay 는 업스트림에 보내고 응답을 그대로 흘려 준다. 클라이언트가 끊으면 r.Context() 가 취소되어 업스트림 요청도 끊긴다(설계 4.2)
func (h *Handlers) relay(w http.ResponseWriter, r *http.Request, d Deployment, body map[string]any, requested string, stream bool) {
	payload, err := json.Marshal(body)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, d.chatURL(), bytes.NewReader(payload))
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	// 클라이언트의 Authorization 은 업스트림에 넘기지 않는다(설계 5장 4번)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.upstream.Do(req)
	if err != nil {
		if r.Context().Err() != nil {
			return // 클라이언트가 먼저 끊었다
		}
		h.logger.Warn("업스트림 연결 실패", "deployment", d.ID, "err", err)
		core.ProxyError{Status: 503, Type: "server_error", Param: nil,
			Message: "Upstream unavailable for model=" + requested + ". The model server did not respond."}.Write(w)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 || !stream || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		h.relayWhole(w, resp, requested)
		return
	}
	h.relayStream(w, r, resp, requested)
}

// relayWhole 은 스트림이 아닌 응답이다. 성공이면 model 을 요청한 이름으로 되돌린다(설계 5장 1번)
func (h *Handlers) relayWhole(w http.ResponseWriter, resp *http.Response, requested string) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		core.ProxyError{Status: 502, Type: "server_error", Message: "Upstream response could not be read."}.Write(w)
		return
	}
	if resp.StatusCode < 400 {
		data = withModel(data, requested)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(data)
}

// relayStream 은 SSE 를 줄 단위로 읽어 바로 쓰고 이벤트마다 내보낸다. 모아서 보내지 않는다(설계 4.4)
func (h *Handlers) relayStream(w http.ResponseWriter, r *http.Request, resp *http.Response, requested string) {
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no") // nginx 가 앞에 있어도 모으지 않게
	w.WriteHeader(http.StatusOK)
	flusher := http.NewResponseController(w)

	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if payload, ok := bytes.CutPrefix(line, []byte("data: ")); ok && !bytes.HasPrefix(payload, []byte("[DONE]")) {
				line = append(append([]byte("data: "), withModel(bytes.TrimRight(payload, "\r\n"), requested)...), '\n')
			}
			if _, werr := w.Write(line); werr != nil {
				return // 클라이언트가 끊었다. defer 가 업스트림 본문을 닫는다
			}
			if len(bytes.TrimSpace(line)) == 0 {
				_ = flusher.Flush() // 이벤트 끝(빈 줄)
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && r.Context().Err() == nil {
				h.logger.Warn("업스트림 스트림이 중간에 끊겼다", "err", err)
			}
			_ = flusher.Flush()
			return
		}
	}
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

func readJSONObject(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("본문이 JSON 객체가 아니다")
	}
	return body, nil
}

// models 는 OpenAI /v1/models 다. litellm 처럼 이 키가 쓸 수 있는 모델 이름만 준다
func (h *Handlers) models(w http.ResponseWriter, r *http.Request) {
	names, _, err := h.groups(r)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(names))
	for _, n := range names {
		// litellm 은 created 를 고정값, owned_by 를 "openai" 로 준다
		data = append(data, map[string]any{"id": n, "object": "model", "created": 1677610602, "owned_by": "openai"})
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// modelGroupInfo 는 litellm /model_group/info 다. 데스크탑이 컨텍스트 길이·추론 지원을 여기서 읽는다
func (h *Handlers) modelGroupInfo(w http.ResponseWriter, r *http.Request) {
	names, byName, err := h.groups(r)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	data := make([]map[string]any, 0, len(names))
	for _, n := range names {
		group := byName[n]
		first := group[0]
		providers := []string{}
		for _, d := range group {
			if !slices.Contains(providers, d.Provider) {
				providers = append(providers, d.Provider)
			}
		}
		data = append(data, map[string]any{
			"model_group":                         n,
			"providers":                           providers,
			"max_input_tokens":                    first.MaxInputTokens,
			"max_output_tokens":                   first.MaxOutputTokens,
			"input_cost_per_token":                first.InputCostPerToken,
			"output_cost_per_token":               first.OutputCostPerToken,
			"mode":                                "chat",
			"tpm":                                 nil,
			"rpm":                                 nil,
			"supports_parallel_function_calling":  false,
			"supports_vision":                     false,
			"supports_function_calling":           false,
			"supports_reasoning":                  first.SupportsReasoning,
			"supported_openai_params":             []string{},
			"configurable_clientside_auth_params": nil,
		})
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"data": data})
}

// groups 는 이 신원이 쓸 수 있는 모델 이름과 이름별 배포다
func (h *Handlers) groups(r *http.Request) ([]string, map[string][]Deployment, error) {
	all, err := h.deployments.Active(r.Context())
	if err != nil {
		return nil, nil, err
	}
	byName := map[string][]Deployment{}
	var names []string
	for _, d := range all {
		if _, seen := byName[d.ModelName]; !seen {
			names = append(names, d.ModelName)
		}
		byName[d.ModelName] = append(byName[d.ModelName], d)
	}
	p, _ := auth.PrincipalFrom(r.Context())
	return visible(p, names), byName, nil
}

func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("추론 처리 실패", "path", r.URL.Path, "err", err)
	core.ProxyError{Status: 500, Message: "Internal Server Error", Type: "internal_server_error"}.Write(w)
}
