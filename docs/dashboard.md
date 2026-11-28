# 대시보드(litellm 관리 화면) 전달

litellm 대시보드(Next.js 정적 빌드, 약 20MB·810개 파일)를 **게이트웨이 실행 파일 안에 넣어** 직접 내준다(`go:embed`). 별도 nginx 컨테이너가 필요 없고, 대시보드와 API 가 같은 출처라 CORS 설정이 없어도 웹 SSO 가 된다. 대시보드 코드는 수정하지 않는다(빌드한 결과물을 그대로 넣는다).

## 빌드

```sh
./build.sh                                   # 이웃 gateway 저장소의 빌드 결과(../gateway/litellm/proxy/_experimental/out)를 넣는다
DASHBOARD_DIR=/path/to/out ./build.sh        # 다른 위치의 대시보드를 넣는다
DASHBOARD_REQUIRED=1 ./build.sh              # 대시보드를 못 찾으면 실패(배포용 빌드가 대시보드 없이 나가지 않게)
DASHBOARD_DIR=none ./build.sh                # 대시보드 없이 빌드(앞단이 따로 내줄 때)
```

`build.sh` 가 `internal/admin/uiassets/` 를 비우고 대시보드 파일을 복사한 뒤 빌드한다. 이 폴더의 파일은 저장소에 넣지 않는다(`.gitignore`, 자리 안내 `README.md` 만 커밋). 대시보드가 없으면 `go build`·`go test` 는 그대로 되고 `/ui` 만 404 다(기동 로그의 `dashboard: false`). 실행 파일 크기는 대시보드만큼(약 +16~18MB, 플랫폼별 36~38MB) 커진다. 같은 소스·같은 대시보드 파일이면 실행 파일의 해시가 같다(재현 가능한 빌드).

## 내주는 경로

대시보드 JS 가 경로를 박아 두었으므로 nginx 설정과 같은 규칙이다. 모두 인증 없이 연다(로그인 화면도 이 파일들이다).

| 경로 | 동작 |
|---|---|
| `/ui`, `/ui/` | `index.html` |
| `/ui/<페이지>` | `<페이지>.html` → `<페이지>/index.html` → 없으면 `index.html`(클라이언트 라우트) |
| `/ui/*.txt`, `/ui/assets/*` | 파일 그대로만. **없으면 404**(HTML 로 답하면 대시보드 라우터가 로그인 화면을 무한히 오간다) |
| `/_next/*`, `/litellm-asset-prefix/_next/*` | 해시가 붙은 번들. 1년 캐시(`immutable`). 두 경로는 같은 파일이다(`assetPrefix` 때문) |
| `/assets/*`, `/favicon.ico` | 정적 파일 |
| `/ui/litellm-runtime-config.json` | **404.** 백엔드를 따로 두지 않으므로 대시보드가 자기 출처(이 서버)로 API 를 부른다 |

HTML 은 `no-cache` 라 새 빌드를 배포하면 바로 바뀐다. 한계: 파일을 압축해서 내주지 않는다(번들이 크지만 사내망 전제). 필요하면 앞단(nginx 등)이 gzip 한다.

## 배치

- **권장: 게이트웨이 하나.** `http(s)://게이트웨이/ui/` 로 연다. `GATEWAY_PUBLIC_URL` 을 그 주소로 둔다(SSO 리다이렉트·로그인 폼이 쓴다).
- 앞단 TLS 종단·IP 허용 목록은 `/ui` 에도 똑같이 적용된다.
- 대시보드를 따로 내려야 하는 고객사(호스트 분리, 대시보드만 자주 교체)는 `DASHBOARD_DIR=none` 으로 빌드하고 기존처럼 nginx 컨테이너를 쓴다. 이때 웹 SSO 는 같은 출처가 아니면 되지 않는다.

## 대시보드 파일 갱신

대시보드를 새 버전으로 바꾸려면 새 빌드 결과를 `DASHBOARD_DIR` 에 두고 게이트웨이를 다시 빌드·배포한다. 지금은 upstream 대시보드 업데이트를 따라가지 않고 고정한다(설계 문서의 결정).
