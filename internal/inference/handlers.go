package inference

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// Handlers 는 OpenAI 표준 추론 라우트다. 클라이언트는 주소와 키만 바꾼다(설계 1장)
type Handlers struct {
	deployments *Deployments
	upstream    *http.Client // 연결·응답 헤더 타임아웃을 정한 클라이언트(설계 4장: connect 5s, read 300s). 스트림 전체 시간은 막지 않는다
	usage       usageSink
	gate        *limits.Gate
	now         func() time.Time
	logger      *slog.Logger
}

func NewHandlers(deployments *Deployments, upstream *http.Client, usage usageSink, gate *limits.Gate, now func() time.Time, logger *slog.Logger) *Handlers {
	return &Handlers{deployments: deployments, upstream: upstream, usage: usage, gate: gate, now: now, logger: logger}
}

// Routes 는 추론 라우트다. litellm 은 LLM API 라우트를 역할과 무관하게 연다(키만 맞으면). 모델 권한은 핸들러가 본다
func (h *Handlers) Routes() []core.Route {
	any := core.Authenticated()
	return []core.Route{
		{Method: http.MethodPost, Path: "/v1/chat/completions", Access: any, Handler: h.proxy(apiChat)},
		{Method: http.MethodPost, Path: "/chat/completions", Access: any, Handler: h.proxy(apiChat)},
		{Method: http.MethodPost, Path: "/v1/responses", Access: any, Handler: h.proxy(apiResponses)},
		{Method: http.MethodPost, Path: "/responses", Access: any, Handler: h.proxy(apiResponses)},
		{Method: http.MethodPost, Path: "/v1/embeddings", Access: any, Handler: h.proxy(apiEmbeddings)},
		{Method: http.MethodPost, Path: "/embeddings", Access: any, Handler: h.proxy(apiEmbeddings)},
		{Method: http.MethodGet, Path: "/v1/models", Access: any, Handler: h.models},
		{Method: http.MethodGet, Path: "/models", Access: any, Handler: h.models},
		{Method: http.MethodGet, Path: "/model_group/info", Access: any, Handler: h.modelGroupInfo},
		// 헬스 체크는 업스트림을 실제로 부르므로 관리자만(litellm 도 관리 라우트다)
		{Method: http.MethodGet, Path: "/health", Access: core.Roles(core.RoleProxyAdmin), Handler: h.health},
		{Method: http.MethodPost, Path: "/health/test_connection", Access: core.Roles(core.RoleProxyAdmin), Handler: h.testConnection},
	}
}

const maxRequestBody = 32 << 20 // 긴 대화 기록·이미지를 담은 요청도 받는다

// proxy 는 OpenAI API 하나를 중계하는 핸들러다. 모델 검사·배포 선택·한도·사용량 기록은 API 와 무관하게 같다
func (h *Handlers) proxy(api upstreamAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h.handle(w, r, api) }
}

func (h *Handlers) handle(w http.ResponseWriter, r *http.Request, api upstreamAPI) {
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
	// 배포를 고른 뒤 한도를 본다. 동시성 한도가 배포 단위라 어느 배포인지 알아야 하기 때문이다(설계 4.1)
	permit, err := h.gate.Admit(r.Context(), limits.Subject{KeyToken: p.KeyToken, KeyAlias: p.KeyAlias, TeamID: p.TeamID, UserID: p.UserID})
	var rejected core.ProxyError
	switch {
	case errors.As(err, &rejected):
		rejected.Write(w)
		return
	case err != nil:
		h.internalError(w, r, err)
		return
	}
	defer permit.Release()
	body["model"] = deployment.UpstreamModel
	stream, _ := body["stream"].(bool)
	h.relay(w, r, api, deployment, body, model, stream, permit)
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
		data = append(data, ModelGroupRow(n, byName[n]))
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"data": data})
}

// ModelGroupRow 는 litellm ModelGroupInfo 한 행이다. 그룹의 첫 배포 값을 쓴다(/model_group/info 와 공개 모델 허브가 같이 쓴다)
func ModelGroupRow(name string, group []Deployment) map[string]any {
	first := group[0]
	providers := []string{}
	for _, d := range group {
		if !slices.Contains(providers, d.Provider) {
			providers = append(providers, d.Provider)
		}
	}
	mode, _ := first.ExtraInfo["mode"].(string)
	if mode == "" {
		mode = "chat"
	}
	return map[string]any{
		"model_group":                         name,
		"providers":                           providers,
		"max_input_tokens":                    first.MaxInputTokens,
		"max_output_tokens":                   first.MaxOutputTokens,
		"input_cost_per_token":                first.InputCostPerToken,
		"output_cost_per_token":               first.OutputCostPerToken,
		"mode":                                mode,
		"tpm":                                 nil,
		"rpm":                                 nil,
		"supports_parallel_function_calling":  false,
		"supports_vision":                     false,
		"supports_function_calling":           false,
		"supports_reasoning":                  first.SupportsReasoning,
		"supported_openai_params":             []string{},
		"configurable_clientside_auth_params": nil,
	}
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
