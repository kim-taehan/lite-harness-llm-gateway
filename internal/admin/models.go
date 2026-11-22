package admin

import (
	"cmp"
	"errors"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
)

// 모델 관리 API(M 5장). 요청·응답 모양은 litellm config 의 model_list 원소와 같다:
// {"model_name", "litellm_params": {"model": "ollama/<tag>", "api_base", ...}, "model_info": {...}}.
// 우리가 쓰는 키는 배포 열로, 나머지 키는 그대로 저장했다가 돌려준다(대시보드가 넣고 다시 읽는다)

// computedInfoKeys 는 응답에서 우리가 만들어 내는 model_info 키다. 화면이 수정 요청에 되돌려 보내도 저장하지 않는다
var computedInfoKeys = []string{"id", "db_model", "created_at", "updated_at", "created_by", "updated_by", "litellm_provider",
	"direct_access", "access_via_team_ids", "max_tokens"}

// secretParamKeys 는 업스트림 자격 증명이다. 우리는 업스트림 인증을 아직 하지 않으므로 저장하지 않는다(평문으로 다시 내보내지 않게)
var secretParamKeys = []string{"api_key", "aws_access_key_id", "aws_secret_access_key", "aws_session_token", "vertex_credentials",
	"azure_ad_token", "client_secret"}

// applyModelSpec 은 litellm 모양 값을 배포에 덮어쓴다. 값이 null 인 나머지 키는 지운다.
// model_info 를 먼저, litellm_params 를 나중에 적용한다(litellm 처럼 litellm_params 의 비용이 이긴다)
func applyModelSpec(d *inference.Deployment, params, info map[string]any) validationError {
	extraInfo, extraParams := maps.Clone(d.ExtraInfo), maps.Clone(d.ExtraParams)
	if extraInfo == nil {
		extraInfo = map[string]any{}
	}
	if extraParams == nil {
		extraParams = map[string]any{}
	}
	errs := applyModelInfo(d, info, extraInfo)
	errs = append(errs, applyModelParams(d, params, extraParams)...)
	d.ExtraInfo, d.ExtraParams = extraInfo, extraParams
	return errs
}

func specError(section, key string, value any, kind, msg string) FieldError {
	return FieldError{Type: kind, Loc: []any{"body", section, key}, Msg: msg, Input: value}
}

// applyModelInfo 는 model_info 를 배포에 덮어쓴다. 알려진 키는 필드로, 나머지는 extra 에 둔다
func applyModelInfo(d *inference.Deployment, info, extraInfo map[string]any) (errs validationError) {
	for k, v := range info {
		ok := true
		switch k {
		case "max_input_tokens":
			d.MaxInputTokens, ok = intPtr(v)
		case "max_output_tokens":
			d.MaxOutputTokens, ok = intPtr(v)
		case "supports_reasoning":
			b, isBool := v.(bool)
			d.SupportsReasoning, ok = b, isBool || v == nil
		case "input_cost_per_token":
			d.InputCostPerToken, ok = floatPtr(v)
		case "output_cost_per_token":
			d.OutputCostPerToken, ok = floatPtr(v)
		case "blocked":
			d.Active = v != true
		default:
			if !slices.Contains(computedInfoKeys, k) {
				setExtra(extraInfo, k, v)
			}
		}
		if !ok {
			errs = append(errs, specError("model_info", k, v, "float_type", "Input should be a valid number"))
		}
	}
	return errs
}

// applyModelParams 는 litellm_params 를 배포에 덮어쓴다. 자격 증명 키는 저장하지 않는다
func applyModelParams(d *inference.Deployment, params, extraParams map[string]any) (errs validationError) {
	for k, v := range params {
		ok := true
		switch k {
		case "model":
			s, _ := v.(string)
			provider, upstream, found := strings.Cut(s, "/")
			if !found || provider == "" || upstream == "" {
				errs = append(errs, specError("litellm_params", k, v, "value_error", "Value error, model must look like '<provider>/<model>' (e.g. ollama/llama3)"))
				continue
			}
			if provider == "ollama_chat" {
				provider = "ollama"
			}
			d.Provider, d.UpstreamModel = provider, upstream
		case "api_base":
			s, isString := v.(string)
			d.BaseURL, ok = s, isString && s != ""
		case "weight":
			n, isInt := intPtr(v)
			ok = isInt && n != nil
			if ok {
				d.Weight = int(*n)
			}
		case "input_cost_per_token":
			d.InputCostPerToken, ok = floatPtr(v)
		case "output_cost_per_token":
			d.OutputCostPerToken, ok = floatPtr(v)
		default:
			if !slices.Contains(secretParamKeys, k) {
				setExtra(extraParams, k, v)
			}
		}
		if !ok {
			errs = append(errs, specError("litellm_params", k, v, "value_error", "Value error, invalid value"))
		}
	}
	return errs
}

func setExtra(extra map[string]any, key string, value any) {
	if value == nil {
		delete(extra, key)
		return
	}
	extra[key] = value
}

func intPtr(v any) (*int64, bool) {
	if v == nil {
		return nil, true
	}
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) {
		return nil, false
	}
	n := int64(f)
	return &n, true
}

func floatPtr(v any) (*float64, bool) {
	if v == nil {
		return nil, true
	}
	f, ok := v.(float64)
	return &f, ok
}

type modelRequest struct {
	ModelName     *string        `json:"model_name"`
	LitellmParams map[string]any `json:"litellm_params"`
	ModelInfo     map[string]any `json:"model_info"`
	Blocked       *bool          `json:"blocked"`
}

func (h *Handlers) createModel(w http.ResponseWriter, r *http.Request) {
	var req modelRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	d := inference.Deployment{Active: true}
	errs := applyModelSpec(&d, req.LitellmParams, req.ModelInfo)
	if req.ModelName == nil || *req.ModelName == "" {
		errs = append(errs, missing(raw, "model_name"))
	}
	if d.Provider == "" && req.LitellmParams["model"] == nil {
		errs = append(errs, FieldError{Type: "missing", Loc: []any{"body", "litellm_params", "model"}, Msg: "Field required", Input: req.LitellmParams})
	}
	if d.BaseURL == "" && req.LitellmParams["api_base"] == nil {
		errs = append(errs, missing(raw, "api_base"))
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	id, _ := req.ModelInfo["id"].(string)
	created, err := h.deployments.Create(r.Context(), inference.NewDeployment{
		ID: &id, ModelName: *req.ModelName, Provider: d.Provider, BaseURL: d.BaseURL, UpstreamModel: d.UpstreamModel, Weight: d.Weight,
		MaxInputTokens: d.MaxInputTokens, MaxOutputTokens: d.MaxOutputTokens, SupportsReasoning: d.SupportsReasoning,
		InputCostPerToken: d.InputCostPerToken, OutputCostPerToken: d.OutputCostPerToken, ExtraParams: d.ExtraParams, ExtraInfo: d.ExtraInfo,
	})
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, deploymentRow(created))
	case errors.Is(err, inference.ErrDeploymentIDTaken):
		proxyError(400, "Model with id "+id+" already exists").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// updateModel 은 PATCH /model/{model_id}/update 다. 보낸 키만 바꾼다. 일시정지 토글은 {"blocked": true|false} 만 보낸다
func (h *Handlers) updateModel(w http.ResponseWriter, r *http.Request) {
	var req modelRequest
	if _, ok := requestBody(w, r, &req); !ok {
		return
	}
	id := r.PathValue("model_id")
	d, err := h.deployments.Find(r.Context(), id)
	if errors.Is(err, inference.ErrDeploymentNotFound) {
		proxyError(404, "Model with id="+id+" not found in db").Write(w)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if errs := applyModelSpec(&d, req.LitellmParams, req.ModelInfo); len(errs) > 0 {
		errs.write(w)
		return
	}
	if req.ModelName != nil && *req.ModelName != "" {
		d.ModelName = *req.ModelName
	}
	if req.Blocked != nil {
		d.Active = !*req.Blocked
	}
	updated, err := h.deployments.Replace(r.Context(), d)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, deploymentRow(updated))
}

func (h *Handlers) deleteModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID *string `json:"id"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.ID == nil {
		validationError{missing(raw, "id")}.write(w)
		return
	}
	err := h.deployments.Delete(r.Context(), *req.ID)
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, row{"message": "Model: " + *req.ID + " deleted successfully"})
	case errors.Is(err, inference.ErrDeploymentNotFound):
		proxyError(404, "Model with id="+*req.ID+" not found in db").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// visibleDeployments 는 관리자면 전부, 아니면 쓸 수 있는 모델의 배포다(litellm 도 비관리자에게 자기 모델만 보인다)
func (h *Handlers) visibleDeployments(r *http.Request) ([]inference.Deployment, error) {
	all, err := h.deployments.All(r.Context())
	if err != nil {
		return nil, err
	}
	p := principal(r.Context())
	if p.IsAdmin() || p.Role == core.RoleProxyAdminViewer {
		return all, nil
	}
	return slices.DeleteFunc(all, func(d inference.Deployment) bool { return !inference.ModelVisible(p, d.ModelName) }), nil
}

// modelInfoV2 는 대시보드 모델 표의 공급원이다(GET /v2/model/info). 거르기·정렬·쪽 나누기를 litellm 쿼리 그대로 받는다
func (h *Handlers) modelInfoV2(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, size := positiveInt(q.Get("page"), 1), positiveInt(q.Get("size"), 50)
	deployments, err := h.visibleDeployments(r)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	deployments = slices.DeleteFunc(deployments, func(d inference.Deployment) bool { return !matchesModelFilters(d, q) })
	sortDeployments(deployments, q.Get("sortBy"), q.Get("sortOrder"))
	total := len(deployments)
	data := make([]row, 0, size)
	for _, d := range pageOf(deployments, (page-1)*size, size) {
		data = append(data, deploymentRow(d))
	}
	core.WriteJSON(w, http.StatusOK, row{"data": data, "total_count": total, "current_page": page,
		"total_pages": (total + size - 1) / size, "size": size})
}

// modelInfoV1 은 GET /v1/model/info 다. litellm_model_id 가 있으면 그 배포 하나만
func (h *Handlers) modelInfoV1(w http.ResponseWriter, r *http.Request) {
	deployments, err := h.visibleDeployments(r)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	id := r.URL.Query().Get("litellm_model_id")
	data := []row{}
	for _, d := range deployments {
		if id == "" || d.ID == id {
			data = append(data, deploymentRow(d))
		}
	}
	core.WriteJSON(w, http.StatusOK, row{"data": data})
}

func matchesModelFilters(d inference.Deployment, q map[string][]string) bool {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	if s := strings.ToLower(get("search")); s != "" && !strings.Contains(strings.ToLower(d.ModelName), s) {
		return false
	}
	if m := get("model"); m != "" && d.ModelName != m {
		return false
	}
	if id := get("modelId"); id != "" && d.ID != id {
		return false
	}
	if b := get("blocked"); b != "" && (b == "true") == d.Active {
		return false
	}
	if get("wildcard_only") == "true" && !strings.Contains(d.ModelName, "*") {
		return false
	}
	if g := get("access_group"); g != "" {
		groups, _ := d.ExtraInfo["access_groups"].([]any)
		if !slices.Contains(groups, any(g)) {
			return false
		}
	}
	return true
}

// sortDeployments 는 litellm sortBy(model_name·created_at·updated_at·costs·status)다. 모르는 값이면 저장 순서를 둔다
func sortDeployments(ds []inference.Deployment, by, order string) {
	var compare func(a, b inference.Deployment) int
	switch by {
	case "model_name":
		compare = func(a, b inference.Deployment) int { return cmp.Compare(a.ModelName, b.ModelName) }
	case "created_at":
		compare = func(a, b inference.Deployment) int { return a.CreatedAt.Compare(b.CreatedAt) }
	case "updated_at":
		compare = func(a, b inference.Deployment) int { return a.UpdatedAt.Compare(b.UpdatedAt) }
	case "costs":
		cost := func(d inference.Deployment) float64 {
			return derefFloat(d.InputCostPerToken) + derefFloat(d.OutputCostPerToken)
		}
		compare = func(a, b inference.Deployment) int { return cmp.Compare(cost(a), cost(b)) }
	case "status":
		compare = func(a, b inference.Deployment) int {
			return cmp.Compare(strconv.FormatBool(!a.Active), strconv.FormatBool(!b.Active))
		}
	default:
		return
	}
	slices.SortStableFunc(ds, func(a, b inference.Deployment) int {
		if order == "desc" {
			return compare(b, a)
		}
		return compare(a, b)
	})
}

func derefFloat(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func positiveInt(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

// deploymentRow 는 litellm 배포 한 행 모양이다. 저장해 둔 나머지 키 위에 우리 값을 얹는다
func deploymentRow(d inference.Deployment) row {
	params := row{}
	maps.Copy(params, d.ExtraParams)
	maps.Copy(params, row{"model": d.Provider + "/" + d.UpstreamModel, "api_base": d.BaseURL, "weight": d.Weight})
	info := row{}
	maps.Copy(info, d.ExtraInfo)
	maps.Copy(info, row{
		"id":                    d.ID,
		"db_model":              true,
		"blocked":               !d.Active,
		"litellm_provider":      d.Provider,
		"direct_access":         true,
		"access_via_team_ids":   []string{},
		"max_input_tokens":      d.MaxInputTokens,
		"max_output_tokens":     d.MaxOutputTokens,
		"max_tokens":            d.MaxOutputTokens,
		"supports_reasoning":    d.SupportsReasoning,
		"input_cost_per_token":  d.InputCostPerToken,
		"output_cost_per_token": d.OutputCostPerToken,
		"created_at":            d.CreatedAt.UTC(),
		"updated_at":            d.UpdatedAt.UTC(),
	})
	return row{"model_name": d.ModelName, "litellm_params": params, "model_info": info}
}
