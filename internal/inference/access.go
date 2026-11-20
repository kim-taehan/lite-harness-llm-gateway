package inference

import (
	"slices"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// 모델 접근 판정(litellm AC:4809-4855 의 기본 부분): 목록이 비어 있으면 전체 허용, "*"·all-proxy-models 도 전체 허용.
// 키 목록의 all-team-models 는 팀 목록을 따르라는 뜻이다. 와일드카드 패턴·액세스 그룹은 아직 없다
func allows(models []string, model string) bool {
	if len(models) == 0 || slices.Contains(models, "*") || slices.Contains(models, "all-proxy-models") {
		return true
	}
	return slices.Contains(models, model)
}

// modelDenied 는 키와 팀의 허용 모델을 본다. 거절이면 litellm 과 같은 403 본문이다(J 5.2: 키를 먼저, 그다음 팀)
func modelDenied(p auth.Principal, model string) (core.ProxyError, bool) {
	keyModels := p.KeyModels
	if slices.Contains(keyModels, "all-team-models") {
		keyModels = nil
	}
	if !allows(keyModels, model) {
		return accessDenied("key_model_access_denied", model), true
	}
	if p.TeamID != "" && !allows(p.TeamModels, model) {
		return accessDenied("team_model_access_denied", model), true
	}
	return core.ProxyError{}, false
}

func accessDenied(kind, model string) core.ProxyError {
	return core.ProxyError{Status: 403, Type: kind, Param: "model",
		Message: "The requested model '" + model + "' is not available for this API key, or the model name is invalid. Check the models available to you and try again."}
}

// visible 은 이 신원이 쓸 수 있는 모델 이름이다(/v1/models)
func visible(p auth.Principal, names []string) []string {
	var result []string
	for _, name := range names {
		if _, denied := modelDenied(p, name); !denied {
			result = append(result, name)
		}
	}
	return result
}
