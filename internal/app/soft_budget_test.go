package app

import (
	"strings"
	"testing"
)

// 소프트 예산: 차단하지 않고, 넘은 대상 수를 /metrics 로 알려 사내 Prometheus 알람이 보게 한다

func TestSoftBudget_MetricsCountTargetsAtOrOverTheSoftBudget(t *testing.T) {
	h, _ := testHandler(t)
	api := apiClient{t: t, h: h}
	metric := func() string { return get(h, "/metrics").Body.String() }
	api.ok("POST", "/key/generate", map[string]any{"soft_budget": 5, "max_budget": 10})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "soft_budget": 5, "max_budget": 10})
	if body := metric(); !strings.Contains(body, `gateway_soft_budget_exceeded{scope="key"} 0`) {
		t.Fatalf("넘은 대상이 없는데 0 이 아니다:\n%s", grepLines(body, "soft_budget"))
	}

	// 사용액 0 이 소프트 예산 0 이상이라 넘은 것으로 센다(사용액을 올리는 API 가 없어 이 값으로 시험한다)
	over := api.ok("POST", "/key/generate", map[string]any{"soft_budget": 0, "max_budget": 10})
	body := metric()
	for _, want := range []string{`gateway_soft_budget_exceeded{scope="key"} 1`, `gateway_soft_budget_exceeded{scope="team"} 0`} {
		if !strings.Contains(body, want) {
			t.Errorf("%q 가 없다:\n%s", want, grepLines(body, "soft_budget"))
		}
	}
	// 차단하지 않는다: 소프트 예산을 넘은 키도 키 정보를 그대로 읽는다
	api.ok("GET", "/key/info?key="+over["key"].(string), nil)
}

func grepLines(text, substr string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, substr) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
