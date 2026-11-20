#!/bin/sh
# 플랫폼별 단일 실행 파일을 dist/ 에 만들고 SHA256SUMS 를 남긴다.
#   ./build.sh            빌드
#   ./build.sh check      같은 소스를 두 번 빌드해 해시가 같은지 본다 (재현 가능한 빌드)
# C 라이브러리를 쓰지 않아(CGO_ENABLED=0) 대상 서버에 아무것도 설치하지 않아도 돈다.
set -eu
VERSION="${VERSION:-dev}"
TARGETS="linux/amd64 linux/arm64 darwin/arm64 darwin/amd64"

build_all() {
  out="$1"
  rm -rf "$out"
  mkdir -p "$out"
  for target in $TARGETS; do
    os="${target%/*}"
    arch="${target#*/}"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -buildvcs=false \
      -ldflags="-s -w -X llm-gateway/internal/app.version=$VERSION" -o "$out/llm-gateway-$os-$arch" ./cmd/llm-gateway
  done
  (cd "$out" && shasum -a 256 llm-gateway-* > SHA256SUMS)
}

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
