package inference

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func params(extra map[string]any) map[string]any {
	p := map[string]any{"guardrail": guardrailProvider, "mode": "pre_call"}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func mustParse(t *testing.T, extra map[string]any) *Guardrail {
	t.Helper()
	g, err := ParseGuardrail("id1", "test", params(extra))
	if err != nil {
		t.Fatalf("설정을 받지 못했다: %v", err)
	}
	return g
}

func prebuilt(name, action string) map[string]any {
	return map[string]any{"pattern_type": "prebuilt", "pattern_name": name, "action": action}
}

func TestParseGuardrailRejectsBadConfig(t *testing.T) {
	cases := map[string]map[string]any{
		"패턴·금지어 없음":    {},
		"없는 기본 패턴":     {"patterns": []any{prebuilt("nope", "MASK")}},
		"깨진 정규식":       {"patterns": []any{map[string]any{"pattern": "([", "name": "x"}}},
		"알 수 없는 동작":    {"patterns": []any{prebuilt("email", "DROP")}},
		"빈 금지어":        {"blocked_words": []any{map[string]any{"keyword": "  "}}},
		"카테고리는 지원 안 함": {"patterns": []any{prebuilt("email", "MASK")}, "categories": []any{map[string]any{"category": "harm"}}},
		"지원하지 않는 시점":   {"patterns": []any{prebuilt("email", "MASK")}, "mode": "logging_only"},
		"너무 긴 정규식":     {"patterns": []any{map[string]any{"pattern": strings.Repeat("a", maxRegexLength+1)}}},
	}
	for name, extra := range cases {
		_, err := ParseGuardrail("id", "n", params(extra))
		var cfg ConfigError
		if !errors.As(err, &cfg) {
			t.Errorf("%s: ConfigError 를 기대했는데 %v", name, err)
		}
	}
	if _, err := ParseGuardrail("id", "n", map[string]any{"guardrail": "presidio", "mode": "pre_call"}); err == nil {
		t.Error("다른 가드레일 종류를 받았다")
	}
	if _, err := ParseGuardrail("id", "", params(map[string]any{"patterns": []any{prebuilt("email", "MASK")}})); err == nil {
		t.Error("이름 없는 가드레일을 받았다")
	}
}

func TestInspectMasksAndBlocks(t *testing.T) {
	g := mustParse(t, map[string]any{
		"patterns":      []any{prebuilt("email", "MASK"), prebuilt("kr_rrn", "BLOCK")},
		"blocked_words": []any{map[string]any{"keyword": "ProjectX", "action": "BLOCK", "description": "codename"}, map[string]any{"keyword": "비밀*", "action": "MASK"}},
	})
	if out, v := g.Inspect("메일은 a@corp.local 입니다"); v != nil || out != "메일은 [EMAIL_REDACTED] 입니다" {
		t.Errorf("가리기: %q %v", out, v)
	}
	if _, v := g.Inspect("번호 900101-1234567"); v == nil || v.Kind != "pattern" || v.Name != "kr_rrn" {
		t.Errorf("패턴 차단: %v", v)
	}
	if _, v := g.Inspect("이건 PROJECTX 얘기"); v == nil || v.Kind != "keyword" || v.Name != "projectx" || v.Description != "codename" {
		t.Errorf("금지어 차단(대소문자 무시): %v", v)
	}
	if out, _ := g.Inspect("비밀번호와 비밀 정보"); out != "[KEYWORD_REDACTED]호와 [KEYWORD_REDACTED]정보" {
		t.Errorf("금지어 가리기(*는 0~1글자): %q", out)
	}
	if out, v := g.Inspect("평범한 문장"); v != nil || out != "평범한 문장" {
		t.Errorf("걸리지 않아야 한다: %q %v", out, v)
	}
}

func TestInspectChecksBlockBeforeMasking(t *testing.T) {
	// 가리기가 먼저 돌아 차단 대상까지 지워 버리면 안 된다: 이메일을 가리는 규칙이 앞에 있어도 이메일 안의 금지어는 차단된다
	g := mustParse(t, map[string]any{
		"patterns":      []any{prebuilt("email", "MASK")},
		"blocked_words": []any{map[string]any{"keyword": "secret", "action": "BLOCK"}},
	})
	if _, v := g.Inspect("write to secret@corp.local"); v == nil {
		t.Error("가리기 전의 글로 차단을 봐야 한다")
	}
}

func TestInspectCustomRegexIsCaseInsensitiveAndNamed(t *testing.T) {
	g := mustParse(t, map[string]any{"patterns": []any{map[string]any{"pattern": `srv-\d+\.corp`, "name": "internal_host", "action": "MASK"}}})
	if out, _ := g.Inspect("접속: SRV-12.CORP 로"); out != "접속: [INTERNAL_HOST_REDACTED] 로" {
		t.Errorf("%q", out)
	}
}

func TestInspectRedactionFormatIsConfigurable(t *testing.T) {
	g := mustParse(t, map[string]any{"patterns": []any{prebuilt("email", "MASK")}, "pattern_redaction_format": "<{pattern_name}>"})
	if out, _ := g.Inspect("a@b.com"); out != "<EMAIL>" {
		t.Errorf("%q", out)
	}
}

func chatBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestApplyToRequestCoversEachAPIShape(t *testing.T) {
	g := mustParse(t, map[string]any{"patterns": []any{prebuilt("email", "MASK")}})
	cases := []struct {
		name string
		api  upstreamAPI
		body string
		want string // 다시 직렬화한 본문에 들어 있어야 하는 것
	}{
		{"chat 문자열", apiChat, `{"messages":[{"role":"user","content":"a@b.com"}]}`, `"content":"[EMAIL_REDACTED]"`},
		{"chat 조각", apiChat, `{"messages":[{"role":"user","content":[{"type":"text","text":"a@b.com"}]}]}`, `"text":"[EMAIL_REDACTED]"`},
		{"responses 문자열", apiResponses, `{"input":"a@b.com"}`, `"input":"[EMAIL_REDACTED]"`},
		{"responses 목록", apiResponses, `{"input":[{"role":"user","content":[{"type":"input_text","text":"a@b.com"}]}]}`, `"text":"[EMAIL_REDACTED]"`},
		{"responses instructions", apiResponses, `{"instructions":"a@b.com","input":"hi"}`, `"instructions":"[EMAIL_REDACTED]"`},
		{"embeddings 문자열", apiEmbeddings, `{"input":"a@b.com"}`, `"input":"[EMAIL_REDACTED]"`},
		{"embeddings 목록", apiEmbeddings, `{"input":["ok","a@b.com"]}`, `"[EMAIL_REDACTED]"`},
	}
	for _, c := range cases {
		body := chatBody(t, c.body)
		if err := applyToRequest([]*Guardrail{g}, body, c.api); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		out, _ := json.Marshal(body)
		if !strings.Contains(string(out), c.want) || strings.Contains(string(out), "a@b.com") {
			t.Errorf("%s: %s", c.name, out)
		}
	}
}

func TestApplyToRequestBlocksWithLitellmShapedError(t *testing.T) {
	g := mustParse(t, map[string]any{"patterns": []any{prebuilt("email", "BLOCK")}})
	body := chatBody(t, `{"messages":[{"role":"system","content":"ok"},{"role":"user","content":"a@b.com"}]}`)
	err := applyToRequest([]*Guardrail{g}, body, apiChat)
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("BlockedError 를 기대했는데 %v", err)
	}
	pe := blocked.ProxyError()
	if pe.Status != 400 || pe.Message != "Content blocked: email pattern detected" || pe.Type != "invalid_request_error" {
		t.Errorf("%+v", pe)
	}
	if pe.Fields["pattern"] != "email" || pe.Fields["guardrail_name"] != "test" || pe.Fields["guardrail_mode"] != "pre_call" {
		t.Errorf("%v", pe.Fields)
	}
	if strings.Contains(pe.Message, "a@b.com") {
		t.Error("걸린 글의 내용이 오류에 들어갔다")
	}
}

func TestRequestModeSkipsPostOnlyGuardrail(t *testing.T) {
	g := mustParse(t, map[string]any{"mode": "post_call", "patterns": []any{prebuilt("email", "BLOCK")}})
	body := chatBody(t, `{"messages":[{"role":"user","content":"a@b.com"}]}`)
	if err := applyToRequest([]*Guardrail{g}, body, apiChat); err != nil {
		t.Errorf("post_call 가드레일이 질문을 막았다: %v", err)
	}
	during := mustParse(t, map[string]any{"mode": []any{"during_call"}, "patterns": []any{prebuilt("email", "BLOCK")}})
	if err := applyToRequest([]*Guardrail{during}, body, apiChat); err == nil {
		t.Error("during_call 은 질문을 검사해야 한다")
	}
}

func TestApplyToResponse(t *testing.T) {
	g := mustParse(t, map[string]any{"mode": []any{"post_call"}, "patterns": []any{prebuilt("email", "MASK")}})
	out, err := applyToResponse([]*Guardrail{g}, []byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"문의 a@b.com"}}],"usage":{"total_tokens":7}}`), apiChat)
	if err != nil || !strings.Contains(string(out), "[EMAIL_REDACTED]") || strings.Contains(string(out), "a@b.com") || !strings.Contains(string(out), `"total_tokens":7`) {
		t.Errorf("chat 응답: %s %v", out, err)
	}
	out, err = applyToResponse([]*Guardrail{g}, []byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"a@b.com"}]}]}`), apiResponses)
	if err != nil || strings.Contains(string(out), "a@b.com") {
		t.Errorf("responses 응답: %s %v", out, err)
	}
	clean := []byte(`{"choices":[{"message":{"content":"안전한 답"}}]}`)
	if out, _ := applyToResponse([]*Guardrail{g}, clean, apiChat); string(out) != string(clean) {
		t.Errorf("바뀐 게 없으면 받은 바이트 그대로여야 한다: %s", out)
	}
	block := mustParse(t, map[string]any{"mode": "post_call", "patterns": []any{prebuilt("email", "BLOCK")}})
	var blocked *BlockedError
	if _, err := applyToResponse([]*Guardrail{block}, []byte(`{"choices":[{"message":{"content":"a@b.com"}}]}`), apiChat); !errors.As(err, &blocked) || blocked.Mode != "post_call" {
		t.Errorf("응답 차단: %v", err)
	}
}
