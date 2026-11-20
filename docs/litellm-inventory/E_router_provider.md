# E. 라우터 · 프로바이더 계층 · 캐시 · 스트리밍 전수조사

조사 기준: `litellm_airgap` 브랜치 소스를 직접 읽음(읽기 전용). 경로는 `litellm/` 기준 상대 경로. ANALYSIS.md · GATEWAY_DESIGN.md 는 참고만 했고 모든 행은 코드로 재확인했다.
권장 표기: 필수 = 1차 Ollama 게이트웨이에 꼭 필요 / 후순위 = 2차 이후 / 제외 = 새 게이트웨이에서 만들지 않음. 폐쇄망 우선, 단순함 우선.
참고 규모: `router.py` 16,728줄, `main.py` 9,314줄, `router_strategy/` 4,508줄 + 하위 라우터 11,977줄, `caching/` 9,822줄. 이 fork 의 router 는 upstream 보다 훨씬 비대하다(complexity/adaptive/quality router, PTU, routing group 등). 전부 따라 만들 대상이 아니다.

---
## 0. 먼저 읽을 것: Ollama 호출 시 litellm 이 body 를 바꾸는 방식

### 0-1. litellm 이 Ollama 를 부르는 경로는 3가지다
| 접두사 | 호출 대상 | 변환 계층 | 비고 |
|---|---|---|---|
| `ollama_chat/<모델>` | Ollama 네이티브 `POST {base}/api/chat` | `llms/ollama/chat/transformation.py` (551줄) | 권장 경로. 아래 표의 변환을 전부 수행 |
| `ollama/<모델>` | Ollama 네이티브 `POST {base}/api/generate` | `llms/ollama/completion/transformation.py` (665줄) | 옛 경로. 프롬프트를 문자열로 합침. 제외 |
| `openai/<모델>` + `api_base=http://ollama:11434/v1` | Ollama 의 OpenAI 호환 `/v1/chat/completions` | `llms/openai/chat/gpt_transformation.py` (OpenAIGPTConfig) | 사실상 pass-through. 새 게이트웨이가 쓸 경로 |
기본 `api_base` 는 `http://localhost:11434`(`main.py:4573`, `llms/ollama/chat/transformation.py:222`), 키는 `OLLAMA_API_BASE`, `OLLAMA_API_KEY` 환경변수. 인증은 키가 있을 때만 `Authorization: Bearer`(`:202-204`).

### 0-2. `ollama_chat` 의 요청 변환 (OpenAI body -> Ollama `/api/chat` body)
| OpenAI 필드 | Ollama 로 가는 모양 | 위치 | pass-through 가능? |
|---|---|---|---|
| `model` | 그대로(접두사 `ollama_chat/` 는 떼고 보냄). 응답 `model` 은 `"ollama_chat/"+model` 로 다시 붙임 | `chat/transformation.py:291, 383` | /v1 이면 그대로. 이름 치환(별칭->실모델)만 필요 |
| `messages[].content` (배열) | 텍스트 파트를 문자열 하나로 합침(`convert_content_list_to_str`) | `:264` | /v1 이면 불필요 |
| `messages[].content[].image_url` | `messages[].images=[base64...]`. `data:` 접두사를 떼고 순수 base64 만. http URL 이미지는 내려받지 않고 문자열 그대로 넣음(확인: `common_utils.py:2497-2515`, 다운로드 코드 없음) | `:265, :274` | /v1 이 data URL 을 받으므로 불필요. 비전은 후순위 |
| `messages[].tool_calls[].function.arguments` | **JSON 문자열 -> 객체**(`json.loads`). id 는 버림 | `:246-262` | /v1 이면 불필요. 네이티브만 필요 |
| `messages[].tool_call_id` (tool 역할) | `tool_call_id` 로 그대로 | `:278-280` | 동일 |
| `messages[].reasoning_content` | `messages[].thinking` | `:263, :270` | /v1 은 `reasoning` 필드(확인 못함, 외부 지식) |
| `max_tokens`, `max_completion_tokens` | `options.num_predict` | `:155-156` | /v1 은 Ollama 가 `max_tokens` 를 받음 |
| `temperature`, `top_p`, `seed` | `options.*` 로 이동 | `:159-164` | /v1 이면 그대로 |
| `frequency_penalty` | `options.repeat_penalty` (의미가 다른 값끼리 이름만 매핑, 사실상 부정확) | `:165-166` | /v1 은 `frequency_penalty` 를 직접 받음 |
| `stop` | `options.stop` | `:167-168` | 그대로 |
| `response_format: json_object` | 최상위 `format:"json"` | `:169-170` | /v1 은 `response_format` 직접 지원 |
| `response_format: json_schema` | 최상위 `format:<schema 객체>` | `:171-173` | 동일 |
| `reasoning_effort` | 최상위 `think`. `gpt-oss*` 는 문자열 그대로, 그 외는 `low/medium/high` 면 `true` | `:174-178` | /v1 에서 지원 여부 확인 못함 |
| `tools`, `functions` | 최상위 `tools` 로 그대로(Ollama 0.4+ 네이티브 도구 호출 전제) | `:183-187, :298-299` | 그대로 가능 |
| `tool_choice`, `functions` | **삭제**("Ollama 요청이 멈춘다"는 주석) | `:188-189` | /v1 에서 `tool_choice` 가 무시/에러인지 확인 못함 |
| `stream` | 최상위 `stream` | `:232, :294` | 그대로 |
| `stream_options`, `n`, `presence_penalty`, `logit_bias`, `logprobs`, `user`, `parallel_tool_calls` | **지원 목록에 없음**. `drop_params` 가 꺼져 있으면 400 `UnsupportedParamsError`, 켜져 있으면 조용히 삭제(`utils.py:4200-4210, 4298-4303`) | `:130-145` 지원 목록 | 정책 결정 필요: 삭제 / 400 |
| `keep_alive`, `num_ctx` 등 Ollama 전용(`top_k`, `mirostat*`, `num_gqa`...) | 요청 body 에 있으면 `options` 로 넣음. 서버 설정(`OllamaChatConfig`)이 있으면 비어 있는 키를 채움 | `:234, :284-288` | **/v1 은 `num_ctx` 를 못 받는다(확인 못함, 외부 지식).** 컨텍스트 길이는 Modelfile 에서 정하거나 네이티브 `/api/chat` 의 `options.num_ctx` 로 보내야 한다 |
| (litellm 내부) `format` 이 json 이고 `function_name` 이 있으면 | 응답 content 를 JSON 으로 파싱해 tool_call 로 변환 | `:353-374` | 제외 (옛 기능 호환) |

### 0-3. `ollama_chat` 의 응답 변환 (Ollama -> OpenAI)
| Ollama 응답 | OpenAI 모양 | 위치 |
|---|---|---|
| NDJSON 스트림(한 줄이 한 JSON, SSE 아님) | 각 줄 -> `chat.completion.chunk` 하나. 마지막에 `[DONE]` 은 프록시가 붙임 | `chat/transformation.py:442-543` |
| `message` | `choices[0].delta` / `message` | `:510-514, :376` |
| `message.thinking` | `reasoning_content` | `:339-342, :488-490` |
| `message.content` 안의 `<think>..</think>` | 태그를 떼고 `reasoning_content` 로 분리(스트림은 상태 머신, `started/finished_reasoning_content`) | `:343-351, :491-508` |
| `message.tool_calls[].function.arguments` (객체) | arguments 가 완성돼 있으면 uuid `id` 를 붙임. `arguments` 를 JSON 문자열로 바꾸는 일은 `litellm.Message` 가 함 | `:474-483` |
| `done_reason` | `finish_reason`. tool_calls 가 한 번이라도 나오면 `tool_calls` 로 덮어씀 | `:335-336, :380-381, :516-522` |
| `prompt_eval_count`, `eval_count` | `usage.prompt_tokens/completion_tokens/total_tokens`. 비정상이면 `token_counter` 로 대체 계산(비스트림만) | `:384-397`, 스트림은 `_done_chunk_usage` `:416-425` |
| (없음) `id`, `created` | `chatcmpl-<uuid>`, 현재 시각을 새로 만듦 | `:536-543`, `llms/ollama/common_utils.py:18-54` |
| 오류 | `OllamaError`. 일부 문구를 보고 400/503/Timeout 으로 매핑 | `exception_mapping_utils.py:1928-1966` |
| 스트림 청크에 `message` 키가 없으면 | 400 으로 올림 | `:544-549` |

### 0-4. `openai/` + `/v1`(openai-compatible) 경로가 하는 일 = 사실상 거의 없음
`OpenAIGPTConfig.transform_request` (`llms/openai/chat/gpt_transformation.py:549-583`): `messages` 를 약간 손질하고(tool 메시지의 이미지 끌어올리기, `image_url` 문자열->객체, PDF URL 처리, `cache_control` 제거 `:403-436`), `max_retries` 를 빼고, tools 가 없으면 `tool_choice` 를 뺀 뒤 `optional_params` 를 body 에 펼쳐 넣는다.
- 지원 파라미터 허용 목록: `gpt_transformation.py:162-205`. 목록 밖 필드는 위 0-2 와 같은 `drop_params` 정책을 탄다.
- openai_like 계열(`llms/openai_like/chat/transformation.py:25-178`)은 추가로 ① `max_completion_tokens`->`max_tokens` 치환(`:164-176`), ② 응답 `usage` 의 `*_tokens: null` 을 0 으로(`:71-85`), ③ json_mode 일 때 단일 tool_call 을 content 로 옮김(`:43-68`) 을 한다.
- 응답: `convert_to_model_response_object` 로 pydantic 모델에 담아 필드를 정규화(`gpt_transformation.py:762-768`). 스트림은 `delta.reasoning`->`delta.reasoning_content`(`:917-935`), HTTP 200 본문 안의 `{"error":...}` 청크를 예외로 승격(`:937-950`).
- LM Studio 는 `response_format.json_schema` 모양 보정만(`llms/lm_studio/chat/transformation.py:35-56`). hosted_vllm 은 `thinking`->`reasoning_effort` 변환, `custom` 도구->function 도구, 비디오 file->`video_url`, assistant 의 `thinking_blocks` 제거(`llms/hosted_vllm/chat/transformation.py:31-179`).

### 0-5. 결론: pass-through 가능한가
- **가능하다.** 새 게이트웨이가 Ollama 의 `/v1/chat/completions` 를 부르면 body 는 거의 그대로 보내도 된다. litellm 의 `ollama_chat` 변환(0-2)은 "네이티브 `/api/chat` 으로 보내기 위해" 필요했던 것이고 `/v1` 을 쓰면 대부분 사라진다.
- **바꿔야 하는 최소 필드(필수)**
  1. `model`: 게이트웨이 별칭 -> 실제 Ollama 모델 태그. 응답 `model` 은 클라이언트가 요청한 이름으로 되돌려 쓴다(litellm 도 `_restamp_streaming_chunk_model` 로 함, `proxy/proxy_server.py` 스트림 루프).
  2. `stream_options.include_usage`: 스트림에서 사용량 회계를 하려면 업스트림에는 늘 `true` 로 보내고, 클라이언트가 켜지 않았으면 마지막 usage 청크만 제거해야 한다(`proxy/common_request_processing.py:560-594`, `_litellm_strip_stream_usage`). Ollama `/v1` 의 지원 여부는 확인 못함(외부 지식으로는 지원).
  3. 지원하지 않는 필드 정책: 삭제할지 400 으로 돌려줄지 하나로 정한다. litellm 기본은 400(`drop_params` 꺼짐).
  4. Authorization: 클라이언트의 게이트웨이 키를 Ollama 로 전달하지 않는다(키 교체/삭제).
- **확인이 필요한 것(확인 못함 항목)**: Ollama `/v1` 의 `tool_choice`, `reasoning_effort`, `response_format.json_schema`, `n`, `logprobs` 처리. 이 저장소 코드로는 확인할 수 없다. `docker-dev/` 의 실물 Ollama 로 한 번 찔러 보는 것을 권장한다.
- **`num_ctx`(컨텍스트 길이)** 는 /v1 로 못 바꾼다. 모델별로 Modelfile 에서 정하거나, 필요하면 Provider SPI 에 "네이티브 /api/chat 모드"를 두어야 한다.
- 네이티브 `/api/chat` 이 정말 필요해질 때만 0-2, 0-3 의 `thinking`, tool_calls arguments 객체<->문자열, NDJSON, `done_reason`, `prompt_eval_count` 변환을 구현하면 된다.

---
## 1. 라우터(`router.py`) - 배포(deployment) 선택·재시도·폴백

| # | 기능 | 구현 위치 | 동작 요약 | 설정 키 | 외부 호출·유료 | 권장 |
|---|---|---|---|---|---|---|
|1|model_list / model group|`router.py:1173`(init), `10872 set_model_list`, `10294 _create_deployment`|같은 `model_name` 을 가진 항목들이 한 그룹. 항목마다 `litellm_params`(model, api_base, api_key, rpm/tpm/weight/order/timeout 등) + `model_info.id`(해시)|`model_list[].model_name/litellm_params/model_info`|없음|필수: 별칭 -> Ollama 배포 매핑의 뼈대|
|2|model_group_alias|`router.py:13440`, `router_utils/common_utils.py:96`|별칭 이름을 다른 그룹 이름으로 치환. `hidden` 옵션으로 목록에서 숨김|`model_group_alias`|없음|필수: 클라이언트 모델명 -> 내부 모델명 변환이 곧 이것|
|3|simple-shuffle(가중 랜덤)|`router_strategy/simple_shuffle.py:43-67`|`weight` > `rpm` > `tpm` 순으로 값이 있는 첫 항목을 가중치로 `random.choices`. 없으면 균등 랜덤|`routing_strategy: simple-shuffle`(기본), `litellm_params.weight/rpm/tpm`|없음|필수(가중 랜덤은 단순). 가중치 없이 균등 랜덤만 1차|
|4|least-busy|`router_strategy/least_busy.py`(225줄)|진행 중 요청 수가 가장 적은 배포 선택. 카운터 TTL 1시간(`:IN_FLIGHT_COUNT_TTL_SECONDS`)|`routing_strategy: least-busy`|없음(Redis 선택)|후순위: 배포가 2개 이상일 때만 의미|
|5|latency-based|`router_strategy/lowest_latency.py`(576줄)|최근 지연(스트림은 TTFT) 평균이 낮은 배포. `ttl` 1시간, `lowest_latency_buffer`, `max_latency_list_size=10`|`routing_strategy: latency-based-routing`, `routing_strategy_args`|없음|후순위|
|6|usage-based v1/v2(TPM/RPM)|`router_strategy/lowest_tpm_rpm.py`(240줄), `lowest_tpm_rpm_v2.py`(679줄)|분당 토큰/요청 사용량이 낮은 배포. 한도 초과 배포 제외. 캐시 키 `global_router:{id}:{model}:tpm:{분}`|`routing_strategy: usage-based-routing(-v2)`, `tpm/rpm`|없음(다중 인스턴스는 Redis)|후순위: 배포별 rpm/tpm 상한은 필요해질 수 있음|
|7|cost-based|`router_strategy/lowest_cost.py`(329줄)|비용표의 입력/출력 단가가 낮은 배포. Ollama 는 단가 0 이라 의미 없음|`routing_strategy: cost-based-routing`|없음|제외: 로컬 모델은 비용 0|
|8|tag 기반 라우팅|`router_strategy/tag_based_routing.py`(792줄)|요청 tag 가 배포 tag 의 부분집합일 때만 후보. `!tag` 제외, `&tag` 필수, 기본 배포|`enable_tag_filtering`, `tag_filtering_match_any`, `tag_routing_prefix`|없음|후순위: 팀별 배포 분리가 필요할 때|
|9|order(우선순위 failover)|`router.py:14888, 16122`|`litellm_params.order` 가 낮은 배포 먼저, 실패하면 다음 순서|`litellm_params.order`|없음|필수에 가까움: 주 Ollama + 예비 Ollama 구성의 가장 단순한 방법|
|10|auto router(의미 기반)|`router_strategy/auto_router/auto_router.py`, `litellm_encoder.py`|`semantic_router` 패키지 + **임베딩 모델 호출**로 질의 의도를 분류해 대상 모델 선택|`model: auto_router/...`, `auto_router_config`|임베딩 호출 필요(내부 모델이면 폐쇄망 OK), 외부 패키지|제외: 의존성 큼, 효용 불확실|
|11|complexity router|`router_strategy/complexity_router/`(README 기준 7개 차원 규칙 점수, 분류기는 선택 시 LLM 호출)|요청 복잡도를 점수로 매겨 티어별 모델로 보냄. 기본은 로컬 규칙|`model: complexity_router/...`|기본 외부 호출 0, 선택 분류기는 모델 호출|제외: 1차 범위 밖|
|12|adaptive / quality router|`router_strategy/adaptive_router/`, `quality_router/`|밴딧 학습·품질 점수 기반 선택(`savings_baseline.py` 는 절감액 계산)|`model: adaptive_router/...`|분류기에 LLM 호출 가능|제외|
|13|LAR-1 semantic routing|`router_strategy/lar1_routing.py`(179줄)|요청 metadata 의 agent confidence 로 분기|`routing_strategy_args`|없음|제외|
|14|provider 예산 제한|`router_strategy/budget_limiter.py`(995줄)|제공자/배포/태그별 $ 예산 초과 시 후보에서 제외. 태그 예산은 `premium_user` 확인(`:978-981`)|`provider_budget_config`|Redis 선택. 태그 예산 유료 게이트|제외: 로컬 모델은 비용 개념 약함. 필요한 건 키/팀 예산(다른 영역)|
|15|routing group|`router.py:2007-2125`, `router_utils/routing_groups.py`|이름 붙은 하위 집합마다 별도 전략·우선순위|`routing_groups`|없음|제외|
|16|routing plugin / pre-routing hook|`router.py:15430-15858`|라우팅 직전에 모델·메시지를 바꾸는 훅(위 auto/complexity/adaptive 의 진입점). Claude Code 세션 고정 라우팅 포함(`:15569`)|`plugins`|없음|제외: 필요하면 Provider SPI 전 단계 필터로 후에|
|17|custom routing strategy|`router.py:16680 set_custom_routing_strategy`|사용자 클래스로 선택 로직 교체|`CustomRoutingStrategy`|없음|제외|
|18|요청 단위 전략 override|`router.py:2176-2255`|헤더/메타로 전략을 바꿈|요청 metadata|없음|제외|
|19|deployment affinity|`router_utils/pre_call_checks/deployment_affinity_check.py`, `router.py:2837`|같은 사용자 키를 같은 배포에 TTL 동안 붙임(프롬프트 캐시 효율)|`deployment_affinity_ttl_seconds=3600`, `model_group_affinity_config`|캐시 필요|후순위: Ollama 는 KV 캐시 재사용 이득이 있어 다중 인스턴스에서만|
|20|wildcard / 패턴 매칭 배포|`router_utils/pattern_match_deployments.py:63-176`|`ollama/*` 같은 와일드카드 model_name 으로 임의 모델 허용|`model_name: "ollama/*"`|없음|후순위: 모델 추가 때마다 설정을 안 고치려는 편의|
|21|max_parallel_requests(배포 동시성)|`router_utils/common_utils.py`(MaxParallelRequestsLimit)|배포별 동시 요청 슬롯. 가득 차면 대기 없이 429|`litellm_params.max_parallel_requests`|없음|필수: Ollama 는 GPU 동시성이 작아 과부하 방어가 핵심|
|22|pre-call checks(컨텍스트·rpm·지역)|`router.py:14198-14380`, `2860 add_optional_pre_call_checks`|프롬프트 토큰이 배포 `max_input_tokens` 를 넘으면 후보 제외. rpm 한도, 지역 필터|`enable_pre_call_checks`, `optional_pre_call_checks`|토큰 계산(아래 8번) 필요|후순위: 1차는 Ollama 가 내는 컨텍스트 초과 에러를 400 으로 매핑하는 것으로 충분|
|23|retry|`router.py:8869-9075 async_function_with_retries`, `9115 should_retry_this_error`, `9210 _time_to_sleep_before_retry`|실패 시 `num_retries`(기본 `openai.DEFAULT_MAX_RETRIES`=2, `router.py:1500-1504`)만큼 재시도. 408·409·429·5xx 만 재시도(`utils.py:7785-7810`). 같은 그룹에 건강한 배포가 있으면 즉시, 아니면 지수 백오프(0.5s x 2^n, 최대 8s, 지터 0.75s, `constants.py:579-581`). 401/403 은 배포가 2개 이상일 때만, 404 는 재시도 안 함|`num_retries`, `retry_after`, `retry_policy`, `model_group_retry_policy`|없음|필수: 단순 재시도(408·429·5xx, 백오프) 만. 정책 객체는 후순위|
|24|retry policy(예외별 횟수)|`router.py:16596`, `router_utils/get_retry_from_policy.py`|예외 종류별 재시도 횟수, 모델 그룹별 정책|`retry_policy`, `model_group_retry_policy`|없음|후순위|
|25|fallback(모델 그룹 단위)|`router.py:8451-8803`, `router_utils/fallback_event_handlers.py`|그룹 A 가 재시도 후에도 실패하면 `fallbacks:[{A:[B,C]}]` 순서로 이동. `*` 는 전역 기본 폴백. `max_fallbacks`(기본 `litellm.max_fallbacks`=None, 코드 주석상 5)|`fallbacks`, `default_fallbacks`, `max_fallbacks`, 요청 `disable_fallbacks`|없음|필수(단순형): 그룹 A 실패 -> 그룹 B 한 단계|
|26|context-window fallback|`router.py:9115-9140`|`ContextWindowExceededError` 면 재시도 없이 더 큰 컨텍스트 모델로 폴백|`context_window_fallbacks`|없음|후순위|
|27|content-policy fallback|`router.py:9741`|콘텐츠 정책 위반 응답 시 폴백|`content_policy_fallbacks`|없음|제외: 로컬 모델에 해당 없음|
|28|스트림 첫 청크 전 실패 -> 재시도/폴백|`router.py:829-948 _aread_first_chunk`, `3483 _acompletion_streaming_iterator`|첫 청크를 라우터가 미리 읽어, 토큰이 나오기 전에 실패하면 재시도·폴백 체인에 태움|(자동)|없음|필수: 스트림도 첫 청크 전 실패는 재시도 가능해야 함|
|29|스트림 중간 단절 -> 폴백 이어쓰기|`router.py:3483-3700`, `3841 _mid_stream_continuation_eligible`, `exceptions.py:1211 MidStreamFallbackError`|이미 일부 텍스트가 나간 뒤 끊기면 기본은 원래 오류를 올림. 켜면 생성된 텍스트를 prefill 로 넣어 다른 배포가 이어서 씀(도구호출·가드레일 있으면 불가)|`enable_mid_stream_fallback_continuation`(기본 꺼짐)|없음|제외: 복잡하고 위험. 중간 단절은 클라이언트에 오류 청크 후 종료|
|30|weighted failover|`router.py:8374 _maybe_run_weighted_failover`|simple-shuffle 일 때 실패한 배포를 빼고 가중치대로 다음 배포 시도|`enable_weighted_failover`|없음|후순위|
|31|cooldown|`router_utils/cooldown_handlers.py:402-505`, `cooldown_cache.py`, `router.py:1481-1483`|실패한 배포를 `cooldown_time`(기본 5초, `constants.py:153`) 동안 후보에서 제외. 조건: 429(단일 배포 그룹 제외), 1분간 요청이 5건 이상이고 실패율 50% 초과(`constants.py:147, 214`), 재시도 불가 상태코드(401/402/404/408). 단일 배포 그룹은 기본적으로 냉각 안 함|`cooldown_time`, `allowed_fails`, `allowed_fails_policy`, `disable_cooldowns`|캐시(in-memory/Redis)|필수: Ollama 가 죽었을 때 잠깐 빼는 서킷브레이커. 규칙은 "연속 N회 실패 -> T초"로 단순화|
|32|타임아웃|`router.py:4972-5003 _get_stream_timeout/_get_non_stream_timeout`, `constants.py:653`|우선순위: 요청 값 > 배포 `timeout`/`stream_timeout` > 라우터 값 > 전역 `request_timeout`. 전역 기본값 6000초(`DEFAULT_REQUEST_TIMEOUT_SECONDS`)|`timeout`, `stream_timeout`, `litellm_params.timeout`, `REQUEST_TIMEOUT`|없음|필수: 연결/읽기/전체 타임아웃. 기본 6000초는 너무 길다(로컬 모델 첫 로드가 길어 별도 설정 필요)|
|33|스트림 최대 지속 시간|`litellm_core_utils/streaming_handler.py:379-391`, `constants.py:166`|스트림이 N초를 넘으면 Timeout. 기본 꺼짐|`LITELLM_MAX_STREAMING_DURATION_SECONDS`|없음|후순위|
|34|반복 청크 감지|`streaming_handler.py:545-583`, `constants.py:572`|같은 청크가 100번 반복되면 InternalServerError(모델 무한 루프 차단)|`REPEATED_STREAMING_CHUNK_LIMIT`=100|없음|후순위|
|35|health check 라우팅|`router.py:1232-1233, 1485-1494, 16484 _async_filter_health_check_unhealthy_deployments`, `router_utils/health_state_cache.py`|백그라운드 헬스체크 결과(건강/불건강, 시각)를 캐시에 두고, 불건강 배포를 후보에서 제외. 오래된 결과는 무시|`enable_health_check_routing`, `health_check_staleness_threshold`, `health_check_ignore_transient_errors`, `background_health_check_model_groups`|배포로 실제 요청(`ahealth_check`)|후순위: 1차는 cooldown 으로 대체|
|36|백그라운드 health check 실행|`main.py:9046 ahealth_check`, `proxy/health_check.py:609-975`, `proxy/proxy_server.py:1728`, `constants.py:44, 1766, 1965`|배포마다 짧은 실제 호출(프롬프트 `"test from litellm"`)로 점검. 기본 간격 300초, 타임아웃 60초. `/health` 엔드포인트|`general_settings.background_health_check`, `health_check_interval`, `DEFAULT_HEALTH_CHECK_PROMPT`, `HEALTH_CHECK_TIMEOUT_SECONDS`|배포 호출(토큰 소모)|필수: `/health`(Ollama `GET /api/tags` 같은 가벼운 점검으로 충분)|
|37|priority 큐 / scheduler|`router.py:5232-5325`, `scheduler.py`(FlowItem 큐), `router_utils/request_priority.py`|요청에 priority 를 주면 큐에서 순서대로 호출|`default_priority`, `polling_interval`|캐시|제외|
|38|배치 호출 / 가장 빠른 응답|`router.py:5005 abatch_completion`, `5140 abatch_completion_fastest_response`|여러 모델에 동시에 보내 가장 빠른 응답 사용|요청 모델 리스트|비용 증가|제외|
|39|silent experiment|`router.py:3198-3300`|같은 요청을 몰래 다른 모델에도 보내 비교|요청 metadata|추가 호출|제외|
|40|client TTL / 클라이언트 캐시|`router.py:1350, 13971 _get_client`, `router_utils/client_initalization_utils.py`|배포별 HTTP 클라이언트를 `client_ttl`(기본 3600초) 동안 재사용|`client_ttl`|없음|필수: 커넥션 풀 재사용(WebFlux 의 WebClient 로 대체)|
|41|배포 단위 설정 반영(upsert/delete)|`router.py:11140-11590`|런타임에 배포 추가/수정/삭제. 인덱스·비용표·예산 동기화|API(`/model/new` 등, 다른 영역)|없음|후순위: 1차는 설정 파일 + 재시작|
|42|Kubernetes pod 탐색|`router_utils/kubernetes_pod_discovery.py`|서비스 뒤의 개별 파드를 배포로 해석|`litellm_params` 파드 탐색 옵션|K8s API|제외|
|43|PTU 지분·access window|`router_utils/ptu_shares.py`, `access_windows.py`|Azure PTU 용량을 팀에 나눔, 시간대별 접근 제한|`model_info` 확장 키|없음|제외|
|44|router 내 응답 캐시|`router.py:1195-1196 cache_responses, caching_groups`, 아래 4장|모델 그룹 간 캐시 공유|`cache_responses`, `caching_groups`|캐시 백엔드|후순위|
|45|라우터 통계 / 응답 헤더|`router.py:12822 set_response_headers`, `router_utils/response_headers.py`, `add_retry_fallback_headers.py`|`x-litellm-attempted-retries`, `x-litellm-attempted-fallbacks`, 남은 rpm/tpm 헤더|(자동)|없음|후순위: 재시도·폴백 횟수 헤더 정도만|
|46|알림(Slack)|`router.py:1237 alerting_config, 16658 _initialize_alerting`|냉각·장애 알림|`alerting_config`|Slack 외부 호출|제외: 폐쇄망 외부 호출|
|47|guardrail 라우팅 / 검색 도구 / 벡터스토어 / 컨테이너 / 영상 / 스킬 / 에이전트 엔드포인트|`router.py:2442-2760 _initialize_*_endpoints`|추론 외 API 를 라우터 경유로 호출|`search_tools`, `guardrail_list`, `assistants_config`|대부분 외부 호출|제외: 1차는 chat completions 하나|

(참고) 라우터가 지원하는 전략 이름은 `types/router.py:1201-1207` 의 6가지(`least-busy`, `latency-based-routing`, `cost-based-routing`, `usage-based-routing-v2`, `usage-based-routing`, `provider-budget-routing`) + 기본 `simple-shuffle`.

---
## 2. 호출 경로(`main.py`)

| # | 기능 | 구현 위치 | 동작 요약 | 설정 키 | 외부 호출·유료 | 권장 |
|---|---|---|---|---|---|---|
|48|completion / acompletion 흐름|`main.py:452 acompletion`, `5319 completion`|`get_llm_provider`(모델 접두사 -> 제공자) -> `get_optional_params`(OpenAI 파라미터 -> 제공자 파라미터) -> 제공자별 디스패치(`6309-6313` 에서 `ollama`, `ollama_chat`) -> `base_llm_http_handler.completion`(요청 변환·HTTP·응답 변환)|-|-|필수: 이 4단계가 Provider SPI 의 모양(`resolve`, `mapParams`, `transform`, `parse`)|
|49|모델명 -> 제공자 해석|`litellm_core_utils/get_llm_provider_logic.py:592-860`, `:276-340`|`ollama/x`, `openai/x`, `hosted_vllm/x` 접두사. api_base 가 알려진 엔드포인트면 제공자를 추정(`ollama.com` 이면 Ollama **클라우드**, `:333`)|`model: "<provider>/<모델>"`|`ollama.com` 으로 가면 외부 호출|필수(접두사 방식 단순화). 외부 엔드포인트 자동 추정은 제외|
|50|OpenAI 파라미터 매핑·미지원 처리|`utils.py:4964 get_optional_params`, `4012-4350`, `__init__.py:231 drop_params`|제공자 지원 목록에 없는 파라미터는 `drop_params` 켜져 있으면 삭제, 아니면 `UnsupportedParamsError`(400)|`litellm.drop_params`, 요청 `drop_params`, `additional_drop_params`|없음|필수: 정책 하나 고정(권장: 알 수 없는 필드는 Ollama 로 그대로 통과, 문서화된 미지원만 거부)|
|51|함수 호출 미지원 모델 처리|`utils.py:4905-4948`|미지원 제공자는 tools 를 프롬프트로 합치거나 에러. `ollama`(구) 는 `format=json` + 프롬프트 삽입, `ollama_chat` 은 예외로 통과|`litellm.add_function_to_prompt`|없음|제외|
|52|예외 매핑(OpenAI 오류 모양)|`litellm_core_utils/exception_mapping_utils.py`(2,901줄), Ollama 는 `:1928-1966`|제공자 오류를 OpenAI 의 `BadRequest/RateLimit/Timeout/ServiceUnavailable/ContextWindowExceeded...` 로 통일하고 상태코드·`type`/`code` 를 맞춤|-|없음|필수: 업스트림 오류 -> OpenAI 오류 JSON 변환(`{error:{message,type,param,code}}`)|
|53|text_completion / embedding(Ollama)|`main.py:7761`, `7240-7260`, `llms/ollama/completion/handler.py:96, 120`|임베딩은 Ollama `/api/embed` 호출. `/v1/completions` 는 `/api/generate`|`input`|없음|후순위(임베딩). `/v1/completions` 는 제외|
|54|가짜 스트림(fake stream)|`llms/base_llm/chat/transformation.py:168`|스트림 미지원 제공자 응답을 청크로 쪼개 흉내. 기본 False|-|없음|제외(Ollama 는 진짜 스트림)|
|55|422 에러 재시도 훅|`llms/base_llm/chat/transformation.py:210-235`|제공자가 422 면 요청을 고쳐 재시도(제공자별 선택)|-|없음|제외|

---
## 3. 프로바이더 계층 분류(160개 디렉터리, `LlmProviders` enum 175개)

구조: `llms/base_llm/chat/transformation.py:91 BaseConfig` 가 `get_supported_openai_params`, `map_openai_params`, `validate_environment`, `get_complete_url`, `transform_request`, `transform_response`, `get_error_class`, `get_model_response_iterator` 를 요구한다. 제공자 하나 = BaseConfig 구현 하나. HTTP 는 `llms/custom_httpx/llm_http_handler.py` 가 공통 처리.
OpenAI 호환 제공자 목록: `constants.py:1066-1142`(약 80개). 그중 JSON 설정만으로 추가되는 것은 `llms/openai_like/providers.json`(35개).

| 분류 | 개수(추정) | 제공자 | 새 게이트웨이 권장 |
|---|---|---|---|
|로컬/온프렘 서버|약 10|`ollama`, `ollama_chat`, `hosted_vllm`, `vllm`, `lm_studio`, `llamafile`, `docker_model_runner`, `xinference`, `oobabooga`, `petals`, `triton`, `infinity`(임베딩), `lemonade`, `langflow`/`ragflow`(워크플로)|**Ollama 만 필수.** vLLM/LM Studio/llama.cpp 계열은 OpenAI 호환이라 Provider SPI 의 "openai-compatible" 구현 하나로 흡수(후순위)|
|범용 OpenAI 호환(주소만 바꾸면 됨)|약 15|`openai_like`, `custom_openai`, `litellm_proxy`, `hosted_vllm`, `lm_studio`, `llamafile`, `docker_model_runner`|후순위: "OpenAI 호환 provider" 한 종류로 일반화|
|클라우드 OpenAI 호환(외부 SaaS)|약 60|`openai`, `groq`, `deepseek`, `together_ai`, `fireworks_ai`, `perplexity`, `mistral`, `codestral`, `xai`, `cerebras`, `sambanova`, `nvidia_nim`, `openrouter`, `deepinfra`, `novita`, `nebius`, `moonshot`, `dashscope`, `volcengine`, `hyperbolic`, `lambda_ai`, `featherless_ai`, `friendliai`, `scaleway`, `ovhcloud`, `vercel_ai_gateway`, `github`, `github_copilot`, `chatgpt`, `heroku`, `helicone` 외 `providers.json` 35개|제외: 폐쇄망에서 외부 호출 불가|
|클라우드 전용 API(변환 필요)|약 25|`anthropic`, `bedrock`, `bedrock_mantle`, `sagemaker*`, `vertex_ai*`, `gemini`, `azure`, `azure_ai`, `cohere*`, `ai21*`, `watsonx*`, `databricks`, `huggingface`, `replicate`, `oci`, `snowflake`, `sap`, `gigachat`, `clarifai`, `predibase`, `nlp_cloud`, `baseten`|제외|
|비채팅(이미지·음성·검색·벡터·OCR)|약 40|`black_forest_labs`, `recraft`, `stability`, `fal_ai`, `runwayml`, `deepgram`, `elevenlabs`, `assemblyai`, `aws_polly`, `voyage`, `jina_ai`, `milvus`, `pg_vector`, `s3_vectors`, `mongodb`, `tavily`, `exa_ai`, `brave`, `serper`, `firecrawl`, `duckduckgo`, `searxng`, `google_pse`, `linkup`, `reducto`, `topaz` 등|제외: 1차는 chat completions 만|
|에이전트·프로토콜|약 10|`a2a`, `a2a_agent`, `langgraph`, `manus`, `claude_code`, `codex`, `cursor`, `microsoft_365_copilot`, `agentcore`, `pass_through`|제외|

주의: 위 개수는 디렉터리·enum 이름으로 분류한 추정치이고, 개별 제공자의 실제 호환 정도는 하나씩 확인하지 않았다(확인 못함).

| # | 기능 | 구현 위치 | 동작 요약 | 설정 키 | 외부 호출·유료 | 권장 |
|---|---|---|---|---|---|---|
|56|BaseConfig 변환 계층|`llms/base_llm/chat/transformation.py:91-521`|제공자별 요청/응답/오류/스트림 이터레이터 변환 인터페이스|-|-|필수: Provider SPI 의 원형|
|57|OpenAI 호환 공통 변환|`llms/openai/chat/gpt_transformation.py`(979줄)|0-4 참조|`api_base`, `api_key`|없음|필수(최소형만)|
|58|openai_like + JSON 제공자|`llms/openai_like/dynamic_config.py`, `json_loader.py`, `providers.json`|코드 없이 JSON 한 항목으로 OpenAI 호환 제공자를 추가|`providers.json`|없음|제외(우리는 Ollama 중심)|
|59|Ollama 모델 목록·컨텍스트 조회|`llms/ollama/common_utils.py:96-290`|`GET /api/tags` 로 모델 나열, `POST /api/show` 로 `context_length`·도구 지원 여부(`template` 에 `tools` 포함 여부)를 알아냄. 실패하면 정적 목록|`api_base`|Ollama 내부 호출|후순위: `/v1/models` 는 Ollama `/v1/models` 를 그대로 중계하거나 설정 파일 목록으로 충분|
|60|Ollama 이미지 재인코딩|`llms/ollama/common_utils.py:65-93`|PNG/JPEG 가 아니면 Pillow 로 JPEG 변환. Pillow 없으면 예외|-|없음|제외: /v1 은 불필요|
|61|Ollama 응답 id 정규화|`llms/ollama/common_utils.py:18-54`, `streaming_handler.py:38, 342`, `convert_dict_to_response.py:881`|`system_fingerprint=="fp_ollama"` 인 응답에 `chatcmpl-<uuid>` id 를 부여하고 원래 id 는 `provider_specific_fields.original_response_id` 로 보존|-|없음|필수: 스트림 전체에서 같은 `id` 유지(OpenAI SDK 가 기대)|

---
## 4. 스트리밍 처리

| # | 기능 | 구현 위치 | 동작 요약 | 설정 키 | 외부 호출·유료 | 권장 |
|---|---|---|---|---|---|---|
|62|SSE 직렬화·종료|`proxy/proxy_server.py:10380 async_data_generator`, `proxy/common_request_processing.py:683-686`, `proxy_server.py` 응답 `media_type="text/event-stream"`|청크마다 `data: {json}\n\n`, 끝에 `data: [DONE]\n\n`. 이미 잘린 프레임은 `\n\n` 을 보정|-|없음|필수|
|63|keepalive|`proxy_server.py:10310-10341 _make_keepalive_resolver`, `constants.py:685`|출력 없이 길어지면 `: ping\n\n` 주석 프레임을 보내 프록시/로드밸런서 끊김 방지|`litellm_settings.sse_keepalive_ping_interval_seconds`, 배포별 설정|없음|필수: Ollama 는 첫 토큰까지 오래 걸릴 수 있음(모델 로딩)|
|64|stream_options.include_usage 처리|`proxy/common_request_processing.py:560-594`, `streaming_handler.py:516-517, 1980-1992`, `utils.py:7988`|클라이언트 값과 무관하게 업스트림에는 `include_usage:true` 를 넣어 사용량을 받고, 클라이언트가 안 켰으면 usage 전용 청크만 제거(`_litellm_strip_stream_usage`). `always_include_stream_usage` 로 강제 가능|`general_settings.always_include_stream_usage`|없음|필수(사용량 회계가 스트림 끝 usage 에 의존)|
|65|청크 정규화|`streaming_handler.py:836-1250 model_response_creator/return_processed_chunk_logic`, `902 set_model_id`|모든 제공자 청크를 `ModelResponseStream` 으로: 첫 청크에 `role:"assistant"`, 마지막에 `finish_reason`, 빈 delta 제거, `id`/`created`/`model` 일관화|-|없음|필수: 최소 규칙만(첫 청크 role, 마지막 finish_reason, 같은 id)|
|66|model 이름 되돌려쓰기|`proxy_server.py` 스트림 루프의 `_restamp_streaming_chunk_model`|응답의 `model` 을 클라이언트가 요청한 이름으로 바꿈|-|없음|필수|
|67|thinking/reasoning 변환|`ollama/chat/transformation.py:488-508`, `openai/chat/gpt_transformation.py:917-935`, `prompt_templates/common_utils.py:2218-2235, 2447-2467`|`message.thinking`/`delta.reasoning`/`<think>` 태그 -> `reasoning_content`. 스트림은 `<think>` 시작/종료 상태 추적|-|없음|필수(최소): 필드 이름 통일. 태그 파싱은 후순위|
|68|tool call 필드 변환(스트림)|`ollama/chat/transformation.py:474-483`, `streaming_handler.py:1094-1250`|Ollama 는 도구 호출을 한 청크에 완성된 객체로 줌 -> uuid id 부여 + arguments 를 문자열로. OpenAI 스트림의 `index` 기반 조각 누적은 `streaming_chunk_builder_utils.py` 가 비스트림 응답 조립에 사용|-|없음|필수: /v1 사용 시 불필요(Ollama 가 OpenAI 모양으로 줌, 확인 못함). 네이티브일 때만|
|69|finish_reason 보정|`streaming_handler.py:1893-1908`, `ollama/chat/transformation.py:516-522`|`stop` 인데 도구 호출이 있었으면 `tool_calls`. 없으면 `stop`|-|없음|필수|
|70|스트림 도중 업스트림 오류|`proxy_server.py:10580-10640`, `llms/openai/chat/gpt_transformation.py:937-950`|HTTP 200 스트림 안의 `{"error":..}` 를 감지해 예외화. 오류가 나면 이미 시작된 SSE 에는 `data: {"error":{...}}\n\n` 후 `[DONE]` 로 종료|-|없음|필수|
|71|클라이언트 중간 단절|`proxy/common_request_processing.py:287-380, 434-513`, `proxy_server.py:10583`|연결이 끊기면(`CancelledError`/`GeneratorExit`) 업스트림 스트림을 닫고, 그때까지 생성된 분량을 부분 청구로 기록, 499 로 기록. 동시성 슬롯 반환|-|없음|필수: 단절 시 업스트림 요청 취소(Ollama GPU 점유 해제) + 부분 사용량 기록|
|72|스트림 응답 캐시 재생|`caching/caching_handler.py:1104 _convert_cached_stream_response`|캐시된 완성 응답을 청크로 쪼개 스트림처럼 돌려줌|-|없음|후순위|
|73|NDJSON(Ollama)·SSE 파서|`llms/base_llm/base_model_iterator.py:87-230`|`data:` 접두사 제거, `[DONE]` 처리, 줄 단위 JSON 파싱. Ollama 네이티브는 `data:` 없는 NDJSON|-|없음|필수(SSE). NDJSON 은 네이티브 모드에서만|
|74|스트림 완료 후 비동기 로깅·비용|`streaming_handler.py:1852-1992, 2298`|모든 청크를 모아 완성 응답을 만들어 비용·로그 콜백 실행|-|없음|필수(최소): 최종 usage 만 기록|

---
## 5. 캐싱(`caching/`)

| # | 기능 | 구현 위치 | 동작 요약 | 설정 키 | 외부 호출·유료 | 권장 |
|---|---|---|---|---|---|---|
|75|캐시 키 생성|`caching/caching.py:379-447`|호출 인자(모델, messages, temperature 등 LLM 파라미터)를 이어 붙여 해시. 네임스페이스 접두. 의미 캐시는 범위 키 추가|`namespace`, 요청 `cache.namespace`|없음|후순위|
|76|in-memory 캐시|`caching/in_memory_cache.py:28-47`|항목 200개, 항목당 1MB, 기본 TTL 600초|`max_size_in_memory`, `default_in_memory_ttl`|없음|후순위: 응답 캐시는 1차 범위 밖. 필요하면 Caffeine 한 종류|
|77|Redis 캐시(+cluster/batch)|`redis_cache.py`, `redis_cluster_cache.py`, `redis_batch.py`, `redis_cluster_node_isolation.py`|분산 캐시. 프록시 설정에서 `cache: true` 이고 type 미지정이면 기본이 `redis`(`proxy/proxy_server.py:6575`)|`cache_params.type/host/port/password`|Redis(내부면 OK)|후순위: 다중 인스턴스 때 쿨다운·rpm 공유용으로만|
|78|DualCache(메모리+Redis)|`caching/dual_cache.py`|라우터 상태(쿨다운·사용량)의 저장소|-|Redis 선택|필수(메모리만): 쿨다운·in-flight 카운터|
|79|disk 캐시|`caching/disk_cache.py`|diskcache 패키지로 파일 저장|`disk_cache_dir`|없음|제외|
|80|semantic 캐시(redis/valkey/qdrant)|`redis_semantic_cache.py`, `valkey_semantic_cache.py`, `qdrant_semantic_cache.py`, `_embedding_router.py`|질의를 **임베딩 모델로 벡터화**해 유사도 임계값 이상이면 캐시 응답. 기본 임베딩 모델명 `text-embedding-ada-002`(OpenAI) — 폐쇄망에서는 내부 임베딩 모델로 바꿔야 함|`similarity_threshold`, `redis_semantic_cache_embedding_model`|임베딩 호출 + 벡터 저장소. 기본값은 외부 OpenAI 모델명|제외|
|81|S3/GCS/Azure Blob 캐시|`s3_cache.py`, `gcs_cache.py`, `azure_blob_cache.py`|객체 스토리지에 저장|`s3_*`, `gcs_*`, `azure_*`|클라우드 외부 호출|제외|
|82|캐시 제어(요청 단위)|`caching/caching.py:640-720, 966 should_use_cache`|`cache:{no-cache, no-store, ttl, s-maxage, namespace}` 로 요청별 제어. `mode: default_on/default_off`|요청 `cache` 필드, `Cache(mode=...)`|없음|제외|
|83|지원 호출 유형|`types/caching.py:44-58`|completion, embedding, transcription, text_completion, rerank, responses, anthropic_messages 등|`supported_call_types`|없음|후순위(chat 만)|
|84|prompt caching 배포 고정|`router_utils/pre_call_checks/prompt_caching_deployment_check.py`, `prompt_caching_cache.py`|프롬프트 캐시를 쓴 배포로 후속 요청을 보냄(Anthropic/OpenAI 류)|`optional_pre_call_checks`|없음|제외|

---
## 6. 모델 정보 · 컨텍스트 윈도 · 토큰 카운팅

| # | 기능 | 구현 위치 | 동작 요약 | 설정 키 | 외부 호출·유료 | 권장 |
|---|---|---|---|---|---|---|
|85|모델 비용·컨텍스트 표|`model_prices_and_context_window.json`(저장소 루트 2.9MB, 4,245개), `litellm/model_prices_and_context_window_backup.json`, `litellm_core_utils/get_model_cost_map.py`, `__init__.py:410-413, 550-552`|모델별 `max_input_tokens`, `max_output_tokens`, 단가, `supports_*` 플래그. **import 시점에 GitHub raw URL 에서 최신본을 내려받는다**(실패하면 번들 백업). 끄려면 `LITELLM_LOCAL_MODEL_COST_MAP=true`(`get_model_cost_map.py:459, 743`)|`LITELLM_LOCAL_MODEL_COST_MAP`, `LITELLM_MODEL_COST_MAP_URL`|**기본 켜짐: 외부 호출(raw.githubusercontent.com)**|제외(표 자체). 필요한 건 Ollama 모델별 컨텍스트 길이를 설정 파일에 직접 적는 것|
|86|Ollama 모델 항목|표의 `ollama/*` 키(예: `ollama/llama3.1`: 8192, 단가 0, `supports_function_calling:true`)|정적 표는 낡고 부정확. 런타임에는 `/api/show` 값을 사용(59번)|-|-|후순위: 설정 파일의 모델별 `context_length`|
|87|모델 정보 조회 API|`utils.py:7031 get_model_info`, `6031 get_max_tokens`, `3031 supports_function_calling`, `3326 supports_vision`, `3344 supports_reasoning`, `router.py:12086 get_router_model_info`, `12621 get_model_group_info`|표 + 배포별 `model_info` 덮어쓰기를 합쳐 능력 플래그 판정|`model_info.*`|없음|후순위|
|88|토큰 카운터|`litellm_core_utils/token_counter.py:417`, `tokenizer.py`, `default_encoding.py`, `utils.py:2591-2714 _select_tokenizer_helper`|기본은 tiktoken(`TIKTOKEN_CACHE_DIR` 를 번들 `tokenizers/` 로 고정해 오프라인 동작, `default_encoding.py:17-30`). 메시지 오버헤드·이미지 타일·도구 정의 토큰을 가산. 번들 파일: anthropic/deepseek tokenizer.json|`litellm.disable_token_counter`, `CUSTOM_TIKTOKEN_CACHE_DIR`|아래 89번 참조|후순위(1차는 Ollama 가 주는 `prompt_eval_count`/`eval_count` 사용). 필요 시 JTokkit|
|89|HF 토크나이저 런타임 다운로드|`utils.py:2674-2714`, `__init__.py:1565-1567 disable_hf_tokenizer_download`(기본 None=허용)|모델 이름이 llama2/llama3/cohere 계열이면 **`hf-internal-testing/llama-tokenizer`, `Xenova/llama-3-tokenizer`, `Xenova/c4ai-command-r-v01-tokenizer` 를 HuggingFace 에서 내려받음**. 실패하면 tiktoken 으로 대체|`litellm.disable_hf_tokenizer_download`|**기본 켜짐: 외부 호출(huggingface.co)**|제외: 토크나이저 외부 다운로드 금지|
|90|사용량 대체 계산|`ollama/chat/transformation.py:384-397`, `litellm_core_utils/token_counter.py`|업스트림이 `usage` 를 안 주면 토큰 카운터로 추정|-|없음|후순위: 추정값은 "추정" 표시|
|91|블로그 RSS 가져오기|`__init__.py:413-416`, `litellm_core_utils/get_blog_posts.py:8, 132`|UI 용 공지를 `https://docs.litellm.ai/blog/rss.xml` 에서 가져옴. 끄려면 `LITELLM_LOCAL_BLOG_POSTS=true`|`LITELLM_BLOG_POSTS_URL`, `LITELLM_LOCAL_BLOG_POSTS`|**기본 켜짐: 외부 호출**|제외|

---
## 7. 정리

### 7-1. 항목 수
총 91행(라우터 47, 호출 경로 8, 프로바이더 계층 6, 스트리밍 13, 캐시 10, 모델 정보·토큰 7) + 프로바이더 분류표 6범주 + Ollama 변환표.

### 7-2. 1차에 필수로 본 것
라우터: model group + 별칭(1,2), 가중/균등 랜덤(3), order 우선순위(9), 배포별 동시성 상한(21), 단순 재시도·백오프(23), 그룹 단위 폴백(25), 스트림 첫 청크 전 재시도(28), 쿨다운(31), 타임아웃(32), `/health`(36), 클라이언트 풀(40).
호출 경로: 모델 해석 -> 파라미터 매핑 -> 변환 -> 호출 4단계(48), 미지원 파라미터 정책(50), OpenAI 오류 모양(52).
스트리밍: SSE, `[DONE]`, keepalive, include_usage 주입·제거, 청크 정규화, model 되돌려쓰기, reasoning 필드 통일, finish_reason 보정, 중간 오류 전달, 단절 시 업스트림 취소와 부분 사용량.
캐시: 메모리 상태 저장소(DualCache 의 메모리 부분)만. 응답 캐시는 후순위.

### 7-3. 놀라운 발견
1. **폐쇄망 위반 기본값이 3개 있다**: 모델 비용표를 import 시점에 GitHub 에서 받음(`LITELLM_LOCAL_MODEL_COST_MAP` 미설정 시), llama/cohere 계열 모델 이름이면 HuggingFace 에서 토크나이저를 받음, 블로그 RSS 를 docs.litellm.ai 에서 받음. `docker-dev/compose.yaml` 이 이 환경변수를 설정하는지는 확인 못함. product.md 1번과 충돌하므로 이전 단계에서 고정 필요.
2. `ollama.com` 이 엔드포인트 목록에 있어 api_base 가 `ollama.com` 이면 Ollama 클라우드로 자동 판정된다(`get_llm_provider_logic.py:333`).
3. `ollama_chat` 은 `tool_choice`, `functions` 를 **조용히 삭제**하고, `stream_options`, `n`, `presence_penalty`, `logit_bias`, `user`, `parallel_tool_calls` 는 `drop_params` 기본(꺼짐)에서 400 을 낸다. 즉 OpenAI SDK 가 흔히 보내는 `stream_options` 만으로도 `ollama_chat` 직결은 400 이 날 수 있는 구조다(프록시가 먼저 처리하므로 평소엔 안 보임).
4. `frequency_penalty` 를 Ollama 의 `repeat_penalty` 로 이름만 바꿔 넣는다(의미가 다른 값). `num_ctx` 기본 2048 문제는 litellm 이 해결하지 않는다(설정으로만).
5. fork 의 router 가 upstream 보다 훨씬 크다: 자동 라우터 3종(complexity/adaptive/quality)이 `router_strategy/` 하위 11,977줄. Ollama 한 종류 1차 범위에서는 거의 전부 제외 대상.
6. 이미지 `image_url` 이 http URL 이면 네이티브 `ollama_chat` 은 내려받지 않고 URL 문자열을 `images` 에 그대로 넣는다(코드상 다운로드 없음). 네이티브 비전은 data URL 만 안전하다.
7. 중간 단절 폴백 이어쓰기(29)는 기본 꺼짐이고, 도구호출·가드레일·응답 제약이 있으면 불가해서 사실상 제한적 기능이다.
8. Redis 시맨틱 캐시의 기본 임베딩 모델명이 `text-embedding-ada-002`(OpenAI)라 폐쇄망에서는 기본값 그대로는 못 쓴다.

### 7-4. 확인 못함
- Ollama `/v1/chat/completions` 의 필드별 실제 지원(`tool_choice`, `reasoning_effort`, `json_schema`, `n`, `logprobs`, `stream_options.include_usage`): 코드로 확인 불가. 실물(`docker-dev/`)로 확인 필요.
- 개별 OpenAI 호환 제공자 60여 개의 세부 차이, `docker-dev/` 에서의 `LITELLM_LOCAL_*` 설정 여부.
- 라우터 중 auto/complexity/adaptive/quality router 내부 상세(규모가 커서 README·진입점만 확인).
