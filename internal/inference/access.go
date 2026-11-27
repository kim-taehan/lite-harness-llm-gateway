package inference

import (
	"slices"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// AccessGroups 는 배포의 model_info.access_groups 로 만든 "그룹 이름 → 그 그룹 모델 이름들"이다(litellm 모델 액세스 그룹)
type AccessGroups map[string][]string

// AccessGroupsOf 는 배포 목록에서 액세스 그룹을 모은다
func AccessGroupsOf(deployments []Deployment) AccessGroups {
	groups := AccessGroups{}
	for _, d := range deployments {
		names, _ := d.ExtraInfo["access_groups"].([]any)
		for _, n := range names {
			if name, ok := n.(string); ok && !slices.Contains(groups[name], d.ModelName) {
				groups[name] = append(groups[name], d.ModelName)
			}
		}
	}
	return groups
}

// 모델 접근 판정(litellm AC:4809-4855 의 기본 부분): 목록이 비어 있으면 전체 허용, "*"·all-proxy-models 도 전체 허용.
// 목록에 액세스 그룹 이름이 있으면 그 그룹의 모델도 허용한다. 키 목록의 all-team-models 는 팀 목록을 따르라는 뜻이다.
// 와일드카드 패턴은 아직 없다
func (g AccessGroups) allows(models []string, model string) bool {
	if len(models) == 0 || slices.Contains(models, "*") || slices.Contains(models, "all-proxy-models") {
		return true
	}
	if slices.Contains(models, model) {
		return true
	}
	return slices.ContainsFunc(models, func(entry string) bool { return slices.Contains(g[entry], model) })
}

// modelDenied 는 키와 팀의 허용 모델을 본다. 거절이면 litellm 과 같은 403 본문이다(J 5.2: 키를 먼저, 그다음 팀)
func modelDenied(p auth.Principal, groups AccessGroups, model string) (core.ProxyError, bool) {
	keyModels := p.KeyModels
	if slices.Contains(keyModels, "all-team-models") {
		keyModels = nil
	}
	if !groups.allows(keyModels, model) {
		return accessDenied("key_model_access_denied", model), true
	}
	if p.TeamID != "" && !groups.allows(p.TeamModels, model) {
		return accessDenied("team_model_access_denied", model), true
	}
	return core.ProxyError{}, false
}

func accessDenied(kind, model string) core.ProxyError {
	return core.ProxyError{Status: 403, Type: kind, Param: "model",
		Message: "The requested model '" + model + "' is not available for this API key, or the model name is invalid. Check the models available to you and try again."}
}

// ModelVisible 은 이 신원이 그 모델 이름을 쓸 수 있는지다(관리 화면의 모델 목록도 같은 기준으로 거른다)
func ModelVisible(p auth.Principal, groups AccessGroups, model string) bool {
	_, denied := modelDenied(p, groups, model)
	return !denied
}

// visible 은 이 신원이 쓸 수 있는 모델 이름이다(/v1/models)
func visible(p auth.Principal, groups AccessGroups, names []string) []string {
	var result []string
	for _, name := range names {
		if _, denied := modelDenied(p, groups, name); !denied {
			result = append(result, name)
		}
	}
	return result
}
