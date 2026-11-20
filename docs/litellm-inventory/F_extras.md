# F. 부가 기능·확장·외부 호출 전수조사 (litellm_airgap 브랜치, 2026-11-20)

범위 표기: 필수=1차 릴리스, 후순위=운영 요구가 생기면, 제외=새 게이트웨이에 안 만듦. "확인 못함"은 코드를 끝까지 못 따라간 것.
LOC 는 `.py` 줄 수 대략치(규모 감 잡기용).

## 0. 한눈에 (결론)
- 기본으로 바깥에 나가는 호출(설정 없이 켜짐)은 6곳: 모델 가격표, 블로그 RSS, Anthropic beta 헤더 설정, 오토라우터 프리셋, 정책 템플릿, GitHub 최신 릴리스. 모두 실패하면 번들본으로 폴백하지만 **시도는 한다**. fork 에 이를 끄는 변경이 없다(Dockerfile·docker-dev·gateway/ 에서 `LITELLM_LOCAL_*` 설정 0건 확인).
- 사용량 telemetry 는 기본 꺼짐(OFF). 새로 생긴 `litellm/telemetry/` 모듈이며 consent 기본 OFF, HTTP 전송은 `LITELLM_TELEMETRY_ENDPOINT` 를 직접 줘야만 가능.
- 라이선스 서버 호출은 `LITELLM_LICENSE` 를 넣었을 때만. 미설정이면 호출 0.
- 새 게이트웨이는 위 6곳을 아예 구현하지 않으면 된다(번들 JSON 만 쓰면 끝).

## 1. 외부로 나가는 호출 전수 (폐쇄망 최우선)

### 1-A. 설정 없이 기본으로 켜짐 (기본 활성)
| 기능 | 구현 위치 | 동작 요약 | 기본 | 외부/유료 | 새 GW 권장 |
|---|---|---|---|---|---|
| 모델 가격표 원격 fetch (import 시점) | litellm/__init__.py:410-412,552 · litellm_core_utils/get_model_cost_map.py:743-770 | `import litellm` 때 `raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json` GET(타임아웃 5초). 실패 시 번들 JSON, 재시도 스레드 기동. `LITELLM_LOCAL_MODEL_COST_MAP=true` 이거나 CLI 프로세스면 로컬만 | ON | 외부(GitHub) | 제외. 번들/운영자 입력 가격표만 |
| 가격표 주기 재fetch | proxy_server.py:9059, 20884 (+ `/reload/model_cost_map` 계열) | DB 스케줄(`pod_reload_is_due`)이 설정돼야 재fetch. 수동 reload 엔드포인트도 같은 URL 사용 | 스케줄 설정 시 / 수동 호출 시 | 외부(GitHub) | 제외 |
| 블로그 RSS | litellm/__init__.py:414-417 · litellm_core_utils/get_blog_posts.py:95,132 · proxy/public_endpoints/public_endpoints.py:517-531 | UI 홈/네비바가 `/public/litellm_blog_posts` 호출 -> 서버가 `https://docs.litellm.ai/blog/rss.xml` GET(5초, 1시간 캐시). 실패 시 번들 blog_posts.json. 끄는 값은 `LITELLM_LOCAL_BLOG_POSTS=true` | ON(UI 열면 발생) | 외부 | 제외 (UI에서도 제거) |
| GitHub 최신 릴리스 | proxy/ui_crud_endpoints/latest_release_endpoints.py:20-25,140 ; proxy_server.py:21504 | UI 가 호출하면 `api.github.com/repos/BerriAI/litellm/releases/latest` 및 tag별 GET(5초, 1시간 캐시). **끄는 env 없음** | ON(UI 열면 발생) | 외부 | 제외 |
| Anthropic beta 헤더 설정 | litellm/__init__.py:418-420 · anthropic_beta_headers_manager.py:148,193 | 최초 사용 시 raw.githubusercontent.com 에서 JSON GET, 실패 시 번들. `LITELLM_LOCAL_ANTHROPIC_BETA_HEADERS=true` 로 끔 | ON(첫 사용 시) | 외부 | 제외 (Anthropic 직접 호출 자체가 범위 밖) |
| 오토라우터 프리셋 | litellm/__init__.py:422-424 · public_endpoints.py:556-575 | 프리셋 카탈로그를 GitHub raw 에서 GET, 실패 시 번들. `LITELLM_LOCAL_AUTOROUTER_PRESETS=true` | ON(호출 시) | 외부 | 제외 |
| 정책 템플릿 | management_endpoints/policy_endpoints/endpoints.py:625,655-672 | UI 정책 템플릿을 GitHub raw 에서 GET, 실패 시 로컬. `LITELLM_LOCAL_POLICY_TEMPLATES=true` | ON(호출 시) | 외부 | 제외 |

### 1-B. 설정/조건으로만 켜짐
| 기능 | 구현 위치 | 동작 요약 | 기본 | 외부/유료 | 새 GW 권장 |
|---|---|---|---|---|---|
| 사용량 telemetry(신규) | litellm/telemetry/*.py, proxy/telemetry/settings.py:34-60 · runtime.py:81-84,149-153 · proxy_server.py:1785-1805 | 그룹별 consent(heartbeat/request_success/token_info/…). 목적지 `local_table`(DB) 또는 `http`. http 는 `LITELLM_TELEMETRY_ENDPOINT` 필수. 5분 주기 flush. `LITELLM_TELEMETRY_DISABLED=true` 면 거부권. `--telemetry` CLI 는 no-op(proxy_cli.py:860) | OFF (consent 기본 OFF, endpoint 기본 None) | endpoint 설정 시 외부 | 제외. 필요하면 로컬 사용량 집계만 |
| 라이선스 온라인 검증 | proxy/auth/litellm_license.py:32,60-98,176-223 | 서명 오프라인 검증 먼저, 실패 시 `https://license.litellm.ai/verify_license/{key}` GET(3회). `LITELLM_LICENSE` 없으면 호출 0 | OFF(키 없으면) | 외부 | 제외 (라이선스 개념 자체 없앰) |
| HuggingFace config/tokenizer | litellm/utils.py:2700-2712,2791,6051,6492 | 특정 모델(llama·command-r 등) 토큰 계산 시 `from_pretrained`, `huggingface.co/{model}/raw/main/config.json` GET | 해당 모델 쓸 때만 | 외부 | 제외 (토큰 수는 Ollama 응답 usage 사용) |
| tiktoken | litellm_core_utils/default_encoding.py:17-35 | 번들 캐시 디렉터리를 강제 지정 -> 오프라인 | ON, 로컬 | 없음 | 후순위(사용량 없는 스트림 토큰 추정용; JVM 토크나이저 필요) |
| 외부 observability/로깅 콜백 | 2-절 | langfuse/datadog/otel 등. 모두 `success_callback` 에 넣어야 | OFF | 외부 | 제외(OTLP 정도만 후순위) |
| 외부 가드레일 SaaS | 3-절 | lakera/aporia/bedrock 등 | OFF | 외부 | 제외 |
| 슬랙/이메일 알림 | integrations/SlackAlerting/slack_alerting.py(2116줄), email_alerting.py, email_templates/ | 예산·장애 알림. 이메일 푸터는 litellm.ai 링크 | OFF | 외부(Slack/SMTP/Resend/SendGrid) | 후순위(내부 웹훅·SMTP 하나) |
| ROI 계산기 OAuth | proxy/roi_calculator/*, types/roi_calculator.py:36-38 | gitlab.com/github.com API 로 OAuth·통계 | OFF(설정 시) | 외부 | 제외 |
| 자동 MCP 서버 카탈로그 | root `mcp_servers.json`(zapier·deepwiki 등 외부 URL), proxy/mcp_registry.json | 번들 JSON. 서버가 직접 fetch 하는 코드는 확인 못함 | 확인 못함(번들 데이터) | URL이 외부 | 제외 |
| UI 로고/파비콘 | proxy_server.py:19014-19046,19141 | `UI_LOGO_PATH` / `LITELLM_FAVICON_URL` 이 http URL 이면 클라이언트에 리다이렉트 | 로컬 파일이 기본 | 설정 시 외부 | 필수(로컬 파일만) |
| 이메일/슬랙 로고 | types/integrations/slack_alerting.py:40 (`litellm-listing.s3.amazonaws.com`) | 알림 메시지에 로고 URL 박힘(수신 측이 로드) | 알림 켜면 | 외부 | 제외 |

### 1-C. 실제 호출은 아니고 외부 주소 문자열만 있는 것 (오탐 방지)
- litellm/exceptions.py:127,152,202,607,1045,1121 · a2a_protocol/exceptions.py:39 · route_llm_request.py:137 · fine_tuning/main.py:326…: `httpx.Request(url="https://litellm.ai"/github.com…)` 는 예외 객체를 만들 때 쓰는 가짜 요청. 네트워크 안 탐.
- budget_manager.py:40 `api.litellm.ai`: 레거시 BudgetManager(hosted) 클래스. proxy 경로 사용 여부 확인 못함 -> 새 GW 제외.
- main.py:3490,3595,6997 OpenRouter·Vercel 의 `HTTP-Referer: litellm.ai` 헤더 값(해당 provider 호출 시에만).
- UI: layout.tsx:2 `next/font/google` -> **빌드 시점**에 폰트를 내려받아 정적 포함(런타임 fetch 아님). 빌드 환경이 인터넷 필요. UI 소스 내 외부 링크(docs.litellm.ai 238건, models.litellm.ai/cookbook leftnav.tsx:380, 로그인 페이지 docs 링크 LoginPage.tsx:251, forms.gle, calendly)는 `<a>`/window.open 이라 **클릭해야 나감**. 이미지·CDN 런타임 로드 코드는 못 찾음(`<img src=https://…>` 0건).
- 빌드/이미지 시점(런타임 아님): Dockerfile:23 pgbouncer 다운로드, npm ci, apk(packages.wolfi.dev), prisma generate(바이너리를 이미지에 굽고 런타임엔 log-only: prisma_migration.py:4,26-32).
- 반드시 막아야 할 사용자 입력 기반 fetch: 메시지 속 이미지 URL, pass-through, MCP 서버 URL, OpenAPI->MCP(petstore 예시) 등은 운영자가 등록한 대상으로 제한 필요.

## 2. callbacks · integrations (litellm/integrations/, 기본은 전부 OFF)
기본값 근거: `litellm.success_callback/failure_callback/input_callback/service_callback = []` (litellm/__init__.py:95-98). 켜는 법은 config `litellm_settings.success_callback`. 허용 문자열 목록은 `_custom_logger_compatible_callbacks_literal` (__init__.py:102-161, 약 58종), UI용 메타 `integrations/callback_configs.json`(19개 항목).

| 기능 | 구현 위치 | 동작 요약 | 기본 | 외부·유료 | 새 GW 권장 |
|---|---|---|---|---|---|
| Prometheus | integrations/prometheus.py, prometheus_metrics_endpoint.py, prometheus_helpers/, middleware/prometheus_auth_middleware.py:37-51 | `/metrics` 노출, 메트릭 86종(types/integrations/prometheus.py). `require_auth_for_metrics_endpoint=True` 가 기본(litellm/__init__.py:173). 콜백 `prometheus` 일 때만 마운트(proxy_server.py:6755) | OFF(콜백 켜야) | 내부(스크레이프) | **필수**: 요청수·토큰·지연·오류·예산 잔여 소수 지표 |
| OpenTelemetry | integrations/opentelemetry.py:391-401, otel/ (mount.py 등) | 트레이스/로그. exporter 기본 `console`, endpoint 는 env | OFF | 설정한 collector | 후순위(OTLP 한 가지) |
| Langfuse / langfuse_otel | integrations/langfuse/, otel/langfuse_logger.py | 트레이스 전송. host env 로 지정 가능(langfuse.py:300-310) | OFF | 클라우드 기본, self-host 가능 | 후순위(사내 Langfuse 쓸 때만) |
| S3 / S3_v2 | integrations/s3.py, s3_v2.py | 로그 JSON 을 S3 에 업로드 | OFF | AWS(또는 사내 S3 호환) | 후순위 |
| GCS bucket / pubsub | integrations/gcs_bucket/, gcs_pubsub/ | 로그를 GCS 로. **premium 게이트**(gcs_bucket.py:49,59) | OFF | GCP·유료 | 제외 |
| Azure storage / Sentinel | integrations/azure_storage/(premium_user_check :354), azure_sentinel/ | 로그 전송 | OFF | Azure·유료 | 제외 |
| Datadog (logs·metrics·LLM obs) | integrations/datadog/ , dd_span_tagger | `DD_SITE` 로 전송 | OFF | 외부 SaaS | 제외 |
| Arize·Phoenix·Braintrust·Langsmith·Opik·Galileo·Helicone·Lunary·Traceloop·Weave·Logfire·Literal·Athina·Argilla·Mlflow·Deepeval·PostHog·AgentOps·Levo·NewRelic·Signoz·Greenscale·Supabase·PromptLayer·WeightsBiases·Openmeter·Lago 등 | integrations/*.py, 디렉터리 각각 | 각 SaaS 로 로그/비용 전송 | OFF | 외부 SaaS | 제외 |
| 비용 export(CloudZero·Focus·Mavvrik·Vantage·PointFive·Ternary) | integrations/cloudzero/, focus/, mavvrik_focus/, vantage/, pointfive/, ternary/ | 사용량을 FinOps SaaS 로 | OFF | 외부 | 제외 |
| generic_api (임의 HTTP 로 로그 POST) | integrations/generic_api/ | 설정한 URL 로 로그 | OFF | 설정한 URL(내부 가능) | 후순위(사내 수집기 연동 시) |
| custom callback(파이썬 클래스) | integrations/custom_logger.py, proxy/custom_hooks/ | 사용자 파이썬 훅 | OFF | 없음 | 제외(JVM 에선 SPI 로 대체 시 후순위) |
| DynamoDB / SQS / ClickHouse / Zerobus | integrations/dynamodb.py, sqs.py, clickhouse/, zerobus/ | 로그 sink | OFF | 외부 | 제외 |
| 스펜드 로그(DB) | proxy_server.py:3007 `disable_spend_logs=False`, 11302 큐 모니터, 8093-8111 정리 job | LiteLLM_SpendLogs 에 요청별 비용 기록. 기본 ON | **ON** | 내부 DB | **필수**(사용량 회계는 핵심) |
| 전용 로그 포맷 | _logging.py:644-660,735 | `JSON_LOGS=true`(엄격히 "true"만)면 JsonFormatter. `LITELLM_LOG` 기본값 **DEBUG**(:660) | 텍스트, DEBUG | 없음 | 필수: JSON 구조 로그, 기본 INFO |
| 이메일 알림 / Slack 알림 | email_alerting.py, SlackAlerting/ (주간·월간 리포트 job: proxy_server.py:11911-11948) | 예산 임계·장애·주간 리포트 | OFF(alerting 설정 시) | 외부 | 후순위 |
| 프로토타입성: shadow_eval, adaptive_concurrency, prompt_layer 등 | integrations/ | 보조 | OFF | 확인 못함 | 제외 |

## 3. guardrails · hooks
guardrail_hooks 디렉터리 약 64개(litellm/proxy/guardrails/guardrail_hooks/). 레지스트리 `guardrail_registry.py:226`. config 의 `guardrails:` 목록에 넣어야 작동. **기본 가드레일 0개**(init_guardrails.py 의 default_on 도 설정으로만).

| 기능 | 구현 위치 | 동작 요약 | 기본 | 외부·유료 | 새 GW 권장 |
|---|---|---|---|---|---|
| litellm_content_filter | guardrail_hooks/litellm_content_filter/ (~2.5k줄), content_filter_data/ | 로컬 정규식·키워드·카테고리로 차단/마스킹 | OFF | 로컬, 외부 0 | **필수**(폐쇄망 내 유일한 완전 로컬 필터, 키워드+정규식 정도로 단순화) |
| presidio (PII) | guardrail_hooks/presidio.py:259-340 | analyzer/anonymizer API(`presidio_*_api_base`)를 호출해 PII 마스킹 | OFF | 사내 서버 필요(Presidio 컨테이너) | 후순위(PII 마스킹 요구 시 사내 Presidio 연동) |
| block_code_execution | guardrail_hooks/block_code_execution/ | 코드블록 언어별 차단/마스킹 | OFF | 로컬 | 후순위 |
| tool_permission / tool_policy | guardrail_hooks/tool_permission.py, tool_policy/ | 도구 호출 허용/차단 | OFF | 로컬 | 후순위 |
| custom_code | guardrail_hooks/custom_code/ (~1.9k줄) | 사용자 파이썬 코드를 가드레일로 실행(샌드박스) | OFF | 로컬 | 제외(임의 코드 실행 위험) |
| generic_guardrail_api | guardrail_hooks/generic_guardrail_api/ | 사내 HTTP 가드레일 서비스 호출 | OFF | 설정 URL | 후순위(사내 서비스 연동 표준 인터페이스로 유용) |
| llm_as_a_judge / decision_model / semantic_guard | guardrail_hooks/llm_as_a_judge/, decision_model/, semantic_guard/ | 모델(LLM·임베딩)에게 판정 | OFF | 내부 모델 가능 | 제외 |
| unified_guardrail / mcp_* / llm_shield_proxy / headroom / compresr | guardrail_hooks/unified_guardrail/(1.5k줄), mcp_security, mcp_end_user_permission, mcp_jwt_signer, llm_shield_proxy(1.7k줄), headroom, compresr | 공통 처리, MCP 보안, 압축 | OFF | 일부 외부 SaaS | 제외 |
| 외부 SaaS 가드레일 약 40종 | lakera_ai(_v2), aporia_ai, aim, airia, akto, alice, azure, bedrock_guardrails, cato_networks, cisco_ai_defense, crowdstrike_aidr, deepkeep, dynamoai, enkryptai, grayswan, guardrails_ai, hiddenlayer, ibm_guardrails, javelin, lasso, model_armor, noma, onyx, openai(moderation), ovalix, pangea, panw_prisma_airs, pillar, prompt_security, promptguard, qohash, qualifire, repelloai, rubrik, singulr, straiker, microsoft_purview, vigil_guard, wingback, xecguard, zscaler_ai_guard, conduct(PyPI), agent_365, typesafe 등 | 각 SaaS 로 텍스트 전송 | OFF | **외부 SaaS 전부** | 제외(폐쇄망 원칙 위반) |
| 프롬프트 인젝션 감지 | proxy/hooks/prompt_injection_detection.py(283줄) | 휴리스틱+(선택) LLM/유사도 검사 | OFF | 로컬 휴리스틱 | 후순위 |
| Azure content safety | proxy/hooks/azure_content_safety.py | Azure API | OFF | 외부 | 제외 |
| 정책 엔진 | proxy/policy_engine/ (~5.3k줄) + 템플릿 fetch(1-A) | 팀/키별 가드레일 묶음 정책 | OFF | 템플릿만 외부 | 제외(가드레일 확정 후 재검토) |
| 가드레일 관리 API·사용량 | guardrails/guardrail_endpoints.py, usage_endpoints.py, usage_tracking.py | CRUD·통계 | ON(라우트) | 내부 | 후순위 |
| 항상 도는 proxy hooks | proxy/hooks/__init__.py:39-50 (PROXY_HOOKS 10개) | parallel_request_limiter(v3), cache_control_check, responses_id_security, litellm_skills, max_iterations_limiter, max_budget_per_session_limiter, sensitive_data_routing, prompt_cache_prediction, autorouter_baseline_cache, daily_token_limiter. 일부는 설정 있을 때만 동작 | ON(hook 등록) | 내부 | **필수**: rate limit(RPM/TPM/동시성), 예산 한도 / 후순위: 나머지 |

## 4. enterprise/ 와 라이선스 잠금
| 기능 | 구현 위치 | 동작 요약 | 기본 | 유료 | 새 GW 권장 |
|---|---|---|---|---|---|
| premium 판정 | proxy_server.py:1106-1108, proxy/auth/litellm_license.py | `LITELLM_LICENSE`(RSA-PSS 서명, public_key.pem 동봉) 없으면 `premium_user=False` (fail-closed). `premium_user` 참조 파일 약 50개 | False | 유료 | 제외. 게이트 자체 없음 |
| 유료 콜백 가드(문자열 콜백) | proxy/common_utils/callback_utils.py:202-322 | llamaguard, secret_detection, openai_moderations, google_text_moderation, llmguard, blocked_user_check, banned_keywords 는 premium 아니면 예외 | 차단 | 유료 | 제외 (banned_keywords/blocked_user 는 content_filter 로 흡수) |
| enterprise_hooks | enterprise/enterprise_hooks/ (aporia_ai, banned_keywords, blocked_user_list, google_text_moderation, openai_moderation) | 위 가드 구현체 | 차단 | 유료+외부 일부 | 제외 |
| litellm_enterprise 콜백 | enterprise/litellm_enterprise/enterprise_callbacks/ (llama_guard, llm_guard, secret_detection 717줄, callback_controls, pagerduty, send_emails, secrets_plugins) | LLM Guard 서버(`LLM_GUARD_API_BASE`) 등. callback_controls 는 요청 헤더로 콜백 비활성(premium) | 차단 | 유료 | 제외(secret_detection 패턴만 후순위 참고) |
| enterprise 라우트 | enterprise/litellm_enterprise/proxy/enterprise_routes.py, audit_logging_endpoints.py, management_endpoints/(project, internal_user), auth/custom_sso_handler.py, vector_stores/, ui_crud_endpoints/ | 감사 로그 API, 프로젝트, 커스텀 SSO 등. 라우터는 항상 include(proxy_server.py `enterprise_router`) 이지만 내부에서 premium 검사 | 라우트 노출, 기능 차단 | 유료 | 후순위: 감사 로그(누가 키·팀·예산을 바꿨나)는 운영자 요구 |
| liteadmin (Slack 연결) | enterprise/litellm_enterprise/proxy/liteadmin.py | Slack 워크스페이스 OAuth 연결 HTML | 확인 못함(premium 검사 있음) | 유료+외부 | 제외 |
| enterprise 라이선스 문구 | enterprise/LICENSE.md | 운영 사용 시 BerriAI 약관 필요 | - | 유료 | 제외 (새 GW 에 코드 안 가져오면 무관, 포크 유지 시에는 법무 확인 필요) |
| 주요 premium 항목 | PREMIUM_WALL_REVIEW.md 에 83개 목록. 이번에 확인한 것: SSO 5명 초과 제한(ui_sso.py:1077-1121, 5154), 코드 gating enforced_params(litellm_pre_call_utils.py:3738), 동적 콜백 비활성(:1282), team admin 지정(team_endpoints.py:4022), 비밀관리자 hashicorp/cyberark/google, SCIM, 감사로그, 파인튜닝 | | | 유료 | 새 GW 는 게이트 0, SSO 인원 제한 없음 |

## 5. MCP · A2A · agent · skills · prompt · vector · rag · search · sandbox
(모두 설정/DB 등록이 있어야 의미 있음. 새 GW 1차 범위에서는 추론+키+팀+예산이 우선)

| 기능 | 구현 위치 | 동작 요약 | 기본 | 외부·유료 | 새 GW 권장 |
|---|---|---|---|---|---|
| MCP 서버 게이트웨이 | proxy/_experimental/mcp_server/ (**약 5.4만 줄**, 코드 베이스 최대급), management_endpoints/mcp_management_endpoints.py, admin_mcp.py, mcp_registry.json | MCP 서버 등록·도구 프록시·OAuth·권한·시맨틱 필터·faults 등 | 라우트는 상시, 서버 등록 시 동작 | 등록한 서버 URL | 후순위. 규모가 압도적이라 별도 단계. 필요하면 "등록한 서버로 단순 프록시"부터 |
| A2A | proxy/a2a/ (965), litellm/a2a_protocol/ (4.9k), agent_endpoints/ (6.3k) | agent-to-agent 프로토콜, agent 레지스트리 | 상시 라우트 | 등록 agent URL | 제외 |
| skills | litellm/skills/, proxy/hooks/litellm_skills/ (968), agent_skills_discovery_router | Anthropic skills 주입 훅. PROXY_HOOKS 에 등록돼 있음 | hook 등록, 사용 시만 동작 | 확인 못함 | 제외 |
| sandbox / code interpreter | litellm/sandbox/, integrations/code_interpreter_interception/ | 코드 실행 샌드박스 도구 | OFF | 외부 샌드박스 | 제외 |
| prompt management | proxy/prompts/ (1.6k), integrations/dotprompt/, bitbucket/, gitlab/, generic_prompt_management/, custom_prompt_management.py | 프롬프트 템플릿을 DB/파일/Git 에서 불러옴 | 상시 라우트 | git 연동은 외부 | 후순위(DB 프롬프트만) |
| vector store | proxy/vector_store_endpoints/ (1.9k), litellm/vector_stores/ (1.8k), vector_store_files/, enterprise vector_stores/ | 벡터 스토어 등록·검색 | 상시 라우트 | 등록한 백엔드 | 제외 |
| RAG | litellm/rag/ (3.6k), proxy/rag_endpoints/ (1.3k) | ingest/query 파이프라인 | 상시 라우트 | 등록한 임베딩·벡터DB | 제외 |
| search | proxy/search_endpoints/, litellm/search/, hooks/search_free_tier.py, integrations/websearch_interception/ | 웹 검색 provider 프록시(perplexity 등) | 상시 라우트 | 외부 검색 SaaS | 제외 |
| memory | proxy/memory/ (562) | 에이전트 메모리 | 확인 못함 | - | 제외 |
| containers / realtime / ocr / video / image / rerank / fine-tuning / batches / files / responses polling | proxy/container_endpoints, realtime_endpoints, ocr_endpoints, video_endpoints, image_endpoints, rerank_endpoints, fine_tuning_endpoints, batches_endpoints, openai_files_endpoints, response_polling | 비-chat OpenAI 호환 surface | 상시 라우트 | provider 따라 | /v1/embeddings·/v1/responses(Ollama 지원 시)는 후순위, 나머지 제외. 담당 영역은 다른 조사(A~E)와 중복 가능 |
| pass-through | proxy/pass_through_endpoints/ (**1.8만 줄**) | provider 원본 API 그대로 프록시 | 설정 시 | provider | 제외 |
| native_compaction / compression_interception | proxy/native_compaction.py, integrations/compression_interception/ | 컨텍스트 압축 | OFF | 모델 호출 | 제외 |
| 기타 신규 영역 | proxy/lens(385), decisions_endpoints(343), moyai_endpoints.py(277, `PROXY_BASE_URL` 사용 quick-connect), analytics_endpoints, enterprise_billing, workflows(0줄), plugin_routes.py, rust_control_plane_router, tracing_endpoints | 부가 UI·실험 기능 | 라우트 상시 | 확인 못함 | 제외 |

## 6. secret managers · 스케줄러·백그라운드 잡
### 6-A. secret manager (litellm/secret_managers/)
기본: `key_management_system` 미설정 -> `os.environ/NAME` 값을 환경변수에서 읽음(secret_managers/main.py:265). 종류(types/secret_managers/main.py:7-16): google_kms, azure_key_vault, aws_secret_manager(+v2), google_secret_manager, hashicorp_vault, cyberark, local, aws_kms, custom.
| 기능 | 위치 | 기본 | 유료 | 새 GW 권장 |
|---|---|---|---|---|
| env 참조(`os.environ/X`) | secret_managers/main.py:257-270 | ON | 무료 | **필수**(설정 파일에 비밀 직접 쓰지 않기) |
| HashiCorp / CyberArk / Google Secret Manager | hashicorp_secret_manager.py:121, cyberark_secret_manager.py:61, google_secret_manager.py | OFF | **premium 게이트** | 후순위(HashiCorp Vault 가 사내에 있으면) |
| AWS / Azure / Google KMS / custom | aws_secret_manager*.py, google_kms.py, custom_secret_manager_loader.py | OFF | 무료 | 제외 |
| 가상키 저장 암호화 | `key_management_settings.store_virtual_keys`(types/secret_managers/main.py:21) | OFF | 무료 | 확인 못함(SALT 키 방식은 다른 영역 조사) |

### 6-B. proxy 기동 시 도는 루프/잡 (proxy_server.py `proxy_startup_event` :1426~, `ProxyStartupEvent` :10982~11990)
| 잡 | 위치 | 주기/조건 | 기본 | 새 GW |
|---|---|---|---|---|
| refresh_model_info | :1754-1771 | 300초(llms/openai_like/model_info.py:16) 외 `disable_model_info_refresh` 로 끔. 외부 호출 여부는 해당 함수 내부 확인 못함 | ON | 후순위(모델 목록 DB 반영) |
| 백그라운드 헬스체크 | :1729 `_run_background_health_check`, :4539 | `use_background_health_checks` | OFF | **필수**(Ollama 살아있는지 확인) |
| 예산 리셋 reset_budget_job | :11235 | `budget_interval` | ON(DB 있을 때) | **필수**(예산 주기 리셋) |
| 지출 배치 기록 update_spend_job + daily tag/gateway/error jobs | :11246-11302 | batch_writing_interval | ON(DB) | **필수**(사용량 회계) |
| spend log 큐 모니터·정리 | :11302, :11481-11524, 8093-8111 | 크기/크론 | 정리는 설정 시 | 후순위(보관 기간 정리는 필수에 가까움) |
| proxy_worker_heartbeat | :11217 | 주기 | ON | 후순위 |
| periodic_reload_job / add_deployment_job / reload_search_tools / reload_mcp_servers | :11339-11398 | 30초 기본 | `store_model_in_db` 일 때 | **필수**(DB 모델 변경 반영) |
| batch/responses cost check | :11553, 11582 | `proxy_batch_polling_interval` | 설정 시 | 제외 |
| key_rotation_job | :11732 | `LITELLM_KEY_ROTATION_ENABLED=true` | OFF | 후순위 |
| 만료 UI 세션 키 정리 | :11787 | `LITELLM_EXPIRED_UI_SESSION_KEY_CLEANUP_ENABLED` | OFF | 후순위 |
| spend capture rate check | :11845 | 2분 후 시작 | ON | 제외 |
| 주간/월간 spend 리포트, user spend alerts | :11911-11948 | Slack alerting 설정 시 | OFF | 후순위 |
| prometheus fallback stats, PTU rollup(cron 00시) | :11971, :11457 | 설정 시 | OFF | 제외 |
| 비밀번호/agent grant 마이그레이션 task | :1565, :1596 | 기동 1회 | ON | 제외 |
| global spend cache warm | :1689 | `max_budget>0` | 조건부 | 후순위 |
| adaptive router flusher | :1741 | 항상 task 생성 | ON | 제외 |
| pod lock / coordination redis | proxy_server.py:1596-1600 근처, db_spend_update_writer | 멀티 pod | 조건부 | 제외(1차는 단일 인스턴스 가정 가능 여부는 설계에서 결정) |
| telemetry runtime start | :1785-1805 | consent OFF 이면 기록 안 함 | 태스크는 생성 | 제외 |
| Pyroscope | :1745 `LITELLM_ENABLE_PYROSCOPE=true` | OFF | OFF | 제외 |

## 7. 프로메테우스·헬스·로깅 포맷 요약
- 헬스: proxy/health_endpoints/_health_endpoints.py — `/health/liveliness`, `/health/liveness`, `/health/readiness`, `/health/readiness/details`, `/health`(모든 모델 실호출 점검), `/health/services`, `/health/history`, `/health/latest`, `/health/shared-status`, `/health/license`, `/health/backlog`, `/health/drain`, `/health/test_connection`, `/settings`, `/active/callbacks`, `/test`. 필수: liveness/readiness/`/health`(모델 점검). 나머지 후순위·제외.
- 메트릭: 86개 `litellm_*` 이름(types/integrations/prometheus.py). 필수 후보는 total/failed requests, input/output tokens, llm latency, in-flight, remaining budget(key/team), deployment 상태. 가드레일(`litellm_guardrail_*`), MCP, managed batch 계열은 제외.
- 로깅: 기본 텍스트, `JSON_LOGS=true` 로 JSON(_logging.py:644-658,735). `LITELLM_LOG` 기본 DEBUG(:660) — 요청 본문이 로그에 남을 수 있어 새 GW 는 INFO 기본 + 비밀 마스킹(규칙 code-quality 3번).

## 8. 놀라운 발견 / 유의점
1. 외부 6곳(가격표·블로그·beta 헤더·오토라우터·정책 템플릿·GitHub 릴리스)이 **기본 ON** 인데 fork 에 끄는 설정이 없다. 특히 GitHub 릴리스(latest_release_endpoints.py)는 끄는 env 자체가 없고 블로그는 UI 열 때마다 서버가 docs.litellm.ai 로 나간다. 가격표는 `import litellm` 시점(프로세스 시작)에 동기 호출이라 폐쇄망에선 시작 지연(타임아웃 5초+재시도 스레드)이 생긴다.
2. 번들 가격표(root `model_prices_and_context_window.json`)는 새 GW 에서 쓸 필요가 거의 없다. 폐쇄망은 Ollama 모델(무료)이라 운영자 입력 단가(`input_cost_per_token`)면 충분.
3. 새 `litellm/telemetry/` 는 철저히 opt-in 이라 안전하지만 Prisma 테이블에 로컬 저장하는 `local_table` 경로가 있다(proxy/telemetry/store.py).
4. `LITELLM_LOG` 기본 DEBUG, `litellm.log`(root, 추적 대상 아님으로 보임)·`license_cache.json`(의존 라이선스 목록, 라이선스 키 아님) 가 워킹트리에 있다.
5. 라이선스 키가 있으면(`LITELLM_LICENSE`) 서명 검증 실패 시 `license.litellm.ai` 로 폴백하므로, 혹시 키를 넣어 운영하던 환경이 있다면 외부 호출이 생긴다. 키 없이 쓰면 0.
6. MCP 가 5.4만 줄, pass-through 가 1.8만 줄로 부가 기능 중 압도적 — 교체 범위에서 제외하면 작업량이 크게 줄어든다. 가드레일 외부 SaaS 약 40종은 전부 폐쇄망 원칙 위반이라 제외 확정.
7. `enterprise/` 는 이미지에 그대로 포함돼 라우트가 상시 마운트(기능만 premium 검사로 막힘)된다. 전체 83개 premium 게이트 목록은 PREMIUM_WALL_REVIEW.md 를 따르되 이번에는 callback_utils.py:202-322, gcs/azure/secret manager, SSO(ui_sso.py:1077-1121)만 직접 확인.
8. 확인 못함: `refresh_model_info` 내부 외부 호출 여부, `mcp_servers.json`(root) 이 코드에서 자동 로드되는지, memory/lens/decisions 의 실제 기능, pass-through 개별 provider URL 전수.

## 9. 새 게이트웨이 필수 후보 (부가 영역만)
Prometheus /metrics(소수 지표, 인증 기본 ON) · 헬스(liveness/readiness/모델 점검) · JSON 구조 로그(비밀 마스킹) · 스펜드 로그 기록·보관 정리 · 예산 리셋·지출 배치 잡 · DB 모델 변경 주기 반영 · rate limit(RPM/TPM/동시성)·예산 훅 · 로컬 content filter(키워드+정규식) · `os.environ/` 비밀 참조 · UI 로고 로컬 파일. 후순위: OTLP, 사내 generic 가드레일 API, Presidio 연동, 감사 로그, Slack/SMTP 알림, 키 로테이션, HashiCorp Vault, MCP(단순 프록시). 제외: 위 외부 호출 전부, 외부 SaaS 콜백·가드레일, enterprise 게이트, A2A/skills/sandbox/vector/rag/search/pass-through.
