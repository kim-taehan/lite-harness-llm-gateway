#!/bin/sh
# 성능 비교 실행기: 게이트웨이 하나를 띄워 같은 시나리오를 모두 잰다.
#   ./run.sh gateway|litellm1|litellm4        (litellm1 = dell 과 같은 1워커, litellm4 = 4워커)
#   RUN_ID=내이름 ./run.sh ...                 세 대상을 같은 RUN_ID 로 돌려야 compare.py 가 한 표로 묶는다
#   QUICK=1 ./run.sh gateway                  스크립트 검증용(짧게)
#   SCENARIOS="rate-100 vus-10" ./run.sh ...  일부 시나리오만
#   KEEP=1                                    끝나도 컨테이너를 지우지 않는다(디버깅)
set -eu

TARGET="${1:-}"
case "$TARGET" in
  gateway)  PORT=4710; HEALTH=/health/liveness ;;
  litellm1) PORT=4721; HEALTH=/health/liveliness ;;
  litellm4) PORT=4724; HEALTH=/health/liveliness ;;
  *) echo "사용: ./run.sh gateway|litellm1|litellm4" >&2; exit 2 ;;
esac

cd "$(dirname "$0")"
export PATH="/Applications/Docker.app/Contents/Resources/bin:/opt/homebrew/bin:$PATH"
RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
OUT_DIR="results/$RUN_ID"
MASTER=sk-bench-master
BASE="http://127.0.0.1:$PORT"
CONTAINER="gwbench-$TARGET-1"
mkdir -p "$OUT_DIR"

if [ "${QUICK:-0}" = 1 ]; then RATE_DUR=5s; VUS_DUR=5s; WARM_DUR=3s; else RATE_DUR=30s; VUS_DUR=20s; WARM_DUR=10s; fi
SCENARIOS="${SCENARIOS:-rate-100 rate-300 rate-1000 vus-1 vus-10 vus-50 vus-200 stream-rate-100}"

cleanup() {
  if [ "${KEEP:-0}" != 1 ]; then
    docker compose --profile '*' down -v --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

say() { printf '%s\n' "== $*"; }

# --- 1. 빌드: 가짜 업스트림(linux 용 정적 바이너리)과, 우리 게이트웨이면 dist/
case "$(docker info --format '{{.Architecture}}')" in
  aarch64|arm64) GOARCH=arm64 ;;
  *) GOARCH=amd64 ;;
esac
say "빌드 (linux/$GOARCH)"
mkdir -p fakeupstream/.build
(cd fakeupstream && CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -trimpath -ldflags="-s -w" -o ".build/fakeupstream-linux-$GOARCH" .)
if [ "$TARGET" = gateway ]; then (cd .. && ./build.sh >/dev/null); fi

# --- 2. 기동
say "기동: $TARGET"
docker compose --profile "$TARGET" up -d --build postgres fakeupstream "$TARGET" >/dev/null
i=0
until curl -fsS "$BASE$HEALTH" >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 90 ]; then
    echo "기동하지 않는다. 로그:" >&2
    docker compose logs --tail 40 "$TARGET" >&2
    exit 1
  fi
  sleep 2
done

# --- 3. 모델과 키 준비(두 게이트웨이 모두 같은 모델 이름, 가상 키로 인증한다)
api() { curl -fsS -X POST "$BASE$1" -H "Authorization: Bearer $MASTER" -H 'Content-Type: application/json' -d "$2"; }
if [ "$TARGET" = gateway ]; then
  # litellm/config.yaml 과 같은 단가. 단가가 있어야 사용량 기록과 spend 갱신을 둘 다 한다
  api /model/new '{"model_name":"bench-model","litellm_params":{"model":"openai/bench-model","api_base":"http://fakeupstream:9100/v1"},"model_info":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002}}' >/dev/null
fi
KEY=""
for _ in $(seq 1 30); do
  KEY=$(api /key/generate '{}' 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin)["key"])' 2>/dev/null) && [ -n "$KEY" ] && break
  sleep 2
done
[ -n "$KEY" ] || { echo "키를 만들지 못했다" >&2; exit 1; }
status=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -d '{"model":"bench-model","messages":[{"role":"user","content":"hi"}]}')
[ "$status" = 200 ] || { echo "준비 확인 요청이 $status 를 받았다" >&2; exit 1; }
say "준비 완료 (가상 키로 200 확인)"

# --- 4. 시나리오
sample() { # 컨테이너 자원 사용을 1초 안팎 간격으로 남긴다
  while :; do
    docker stats --no-stream --format '{{.CPUPerc}},{{.MemUsage}}' "$1" 2>/dev/null | sed "s/^/$(date +%s),/"
    sleep 1
  done
}

run_one() { # 이름 모드 스트림 값(RATE 또는 VUS) 시간
  name=$1 mode=$2 stream=$3 value=$4 duration=$5
  if [ "$mode" = rate ]; then load="-e RATE=$value"; else load="-e VUS=$value"; fi
  : >"$OUT_DIR/$TARGET-$name.stats.csv"
  sample "$CONTAINER" >>"$OUT_DIR/$TARGET-$name.stats.csv" &
  sampler=$!
  # shellcheck disable=SC2086
  docker compose run --rm -T -e MODE="$mode" -e STREAM="$stream" -e DURATION="$duration" $load \
    -e BASE_URL="http://$TARGET:4000" -e API_KEY="$KEY" -e LABEL="$TARGET/$name" \
    -e OUT="/results/$RUN_ID/$TARGET-$name.json" k6 run /scripts/chat.js >"$OUT_DIR/$TARGET-$name.log" 2>&1 || true
  kill "$sampler" 2>/dev/null || true
  wait "$sampler" 2>/dev/null || true
  # k6 가 진행 표시줄을 마지막에 다시 그리므로 결과 줄(라벨로 시작)을 직접 찾는다
  grep -a "^$TARGET/$name " "$OUT_DIR/$TARGET-$name.log" | tail -n 1 || echo "$TARGET/$name: 결과 줄이 없다(로그: $OUT_DIR/$TARGET-$name.log)"
}

say "워밍업(결과 버림)"
run_one warmup rate 0 50 "$WARM_DUR" >/dev/null
rm -f "$OUT_DIR/$TARGET-warmup.json" "$OUT_DIR/$TARGET-warmup.stats.csv"

for s in $SCENARIOS; do
  case "$s" in
    rate-*)        run_one "$s" rate 0 "${s#rate-}" "$RATE_DUR" ;;
    vus-*)         run_one "$s" vus  0 "${s#vus-}"  "$VUS_DUR" ;;
    stream-rate-*) run_one "$s" rate 1 "${s#stream-rate-}" "$RATE_DUR" ;;
    *) echo "모르는 시나리오: $s" >&2 ;;
  esac
done

# --- 5. 정합성: 두 게이트웨이가 모두 요청을 사용량 기록으로 남겼는지(한쪽만 DB 에 안 쓰면 비교가 의미 없다)
say "정합성 확인(사용량 기록)"
sleep 12 # litellm 은 사용량을 배치로 쓴다
total=$(python3 - "$OUT_DIR" "$TARGET" <<'PY'
import glob, json, sys
out, target = sys.argv[1:3]
print(sum(json.load(open(p))["requests"]["count"] for p in glob.glob(f"{out}/{target}-*.json")))
PY
)
if [ "$TARGET" = gateway ]; then
  logged=$(docker compose exec -T postgres psql -U postgres -d gateway -tAc 'select count(*) from request_log')
else
  logged=$(docker compose exec -T postgres psql -U postgres -d litellm -tAc 'select count(*) from "LiteLLM_SpendLogs"')
fi
echo "$TARGET: k6 가 보낸 요청(워밍업 제외) $total 건, DB 사용량 기록 $logged 건(워밍업 포함)" | tee "$OUT_DIR/$TARGET-integrity.txt"
docker compose logs --tail 300 "$TARGET" >"$OUT_DIR/$TARGET-container.log" 2>&1 || true # 서버 쪽 오류를 나중에 볼 수 있게
say "끝: $OUT_DIR"
