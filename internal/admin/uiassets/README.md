# 대시보드 파일 자리

빌드할 때 `build.sh` 가 litellm 대시보드의 정적 빌드 결과(Next.js `out/`)를 이 폴더에 복사해 실행 파일에 넣는다(`go:embed`). 저장소에는 이 파일만 둔다.
`index.html` 이 없으면 게이트웨이는 `/ui` 를 내주지 않는다(404). 사용법은 `docs/dashboard.md` 를 본다.
