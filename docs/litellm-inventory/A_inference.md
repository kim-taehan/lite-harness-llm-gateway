# A. 추론 API 표면 전수조사 (litellm/proxy 데이터 플레인)

조사 방법: `litellm/proxy/**` 의 FastAPI 데코레이터를 AST 로 뽑아(`.venv/bin/python`) 경로·메서드·함수를 확정하고, 핸들러 본문과 `gateway/routes/allowlist.py` 를 직접 읽었다. 문서(ANALYSIS.md, GATEWAY_DESIGN.md)는 옮기지 않았다.
표기: 파일:라인 은 **함수 정의 라인**(데코레이터는 그보다 위). 경로 앞 `[/v1]` 은 `/v1/...` 과 접두 없는 `/...` 가 둘 다 등록됨을 뜻한다(litellm 은 거의 모든 라우트를 이중 등록).
"유료" 열: 핸들러에서 `premium_user` 검사를 직접 확인한 것만 "유료 게이트"로 적었다. 데이터 플레인 엔드포인트 자체는 대부분 라이선스 검사가 없다. 못 찾았으면 "확인 못함".
Ollama 지원 여부: `litellm/llms/ollama/` 에 chat·completion(+embed 처리)만 있다. 그 외 모달리티는 Ollama 로 가능한지 코드로 확인 못함.

## 1. 핵심 OpenAI 표준 추론
| 기능 | 엔드포인트 | 구현 위치 | 동작 요약 | 외부호출·유료 | 권장 |
|---|---|---|---|---|---|
| 채팅 완성(스트리밍 포함) | POST `[/v1]/chat/completions`, `/engines/{model}/chat/completions`, `/openai/deployments/{model}/chat/completions` | litellm/proxy/proxy_server.py:12646 `chat_completion` | 본문 읽기 → metadata 에 user/team/org/agent id 주입 → 공통 처리(아래 3절) → 응답. stream=true 면 SSE. 가드레일 차단은 200+content_filter 로 변환(12700대) | 업스트림 모델 호출뿐. 유료 게이트 없음 | **필수** — 핵심 API |
| 모델 목록 | GET `[/v1]/models` | proxy_server.py:12210 `model_list` | 키/팀/조직 권한으로 걸러낸 모델 목록. `anthropic-version` 헤더면 Anthropic 형식, `?client_version=` 이면 Codex 카탈로그 형식, healthy_only·include_metadata·scope 등 쿼리 다수 | 없음 | **필수** — OpenAI 표준. 쿼리 옵션은 후순위 |
| 모델 단건 | GET `[/v1]/models/{model_id}` | proxy_server.py:12482 `model_info` | 단건 조회 | 없음 | **필수** — SDK 호환용, 구현 작음 |
| 임베딩 | POST `[/v1]/embeddings`, `/engines/{model}/embeddings`, `/openai/deployments/{model}/embeddings` | proxy_server.py:12995 `embeddings` | 공통 처리 route_type=aembedding. Ollama embed 지원(llms/ollama/completion/handler.py) | 없음 | **필수** — RAG·코딩도구 인덱싱에 흔함, Ollama 지원 |
| 텍스트 완성(레거시) | POST `[/v1]/completions`, `/engines/{model}/completions`, `/openai/deployments/{model}/completions` | proxy_server.py:12811 `completion` | route_type=atext_completion | 없음 | 후순위 — 일부 에디터 자동완성이 씀 |
| 헬스 | GET `/health/liveliness`, `/health/liveness`, `/health/readiness`, `/health` | health_endpoints/_health_endpoints.py:2085, 1974, 1230 | liveness·readiness 는 인증 없음 추정(의존성 미선언), `/health` 는 모델 실호출 점검 | 모델 호출(health) | **필수** — 운영(k8s)용. liveness/readiness 만 필수, `/health` 는 후순위 |

## 2. 그 외 모델 추론·변환 엔드포인트
| 기능 | 엔드포인트 | 구현 위치 | 동작 요약 | 외부·유료 | 권장 |
|---|---|---|---|---|---|
| Responses API | POST `[/v1]/responses`, `/openai/v1/responses`; GET/DELETE `.../{id}`; GET `.../{id}/input_items`; POST `/responses/compact`, `/responses/input_tokens`, `/responses/{id}/cancel`; WS `[/v1]/responses` | response_api_endpoints/endpoints.py:267, 758, 1135, 1251, 1319, 1626, 1732, 2335 | OpenAI Responses 스펙. background+polling_via_cache(response_polling/), 응답 id 보안 훅(hooks/responses_id_security.py), WebSocket 모드 | 비용 확인은 `litellm_enterprise.CheckResponsesCost`(971행) 임포트 — 유료 모듈 의존, 없으면 폴백인지 확인 못함 | 후순위 — Codex 류 클라이언트가 씀. 필요 시 생성 POST 만 먼저 |
| Cursor 호환 | GET `/cursor/models`, `/cursor/v1/models`; POST `/cursor/chat/completions`; `/cursor/{endpoint}` 패스스루 | response_api_endpoints/endpoints.py:519, 552; pass_through_endpoints/llm_passthrough_endpoints.py:3591 | Cursor IDE 가 보내는 Responses 형식을 변환 | 패스스루는 외부 주소 가능(확인 못함) | 제외 — 폐쇄망 요구와 무관 |
| Anthropic Messages | POST `/v1/messages`, `/v1/messages/count_tokens` | anthropic_endpoints/endpoints.py:162, 359 | 모든 모델을 Anthropic 형식으로 노출(route_type=anthropic_messages). 코드 docstring 이 "BETA, `/anthropic/v1/messages` 쓰라" 고 안내 | 없음 | 후순위 — Claude Code 를 Ollama 앞에 붙일 때만 |
| Claude Code 게이트웨이 | `/claude_code_gateway/*` (OAuth device flow, `/managed/settings`, OTLP `/v1/metrics·logs·traces`) | anthropic_endpoints/gateway_endpoints.py:177~476 | `enable_claude_code_gateway` 설정으로 켜짐(125행) | 확인 못함 | 제외 |
| Claude Code 마켓플레이스 | GET `/claude-code/marketplace.json`, CRUD `/claude-code/plugins*` | anthropic_endpoints/claude_code_endpoints/claude_code_marketplace.py:115~791 | 플러그인 레지스트리 | 플러그인 원본이 외부 git 일 수 있음(확인 못함) | 제외 — 플러그인은 별도 저장소(plugins-repo) |
| Anthropic skills | POST/GET/DELETE `/v1/skills*` | anthropic_endpoints/skills_endpoints.py:89~414 | 스킬 저장·조회 | 확인 못함 | 제외 |
| Gemini(Google) 네이티브 | POST `[/v1beta]/models/{m}:generateContent`, `:streamGenerateContent`, `:countTokens` | google_endpoints/endpoints.py:143, 204, 271 | Google 형식 입출력 | 없음(업스트림만) | 제외 |
| Gemini Interactions·Agents | `[/v1beta]/interactions*`, `/v1beta/agents*` | google_endpoints/endpoints.py:339~561; agents_endpoints.py:149~394 | Google 전용 | 확인 못함 | 제외 |
| 이미지 생성·편집 | POST `[/v1]/images/generations`, `/images/edits` (+`/openai/deployments/{m}/images/...`) | image_endpoints/endpoints.py:88, 245 | route_type=aimage_generation/aimage_edit | 없음 | 후순위 — Ollama 이미지 지원 확인 못함 |
| 음성 합성 | POST `[/v1]/audio/speech` | proxy_server.py:13228 | route_type=aspeech | 없음 | 제외 — 로컬 TTS 모델 계획 없으면 불필요 |
| 음성 전사 | POST `[/v1]/audio/transcriptions` | proxy_server.py:13365 | multipart 업로드, route_type=atranscription | 없음 | 후순위 — 내부 Whisper 서버가 있을 때만 |
| 모더레이션 | POST `[/v1]/moderations` | proxy_server.py:13106 | route_type=amoderation | 없음 | 제외 — 내부 모델 없음 |
| 리랭크 | POST `[/v1|/v2]/rerank` | rerank_endpoints/endpoints.py:45 | route_type=arerank | 없음 | 후순위 — 사내 RAG 가 있으면 |
| OCR | POST `[/v1]/ocr` | ocr_endpoints/endpoints.py:272 | route_type=aocr | 없음 | 제외 |
| 웹 검색 | POST `[/v1]/search[/{tool}]`, GET `[/v1]/search/tools` | search_endpoints/endpoints.py:42, 231 | 외부 검색 프로바이더 중계 | **외부 호출**(검색 API) | 제외 — 폐쇄망 위배 |
| 영상 생성 | `[/v1]/videos*` 9종(생성·목록·상태·콘텐츠·remix·characters·edits·extensions) | video_endpoints/endpoints.py:53~864 | 비동기 영상 작업 | 외부 프로바이더 | 제외 |
| 토큰 카운트 유틸 | POST `/utils/token_counter` | proxy_server.py:15186 | 모델 토크나이저로 계산 | 없음 | 후순위 — allowlist 에도 없음(`/utils/` 미포함) |
| 지원 파라미터·변환 유틸 | GET `/utils/supported_openai_params`, `/utils/model_info`; POST `/utils/transform_request` | proxy_server.py:15410, 15454, 15489 | 디버그 성격 | 없음 | 제외 |
| 큐 요청(미완성) | POST `/queue/chat/completions` | proxy_server.py:18207 `async_queue_request` | docstring 이 "v2 attempt at a background worker" — 실험적 | 없음 | 제외 — allowlist 에는 있음 |
| decisions·systemone·extraction | POST `[/v1]/decisions`, `/systemone`, `/extraction` (+`/laya/v1/systemone`, `/bespoke/v1/systemone`) | decisions_endpoints/endpoints.py:225, 269, 315; llm_passthrough_endpoints.py:698, 707 | 특정 업체(Nimble/laya/bespoke) 연동 추정 | 외부 업체 추정(확인 못함) | 제외 |
| RAG | POST `[/v1]/rag/ingest`, `/rag/query` | rag_endpoints/endpoints.py:622, 791 | 업로드→벡터화→검색 파이프라인 | 외부 벡터DB·임베딩 가능 | 제외 |
| 메모리 | POST/GET/PUT/DELETE `/v1/memory[/{key}]` | memory/memory_endpoints.py:291~546 | 키-값 저장 | 확인 못함 | 제외 |

## 3. OpenAI 상태 저장형 리소스 (Ollama 에 대응물 없음)
| 기능 | 엔드포인트 | 구현 위치 | 동작 요약 | 외부·유료 | 권장 |
|---|---|---|---|---|---|
| 파일 | POST/GET/DELETE `[/{provider}][/v1]/files[/{id}[/content]]` (5함수 x 3경로) | openai_files_endpoints/files_endpoints.py:553, 960, 1268, 1466, 1691 | 업스트림 프로바이더(OpenAI/Azure/Vertex 등)에 파일 위임. 관리형 id 는 enterprise `managed_files` 훅(enterprise/enterprise_hooks/__init__.py) | 외부 프로바이더 위임 / enterprise 훅 의존 | 제외 — Ollama 에 Files 없음 |
| 배치 | POST/GET `[/{provider}][/v1]/batches[/{id}][/cancel]` | batches_endpoints/endpoints.py:251, 563, 870, 1055 | 프로바이더 배치 API 위임 + litellm 자체 실행 배치(litellm_executed_batches.py) | 상동 | 제외 |
| 파인튜닝 | `[/v1]/fine_tuning/jobs*` 4종(+`fine-tuning` 별칭 허용) | fine_tuning_endpoints/endpoints.py:76, 239, 387, 514 | 프로바이더 위임 | 외부 | 제외 |
| Assistants·Threads | `[/v1]/assistants` GET/POST/DELETE, `/threads`, `/threads/{id}`, `/messages`, `/runs` | proxy_server.py:13939~14463 | 프로바이더 위임 | 외부 | 제외 |
| 벡터스토어(OpenAI 형) | `[/v1]/vector_stores*` CRUD·search, `/v1/indexes`, `/{id}/files*` | vector_store_endpoints/endpoints.py:111~626; vector_store_files_endpoints/endpoints.py:560~1098 | 관리형 벡터스토어는 enterprise `managed_vector_stores` 훅(endpoints.py:239) | 외부 벡터DB / enterprise 훅 | 제외 |
| 벡터스토어 관리 | `/vector_store/new·list·delete·info·update` | vector_store_endpoints/management_endpoints.py:280~618 | 레지스트리 CRUD(관리면) | — | 제외 |
| Containers | `[/v1]/containers*` 4종 | container_endpoints/endpoints.py:44~387 | 코드 인터프리터 컨테이너 위임 | 외부 | 제외 |
| Evals | `/v1/evals*` 11종 | openai_evals_endpoints/endpoints.py:33~952 | 프로바이더 위임 | 외부 | 제외 |

## 4. 실시간·WebSocket
| 기능 | 엔드포인트 | 구현 위치 | 동작 요약 | 외부·유료 | 권장 |
|---|---|---|---|---|---|
| Realtime WS | WS `[/openai][/v1]/realtime` | proxy_server.py:13628 `realtime_websocket_endpoint` | 업스트림 realtime 소켓 중계, 인증은 `user_api_key_auth_websocket*`(auth/user_api_key_auth.py:1182~) | 외부 | 제외 — Ollama 미지원 |
| Realtime HTTP | POST `[/v1]/realtime/client_secrets`, `/realtime/calls`, `/realtime/transcription_sessions` | realtime_endpoints/endpoints.py:239, 382, 544 | 임시 토큰·WebRTC 호출 | 외부 | 제외 |
| Live sessions / Vertex live | WS `[/openai][/v1]/live/sessions`, `/vertex_ai/live` | proxy_server.py:13843, 13514 | Gemini Live 중계 | 외부 | 제외 |
| Deepgram·OpenAI WS 패스스루 | WS `/deepgram/listen`, `/openai/{endpoint}` | llm_passthrough_endpoints.py:3376, 3461 | 소켓 중계 | 외부 | 제외 |

## 5. 에이전트·MCP
| 기능 | 엔드포인트 | 구현 위치 | 동작 요약 | 외부·유료 | 권장 |
|---|---|---|---|---|---|
| A2A 호출 | POST `/a2a/{id}`, `/a2a/{id}/message/send`, `/v1/a2a/{id}/message/send`; GET `/a2a/{id}/.well-known/agent-card.json` | agent_endpoints/a2a_endpoints.py:574, 643, 658 | JSON-RPC, message/stream 은 SSE(278행) | 등록된 에이전트 URL 호출(내부 가능) | 제외 |
| A2A 디스커버 | POST `/v1/a2a/discover` | a2a/endpoints.py:73 | 임의 URL 의 agent card 가져옴 | **임의 외부 URL 호출 가능** | 제외 |
| MCP 스트리머블 HTTP | `/mcp`, `/{server}/mcp`, `/toolset/{name}/mcp`, `/mcp/proxy` | _experimental/mcp_server/server.py:2955~2957 (mount), proxy_server.py:21642~21828 | MCP 서버 집약·툴 호출 | 업스트림 MCP 서버 호출(내부 가능). OAuth 부속 라우트 수십 개(discoverable_endpoints.py) | 후순위 — 단 아래 "놀라운 발견" 참고 |
| MCP REST | GET `/mcp-rest/tools/list`, POST `/tools/call` | _experimental/mcp_server/rest_endpoints.py:1017, 1338 | REST 로 툴 목록·호출 | 상동 | 후순위 |

## 6. 프로바이더 패스스루 (원 프로바이더 API 를 그대로 중계)
llm_passthrough_endpoints.py 에 약 30개. 전부 `GET|POST|PUT|DELETE|PATCH /{prefix}/{endpoint:path}` 이고 업스트림 키를 서버가 대신 주입한다.
대상: `/gemini`(:351) `/cohere`(:418) `/fal_ai`(:483) `/vllm`(:519) `/mistral`(:604) `/typesafe`(:657) `/openrouter`(:755) `/milvus`(:791) `/anthropic`(:934) `/bedrock`(:1449) `/comprehendmedical`(:1557) `/azure_speech`(:1719) `/transcribe`(:1804) `/assemblyai`(:2072) `/nvidia_nim`(:2279) `/azure`·`/azure_ai`(:2372) `/vertex_ai`·`/vertex-ai`(:3158) `/openai`(:3201) `/cursor`(:3591) `/tinyfish`(:3704) `/gigachat`(:4079) `/watsonx`(:4303), `/langfuse`(vertex_ai_endpoints/langfuse_endpoints.py:160), `/openai_passthrough`(openai_passthrough_endpoints.py:15), 사용자 정의 패스스루(config 로 동적 등록, pass_through_endpoints.py:3098~3198).
- 외부호출·유료: 기본 업스트림이 **하드코딩된 외부 주소**(예 `https://api.anthropic.com`:943, `api.mistral.ai`:613, `openrouter.ai`:761, `generativelanguage.googleapis.com`:364, `api.cohere.com`:427, AWS 리전 주소:1480). env 로 덮어쓸 수 있음. 유료 게이트는 확인 못함.
- 권장: **제외** — 폐쇄망 원칙 위배(외부 주소 기본값). 필요하면 `/vllm` 류 "내부 OpenAI 호환 서버 중계" 한 가지만 후순위로 따로 설계. 추론 API 는 `/v1/chat/completions` 로 통일.

## 7. 운영·부속 (추론 표면 주변)
| 기능 | 엔드포인트 | 구현 위치 | 권장 |
|---|---|---|---|
| Prometheus | GET `/metrics`(Mount) | allowlist GATEWAY_MOUNT_PATHS | 후순위 — 운영 가시성 |
| 루트·문서 | `/`, `/routes`, `/openapi.json`, `/docs`, `/redoc`, `/test` | allowlist GATEWAY_EXACT_PATHS | 제외(`/routes`·`/test` 불필요) |
| 사용량 수집 | POST `/api/event_logging/batch` | anthropic_endpoints/endpoints.py:431(인증 의존성 미선언) | 제외 — 사실상 telemetry 수신구 |
| 텔레메트리 | `/telemetry/*` | telemetry/endpoints.py:68~271 | 제외 — "telemetry 0" 원칙 |
| 메모리 요약 | GET `/debug/memory/summary` | allowlist 만 확인, 구현 확인 못함 | 제외 |

## 요청 처리 공통 흐름 (chat/completions 기준)
진입은 FastAPI 의존성 `user_api_key_auth` → 핸들러 → `ProxyBaseLLMRequestProcessing`. 다른 엔드포인트(embeddings·responses·messages 등)도 같은 클래스에 route_type 만 바꿔 들어간다.
1. **미들웨어**(proxy/middleware/): 요청 크기 제한(premium 일 때만 설정값 적용, proxy_server.py:2767·21536), 본문 압축 해제, gzip, 보안 헤더, in-flight 집계, admission control, 예산 예약 해제 등.
2. **인증·인가** `user_api_key_auth`(auth/user_api_key_auth.py:4216 → builder :2088): `x-litellm-api-key` 우선 → 커스텀 인증 → JWT/OAuth2 라우팅 → 마스터키 → 키(DB/캐시) 조회 → 팀·유저·프로젝트·end-user 조회 → `common_checks`(auth_checks.py:1076: 팀 차단, 모델 접근, 팀원 모델 범위, 예산 등). 전역 OAuth2 토큰 검증은 premium 아니면 403(:2234 부근). 키·팀·예산 모델이 곧 "운영자 직접 다루기" 범위.
3. **핸들러**: 본문 파싱 `read_request_body`, metadata 에 user/team/org/agent id 주입(proxy_server.py:12680대).
4. **공통 사전 처리** `common_processing_pre_call_logic`(common_request_processing.py:2170): `model` 문자열 검증 → `add_litellm_data_to_request`(litellm_pre_call_utils.py, :2293 호출; 헤더·키/팀 metadata·가드레일/정책 병합 — 가드레일·정책은 key/team 에 지정되면 `premium_user_check()` 로 **유료 게이트**(:3783~3860)) → 파일검색 벡터스토어 권한 확인(:143) → 모델 별칭 매핑(키·팀 aliases, 팀 단위 model_group_alias) → 키>팀 계층 router_settings 적용 → stream_options 자동 usage → `x-litellm-call-id` 확정 → 로깅 객체 생성 → 모델 단위 가드레일 병합 → 자동 라우터 압축 → 가드레일 추가 태그 예산 → **`ProxyLogging.pre_call_hook`**(:2481; utils.py:2771~): 훅 순회.
   - 기본 훅(hooks/__init__.py:39 `PROXY_HOOKS`): parallel_request_limiter(키·팀 TPM/RPM/동시요청), cache_control_check, responses_id_security, litellm_skills, max_iterations_limiter, max_budget_per_session_limiter, sensitive_data_routing, prompt_cache_prediction, autorouter_baseline_cache, daily_token_limiter. DB 필요한 훅은 prisma 없으면 건너뜀(utils.py:1762).
   - 유료 디렉터리 훅(enterprise/enterprise_hooks): managed_files, managed_vector_stores 만 `ENTERPRISE_PROXY_HOOKS` 로 등록. 그 외 파일(aporia_ai, banned_keywords, blocked_user_list, google/openai_moderation)은 레지스트리에 안 들어감.
5. **폴백 재시도 포함 사전 호출** `_pre_call_with_fallbacks`(:2521): 모델별 한도 초과 시 설정된 fallback 모델로 사전 호출 재시도.
6. **병렬 모더레이션** `during_call_hook`(:2915) 시작 → **라우팅** `route_request`(route_llm_request.py:713): route_type→llm_router 메서드 매핑(143~254 표), 모델 완전 차단 확인(:124), 와일드카드·자동 라우터·a2a 에이전트 모델(:119) 분기, 라우터가 로드밸런싱·쿨다운·재시도·폴백 수행(litellm Router).
7. **응답**: 비스트리밍 → `post_call_success_hook`(:3184: 가드레일·마스킹) → `x-litellm-*` 헤더(call-id, model-id, response-cost, key-spend, key-rpm/tpm/max-budget, 지연 ms 등 `get_custom_headers` :2024). 스트리밍 → 첫 청크를 버퍼링하며 클라이언트 끊김 감시(:1042), 필요 시 keepalive SSE(:1393), `async_streaming_data_generator`(:4383) 가 청크 훅·비용 주입(:4576)·`data: ...\n\n` 직렬화, 종료 시 `data: [DONE]`. 클라이언트가 끊으면 부분 사용량을 과금(:434).
8. **오류**: `handle_llm_api_exception`(:4136) 이 `ProxyException`(OpenAI 모양 message/type/param/code)으로 변환, `post_call_failure_hook` 실행. 스트림 중 오류는 SSE 오류 프레임(:1120~1150).
9. **사후 기록**: 성공·실패 콜백(비동기 큐) → 비용 계산 → 키·팀·유저·end-user 지출 갱신, SpendLogs 기록, 예산 예약 정산(spend_tracking/budget_reservation.py), Prometheus 등. (이 영역은 다른 조사 범위; 여기선 진입점만 확인)

## gateway/routes/allowlist.py 와 실제 라우트의 차이
- allowlist 는 `/v1/chat/` `/v1/completions` `/v1/embeddings` `/v1/moderations` `/v1/audio/` `/v1/images/` `/v1/files` `/v1/batches` `/v1/fine_tuning/` `/v1/responses` `/v1/threads` `/v1/assistants` `/v1/vector_stores` `/v1/models` `/v1/messages` `/v1/rerank` `/v1/ocr` `/v1/rag/` `/v1/videos` `/v1/search` `/v1/containers` `/v1/evals` `/v1/memory` `/v1/a2a/` `/v1/realtime` + 패스스루 약 40 접두 + 동적 `/{provider}/` `/toolset/` + `/health` `/metrics` 를 남긴다. 즉 **현재 게이트웨이는 위 표 대부분을 그대로 남기는 "넓은" 허용 목록**이다. 새 게이트웨이는 이보다 훨씬 좁게 가도 된다.

## 놀라운 발견
1. **allowlist 가 MCP 를 실제로는 안 남긴다.** 모듈 docstring 은 "MCP tool-call endpoints" 를 적었지만 `/mcp`, `/mcp-rest`, `/{server}/mcp` 접두가 없고 Mount 허용도 `/metrics` 뿐이다(`/mcp` 는 `app.mount("/mcp", ...)`, server.py:2956). 문서와 코드 불일치.
2. **패스스루 기본 업스트림이 하드코딩된 외부 주소**(anthropic·mistral·openrouter·gemini·cohere·AWS 등)이고 allowlist 가 전부 남긴다. 폐쇄망 원칙(제품 원칙 1)과 충돌 — env 를 안 주면 외부로 나가려 한다.
3. 데이터 플레인 엔드포인트 자체에는 `premium_user` 게이트가 거의 없다. 유료 게이트는 **키/팀 단위 가드레일·정책 지정**(litellm_pre_call_utils.py:3783~3860), **전역 OAuth2 토큰 검증**(user_api_key_auth.py:2234), 요청 크기 제한 설정, `enforced_params`(proxy_server.py:7175), 관리형 파일·벡터스토어 훅 쪽에 몰려 있다. 즉 기본 추론 경로를 재구현해도 유료 기능을 "흉내" 낼 필요는 거의 없다.
4. `/v1/models` 한 핸들러에 Codex 카탈로그(`client_version`), Anthropic 형식(`anthropic-version` 헤더), healthy_only 같은 **클라이언트별 변형**이 들어 있다. OpenAI 표준 최소형만 만들고 변형은 후순위.
5. `chat_completion` 은 가드레일이 차단하면 **HTTP 200 + finish_reason=content_filter** 를 돌려준다(proxy_server.py:12700대). OpenAI SDK 호환을 지키려면 이 동작을 의도적으로 따를지 결정해야 한다.
6. 라우트 이중 등록(`/v1/x` 와 `/x`, `/engines/{m}/...`, `/openai/deployments/{m}/...`)이 일반적이다. Azure SDK 호환용이므로 필요한 클라이언트만 남기면 된다(확인 못함: 사내 클라이언트가 Azure 형식을 쓰는지).
7. 이 fork 는 upstream 보다 훨씬 앞서 있고(함수 시그니처에 `Final`·pyright 주석 다수) 공통 흐름이 4800줄(common_request_processing.py)이다. 스트리밍 끊김 과금·첫 청크 버퍼링·keepalive 같은 **운영 안정성 로직**이 이 안에 많아서, 재구현 시 "chat/completions 스트리밍" 이 가장 큰 덩어리다.

## 항목 수
- 행(기능) 수: 1절 6 + 2절 22 + 3절 8 + 4절 4 + 5절 4 + 6절 1(묶음, 약 30개 경로군) + 7절 5 = 50행
- 개별 (메서드, 경로) 등록 수: AST 기준 위 파일들에서 이중 등록 포함 약 330개(대략치, 정확 집계는 안 함)

## 새 게이트웨이 권장 요약
- **필수**: `/v1/chat/completions`(스트리밍 포함), `/v1/models`(+`/{id}`), `/v1/embeddings`, `/health/liveliness`·`/health/readiness`, 공통 흐름 중 인증·모델 접근·키 한도/예산·사용량 기록·OpenAI 형식 오류·SSE
- **후순위**: `/v1/completions`, `/v1/responses`(생성), `/v1/messages`(+count_tokens), 이미지·전사·rerank, `/utils/token_counter`, `/metrics`, MCP, `/v1/models` 확장 쿼리
- **제외**: 파일·배치·파인튜닝·assistants·threads·벡터스토어·containers·evals·videos·ocr·rag·search·memory·decisions, realtime/live WS, A2A, 모든 프로바이더 패스스루, Gemini 네이티브, Claude Code 게이트웨이·마켓플레이스, telemetry
