# llm-gateway

폐쇄망용 LLM 게이트웨이. litellm 프록시를 대체한다. **Go 단일 실행 파일**이라 대상 서버에 런타임을 설치하지 않아도 된다.
설계: [GATEWAY_DESIGN.md](GATEWAY_DESIGN.md) · 작업 규칙: [CLAUDE.md](CLAUDE.md)

## 만들기·점검

```
./build.sh          # dist/ 에 linux amd64·arm64, darwin arm64·amd64 + SHA256SUMS
./build.sh check    # 같은 소스를 두 번 빌드해 해시가 같은지 (재현 가능한 빌드)
./check.sh          # 서식 · vet · 테스트(모듈 경계 포함) · govulncheck · 다른 OS vet · 재현 빌드 · 훅 테스트
```

## 띄우기

```
GATEWAY_MASTER_KEY=sk-... GATEWAY_DATABASE_URL=postgres://user:pass@db:5432/gateway ./llm-gateway-linux-amd64
```

| 환경 변수 | 기본값 | 뜻 |
|---|---|---|
| `GATEWAY_MASTER_KEY` | 없음(필수) | 관리자 키. 없으면 기동하지 않는다 |
| `GATEWAY_DATABASE_URL` | 없음(필수) | PostgreSQL. 기동 때 마이그레이션을 적용한다(여러 대가 동시에 떠도 한 대만) |
| `GATEWAY_LISTEN` | `:4000` | 수신 주소(litellm 기본 포트) |

`SIGTERM` 을 받으면 liveness 를 503 으로 내리고, 진행 중인 요청을 최대 60초 기다린 뒤 끝난다.

## 코드 구조

의존은 한 방향이다: `cmd` → `app` → `admin` → `inference` → `auth` · `limits` → `core`. 모두 `internal/` 안이고, 방향은 `internal/app/architecture_test.go` 가 검사한다.

| 패키지 | 책임 |
|---|---|
| `cmd/llm-gateway` | 진입점 — `app.Run` 만 부른다 |
| `internal/app` | 조립: 설정, 모듈 라우트를 권한 검사로 감싸 등록, 기동·정상 종료 |
| `internal/admin` | litellm 모양 관리 API(팀·사용자·팀원). auth 와 limits 값을 합쳐 응답을 조립한다 |
| `internal/auth` | 신원: 키 검증, 라우트 권한 검사(`Guard`), 팀·사용자·팀원(`team`·`app_user`·`team_member`) |
| `internal/limits` | 한도: 키·팀·사용자의 예산·RPM·TPM·동시성 설정(`limit_policy`). 요청 검사는 M3 |
| `internal/core` | 바탕: 라우트 권한 선언(`Route`·`Access`·`Role`), litellm 모양 오류, 헬스, DB(트랜잭션을 ctx 로), 마이그레이션(`migrations/*.sql` 를 실행 파일에 넣는다) |
| `internal/core/dbtest` | 시험 도우미: 실제 PostgreSQL(docker 또는 `GATEWAY_TEST_DATABASE_URL`)에 시험마다 새 DB |
| `internal/inference` | 뒤 단계에서 추가 (설계 12장 M3) |

시험은 실제 PostgreSQL 에서 돈다. Docker 가 있으면 `postgres:16-alpine` 을 알아서 띄우고, 없으면 `GATEWAY_TEST_DATABASE_URL` 을 준다.
