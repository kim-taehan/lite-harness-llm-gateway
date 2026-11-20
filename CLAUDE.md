# llm-gateway

폐쇄망용 LLM 게이트웨이. **Go 단일 실행 파일**로 만든다. Python litellm 게이트웨이를 대체하는 별개 서비스다.
설계는 `GATEWAY_DESIGN.md`, litellm 조사 자료는 `docs/litellm-inventory/`, 이전 저장소의 문서·규칙은 `docs/legacy/`(참고용, 적용되는 규칙 아님).
관례는 배포 서버 `../deploy-server`(같은 Go, 표준 라이브러리 우선)와 맞춘다. Kotlin 시절 코드는 `archive/kotlin` 브랜치에 있다.

```
./check.sh      # 커밋 전 점검: gofmt · vet · 테스트(모듈 경계 포함) · govulncheck · 다른 OS vet · 재현 빌드 · 훅 테스트
./build.sh      # dist/ 에 플랫폼별 단일 실행 파일 + SHA256SUMS
```
Claude Code 셸에는 `go` 가 PATH 에 없을 수 있다. `export PATH=/opt/homebrew/bin:$PATH` 로 쓴다.

## 코딩 원칙

**클린 코드와 SOLID 를 따른다. 단, 단순함과 충돌하면 단순함이 이긴다.**

- 이름은 하는 일을 말한다. `util`·`common`·`helper`·`manager` 같은 뭉뚱그린 이름을 피한다
- 함수는 한 가지 일만 한다. 50줄을 넘으면 나눌지 따진다
- 깊은 중첩 대신 일찍 돌려준다
- 같은 코드가 세 번 나오면 함수로 뺀다. 두 번까지는 그대로 둔다
- 주석은 "왜"만 적는다(한국어). 코드가 말하는 "무엇"은 적지 않는다. export 된 이름에는 이름으로 시작하는 문서 주석을 단다
- 내 변경으로 안 쓰게 된 import·변수·함수는 지운다
- 확장이 실제로 예상되는 곳(예: `LlmProvider`, 관리 API 핸들러 등록)만 기존 코드를 고치지 않고 더하게 만든다. 한 번만 쓰이는 코드에 인터페이스를 만들지 않는다
- 요청받지 않은 기능, 추측성 유연성, 일어날 수 없는 경우의 오류 처리를 넣지 않는다

## Go 관례

### 언어
- 의존성은 표준 라이브러리 우선이다. 외부 패키지를 더하려면 설계 문서 2장 "의존성" 줄에 이유를 적는다(지금 계획은 `pgx` 하나)
- 오류는 값이다. 예상되는 실패(예산 초과, 모델 없음)는 이름 붙은 오류(`var ErrX = errors.New(…)`)나 오류 타입으로 돌려주고 `errors.Is`·`errors.As` 로 가른다. `panic` 은 프로그래머 실수에만 쓴다
- 오류를 감쌀 때 `fmt.Errorf("…: %w", err)` 로 원인을 남긴다. 오류를 버리지 않는다(`_ =` 는 이유 주석과 함께만)
- 인터페이스는 쓰는 쪽 패키지에 작게 둔다. 바깥과 닿는 곳(업스트림 호출·Redis·시계)만 인터페이스로 받아 테스트에서 가짜로 바꾼다
- 비밀 값(키, 토큰)을 담는 타입은 `String()`·`LogValue()` 가 값을 드러내지 않게 한다
- 로그는 `log/slog` 구조 로그만 쓴다. 표준 `log` 와 `fmt.Print` 로그를 쓰지 않는다

### 동시성·취소
- **ctx 를 끝까지 넘긴다.** 요청 처리의 첫 인자는 `ctx context.Context` 이고 `r.Context()` 에서 시작한다. `context.Background()`·`TODO()` 는 조립 지점(`internal/app`, `cmd`)에서만 쓴다. 클라이언트가 끊으면 업스트림 호출과 DB 쿼리까지 취소돼야 하기 때문이다
- 업스트림 HTTP 요청은 `http.NewRequestWithContext` 로 만들고, 타임아웃을 정한 `*http.Client` 를 주입받는다. `http.DefaultClient`·`http.Get` 을 쓰지 않는다
- 요청보다 오래 사는 goroutine 을 띄우지 않는다. 백그라운드 작업(배치 기록, 예산 리셋)은 `app` 의 루트 ctx 아래에서 띄우고 종료 때 기다린다
- 정리할 자원은 `defer` 로 닫는다. 요청 ctx 가 취소된 뒤에도 해야 하는 일(슬롯 반납 등)은 `context.WithoutCancel` 로 한다
- `time.Sleep` 대신 ctx 와 함께 `select` 로 기다린다. 시간은 주입한 시계로 읽어 테스트에서 바꿀 수 있게 한다
- 여러 goroutine 이 건드리는 상태는 `sync.Mutex`·`atomic` 으로 지킨다. 채널이 가득 찰 수 있는 곳은 `select`+`default` 로 막히지 않게 한다
- 스트리밍은 받은 만큼 바로 쓰고 `Flush` 한다. 전체를 모아서 돌려주지 않는다

### 구조·테스트
- 코드는 모두 `internal/` 아래 모듈(`core`, `auth`, `limits`, `inference`, `admin`)과 조립 지점 `app` 에 둔다. 의존 방향은 `app → admin → inference → auth·limits → core` 이고 `internal/app/architecture_test.go` 가 검사한다
- 라우트는 `core.Route{Method, Path, Access, Handler}` 로만 내놓는다. 권한 선언 없는 라우트는 만들 수 없게 `app` 이 모두 `auth.Guard` 로 감싼다
- 테스트는 내부 구현이 아니라 동작(HTTP 응답·결과)을 본다. 업스트림·DB·시계는 가짜나 실제 컨테이너로 바꾼다
- `gofmt` 와 `go vet` 경고는 0 으로 유지한다

## 하네스(훅)

개발 표준과 아키텍처는 문서만이 아니라 훅으로 강제한다(`.claude/settings.json`, `.claude/hooks/`).

| 시점 | 훅 | 막는 것 |
|---|---|---|
| Edit·Write 직전 | `guard_edit.py` | 편집 결과 파일 전체를 검사한다. 모듈 의존 방향 위반, `cmd` 가 `app` 밖을 가져옴, 설계에 없는 모듈·`util` 류 패키지, 조립 지점 밖 `context.Background()`, `time.Sleep`, 기본 HTTP 클라이언트, 표준 `log`, 외부 URL, 커밋된 마이그레이션 수정, 마이그레이션의 행 쓰기 |
| 작업 종료 | `verify_on_stop.py` | 소스가 바뀌었으면 gofmt·vet·테스트(모듈 경계 포함)를 돌려 실패하면 끝내지 못하게 한다. 같은 상태는 다시 돌리지 않고, 연속 3번 막으면 사용자에게 넘긴다 |

- 훅이 막으면 우회하지 않는다. 규칙이 틀렸다고 보이면 사용자에게 알리고 훅과 이 표를 함께 고친다
- 훅을 고치면 `python3 .claude/hooks/test_guard_edit.py` 로 확인한다
- 편집 훅은 Edit·Write 도구에만 걸린다. 셸(heredoc 등)로 Go 파일을 쓰지 않는다. 그래도 종료 훅과 `check.sh` 가 같은 규칙을 다시 본다
