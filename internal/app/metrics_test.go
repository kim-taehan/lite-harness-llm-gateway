package app

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"llm-gateway/internal/limits"
)

func TestMetrics_OpenWithoutKeyAndCountsRequestsByRoute(t *testing.T) {
	h, _ := testHandler(t)
	get(h, "/health/liveness")
	get(h, "/nowhere")

	rec := get(h, "/metrics")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("키 없이 /metrics: %d", rec.Code)
	}
	for _, want := range []string{
		`gateway_http_requests_total{method="GET",route="GET /health/liveness",status="200"} 1`,
		`gateway_http_requests_total{method="GET",route="/",status="404"} 1`,
		`gateway_usage_queue_length`,
		`gateway_usage_dropped_total 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%q 가 없다", want)
		}
	}
}

type recordingSink struct{ got []limits.UsageEvent }

func (r *recordingSink) Record(e limits.UsageEvent) { r.got = append(r.got, e) }

func TestObservedUsage_CountsTokensStatusAndFirstTokenPerModel(t *testing.T) {
	m := newMetrics(limits.NewUsageRecorder(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), limits.UsageOptions{QueueCapacity: 1}))
	next := &recordingSink{}
	ttft := 250
	m.observing(next).Record(limits.UsageEvent{Model: "qwen", Status: "success", PromptTokens: 10, CompletionTokens: 5, TTFTMS: &ttft})
	m.observing(next).Record(limits.UsageEvent{Model: "qwen", Status: "cancelled", PromptTokens: 2})

	if len(next.got) != 2 {
		t.Fatalf("다음 단계로 넘긴 기록 = %d, 2 여야 한다", len(next.got))
	}
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, f := range families {
		for _, metric := range f.GetMetric() {
			key := f.GetName()
			for _, l := range metric.GetLabel() {
				key += "|" + l.GetName() + "=" + l.GetValue()
			}
			if c := metric.GetCounter(); c != nil {
				got[key] = c.GetValue()
			}
			if h := metric.GetHistogram(); h != nil {
				got[key+"|count"] = float64(h.GetSampleCount())
			}
		}
	}
	want := map[string]float64{
		"gateway_llm_tokens_total|model=qwen|type=prompt":          12,
		"gateway_llm_tokens_total|model=qwen|type=completion":      5,
		"gateway_llm_requests_total|model=qwen|status=success":     1,
		"gateway_llm_requests_total|model=qwen|status=cancelled":   1,
		"gateway_llm_time_to_first_token_seconds|model=qwen|count": 1,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, %v 여야 한다", k, got[k], v)
		}
	}
}
