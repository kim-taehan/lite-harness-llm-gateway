package admin

import (
	"embed"
	"encoding/json"
	"maps"
	"net/http"
	"slices"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// 대시보드 설정 화면(L 2.4·2.5, I 4.1·4.2). 기본값과 field_schema 는 litellm 이 빈 설정에서 내는 응답을 그대로 뽑아 둔 것이다
// (dashboarddata/*_settings.json, litellm _get_settings_with_schema). 관리자가 바꾼 값만 core.Settings 에 저장한다.
// 대시보드는 field_schema 의 설명으로 관리자 패널 입력란을 그리므로 스키마를 비우지 않는다

//go:embed dashboarddata
var dashboardData embed.FS

// settingsForm 은 설정 묶음 하나다
type settingsForm struct {
	name          string // core.Settings 이름(litellm litellm_settings 의 키)
	updateMessage string
	defaults      map[string]any
	schema        map[string]any
}

func loadSettingsForm(name, file, updateMessage string) settingsForm {
	data, err := dashboardData.ReadFile("dashboarddata/" + file)
	if err != nil {
		panic(err) // 내장 파일이라 일어나지 않는다
	}
	var doc struct {
		Values      map[string]any `json:"values"`
		FieldSchema map[string]any `json:"field_schema"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		panic(err)
	}
	return settingsForm{name: name, updateMessage: updateMessage, defaults: doc.Values, schema: doc.FieldSchema}
}

var (
	uiSettingsForm   = loadSettingsForm("ui_settings", "ui_settings.json", "UI settings updated successfully")
	uiThemeForm      = loadSettingsForm("ui_theme_config", "ui_theme_settings.json", "UI theme settings updated successfully")
	defaultTeamForm  = loadSettingsForm("default_team_params", "default_team_settings.json", "Default team settings updated successfully")
	internalUserForm = loadSettingsForm(auth.DefaultUserParamsSetting, "internal_user_settings.json", "Internal user settings updated successfully")
)

// settingsValues 는 기본값 위에 저장값을 얹는다. 스키마에 없는 키는 내지 않는다(litellm 허용 목록과 같다)
func (h *Handlers) settingsValues(r *http.Request, form settingsForm) (map[string]any, map[string]any, error) {
	stored, err := h.settings.Get(r.Context(), form.name)
	if err != nil {
		return nil, nil, err
	}
	values := maps.Clone(form.defaults)
	for k, v := range stored {
		if _, known := form.defaults[k]; known {
			values[k] = v
		}
	}
	return values, stored, nil
}

func (h *Handlers) getSettings(form settingsForm) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values, stored, err := h.settingsValues(r, form)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		body := row{"values": values, "field_schema": form.schema}
		if form.name == uiSettingsForm.name {
			// litellm 은 환경에서 정해지는 두 값을 덧붙이고, 값마다 출처를 준다
			values["enable_ptu_cost_attribution"], values["apply_user_budget_to_team_keys"] = false, false
			source := row{}
			for k := range values {
				source[k] = "default"
				if _, ok := stored[k]; ok {
					source[k] = "db"
				}
			}
			body["source"] = source
		}
		core.WriteJSON(w, http.StatusOK, body)
	}
}

// updateSettings 는 보낸 키만 바꾼다. 스키마에 없는 키는 litellm 처럼 받지 않는다
func (h *Handlers) updateSettings(form settingsForm) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var changes map[string]any
		if _, ok := requestBody(w, r, &changes); !ok {
			return
		}
		for k, v := range changes {
			if _, known := form.defaults[k]; !known {
				detailError(http.StatusBadRequest, "Unknown setting: "+k).Write(w)
				return
			}
			if role, isRole := v.(string); k == "user_role" && (!isRole || !slices.Contains(assignableRoles, core.Role(role))) {
				validationError{{Type: "literal_error", Loc: []any{"body", "user_role"},
					Msg: "Input should be 'proxy_admin', 'proxy_admin_viewer', 'internal_user' or 'internal_user_viewer'", Input: v}}.write(w)
				return
			}
		}
		if _, err := h.settings.Merge(r.Context(), form.name, changes); err != nil {
			h.internalError(w, r, err)
			return
		}
		values, _, err := h.settingsValues(r, form)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		settings := row{}
		for k, v := range values {
			if v != nil { // litellm 은 exclude_none 으로 돌려준다
				settings[k] = v
			}
		}
		core.WriteJSON(w, http.StatusOK, row{"message": form.updateMessage, "status": "success", "settings": settings})
	}
}

// serveEmbedded 는 내장 파일 하나를 그대로 준다.
//   - logo.png: 테마에 로고·파비콘을 정하지 않았을 때의 기본 그림(litellm 로고를 쓰지 않는다)
//   - provider_fields.json: 모델 추가 폼의 제공자 목록. litellm provider_create_fields.json 에서 우리가 중계하는
//     제공자(Ollama, Ollama Chat, Hosted vLLM)만 뽑았다
//   - model_cost_map.json: litellm 동봉 가격표에서 같은 제공자 항목만 뽑았다(폐쇄망이라 원격 가격표를 읽지 않는다)
func serveEmbedded(file, contentType string) http.HandlerFunc {
	data, err := dashboardData.ReadFile("dashboarddata/" + file)
	if err != nil {
		panic(err) // 내장 파일이라 일어나지 않는다
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(data) // 쓰기 실패는 클라이언트가 끊은 것이다
	}
}
