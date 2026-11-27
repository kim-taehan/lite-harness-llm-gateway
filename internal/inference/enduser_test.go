package inference

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEndUserID_FollowsLitellmOrder(t *testing.T) {
	decode := func(body string) map[string]any {
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		var m map[string]any
		if err := decoder.Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	cases := []struct {
		name, body string
		headers    map[string]string
		want       string
	}{
		{"user 없음", `{"model":"m"}`, nil, ""},
		{"본문 user", `{"user":"u1"}`, nil, "u1"},
		{"숫자 user", `{"user":42}`, nil, "42"},
		{"빈 user 는 없는 것", `{"user":"  "}`, nil, ""},
		{"헤더가 본문보다 먼저", `{"user":"body"}`, map[string]string{"X-Litellm-Customer-Id": "hdr"}, "hdr"},
		{"end-user 헤더", `{}`, map[string]string{"x-litellm-end-user-id": "h2"}, "h2"},
		{"litellm_metadata.user", `{"litellm_metadata":{"user":"lm"}}`, nil, "lm"},
		{"metadata.user_id", `{"metadata":{"user_id":"md"}}`, nil, "md"},
		{"문자열로 온 metadata", `{"metadata":"{\"user_id\":\"str\"}"}`, nil, "str"},
		{"본문 user 가 metadata 보다 먼저", `{"user":"a","metadata":{"user_id":"b"}}`, nil, "a"},
		{"safety_identifier 는 읽지 않는다", `{"safety_identifier":"sf"}`, nil, ""},
		{"객체가 아닌 metadata", `{"metadata":[1]}`, nil, ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		if got := endUserID(r, decode(c.body)); got != c.want {
			t.Errorf("%s: %q, 기대 %q", c.name, got, c.want)
		}
	}
}
