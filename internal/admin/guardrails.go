package admin

import (
	"context"
	"errors"
	"maps"
	"net/http"

	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
)

// 가드레일 관리 API(대시보드 Guardrails 화면, litellm /guardrails/*). 요청·응답의 내용을 검사해 막거나 가리는 규칙이다.
// 만들기·고치기·지우기는 관리자만이고 모두 감사 기록에 남는다. 조회는 읽기 전용 관리자까지(규칙에 사내 금지어·정규식이 들어 있다).
// 대시보드가 쓰는 종류는 litellm_content_filter 하나다. litellm 은 이 화면이 일부 유료지만 우리는 막지 않는다

// guardrailBody 는 가드레일 한 개의 입력이다. 만들기·바꾸기(PUT)는 {"guardrail": 이것}, 고치기(PATCH)는 이것을 그대로 받는다
type guardrailBody struct {
	Name   core.Change[string]         `json:"guardrail_name"`
	Params core.Change[map[string]any] `json:"litellm_params"`
	Info   core.Change[map[string]any] `json:"guardrail_info"`
}

// guardrailRow 는 대시보드 표가 읽는 한 줄이다. 표는 litellm_params 가 null 이면 깨지고 default_on 을 늘 읽는다
func guardrailRow(g inference.GuardrailRecord) row {
	params := maps.Clone(g.Params)
	if _, has := params["default_on"]; !has {
		params["default_on"] = false
	}
	return row{
		"guardrail_id": g.ID, "guardrail_name": g.Name, "litellm_params": params, "guardrail_info": orEmptyMetadata(g.Info),
		"created_at": isoTime(g.CreatedAt), "updated_at": isoTime(g.UpdatedAt), "guardrail_definition_location": "db",
	}
}

// listGuardrails 는 /guardrails/list 와 /v2/guardrails/list 다. 읽기 전용 관리자 이상만 규칙을 보고, 나머지는 빈 목록이다(키 만들기 화면 등이 부른다)
func (h *Handlers) listGuardrails(w http.ResponseWriter, r *http.Request) {
	rows := []row{}
	if principal(r.Context()).CanViewAll() {
		records, err := h.guardrails.List(r.Context())
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		for _, g := range records {
			rows = append(rows, guardrailRow(g))
		}
	}
	core.WriteJSON(w, http.StatusOK, row{"guardrails": rows})
}

func (h *Handlers) guardrailInfo(w http.ResponseWriter, r *http.Request) {
	g, err := h.guardrails.Find(r.Context(), r.PathValue("guardrail_id"))
	if errors.Is(err, inference.ErrGuardrailNotFound) {
		guardrailMissing(r.PathValue("guardrail_id")).Write(w)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, guardrailRow(g))
}

func guardrailMissing(id string) core.DetailError {
	return core.DetailError{Status: http.StatusNotFound, Detail: "Guardrail with ID " + id + " not found"}
}

// writeGuardrailFailure 는 저장소가 돌려준 오류를 응답으로 바꾼다
func (h *Handlers) writeGuardrailFailure(w http.ResponseWriter, r *http.Request, id string, err error) {
	var invalid inference.ConfigError
	switch {
	case errors.As(err, &invalid):
		core.DetailError{Status: http.StatusBadRequest, Detail: "Guardrail configuration error: " + invalid.Error()}.Write(w)
	case errors.Is(err, inference.ErrGuardrailNameTaken):
		core.DetailError{Status: http.StatusConflict, Detail: "A guardrail with this name already exists"}.Write(w)
	case errors.Is(err, inference.ErrGuardrailNotFound):
		guardrailMissing(id).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// guardrailInput 은 만들기·바꾸기 본문({"guardrail": {...}})에서 이름·설정·정보를 꺼낸다
func guardrailInput(w http.ResponseWriter, r *http.Request) (name string, params, info map[string]any, ok bool) {
	var req struct {
		Guardrail *guardrailBody `json:"guardrail"`
	}
	if _, ok := requestBody(w, r, &req); !ok {
		return "", nil, nil, false
	}
	if req.Guardrail == nil || req.Guardrail.Name.Value == nil || req.Guardrail.Params.Value == nil {
		core.DetailError{Status: http.StatusBadRequest, Detail: "guardrail_name and litellm_params are required"}.Write(w)
		return "", nil, nil, false
	}
	return *req.Guardrail.Name.Value, *req.Guardrail.Params.Value, derefMap(req.Guardrail.Info.Value), true
}

func (h *Handlers) newGuardrail(w http.ResponseWriter, r *http.Request) {
	name, params, info, ok := guardrailInput(w, r)
	if !ok {
		return
	}
	g, err := h.guardrails.Create(r.Context(), name, params, info)
	if err != nil {
		h.writeGuardrailFailure(w, r, "", err)
		return
	}
	core.WriteJSON(w, http.StatusOK, guardrailRow(g))
}

// replaceGuardrail 은 PUT 이다: 이름·설정·정보를 통째로 바꾼다
func (h *Handlers) replaceGuardrail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("guardrail_id")
	name, params, info, ok := guardrailInput(w, r)
	if !ok {
		return
	}
	g, err := h.guardrails.Replace(r.Context(), id, name, params, info)
	if err != nil {
		h.writeGuardrailFailure(w, r, id, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, guardrailRow(g))
}

// patchGuardrail 은 PATCH 다(대시보드가 쓴다). 보낸 필드만 바꾸고, litellm_params 는 키별로 덮어쓴다(null 이면 그 키를 지운다)
func (h *Handlers) patchGuardrail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("guardrail_id")
	var req guardrailBody
	if _, ok := requestBody(w, r, &req); !ok {
		return
	}
	current, err := h.guardrails.Find(r.Context(), id)
	if err != nil {
		h.writeGuardrailFailure(w, r, id, err)
		return
	}
	name, params, info := current.Name, maps.Clone(current.Params), current.Info
	if req.Name.Value != nil {
		name = *req.Name.Value
	}
	for key, value := range derefMap(req.Params.Value) {
		if value == nil {
			delete(params, key)
			continue
		}
		params[key] = value
	}
	if req.Info.Set {
		info = derefMap(req.Info.Value)
	}
	g, err := h.guardrails.Replace(r.Context(), id, name, params, info)
	if err != nil {
		h.writeGuardrailFailure(w, r, id, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, guardrailRow(g))
}

func (h *Handlers) deleteGuardrail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("guardrail_id")
	if err := h.guardrails.Delete(r.Context(), id); err != nil {
		h.writeGuardrailFailure(w, r, id, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"message": "Guardrail " + id + " deleted successfully"})
}

// guardrailUISettings 는 Add Guardrail 폼이 읽는 선택지다. 내용 필터 폼은 content_filter_settings 가 없으면 아무것도 보여 주지 않는다
func (h *Handlers) guardrailUISettings(w http.ResponseWriter, _ *http.Request) {
	patterns, categories := inference.PrebuiltPatternInfos()
	prebuilt := make([]row, len(patterns))
	for i, p := range patterns {
		prebuilt[i] = row{"name": p.Name, "display_name": p.DisplayName, "category": p.Category, "description": p.Description}
	}
	modes := inference.GuardrailModes()
	core.WriteJSON(w, http.StatusOK, row{
		"supported_entities": []any{}, "supported_actions": []any{}, "pii_entity_categories": []any{}, "decision_model_providers": []any{},
		"supported_modes": modes, "supported_modes_by_provider": row{"litellm_content_filter": modes},
		"providers_without_directional_logging_only_scope": []any{},
		"content_filter_settings": row{"prebuilt_patterns": prebuilt, "pattern_categories": categories,
			"supported_actions": []string{"BLOCK", "MASK"}, "content_categories": []any{}},
	})
}

// guardrailProviderParams 는 폼의 "종류" 선택지다. 화면은 ui_friendly_name 이 정확히 이 문자열인 항목을 내용 필터로 알아본다
func (h *Handlers) guardrailProviderParams(w http.ResponseWriter, _ *http.Request) {
	core.WriteJSON(w, http.StatusOK, row{"litellm_content_filter": row{"ui_friendly_name": "LiteLLM Content Filter"}})
}

// guardrailBefore 는 감사 기록의 이전 값이다
func (h *Handlers) guardrailBefore(ctx context.Context, id string) (any, error) {
	g, err := h.guardrails.Find(ctx, id)
	if errors.Is(err, inference.ErrGuardrailNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return guardrailRow(g), nil
}
