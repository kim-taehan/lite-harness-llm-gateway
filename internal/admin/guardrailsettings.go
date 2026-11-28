package admin

import (
	"maps"
	"reflect"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// 키·팀에 붙이는 가드레일 설정(metadata 의 guardrails·disable_global_guardrails·opted_out_global_guardrails).
// 대시보드는 키·팀을 만들 때 guardrails 와 disable_global_guardrails 를 본문 맨 위에 보낸다(litellm 이 metadata 로 옮긴다).
// guardrails 는 누구나 더할 수 있다(검사가 늘 뿐이다). 전사 정책을 끄는 두 설정은 관리자만 정할 수 있다: 그렇지 않으면
// 키 소유자나 팀 관리자가 자기 키의 metadata 에 적어 정책을 벗어난다

// activeGuardrailBypass 는 metadata 에서 지금 켜진 "끄는 설정"만 고른다(false·빈 목록은 없는 것과 같다)
func activeGuardrailBypass(metadata map[string]any) map[string]any {
	active := map[string]any{}
	for _, field := range auth.GuardrailBypassFields {
		switch v := metadata[field].(type) {
		case bool:
			if v {
				active[field] = v
			}
		case []any:
			if len(v) > 0 {
				active[field] = v
			}
		}
	}
	return active
}

// guardrailBypassWidened 는 next 가 current 에 없던 "끄는 설정"을 켜거나 바꾸는지다. 끄는 설정을 없애는 것은 정책을 조이는 일이라 허용한다
func guardrailBypassWidened(next, current map[string]any) bool {
	wanted := activeGuardrailBypass(next)
	return len(wanted) > 0 && !reflect.DeepEqual(wanted, activeGuardrailBypass(current))
}

const guardrailBypassDenied = "Only proxy admins can disable or opt out of global guardrails (disable_global_guardrails, opted_out_global_guardrails)."

// prepareGuardrailMetadata 는 본문 맨 위의 guardrails·disable_global_guardrails 를 metadata 에 합치고, 관리자가 아니면
// 끄는 설정을 새로 켜는 것을 거절한다(거절 문구를 돌려준다). current 는 저장된 metadata(새로 만들 때는 nil)다.
// 합칠 것도 보낸 metadata 도 없으면 meta 를 그대로 돌려준다
func prepareGuardrailMetadata(p auth.Principal, current map[string]any, meta core.Change[map[string]any],
	names core.Change[[]string], disable core.Change[bool]) (core.Change[map[string]any], string) {
	if !meta.Set && !names.Set && !disable.Set {
		return meta, ""
	}
	base := maps.Clone(current)
	if meta.Set {
		base = maps.Clone(derefMap(meta.Value))
	}
	if base == nil {
		base = map[string]any{}
	}
	if names.Set {
		if list := derefSlice(names.Value); len(list) > 0 {
			base["guardrails"] = anyStrings(list)
		} else {
			delete(base, "guardrails")
		}
	}
	if disable.Set {
		if derefBool(disable.Value) {
			base["disable_global_guardrails"] = true
		} else {
			delete(base, "disable_global_guardrails")
		}
	}
	if !p.IsAdmin() && guardrailBypassWidened(base, current) {
		return meta, guardrailBypassDenied
	}
	return core.To(base), ""
}

func anyStrings(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}
