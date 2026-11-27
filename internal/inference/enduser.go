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
