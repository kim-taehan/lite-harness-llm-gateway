package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// 태그 관리 API(대시보드 태그 관리 화면, litellm /tag/*). 태그에 예산·RPM·TPM·TPD 를 정하면 요청이 그 태그를 붙였을 때 걸린다.
// 관리자만 부를 수 있다. litellm 과 다른 점: 태그 목록이 litellm 원본은 배열인데 대시보드 타입(이름→태그 사전)을 따라 객체로 준다.
// 태그의 models 는 저장만 하고 태그 기반 라우팅·접근 판정에는 쓰지 않는다

type tagRequest struct {
	Name        *string               `json:"name"`
	Description core.Change[string]   `json:"description"`
	Models      core.Change[[]string] `json:"models"`
	limitFields
}

func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.999999") }

func tagRow(t limits.Tag, p limits.Policy) row {
	r := row{
		"name": t.Name, "description": t.Description, "models": nonNil(t.Models), "model_info": row{},
		"created_at": isoTime(t.CreatedAt), "updated_at": isoTime(t.UpdatedAt), "created_by": t.CreatedBy, "updated_by": t.UpdatedBy,
	}
	if p.HasLimits() {
		r["litellm_budget_table"] = row{
			"max_budget": p.MaxBudget, "soft_budget": p.SoftBudget, "tpm_limit": p.TPMLimit, "rpm_limit": p.RPMLimit, "tpd_limit": p.TPDLimit,
			"max_parallel_requests": p.MaxParallelRequests, "budget_duration": p.BudgetDuration,
			"model_max_budget": modelBudgetsView(p.ModelBudgets),
		}
	}
	return r
}

// tagRows 는 태그들의 응답 행이다(이름 → 행). 한도는 한 번에 읽는다
func (h *Handlers) tagRows(ctx context.Context, tags []limits.Tag) (map[string]row, error) {
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	policies, err := h.limits.Find(ctx, limits.ScopeTag, names)
	if err != nil {
		return nil, err
	}
	result := make(map[string]row, len(tags))
	for _, t := range tags {
		result[t.Name] = tagRow(t, policies[t.Name])
	}
	return result, nil
}

func (h *Handlers) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.tags.List(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	rows, err := h.tagRows(r.Context(), tags)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, rows)
}

func (h *Handlers) newTag(w http.ResponseWriter, r *http.Request) {
	req, name, ok := h.tagBody(w, r)
	if !ok {
		return
	}
	var created limits.Tag
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if created, err = h.tags.Create(ctx, name, req.Description.Value, req.Models.Or(nil), principal(ctx).UserID); err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeTag, name, req.patch())
		return err
	})
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		h.writeTag(w, r, "Tag "+name+" created successfully", created)
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.Is(err, limits.ErrTagExists):
		core.DetailError{Status: http.StatusBadRequest, Detail: "Tag " + name + " already exists"}.Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handlers) updateTag(w http.ResponseWriter, r *http.Request) {
	req, name, ok := h.tagBody(w, r)
	if !ok {
		return
	}
	var updated limits.Tag
	err := h.db.InTx(r.Context(), func(ctx context.Context) error {
		var err error
		if updated, err = h.tags.Update(ctx, name, req.Description, req.Models, principal(ctx).UserID); err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeTag, name, req.patch())
		return err
	})
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		h.writeTag(w, r, "Tag "+name+" updated successfully", updated)
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.Is(err, limits.ErrTagNotFound):
		core.DetailError{Status: http.StatusNotFound, Detail: "Tag " + name + " not found"}.Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// tagBody 는 생성·수정 본문과 태그 이름이다. 이름이 없거나 비면 FastAPI 와 같은 422 를 쓴다
func (h *Handlers) tagBody(w http.ResponseWriter, r *http.Request) (tagRequest, string, bool) {
	var req tagRequest
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return req, "", false
	}
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		validationError{missing(raw, "name")}.write(w)
		return req, "", false
	}
	return req, *req.Name, true
}

func (h *Handlers) writeTag(w http.ResponseWriter, r *http.Request, message string, t limits.Tag) {
	rows, err := h.tagRows(r.Context(), []limits.Tag{t})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"message": message, "tag": rows[t.Name]})
}

func (h *Handlers) tagInfo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names *[]string `json:"names"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.Names == nil {
		validationError{missing(raw, "names")}.write(w)
		return
	}
	found, err := h.tags.Find(r.Context(), *req.Names)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	var missingNames []string
	tags := make([]limits.Tag, 0, len(found))
	for _, name := range *req.Names {
		if t, ok := found[name]; ok {
			tags = append(tags, t)
		} else {
			missingNames = append(missingNames, name)
		}
	}
	if len(missingNames) > 0 {
		core.DetailError{Status: http.StatusNotFound, Detail: "Tags not found: " + pyList(missingNames)}.Write(w)
		return
	}
	rows, err := h.tagRows(r.Context(), tags)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, rows)
}

func (h *Handlers) deleteTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name *string `json:"name"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.Name == nil {
		validationError{missing(raw, "name")}.write(w)
		return
	}
	err := h.db.InTx(r.Context(), func(ctx context.Context) error { return h.tags.Delete(ctx, *req.Name) })
	switch {
	case err == nil:
		core.WriteJSON(w, http.StatusOK, row{"message": "Tag " + *req.Name + " deleted successfully"})
	case errors.Is(err, limits.ErrTagNotFound):
		core.DetailError{Status: http.StatusNotFound, Detail: "Tag " + *req.Name + " not found"}.Write(w)
	default:
		h.internalError(w, r, err)
	}
}
