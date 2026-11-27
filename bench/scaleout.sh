#!/bin/sh
# 스케일아웃 시험: 게이트웨이를 여러 대 띄워 nginx 뒤에 두고 실제 Redis 로 한도를 공유한다.
#   ./scaleout.sh limits               2대: 분당 10건 한도가 두 대 합쳐서 지켜지는지(redis) / 대수만큼 늘어나는지(memory)
#   ./scaleout.sh outage               2대: Redis 를 멈춰도 요청이 막히지 않는지, 돌아오면 다시 공유하는지
#   ./scaleout.sh throughput [N...]    N대(기본 1 2 4)의 처리량. VUS(기본 400)·DURATION(기본 20s)로 조절
#   KEEP=1                             끝나도 컨테이너를 지우지 않는다(디버깅)
set -eu

cd "$(dirname "$0")"
export PATH="/Applications/Docker.app/Contents/Resources/bin:/opt/homebrew/bin:$PATH"
DC="docker compose -f docker-compose.scaleout.yml"
LB=http://127.0.0.1:4730
MASTER=sk-bench-master
OUT_DIR=results/scaleout
mkdir -p "$OUT_DIR"

cleanup() { [ "${KEEP:-0}" = 1 ] || $DC --profile '*' down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT
say() { printf '%s\n' "== $*"; }

build() {
  case "$(docker info --format '{{.Architecture}}')" in aarch64|arm64) goarch=arm64 ;; *) goarch=amd64 ;; esac
  mkdir -p fakeupstream/.build
  (cd fakeupstream && CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o ".build/fakeupstream-linux-$goarch" .)
  (cd .. && ./build.sh >/dev/null)
}

# n 대를 띄우고, 로드밸런서를 거친 요청이 모든 대에서 성공할 때까지 기다린다(라운드 로빈이라 n*4 번이면 모든 대에 닿는다)
up() {
  n=$1 mode=$2
  COUNTER_STORE="$mode" $DC up -d --build --scale gateway="$n" postgres redis fakeupstream gateway lb >/dev/null 2>&1
  i=0
  while :; do
    ok=1
    for _ in $(seq 1 $((n * 4))); do
      [ "$(curl -s -o /dev/null -w '%{http_code}' "$LB/health/readiness" 2>/dev/null)" = 200 ] || { ok=0; break; }
    done
    [ "$ok" = 1 ] && break
    i=$((i + 1))
    [ "$i" -gt 60 ] && { echo "기동하지 않는다" >&2; $DC logs --tail 30 gateway >&2; exit 1; }
    sleep 2
  done
}

api() { curl -fsS -X POST "$LB$1" -H "Authorization: Bearer $MASTER" -H 'Content-Type: application/json' -d "$2"; }
setup_model() {
  api /model/new '{"model_name":"bench-model","litellm_params":{"model":"openai/bench-model","api_base":"http://fakeupstream:9100/v1"},"model_info":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002}}' >/dev/null
}
make_key() { api /key/generate "$1" | python3 -c 'import sys,json;print(json.load(sys.stdin)["key"])'; }
chat_code() {
  curl -s -o /dev/null -w '%{http_code} %{time_total}\n' -X POST "$LB/v1/chat/completions" -H "Authorization: Bearer $1" \
    -H 'Content-Type: application/json' -d '{"model":"bench-model","messages":[{"role":"user","content":"hi"}]}'
}
# 한도는 1분 고정 윈도우라 분 경계를 넘으면 값이 섞인다. 경계 직전이면 다음 분까지 기다린다
align_window() {
  s=$(date +%S)
  s=${s#0}
  [ "${s:-0}" -ge 45 ] && sleep $((61 - s))
  return 0
}

limits_case() {
  mode=$1
  say "한도 공유: 게이트웨이 2대, 카운터 저장소=$mode, 키 한도 분당 10건"
  up 2 "$mode"
  setup_model
  key=$(make_key '{"rpm_limit":10}')
  align_window
  ok=0 limited=0 other=0
  for _ in $(seq 1 30); do
    case "$(chat_code "$key" | cut -d' ' -f1)" in 200) ok=$((ok + 1)) ;; 429) limited=$((limited + 1)) ;; *) other=$((other + 1)) ;; esac
  done
  echo "카운터=$mode: 통과 $ok, 한도 초과(429) $limited, 그 밖 $other  (30건 중. 기대: redis 면 통과 10, memory 면 대수만큼 늘어 20)" | tee -a "$OUT_DIR/limits.txt"
  $DC down -v --remove-orphans >/dev/null 2>&1
}

outage() {
  say "Redis 장애: 게이트웨이 2대, 카운터 저장소=redis"
  up 2 redis
  setup_model
  key=$(make_key '{"rpm_limit":10}')
  align_window
  for _ in $(seq 1 5); do chat_code "$key" >/dev/null; done
  echo "Redis 정상일 때 5건 보냄"
  $DC stop redis >/dev/null 2>&1
  say "Redis 중지 후 20건 (5xx 가 없어야 하고 지연이 길어지면 안 된다)"
  codes="" max=0
  for _ in $(seq 1 20); do
    line=$(chat_code "$key")
    codes="$codes ${line%% *}"
    t=${line#* }
    max=$(python3 -c "print(max($max, $t))")
  done
  echo "응답 코드:$codes" | tee "$OUT_DIR/outage.txt"
  echo "가장 느린 응답: ${max}초" | tee -a "$OUT_DIR/outage.txt"
  $DC start redis >/dev/null 2>&1
  sleep 8 # 서킷 브레이커가 열려 있는 5초가 지나길 기다린다
  for _ in $(seq 1 10); do chat_code "$key" >/dev/null; done
  say "게이트웨이 로그(장애·복구는 인스턴스마다 한 번씩만 있어야 한다)"
  $DC logs gateway 2>&1 | grep -a "Redis 카운터" | sed -E 's/^[^|]*\| //' | cut -c1-200 | tee -a "$OUT_DIR/outage.txt"
  $DC down -v --remove-orphans >/dev/null 2>&1
}

throughput() {
  for n in ${*:-1 2 4}; do
    say "처리량: 게이트웨이 ${n}대(각 ${GW_CPUS:-1}코어), 카운터 저장소=redis, 동시 접속 ${VUS:-400}"
    up "$n" redis
    setup_model
    # 한도가 걸려 있어야 요청마다 Redis 를 탄다(값은 아주 높게 둬서 걸리지는 않는다)
    key=$(make_key '{"rpm_limit":100000000,"tpm_limit":1000000000}')
    $DC run --rm -T -e MODE=vus -e VUS=50 -e DURATION=10s -e STREAM=0 -e BASE_URL=http://lb:8080 -e API_KEY="$key" \
      -e LABEL=warmup -e OUT=/results/scaleout/warmup.json k6 run /scripts/chat.js >/dev/null 2>&1 || true
    $DC run --rm -T -e MODE=vus -e VUS="${VUS:-400}" -e DURATION="${DURATION:-20s}" -e STREAM=0 -e BASE_URL=http://lb:8080 -e API_KEY="$key" \
      -e LABEL="gateway x$n" -e OUT="/results/scaleout/throughput-$n.json" k6 run /scripts/chat.js >"$OUT_DIR/throughput-$n.log" 2>&1 || true
    grep -a "^gateway x$n " "$OUT_DIR/throughput-$n.log" | tail -n 1 | tee -a "$OUT_DIR/throughput.txt"
    $DC down -v --remove-orphans >/dev/null 2>&1
  done
}

case "${1:-}" in
  limits) build; : >"$OUT_DIR/limits.txt"; limits_case memory; limits_case redis ;;
  outage) build; outage ;;
  throughput) build; shift; : >"$OUT_DIR/throughput.txt"; throughput "$@" ;;
  *) echo "사용: ./scaleout.sh limits | outage | throughput [N...]" >&2; exit 2 ;;
esac
say "끝: $OUT_DIR"
