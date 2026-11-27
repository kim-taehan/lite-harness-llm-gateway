package app

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// metrics 는 Prometheus 지표 한 벌이다. 전역 레지스트리 대신 인스턴스로 들고 있어 테스트마다 새로 만들 수 있다(설계 11.3)
type metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	tokens   *prometheus.CounterVec
	llm      *prometheus.CounterVec
	ttft     *prometheus.HistogramVec
}

func newMetrics(usage *limits.UsageRecorder) *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_http_requests_total", Help: "처리한 HTTP 요청 수"}, []string{"method", "route", "status"}),
		// 스트림은 수 분 이어질 수 있어 위쪽 구간을 길게 잡는다
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "gateway_http_request_duration_seconds", Help: "응답을 끝낼 때까지 걸린 시간",
			Buckets: []float64{.01, .05, .1, .5, 1, 2.5, 5, 10, 30, 60, 120, 300}}, []string{"method", "route"}),
		// 모델 이름은 운영자가 등록한 것뿐이라 라벨 종류가 늘지 않는다. 키·팀은 종류가 많아 라벨로 쓰지 않는다
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_llm_tokens_total", Help: "모델별 토큰 수(type=prompt|completion)"}, []string{"model", "type"}),
		llm: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_llm_requests_total", Help: "모델별 추론 요청 수(status=success|error|cancelled)"}, []string{"model", "status"}),
		ttft: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "gateway_llm_time_to_first_token_seconds", Help: "스트림에서 첫 토큰까지 걸린 시간",
			Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60}}, []string{"model"}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests, m.duration, m.tokens, m.llm, m.ttft,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "gateway_usage_queue_length", Help: "아직 DB 에 쓰지 않은 사용량 기록 수"},
			func() float64 { return float64(usage.Queued()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "gateway_usage_dropped_total", Help: "사용량 채널이 가득 차 버린 기록 수"},
			func() float64 { return float64(usage.Dropped()) }),
	)
	return m
}

// route 는 litellm 처럼 인증 없이 연다(설계 11.3 "litellm 기본을 따른다"). 접근을 막으려면 앞단에서 경로를 막는다
func (m *metrics) route() core.Route {
	return core.Route{Method: http.MethodGet, Path: "/metrics", Access: core.Public(),
		Handler: promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}).ServeHTTP}
}

// instrument 는 요청 수와 처리 시간을 센다. route 라벨은 mux 가 맞춘 패턴이라 경로 값이 달라도 종류가 늘지 않는다
func (m *metrics) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		m.requests.WithLabelValues(r.Method, route, statusLabel(rec.status)).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

// usageSink 는 inference 가 사용량을 넘기는 곳이다
type usageSink interface{ Record(limits.UsageEvent) }

// observedUsage 는 사용량을 넘기기 전에 모델별 지표를 센다. inference·limits 는 지표를 모른다
type observedUsage struct {
	next usageSink
	m    *metrics
}

func (m *metrics) observing(next usageSink) usageSink { return observedUsage{next: next, m: m} }

func (o observedUsage) Record(e limits.UsageEvent) {
	o.m.llm.WithLabelValues(e.Model, e.Status).Inc()
	o.m.tokens.WithLabelValues(e.Model, "prompt").Add(float64(e.PromptTokens))
	o.m.tokens.WithLabelValues(e.Model, "completion").Add(float64(e.CompletionTokens))
	if e.TTFTMS != nil {
		o.m.ttft.WithLabelValues(e.Model).Observe(float64(*e.TTFTMS) / 1000)
	}
	o.next.Record(e)
}

// statusRecorder 는 응답 상태 코드를 기억한다. 스트리밍 중계가 http.ResponseController 로 Flush 하므로 Unwrap 으로 원래 writer 를 드러낸다
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func statusLabel(code int) string { return strconv.Itoa(code) }
