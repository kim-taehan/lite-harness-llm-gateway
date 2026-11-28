#!/bin/sh
# 플랫폼별 단일 실행 파일을 dist/ 에 만들고 SHA256SUMS 를 남긴다.
#   ./build.sh            빌드
#   ./build.sh check      같은 소스를 두 번 빌드해 해시가 같은지 본다 (재현 가능한 빌드)
# 대시보드(litellm 대시보드의 정적 빌드 결과)는 실행 파일 안에 넣는다. 위치는 DASHBOARD_DIR(기본: 이웃 gateway 저장소의 빌드 결과).
#   DASHBOARD_DIR=none        대시보드 없이 빌드(/ui 는 404, 앞단이 따로 내줄 때)
#   DASHBOARD_REQUIRED=1      대시보드를 못 찾으면 실패(배포용 빌드가 대시보드 없이 나가지 않게)
# C 라이브러리를 쓰지 않아(CGO_ENABLED=0) 대상 서버에 아무것도 설치하지 않아도 돈다.
set -eu
VERSION="${VERSION:-dev}"
# 로그인 방식(GATEWAY_LOGIN_METHODS)과 카운터 저장소(GATEWAY_COUNTER_STORE)는 빌드가 아니라 실행할 때의 환경 변수로 고른다
TARGETS="linux/amd64 linux/arm64 darwin/arm64 darwin/amd64"

DASHBOARD_DIR="${DASHBOARD_DIR:-../gateway/litellm/proxy/_experimental/out}"
ASSETS="internal/admin/uiassets"

# sync_dashboard 는 go:embed 가 읽는 폴더를 이번 빌드의 대시보드로 맞춘다(이전 빌드의 파일이 남지 않게 먼저 비운다)
sync_dashboard() {
  find "$ASSETS" -mindepth 1 -maxdepth 1 ! -name README.md -exec rm -rf {} +
  if [ "$DASHBOARD_DIR" != "none" ] && [ -f "$DASHBOARD_DIR/index.html" ]; then
    cp -R "$DASHBOARD_DIR"/. "$ASSETS"/
    echo "대시보드 포함: $DASHBOARD_DIR ($(find "$ASSETS" -type f | wc -l | tr -d ' ')개 파일)" >&2
  elif [ "${DASHBOARD_REQUIRED:-}" = "1" ]; then
    echo "대시보드를 찾지 못했다: $DASHBOARD_DIR/index.html (DASHBOARD_DIR 를 정한다)" >&2
    exit 1
  else
    echo "경고: 대시보드 없이 빌드한다(/ui 는 404). DASHBOARD_DIR=$DASHBOARD_DIR" >&2
  fi
}

build_all() {
  out="$1"
  rm -rf "$out"
  mkdir -p "$out"
  for target in $TARGETS; do
    os="${target%/*}"
    arch="${target#*/}"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X llm-gateway/internal/core.Version=$VERSION" -o "$out/llm-gateway-$os-$arch" ./cmd/llm-gateway
  done
  (cd "$out" && shasum -a 256 llm-gateway-* > SHA256SUMS)
}

sync_dashboard
if [ "${1:-}" = "check" ]; then
  build_all "dist-check-a"
  build_all "dist-check-b"
  if diff -q dist-check-a/SHA256SUMS dist-check-b/SHA256SUMS >/dev/null; then
    echo "재현 가능: 두 번 빌드한 파일의 sha256 이 모두 같다"
    cat dist-check-a/SHA256SUMS
    rm -rf dist-check-a dist-check-b
  else
    echo "재현 불가: 해시가 다르다" >&2
    diff dist-check-a/SHA256SUMS dist-check-b/SHA256SUMS >&2 || true
    exit 1
  fi
else
  build_all dist
  ls -l dist | awk 'NR>1 {printf "%8.1f MB  %s\n", $5/1048576, $9}'
fi
