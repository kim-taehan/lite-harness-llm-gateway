package admin

import (
	"errors"
	"net/http"
	"strings"

	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// 프로바이더 예산 관리 API. litellm 은 설정 파일(router_settings.provider_budget_config)에 적고 조회만 있는데(GET /provider/budgets),
// 우리는 설정 파일이 없어 쓰기 API 를 더했다. 프로바이더는 모델 배포의 provider 이름이다.
// 예산을 넘으면 그 프로바이더의 배포를 후보에서 빼고, 같은 모델의 다른 배포가 없으면 429 다(추론 경로)

// providerBudgetRow 는 litellm ProviderBudgetResponseObject 다
func providerBudgetRow(p limits.Policy) row {
	return row{"budget_limit": p.MaxBudget, "time_period": p.BudgetDuration, "spend": p.Spend, "budget_reset_at": utc(p.BudgetResetAt)}
}

func (h *Handlers) listProviderBudgets(w http.ResponseWriter, r *http.Request) {
	policies, err := h.limits.FindAll(r.Context(), limits.ScopeProvider)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	providers := row{}
	for name, p := range policies {
		if p.MaxBudget != nil {
			providers[name] = providerBudgetRow(p)
		}
	}
	core.WriteJSON(w, http.StatusOK, row{"providers": providers})
}

func (h *Handlers) setProviderBudget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    *string             `json:"provider"`
		BudgetLimit *float64            `json:"budget_limit"`
		TimePeriod  core.Change[string] `json:"time_period"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	var errs validationError
	if req.Provider == nil || strings.TrimSpace(*req.Provider) == "" {
		errs = append(errs, missing(raw, "provider"))
	}
	if req.BudgetLimit == nil {
		errs = append(errs, missing(raw, "budget_limit"))
	}
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	policy, err := h.limits.Apply(r.Context(), limits.ScopeProvider, *req.Provider,
		limits.Patch{MaxBudget: core.To(*req.BudgetLimit), BudgetDuration: req.TimePeriod})
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, row{"message": "Provider budget for " + *req.Provider + " set successfully",
			"provider": *req.Provider, "budget": providerBudgetRow(policy)})
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) deleteProviderBudget(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	found, err := h.limits.Find(r.Context(), limits.ScopeProvider, []string{name})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if _, ok := found[name]; !ok {
		core.DetailError{Status: http.StatusNotFound, Detail: "Provider budget for " + name + " not found"}.Write(w)
		return
	}
	if err := h.limits.Delete(r.Context(), limits.ScopeProvider, []string{name}); err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"message": "Provider budget for " + name + " deleted successfully"})
}
