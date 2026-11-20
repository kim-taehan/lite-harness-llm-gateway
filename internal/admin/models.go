package admin

import (
	"errors"
	"net/http"
	"strings"

	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
)

// litellm 모델 관리 API 중 배포 등록·삭제만 먼저 둔다(데스크탑 연결을 위해). /model/info·/model/update 등은 뒤에 litellm 계약을 조사해 붙인다.
// 요청 모양은 litellm config 의 model_list 원소와 같다: {"model_name", "litellm_params": {"model": "ollama/<tag>", "api_base"}, "model_info": {...}}

func (h *Handlers) createModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelName     *string `json:"model_name"`
		LitellmParams struct {
			Model   string `json:"model"`
			APIBase string `json:"api_base"`
			Weight  int    `json:"weight"`
		} `json:"litellm_params"`
		ModelInfo struct {
			ID                 *string  `json:"id"`
			MaxInputTokens     *int64   `json:"max_input_tokens"`
			MaxOutputTokens    *int64   `json:"max_output_tokens"`
			SupportsReasoning  bool     `json:"supports_reasoning"`
			InputCostPerToken  *float64 `json:"input_cost_per_token"`
			OutputCostPerToken *float64 `json:"output_cost_per_token"`
		} `json:"model_info"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	var errs validationError
	if req.ModelName == nil || *req.ModelName == "" {
		errs = append(errs, missing(raw, "model_name"))
	}
	provider, upstream, found := strings.Cut(req.LitellmParams.Model, "/")
	if !found || upstream == "" {
		errs = append(errs, FieldError{Type: "value_error", Loc: []any{"body", "litellm_params", "model"},
			Msg: "Value error, model must look like '<provider>/<model>' (e.g. ollama/llama3)", Input: req.LitellmParams.Model})
	}
	if req.LitellmParams.APIBase == "" {
		errs = append(errs, missing(raw, "api_base"))
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	if provider == "ollama_chat" {
		provider = "ollama"
	}
	info := req.ModelInfo
	d, err := h.deployments.Create(r.Context(), inference.NewDeployment{
		ID: info.ID, ModelName: *req.ModelName, Provider: provider, BaseURL: req.LitellmParams.APIBase, UpstreamModel: upstream,
		Weight: req.LitellmParams.Weight, MaxInputTokens: info.MaxInputTokens, MaxOutputTokens: info.MaxOutputTokens,
		SupportsReasoning: info.SupportsReasoning, InputCostPerToken: info.InputCostPerToken, OutputCostPerToken: info.OutputCostPerToken,
	})
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, deploymentRow(d))
	case errors.Is(err, inference.ErrDeploymentIDTaken):
		proxyError(400, "Model with id "+derefOr(info.ID, "")+" already exists").Write(w)
	default:
		h.internalError(w, r, err)
	}
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

func deploymentRow(d inference.Deployment) row {
	return row{
		"model_name": d.ModelName,
		"litellm_params": row{
			"model":    d.Provider + "/" + d.UpstreamModel,
			"api_base": d.BaseURL,
			"weight":   d.Weight,
		},
		"model_info": row{
			"id":                    d.ID,
			"db_model":              true,
			"max_input_tokens":      d.MaxInputTokens,
			"max_output_tokens":     d.MaxOutputTokens,
			"supports_reasoning":    d.SupportsReasoning,
			"input_cost_per_token":  d.InputCostPerToken,
			"output_cost_per_token": d.OutputCostPerToken,
			"created_at":            d.CreatedAt.UTC(),
			"updated_at":            d.UpdatedAt.UTC(),
		},
	}
}
