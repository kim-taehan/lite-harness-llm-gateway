package auth

import "slices"

// 키·팀의 metadata 에 두는 가드레일 설정(litellm 과 같은 이름이다).
//   - guardrails: 이 키·팀의 요청에 적용할 가드레일 이름
//   - disable_global_guardrails: 모든 요청에 거는(default_on) 가드레일을 끈다
//   - opted_out_global_guardrails: 이 이름의 default_on 가드레일만 끈다
// 뒤의 둘은 전사 정책을 벗어나는 설정이라 관리자만 정할 수 있다(admin 쪽 검사). 요청 본문으로는 정할 수 없다

const (
	metaGuardrails    = "guardrails"
	metaDisableGlobal = "disable_global_guardrails"
	metaOptedOut      = "opted_out_global_guardrails"
)

// GuardrailBypassFields 는 관리자만 정할 수 있는 metadata 필드다
var GuardrailBypassFields = []string{metaDisableGlobal, metaOptedOut}

// withGuardrailSettings 는 키와 팀의 metadata 에서 가드레일 설정을 읽어 신원에 담는다. 끄는 설정은 키가 팀보다 우선이다
func withGuardrailSettings(p Principal, keyMeta, teamMeta map[string]any) Principal {
	p.Guardrails = slices.Compact(slices.Sorted(slices.Values(append(stringList(keyMeta[metaGuardrails]), stringList(teamMeta[metaGuardrails])...))))
	p.DisableGlobalGuardrails = settingOr(keyMeta, teamMeta, metaDisableGlobal, boolOf)
	p.OptedOutGuardrails = settingOr(keyMeta, teamMeta, metaOptedOut, stringList)
	return p
}

// settingOr 는 키 metadata 에 그 필드가 있으면 키의 값, 없으면 팀의 값이다
func settingOr[T any](keyMeta, teamMeta map[string]any, field string, read func(any) T) T {
	if v, ok := keyMeta[field]; ok {
		return read(v)
	}
	return read(teamMeta[field])
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func stringList(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
