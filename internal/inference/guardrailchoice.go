package inference

import (
	"llm-gateway/internal/auth"
)

// 요청 하나가 이름으로 더할 수 있는 가드레일 수의 상한이다. 이름마다 저장된 가드레일과 맞춰 보므로 본문이 이름을 잔뜩 실어 보내지 못하게 한다
const maxRequestedGuardrails = 100

// guardrailChoice 는 호출자의 키·팀 설정과 요청 본문이 원하는 가드레일이다.
// 요청 본문은 가드레일을 더할 수만 있고, 전사 정책을 끄는 것(disable_global_guardrails 등)은 키·팀 설정(관리자가 정한다)에서만 읽는다.
// 본문의 guardrails 는 업스트림에 보내지 않도록 지운다
func guardrailChoice(p auth.Principal, body map[string]any) GuardrailChoice {
	requested := append([]string(nil), p.Guardrails...)
	sources := []any{body["guardrails"], objectOf(body["metadata"])["guardrails"], objectOf(body["litellm_metadata"])["guardrails"]}
	delete(body, "guardrails")
	for _, source := range sources {
		requested = appendGuardrailNames(requested, source)
	}
	if len(requested) > maxRequestedGuardrails {
		requested = requested[:maxRequestedGuardrails]
	}
	return GuardrailChoice{Requested: requested, DisableGlobal: p.DisableGlobalGuardrails, OptedOut: p.OptedOutGuardrails}
}

// appendGuardrailNames 는 이름의 목록을 더한다. 항목은 문자열이거나 {이름: 설정} 객체다(litellm 도 둘 다 받는다. 설정은 쓰지 않는다)
func appendGuardrailNames(names []string, source any) []string {
	list, _ := source.([]any)
	for _, item := range list {
		switch v := item.(type) {
		case string:
			names = append(names, v)
		case map[string]any:
			for name := range v {
				names = append(names, name)
			}
		}
		if len(names) > maxRequestedGuardrails {
			return names
		}
	}
	return names
}
