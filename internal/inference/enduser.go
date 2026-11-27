package inference

import (
	"encoding/json"
	"net/http"
	"strings"
)

// 고객(end user)을 알리는 표준 헤더(litellm STANDARD_CUSTOMER_ID_HEADERS)
var endUserHeaders = []string{"x-litellm-customer-id", "x-litellm-end-user-id"}

// endUserID 는 요청이 밝힌 고객이다. litellm 과 같은 순서로 본다: 표준 헤더, 본문 user, litellm_metadata.user, metadata.user_id.
// 호출자가 정하는 값이라 신뢰할 수 있는 앱이 채워 줄 때만 뜻이 있다(없는 id 는 한도가 걸리지 않는다). safety_identifier 는 읽지 않는다
func endUserID(r *http.Request, body map[string]any) string {
	for _, header := range endUserHeaders {
		if id := strings.TrimSpace(r.Header.Get(header)); id != "" {
			return id
		}
	}
	if id := idString(body["user"]); id != "" {
		return id
	}
	if id := idString(objectOf(body["litellm_metadata"])["user"]); id != "" {
		return id
	}
	return idString(objectOf(body["metadata"])["user_id"])
}

// 요청 하나가 붙일 수 있는 태그 수와 태그 이름 길이의 상한이다. 등록하지 않은 태그는 한도가 없지만 조회 비용은 있어서 막아 둔다
const (
	maxRequestTags = 20
	maxTagLength   = 200
)

// requestTags 는 요청이 붙인 태그다. litellm 처럼 헤더 x-litellm-tags(쉼표로 구분), 본문의 tags, metadata.tags, litellm_metadata.tags 를 합친다.
// 중복·빈 값·너무 긴 값은 빼고 maxRequestTags 개까지만 쓴다
func requestTags(r *http.Request, body map[string]any) []string {
	var tags []string
	seen := map[string]bool{}
	add := func(v any) {
		name, ok := v.(string)
		name = strings.TrimSpace(name)
		if !ok || name == "" || len(name) > maxTagLength || seen[name] || len(tags) >= maxRequestTags {
			return
		}
		seen[name] = true
		tags = append(tags, name)
	}
	for _, part := range strings.Split(r.Header.Get("x-litellm-tags"), ",") {
		add(part)
	}
	for _, source := range []any{body["tags"], objectOf(body["metadata"])["tags"], objectOf(body["litellm_metadata"])["tags"]} {
		list, _ := source.([]any)
		for _, item := range list {
			add(item)
		}
	}
	return tags
}

// idString 은 문자열이거나 숫자인 값을 id 문자열로 바꾼다. 그 밖의 값과 빈 문자열은 없는 것으로 본다
func idString(v any) string {
	switch id := v.(type) {
	case string:
		return strings.TrimSpace(id)
	case json.Number:
		return id.String()
	}
	return ""
}

// objectOf 는 JSON 객체이거나, 객체를 담은 JSON 문자열(multipart 나 extra_body 로 오는 경우)이면 그 객체다
func objectOf(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case string:
		var parsed map[string]any
		if json.Unmarshal([]byte(m), &parsed) == nil {
			return parsed
		}
	}
	return nil
}
