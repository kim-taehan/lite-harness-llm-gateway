// 게이트웨이 성능 시험: POST /v1/chat/completions 를 부른다. 설정은 모두 환경 변수다.
//   MODE=rate  일정한 도착 속도(RATE 건/초). 느려져도 요청을 줄이지 않아 지연 백분위가 정확하다(밀린 건은 dropped_iterations)
//   MODE=vus   일정한 동시 접속(VUS 개). 한 요청이 끝나야 다음을 보내므로 최대 처리량을 본다
//   STREAM=1   스트리밍. 첫 바이트까지 시간(http_req_waiting)이 첫 토큰 지연의 근사치다
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

// 응답 코드별 개수. status_0 은 응답을 못 받은 경우(연결 끊김·시간 초과 등)다. 모르는 코드는 status_other
const statusCounters = {};
for (const code of [200, 401, 429, 500, 502, 503, 504, 0]) statusCounters[code] = new Counter(`status_${code}`);
const statusOther = new Counter('status_other');
let sampledErrors = 0; // 이 VU 가 로그로 남긴 오류 수(너무 많이 남지 않게)

const BASE_URL = __ENV.BASE_URL;
const API_KEY = __ENV.API_KEY;
const MODEL = __ENV.MODEL || 'bench-model';
const STREAM = __ENV.STREAM === '1';
const MODE = __ENV.MODE || 'rate';
const DURATION = __ENV.DURATION || '30s';
const OUT = __ENV.OUT || '/results/summary.json';

const scenario = MODE === 'vus'
  ? { executor: 'constant-vus', vus: Number(__ENV.VUS || 10), duration: DURATION }
  : {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 100),
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: Number(__ENV.PRE_VUS || 100),
      maxVUs: Number(__ENV.MAX_VUS || 500),
    };

export const options = {
  scenarios: { bench: scenario },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

const params = { headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${API_KEY}` } };
const body = JSON.stringify({ model: MODEL, stream: STREAM, messages: [{ role: 'user', content: 'hi' }] });

export default function () {
  const res = http.post(`${BASE_URL}/v1/chat/completions`, body, params);
  (statusCounters[res.status] || statusOther).add(1);
  if (res.status !== 200 && sampledErrors < 2) {
    sampledErrors++;
    console.warn(`실패 응답 status=${res.status} error=${res.error || ''} body=${String(res.body).slice(0, 120)}`);
  }
  check(res, {
    'status 200': (r) => r.status === 200,
    // 비스트림은 usage 가 본문에, 스트림은 마지막에 [DONE] 이 있어야 끝까지 받은 것이다
    'complete body': (r) => (STREAM ? r.body.includes('[DONE]') : r.body.includes('"usage"')),
  });
}

function pick(data, name, keys) {
  const m = data.metrics[name];
  if (!m) return null;
  const out = {};
  for (const k of keys) out[k] = m.values[k];
  return out;
}

export function handleSummary(data) {
  const trend = ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'];
  const summary = {
    label: __ENV.LABEL || '',
    mode: MODE,
    stream: STREAM,
    rate: MODE === 'rate' ? Number(__ENV.RATE || 100) : null,
    vus: MODE === 'vus' ? Number(__ENV.VUS || 10) : null,
    duration: DURATION,
    duration_ms: pick(data, 'http_req_duration', trend),
    ttfb_ms: pick(data, 'http_req_waiting', trend),
    requests: pick(data, 'http_reqs', ['count', 'rate']),
    failed_rate: pick(data, 'http_req_failed', ['rate']),
    checks_rate: pick(data, 'checks', ['rate']),
    dropped: pick(data, 'dropped_iterations', ['count']),
    vus_max: pick(data, 'vus_max', ['max']),
    status: {},
  };
  for (const name of Object.keys(data.metrics)) {
    if (name.startsWith('status_')) summary.status[name.slice(7)] = data.metrics[name].values.count;
  }
  const d = summary.duration_ms;
  const line = `${summary.label} 요청 ${summary.requests.count}건 (${summary.requests.rate.toFixed(1)}/s) ` +
    `p50 ${d.med.toFixed(1)}ms p95 ${d['p(95)'].toFixed(1)}ms p99 ${d['p(99)'].toFixed(1)}ms ` +
    `실패 ${(summary.failed_rate.rate * 100).toFixed(2)}% 밀린 요청 ${summary.dropped ? summary.dropped.count : 0}\n`;
  return { [OUT]: JSON.stringify(summary, null, 2), stdout: line };
}
