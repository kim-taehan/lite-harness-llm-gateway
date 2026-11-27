package admin

import (
	"context"
	"errors"
	"net/http"

	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// 고객(end user) 관리 API(litellm /customer/*). 고객은 요청의 user 값으로 식별하는 최종 사용자이고, 예산·RPM·TPM·TPD·모델별 한도는
// 키·팀과 같은 필드(max_budget, budget_duration, …)로 정한다. 관리자만 부를 수 있다.
// 대시보드는 이 API 를 쓰지 않아(목록만 빈 배열로 부른다) 응답 모양은 litellm 의 주요 필드만 맞췄다

type customerRequest struct {
	UserID  *string             `json:"user_id"`
	Alias   core.Change[string] `json:"alias"`
	Blocked core.Change[bool]   `json:"blocked"`
	limitFields
}

func customerRow(c limits.Customer, p limits.Policy) row {
	return row{
		"user_id": c.UserID, "alias": c.Alias, "blocked": c.Blocked, "spend": p.Spend,
		"allowed_model_region": nil, "default_model": nil, "budget_id": nil,
		"litellm_budget_table": row{
			"max_budget": p.MaxBudget, "soft_budget": p.SoftBudget, "tpm_limit": p.TPMLimit, "rpm_limit": p.RPMLimit, "tpd_limit": p.TPDLimit,
			"max_parallel_requests": p.MaxParallelRequests, "budget_duration": p.BudgetDuration, "budget_reset_at": utc(p.BudgetResetAt),
			"model_max_budget": modelBudgetsView(p.ModelBudgets),
		},
	}
}

func customerNotFound(id string) core.ProxyError {
	return proxyError(400, "End User Id="+id+" does not exist in db")
}

// customerRows 는 고객들의 응답 행이다(한도는 한 번에 읽는다)
func (h *Handlers) customerRows(ctx context.Context, customers []limits.Customer) ([]row, error) {
	ids := make([]string, len(customers))
	for i, c := range customers {
		ids[i] = c.UserID
	}
	policies, err := h.limits.Find(ctx, limits.ScopeEndUser, ids)
	if err != nil {
		return nil, err
	}
	result := make([]row, len(customers))
	for i, c := range customers {
		result[i] = customerRow(c, policies[c.UserID])
	}
	return result, nil
}

func (h *Handlers) newCustomer(w http.ResponseWriter, r *http.Request) {
	var req customerRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.UserID == nil {
		validationError{missing(raw, "user_id")}.write(w)
		return
	}
	var created limits.Customer
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if created, err = h.customers.Create(ctx, *req.UserID, req.Alias.Value, req.Blocked.Value != nil && *req.Blocked.Value); err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeEndUser, created.UserID, req.patch())
		return err
	})
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		h.writeCustomer(w, r, created)
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.Is(err, limits.ErrCustomerExists):
		proxyError(400, "Customer with user_id="+*req.UserID+" already exists").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) updateCustomer(w http.ResponseWriter, r *http.Request) {
	var req customerRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.UserID == nil {
		validationError{missing(raw, "user_id")}.write(w)
		return
	}
	var updated limits.Customer
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if updated, err = h.customers.Update(ctx, *req.UserID, req.Alias, req.Blocked); err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeEndUser, updated.UserID, req.patch())
		return err
	})
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		h.writeCustomer(w, r, updated)
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.Is(err, limits.ErrCustomerNotFound):
		customerNotFound(*req.UserID).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) writeCustomer(w http.ResponseWriter, r *http.Request, c limits.Customer) {
	rows, err := h.customerRows(r.Context(), []limits.Customer{c})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, rows[0])
}

func (h *Handlers) customerInfo(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("end_user_id")
	if id == "" {
		validationError{{Type: "missing", Loc: []any{"query", "end_user_id"}, Msg: "Field required", Input: nil}}.write(w)
		return
	}
	c, err := h.customers.Find(r.Context(), id)
	switch {
	case err == nil:
		h.writeCustomer(w, r, c)
	case errors.Is(err, limits.ErrCustomerNotFound):
		customerNotFound(id).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) listCustomers(w http.ResponseWriter, r *http.Request) {
	customers, err := h.customers.List(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	rows, err := h.customerRows(r.Context(), customers)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if rows == nil {
		rows = []row{}
	}
	core.WriteJSON(w, http.StatusOK, rows)
}

// userIDs 는 {"user_ids": [...]} 본문이다. 없으면 FastAPI 와 같은 422 를 쓴다
func (h *Handlers) userIDs(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var req struct {
		UserIDs *[]string `json:"user_ids"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return nil, false
	}
	if req.UserIDs == nil {
		validationError{missing(raw, "user_ids")}.write(w)
		return nil, false
	}
	return *req.UserIDs, true
}

func (h *Handlers) blockCustomers(blocked bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids, ok := h.userIDs(w, r)
		if !ok {
			return
		}
		var result []limits.Customer
		err := h.db.InTx(r.Context(), func(ctx context.Context) error {
			changed, err := h.customers.SetBlocked(ctx, ids, blocked)
			result = changed
			if err == nil && !blocked {
				result, err = h.customers.Blocked(ctx) // litellm 은 풀고 난 뒤 아직 막힌 고객을 돌려준다
			}
			return err
		})
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		rows := make([]row, len(result))
		for i, c := range result {
			rows[i] = row{"user_id": c.UserID, "alias": c.Alias, "blocked": c.Blocked}
		}
		core.WriteJSON(w, http.StatusOK, row{"blocked_users": rows})
	}
}

func (h *Handlers) deleteCustomers(w http.ResponseWriter, r *http.Request) {
	ids, ok := h.userIDs(w, r)
	if !ok {
		return
	}
	var deleted int
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		deleted, err = h.customers.Delete(ctx, ids)
		return err
	})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"deleted_customers": deleted, "message": "Successfully deleted customer(s)"})
}
