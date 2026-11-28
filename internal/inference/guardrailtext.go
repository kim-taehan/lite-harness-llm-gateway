package inference

import (
	"bytes"
	"encoding/json"

	"llm-gateway/internal/core"
)

// 요청·응답 본문에서 사람이 쓴(또는 모델이 쓴) 글을 찾아 검사하고 가린 글로 바꿔 넣는다. API 마다 글이 있는 자리가 다르다

// textFunc 는 글 하나를 받아 (바꾼 글, 오류)를 돌려준다. 오류가 나면 거기서 멈춘다(차단)
type textFunc func(string) (string, error)

// rewriteRequestTexts 는 요청 본문의 글에 fn 을 적용한다. 바뀐 글은 body 에 바로 쓴다
func rewriteRequestTexts(body map[string]any, api upstreamAPI, fn textFunc) error {
	switch api.path {
	case apiChat.path:
		messages, _ := body["messages"].([]any)
		for _, m := range messages {
			if msg, ok := m.(map[string]any); ok {
				if err := rewriteContent(msg, "content", fn); err != nil {
					return err
				}
			}
		}
	case apiResponses.path:
		if err := rewriteString(body, "instructions", fn); err != nil {
			return err
		}
		switch input := body["input"].(type) {
		case string:
			return rewriteString(body, "input", fn)
		case []any:
			for _, item := range input {
				if entry, ok := item.(map[string]any); ok {
					if err := rewriteContent(entry, "content", fn); err != nil {
						return err
					}
				}
			}
		}
	case apiEmbeddings.path:
		switch input := body["input"].(type) {
		case string:
			return rewriteString(body, "input", fn)
		case []any:
			for i, item := range input {
				if s, ok := item.(string); ok {
					out, err := fn(s)
					if err != nil {
						return err
					}
					input[i] = out
				}
			}
		}
	}
	return nil
}

// rewriteContent 는 container[key] 가 글이거나 {"text": …} 조각의 목록일 때 그 글에 fn 을 적용한다
func rewriteContent(container map[string]any, key string, fn textFunc) error {
	switch content := container[key].(type) {
	case string:
		return rewriteString(container, key, fn)
	case []any:
		for _, part := range content {
			if p, ok := part.(map[string]any); ok {
				if err := rewriteString(p, "text", fn); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func rewriteString(container map[string]any, key string, fn textFunc) error {
	s, ok := container[key].(string)
	if !ok || s == "" {
		return nil
	}
	out, err := fn(s)
	if err != nil {
		return err
	}
	container[key] = out
	return nil
}

// rewriteResponseTexts 는 스트림이 아닌 응답 본문의 모델이 쓴 글에 fn 을 적용한다. 바뀐 게 없으면 받은 바이트를 그대로 돌려준다
func rewriteResponseTexts(data []byte, api upstreamAPI, fn textFunc) ([]byte, error) {
	var obj map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&obj); err != nil {
		return data, nil // 객체가 아닌 응답은 검사할 글이 없다
	}
	changed := false
	wrapped := func(s string) (string, error) {
		out, err := fn(s)
		changed = changed || out != s
		return out, err
	}
	switch api.path {
	case apiChat.path:
		choices, _ := obj["choices"].([]any)
		for _, c := range choices {
			choice, ok := c.(map[string]any)
			if !ok {
				continue
			}
			message, _ := choice["message"].(map[string]any)
			if message != nil {
				if err := rewriteContent(message, "content", wrapped); err != nil {
					return nil, err
				}
			}
			if err := rewriteString(choice, "text", wrapped); err != nil {
				return nil, err
			}
		}
	case apiResponses.path:
		output, _ := obj["output"].([]any)
		for _, o := range output {
			if item, ok := o.(map[string]any); ok {
				if err := rewriteContent(item, "content", wrapped); err != nil {
					return nil, err
				}
			}
		}
	}
	if !changed {
		return data, nil
	}
	return json.Marshal(obj)
}

// BlockedError 는 가드레일이 요청이나 응답을 막았다는 오류다
type BlockedError struct {
	Guardrail string
	Mode      string
	Violation Violation
}

func (e *BlockedError) Error() string { return e.message() }

func (e *BlockedError) message() string {
	v := e.Violation
	if v.Kind == "keyword" {
		msg := "Content blocked: keyword '" + v.Name + "' detected"
		if v.Description != "" {
			msg += " (" + v.Description + ")"
		}
		return msg
	}
	return "Content blocked: " + v.Name + " pattern detected"
}

// ProxyError 는 litellm 이 막았을 때 돌려주는 400 과 같은 모양이다. 걸린 글의 내용은 싣지 않는다
func (e *BlockedError) ProxyError() core.ProxyError {
	fields := map[string]any{"error": e.message(), "guardrail_name": e.Guardrail, "guardrail_mode": e.Mode}
	if e.Violation.Kind == "keyword" {
		fields["keyword"] = e.Violation.Name
		fields["description"] = nilIfEmpty(e.Violation.Description)
	} else {
		fields["pattern"] = e.Violation.Name
	}
	return core.ProxyError{Status: 400, Message: e.message(), Type: "invalid_request_error", Param: nil, Fields: fields}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// applyToRequest 는 질문 검사 시점의 가드레일을 차례로 적용한다. 가려야 하면 body 를 고치고, 막아야 하면 BlockedError 다
func applyToRequest(guardrails []*Guardrail, body map[string]any, api upstreamAPI) error {
	for _, g := range guardrails {
		if !g.runsPre() {
			continue
		}
		err := rewriteRequestTexts(body, api, inspector(g, "pre_call"))
		if err != nil {
			return err
		}
	}
	return nil
}

// applyToResponse 는 응답 검사 시점의 가드레일을 적용한다
func applyToResponse(guardrails []*Guardrail, data []byte, api upstreamAPI) ([]byte, error) {
	for _, g := range guardrails {
		if !g.runsPost() {
			continue
		}
		var err error
		if data, err = rewriteResponseTexts(data, api, inspector(g, "post_call")); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func inspector(g *Guardrail, mode string) textFunc {
	return func(text string) (string, error) {
		out, violation := g.Inspect(text)
		if violation != nil {
			return text, &BlockedError{Guardrail: g.Name, Mode: mode, Violation: *violation}
		}
		return out, nil
	}
}
