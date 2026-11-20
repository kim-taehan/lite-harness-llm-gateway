# LiteLLM 코드베이스 전체 분석

> 버전: v1.108.0
> 분석 대상: `<workspace>/litellm`
> 작성일: 2026-10-22

---

## 1. 프로젝트 개요

LiteLLM은 100개 이상의 LLM 제공자(OpenAI, Anthropic, Bedrock, Azure AI, Vertex AI, Gemini, Mistral, Groq, Cohere, Ollama 등)에 OpenAI호환 단일 API로 접근하게 해주는 Python 라이브러리이자, 이를 래핑하여 배포·운영하는 **Proxy Server**(FastAPI 기반)다. 두 가지 사용 경로가 coexist한다:

- **SDK 경로** (`litellm.main`): `litellm.completion(...)` / `litellm.embedding(...)` 형태로, Python 코드에서 직접 호출. 제공자별로 다른 클라이언트/SDK를 일관된 인터페이스로 노출
- **Proxy 경로** (`litellm.proxy`): `litellm --config config.yaml`으로 띄우는 HTTP 서버. OpenAI 호환 엔드포인트(`POST /v1/chat/completions` 등)를 제공하고, 그 위에 인증(API key, JWT, SSO), 권한(team/project/model access), 예산, 속도 제한, 라우팅/로드밸런싱/폴백, 스파인 추적, 가드레일, 관측(callbacks)을 얹는다

핵심 특징:
- **Provider-agnostic abstraction**: `litellm/llms/<provider>/` 폴더당 한 제공자. 각 제공자가 `BaseConfig` ABC(변환/토큰화)와 `handler.py`(실제 호출) 두 개를 구현
- **Router 기반 라우팅**: 같은 model group에 여러 deployment를 두고 `simple-shuffle`, `least-busy`, `usage-based-routing`, `latency-based-routing`, `cost-based-routing` 전략으로 분산
- **Proxy Server**: FastAPI app + 10여 개 미들웨어 + DB(Prisma, Postgres) + Redis + SSO/OAuth2 + 예산/스펜드 추적 + 가드레일
- **관측**: 50개 이상의 observability 통합(integrations/), cost 계산(cost.json 기반), OTEL, langfuse, arms, datadog, langsmith, helicone 등
- **확장**: 50개 이상의 integration, guardrail, hook, proxy endpoint 모듈

## 2. 디렉토리 구조

```
litellm/                              # 핵심 SDK + Proxy 소스 루트
├── main.py                           # SDK 진입점 (completion, acompletion, embedding 등)
├── router.py                         # Router 클래스 (16,728줄). deployment 선택, 폴백, 로드밸런싱
├── _logging.py                       # verbose_logger / verbose_proxy_logger 헬퍼
├── cost.json                         # 모델별 단위 비용 (token pricing)
├── cost_calculator.py                # completion_cost, token 계산
├── constants.py                      # 글로벌 상수 (API key, header, redis 키 등)
├── exceptions.py                     # ProviderRetryError, Timeout, Authentication, ...
├── types/                            # TypedDict, Pydantic 모델, CallTypes
│   ├── litellm_params.py             # LiteLLMLogging, CallTypes, ProviderConfigManager
│   ├── utils.py                      # ChatCompletionResponse, ModelResponse, TokenUsage
│   └── llms/                         # 제공자별 응답/요청 TypedDict (openai, anthropic, ...)
├── llms/                             # 159개 제공자 모듈
│   ├── base_llm/                     # BaseConfig ABC (chat/transformation.py 등)
│   ├── openai/                       # OpenAI (chat/handler.py + transformation.py)
│   ├── anthropic/
│   ├── bedrock/
│   ├── azure_ai/
│   ├── gemini/
│   └── ... (mistral, groq, cohere, ollama, ollama_vertex, ...)
├── litellm_core_utils/               # 제공자 검출, 비용, 로깅, 헬퍼
│   ├── get_llm_provider.py           # 모델명 → 제공자 매핑
│   ├── cost_calculator.py
│   └── litellm_logging.py            # Logging 클래스 (pre_call/post_call, async_success_handler)
├── proxy/                            # FastAPI 기반 Proxy Server
│   ├── proxy_server.py               # @app 정의, 미들웨어, 라우트 (21,897줄)
│   ├── proxy_cli.py                  # `litellm` CLI 진입 (Typer)
│   ├── proxy_config.py               # YAML config 파싱
│   ├── proxy_auth.py                 # legacy re-export
│   ├── auth/                         # user_api_key_auth.py, OAuth2, JWT, SSO
│   ├── db/                           # Prisma client, DB 트랜잭, 스패인 배치
│   ├── spend_tracking/               # 예산/스펜드 로그, 롤업
│   ├── hooks/                        # ratelimiter, guardrail, callback hook
│   ├── guardrails/
│   ├── client/                       # OpenAI-style client
│   ├── endpoints/ / *_endpoints.py   # 각 리소스(모델, 팀, 사용자, 비용 등) API
│   ├── middleware.py, health_check.py, telemetry.py, ...
├── integrations/                     # observability 통합 (langfuse, datadog, helicone, ...)
│   ├── custom_logger.py
│   ├── langfuse/
│   ├── datadog/
│   ├── langsmith/
│   ├── helicone/
│   └── ... (40개 이상)
├── caching/                          # RedisCache, InMemoryCache
├── llms/                             # (위와 동일)
├── model_prices_and_context_window_backup.json
├── proxy_auth/
└── ...
ui/litellm-dashboard/                 # Admin UI (React/Next.js)
schema.prisma                         # Prisma 스키마 (60+ 모델: LiteLLM_BudgetTable, TeamTable, UserTable, ...)
cookbook/                             # 예제 notebook
docs/ cookbook/                       # 사용자 문서
tests/                                # pytest
├── e2e/                              # end-to-end test
├── integration/
├── code_coverage_tests/
├── documentation_tests/
├── fake_openai_endpoint.py           # 목 OpenAI 서버
├── _vcr_conftest_common.py           # record/replay VCR
.github/workflows/                    # CI (e2e, codspeed, osv-scan, scorecard, publish)
└── pyproject.toml                    # 의존성, console_script = litellm
```

전체 규모: `litellm/proxy/proxy_server.py` ≈ 21.9k줄, `litellm/router.py` ≈ 16.7k줄, `litellm/main.py` ≈ 9k줄, schema.prisma 60개 모델, `litellm/llms/` 159개 제공자 디렉터리.

## 3. 핵심 아키텍처

### 3.1 SDK 호출 경로 (`litellm.main.completion`)

```
caller
  └── litellm.main.completion()                    (main.py:5319)
        ├── _get_llm_provider(...)                  # main.py:4644
        │     └── get_llm_provider(litellm.params.get_llm_provider)   (litellm_core_utils/)
        │           └── model_prices_and_context_window_backup.json / models/ 검색
        ├── response = provider_config.get_complete_endpoint(...)? / direct call
        │     ├── get_llm_provider(model) → provider_name ("openai", "anthropic", "bedrock", ...)
        │     ├── ProviderConfigManager().get_config(provider_name)   (litellm/utils.py)
        │           └── import litellm.llms.<provider>.chat.transformation  → BaseConfig 서브클래스
        │     └── provider_config.transform_request(request, api_key, api_base)
        │           └── httpx / aiohttp / httpx-async  call
        └── response  : ModelResponse  (litellm/types/utils.py)
```

동기/비동기(`acompletion`, main.py:452) 쌍이 있고, 응답은 `litellm.types.utils.ModelResponse`로 정규화된다. `completion_extras/`, `chat_completions/`, `embeddings/`, `messages/`, `assistants/` 등이 같은 패턴을 다시 사용.

### 3.2 Provider 변환 계층 (`BaseConfig`)

`litellm/llms/base_llm/chat/transformation.py:91`에 `BaseConfig(abc.ABC)`가 정의된다. 모든 제공자 `chat/transformation.py`는 이 ABC를 상속해 `transform_request` / `transform_response` / `get_supported_openai_params` / `validate_environment` / `transform_completion_messages` 같은 메서드를 구현한다. 이 단계를 거치면:

- **요청**: OpenAI 스키마 → provider 스키마 (예: Anthropic system, Bedrock anthropic messages, Gemini contents)
- **응답**: provider 스키마 → `ModelResponse` (id, object, choices[].message/finish_reason, usage)

`litellm/llms/anthropic/chat/handler.py`는 `httpx.AsyncClient` + `BaseAsyncLLM`를 조합해 `/v1/messages`를 호출하고 `BaseLLMException` 변환을 담당한다.

### 3.3 Router — 로드밸런싱·폴백 (`litellm/router.py:1156`)

`Router` 클래스는 model group → deployment 목록을 유지하고 다음을 제공한다:

- `acomp()` / `rcompletion()` (`:3301`) — async completion
- `_select_deployment_async` (`:2333`) — 전략별 선택:
  - `simple-shuffle`: 랜덤 (기본값)
  - `least-busy`: 현재 인플라이트 요청 수 기준
  - `usage-based-routing(-v2)`: LowestTPMLoggingHandler 기반
  - `latency-based-routing`: LatencyBasedRoutingHandler
  - `cost-based-routing`: LowestCostLoggingHandler
- 폴백: `fallbacks`, `content_policy_fallbacks`, `context_window_fallbacks`, `model_group_fallbacks` — 특정 deployment 실패 시 다음 group으로 전환
- cooldown / allowed_fails: 실패한 deployment를 일시 제외
- `RedisCache`(redis_url)로 상태 공유 → 다중 인스턴스 클러스터

### 3.4 Proxy Server (`litellm/proxy/proxy_server.py`)

```
litellm --config config.yaml   (console_script → litellm.proxy.proxy_cli:run)
  └── litellm.proxy.proxy_server
        ├── app = FastAPI(...)                    (:1968)
        ├── 미들웨어 체인 (가장 바깥에서 안으로):
        │     1. GZipBufferedResponseMiddleware
        │     2. SecurityHeadersMiddleware
        │     3. InFlightRequestsMiddleware
        │     4. LensInternalMiddleware
        │     5. RedisRequestBatchMiddleware
        │     6. BudgetReservationReleaseMiddleware
        │     7. TelemetryMiddleware
        │     8. BillableRequestMetricsMiddleware
        │     9. PrometheusAuthMiddleware
        │    10. CORSMiddleware
        │    11. RequestBodyDecompressionMiddleware   (premium)
        ├── 라우트: @app.post("/v1/chat/completions"), @app.websocket("/v1/realtime") 등
        ├── auth: user_api_key_auth.py → UserAPIKeyAuth 객체
        └── DB: Prisma client (Postgres), Redis
```

### 3.5 인증 흐름 (proxy)

`litellm/proxy/auth/user_api_key_auth.py:1`에서 시작. `APIKeyHeader(x-api-key)`로 키를 받아 (1) Prisma `LiteLLM_VerificationToken` 조회 또는 Redis 캐시, (2) JWT/SSO 검증(`handle_jwt.py`, `oauth2_check.py`), (3) 팀/프로젝트/사용자/모델 접근 권한(`auth_checks.py`, `model_access_denied.py`), (4) 예산·소프트 예산(`fallback_budget.py`), (5) IP/네트워크(`trusted_proxy_utils.py`) 체크. 결과 `UserAPIKeyAuth`(proxy/_types.py)가 각 엔드포인트에 주입.

## 4. 주요 시스템

### 4.1 제공자 변환 (`llms/`)

159개 제공자 디렉터리. 공통 구조:
- `chat/transformation.py` — `BaseConfig` 서브클래스 (`OpenAIConfig`, `AnthropicConfig`, `BedrockConfig` ...)
- `chat/handler.py` — 실제 HTTP 호출 (동기/비동기)
- `embedding/`, `image_generation/`, `audio/`, `files/`, `fine_tuning/`, `batch_completion/` 등 — 다른 API에 같은 패턴
- `common_utils.py` — 공통 헬퍼
- `cost_calculator/` (필요시)

### 4.2 비용/토큰 계산 (`cost_calculator.py`, `cost.json`)

`completion_cost(response)`는 `cost.json`(모델별 input/output/cache read/write 토큰 단가)를 사용해 cost를 계산. `litellm/token_counter`에서는 tiktoken 기반 tokenizer(모델별) + `tokenizer.py` 폴백으로 토큰을 세며, provider-specific tokenizer(`bedrock/`, `gemini/`)는 각 `llms/<provider>/completion/`에 있다.

### 4.3 관측/로깅 (`integrations/`, `litellm_core_utils/litellm_logging.py`)

`Logging` 클래스(`:611`)는 요청별 컨텍스트(model, messages, litellm_call_id, api_key)를 유지하며:
- `pre_call()` (`:1548`) — 요청 전에 callback pre_call
- `post_call()` (`:1761`) — 응답 후 cost, latency 계산
- `async_success_handler()` (`:3695`) — 성공 시 callback (langfuse, datadog, sls, ...)
- `async_failure_handler()` (`:4408`) — 실패 시 callback

`integrations/callback_configs.json`은 callback 이름 → 모듈/클래스 매핑. `LiteLLMLoggingCallbackManager`가 콜백 객체 pool을 관리(50+ 통합).

### 4.4 예산·스펜드 추적 (`proxy/spend_tracking/`)

- `baseline_accounting.py` — 스페인 로그, 데일리 롤업
- `spend_capture_rate.py` — capture rate 계산
- `budget_reservation.py` — 예산 예약/해제
- `ptu_flat_cost_rollup.py` — provisioned throughput
- `daily_spend_bulk_upsert.py`, `model_usage_rollup.py` — 데일리 집계
- DB: `LiteLLM_SpendLogs`, `LiteLLM_DailyUserSpend`, `LiteLLM_DailyTeamSpend`, `LiteLLM_DailyOrganizationSpend`, `LiteLLM_DailyModelUsage` 등

### 4.5 속도 제한 (`proxy/hooks/`)

`dynamic_rate_limiter.py`(+v3), `parallel_request_limiter.py`, `batch_rate_limiter.py`, `max_budget_per_session_limiter.py` — Redis/DB 기반 TPS, parallel, budget.

### 4.6 가드레일 (`proxy/guardrails/`)

LlamaGuard, Presidio, custom guardrail 호출 — pre/post model call에 텍스트 인젝션/민감 데이터 필터.

## 5. 데이터 모델

Prisma 스키마(`schema.prisma`)는 60개 모델:

- **인증**: `LiteLLM_VerificationToken`, `LiteLLM_JWTKeyMapping`, `LiteLLM_UserTable`, `LiteLLM_TeamTable`, `LiteLLM_ProjectTable`, `LiteLLM_DeletedVerificationToken`
- **구조**: `LiteLLM_BudgetTable`, `LiteLLM_OrganizationTable`, `LiteLLM_ProxyModelTable`, `LiteLLM_ObjectPermissionTable`, `LiteLLM_EndUserTable`
- **스펜드/로깅**: `LiteLLM_SpendLogs`, `LiteLLM_ErrorLogs`, `LiteLLM_Daily*` (10개)
- **MCP**: `LiteLLM_MCPServerTable`, `LiteLLM_MCPToolsetTable`, `LiteLLM_MCPUserCredentials`
- **에이전트**: `LiteLLM_AgentsTable`, `LiteLLM_AgentIdentity`, `LiteLLM_VerifiedSubject`
- **설정**: `LiteLLM_Config`, `LiteLLM_TeamMembership`, `LiteLLM_OrganizationMembership`, `LiteLLM_AuditLog`

`litellm.types.utils`에는 SDK 레벨 Pydantic/TypedDict: `ModelResponse`, `ChatCompletionResponse`, `TokenUsage`, `CompletionResponse`, `EmbeddingResponse`. `proxy/_types.py`는 Proxy 전용 `UserAPIKeyAuth`, `LiteLLM_VerificationToken`, `ProxyConfig`...

## 6. 진입점

### 6.1 CLI — `litellm`
`pyproject.toml`의 `scripts`: `litellm = "litellm.proxy.proxy_cli:run"`. `Typer` 기반 CLI가 `--config`, `--host`, `--port`, `--db_url`, `--master_key`, `--detailed_debug`, `-v` 등을 받아 `uvicorn`으로 `proxy_server:app`을 띄운다.

### 6.2 SDK — `litellm.completion`
`litellm.main.completion()` (`:5319`) — 동기, `acompletion()` (`:452`) — 비동기. `embedding`(`:6544`), `rerank`, `transcription` 등도 `main.py`에.

### 6.3 HTTP 엔드포인트
`app.post("/v1/chat/completions")`, `app.get("/v1/models")`, `app.post("/v1/embeddings")`, `app.websocket("/v1/realtime")`, `app.get("/health/liveliness")`, `app.post("/key/generate")`, `app.post("/team/...")`, `app.post("/user/...")` 등 — `proxy_server.py`에 직접 `@app` 데코레이터로, 일부는 `*_endpoints.py` 모듈에서 `APIRouter`로 분리.

## 7. 의존성 (`pyproject.toml`)

핵심 런타임 (필수):
- `fastuuid`, `httpx`, `openai (>= 2.8.0)`, `tiktoken`, `click`, `pydantic (>= 2.7.2)`, `importlib-metadata`
- `python-dotenv`, `tokenizers (>= 0.13.2)`, `uvloop`, `aiohttp`, `httpx-sse`, `jinja2`(선택)
- `boto3 (>= 1.27.20)`, `azure-identity (>= 1.15.0)`, `azure-storage-blob`, `azure-servicebus`(선택)
- `google-cloud-aiplatform (>= 1.88.0)`, `google-cloud-storage (>= 2.13.0)`, `db-dtypes`, `sqlalchemy`, `pyyaml`
- `posthog (>= 3.7.0)`, `opentelemetry-* 1.33.1`, `grpcio-status`

proxy (optional): `fastapi >= 0.115.9`, `starlette >= 0.40.0`, `uvicorn[standard]`, `psycopg (>= 3.0.0)`, `psycopg-pool (>= 3.0.0)`, `redis`, `slowapi`, `opentelemetry-instrumentation-fastapi`, `prisma`, `prisma-schema-diff`

개발: `pytest`, `pytest-asyncio`, `pytest-recording`, `httpx`, `vcrpy`, `ruff`, `mypy`, `black` (line-length 150)

## 8. 테스트와 CI/CD

`tests/`는 pytest 기반:
- `e2e/` — end-to-end (record/replay VCR + `_fake_openai_endpoint_server.py`)
- `integration/` — 실 API 통합
- `code_coverage_tests/` — 커버리지
- `documentation_tests/` — cookbook/docs 코드 검증
- `_vcr_conftest_common.py` + `httpx_vcr`로 record/replay
- `fake_openai_endpoint.py` — 목 OpenAI 서버

`.github/workflows/` (30개+):
- `e2e_record_replay.yml` — VCR record/replay
- `test-*.yml` (multiple: unit, e2e, proxy, redis-chaos)
- `codspeed.yml` — 벤치마크
- `osv-scan.yml`, `image-scan.yml`, `codeql.yml`, `scorecard.yml` — 보안
- `sync-schema.yml`, `check-schema-sync.yml` — Prisma 스키마 동기화 검증
- `publish-lint-base-counts.yml`, `conventional-commits.yml`
- `create-release-branch.yml`, `create-rc-branch.yml` — 릴리스

## 9. 문서

- `docs/` + `cookbook/` — Jupyter notebook 기반 공식 문서
- `cookbook/` 안의 Python/Shell snippet들은 `documentation_tests/`에서 실행되어 검증
- `README.md` — 전체 구조·설치·사용법
- UI: `ui/litellm-dashboard/` (React/Next.js) + Swagger UI(`/swagger`) — `proxy_server.py:2832` `mount_swagger_ui()`가 lazy-load 플러그인을 주입
- `examples/` — 배포 예시

## 10. 설계 특징

1. **Provider plugin 아키텍처**: `litellm/llms/<provider>/` 디렉터리가 자체 변환/핸들러를 가지므로 새 제공자 추가가 격리. `ProviderConfigManager`는 런타임에 import하여 `BaseConfig` 서브클래스를 가져온다.
2. **Router / Deployment 단위 라우팅**: 한 model group에 여러 deployment. 전략(strategy) 플러그인(`LatencyBasedRoutingHandler`, `LowestCostLoggingHandler` ...)을 통해 확장. 폴백/컨텐트-포lici/컨텍스트-윈도우 폴백을 model 그룹별로 설정.
3. **미들웨어 우선 Proxy**: Starlette 미들웨어 10여 개가 요청-응답 주위를 싸서 인증/관측/예산/메트릭을 분리. 각 미들웨어가 독립적이도록 `factory=lambda`로 지연 초기화(proxy_server.py:2791, 2807).
4. **Prisma + Redis**: 구조화 데이터(User, Team, Budget, SpendLog)는 Postgres(Prisma), 상태(cooldown, rate-limit, in-flight)는 Redis. `db_url` 설정 시에만 DB 의존.
5. **콜백 기반 관측**: 50+ 통합이 `CustomLogger` 인터페이스를 구현. `callback_configs.json`이 이름→모듈 매핑, `LiteLLMLoggingCallbackManager`가 pool 관리 → 런타임 등록/제거 가능.
6. **VCR record/replay CI**: `tests/_vcr_conftest_common.py` + `httpx_vcr`로 실 API 트래픽을 녹화/반복. 외부 API 의존 없이 CI.
7. **스위프트/동기 이중 지원**: 모든 주요 엔트리(`completion`, `embedding`, `transcription`, `image_generation`)가 sync/async 쌍을 제공.
8. **lazy import & lazy feature**: `_lazy_imports.py`, `_lazy_features.py` — 대용량 모듈을 지연 import하여 부팅 시간과 메모리 절감. Swagger UI도 lazy-load 태그별 로딩(proxy_server.py:2832-2916).
