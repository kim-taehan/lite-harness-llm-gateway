# D. 비용·예산·속도제한·사용량 회계 전수조사

경로 기준: `litellm/` 아래 (P = `litellm/proxy/`). 라인은 2026-11 시점 `litellm_airgap` 워킹트리. "premium" = `premium_user is not True` 게이트(LITELLM_LICENSE 필요, 이 fork 는 게이트 코드를 아직 안 지웠다 — `PREMIUM_WALL_REVIEW.md` 참고).
권장 열: 필수 / 후순위 / 제외.

## 0. 먼저 읽을 요약 (spend 가 어떻게 쓰이고 얼마나 정확한가)

실제 흐름(코드 확인):
1. 인증 시 예산 확인 → 예산이 걸린 엔티티가 있으면 **요청 전에 "최대 비용 추정치"를 카운터에 미리 예약**(`user_api_key_auth.py:3925-3960` → `spend_tracking/budget_reservation.py:231`). `general_settings.disable_budget_reservation` 으로 끈다. 추정치 = 입력 토큰 수 × 입력단가 + max_tokens(없으면 모델 상한)×출력단가 (`budget_reservation.py:1286-1470`). 모델 단가를 모르거나 0 이면 예약 안 함(`:289-291`).
2. 응답 후 콜백 `_PROXY_track_cost_callback`(P/hooks/proxy_track_cost_callback.py:556)이 비용 계산 → **`increment_spend_counters`(P/proxy_server.py:3411)를 await 로 즉시 실행**. 카운터는 `spend_counter_cache`(DualCache: 프로세스 메모리 + 있으면 Redis). 예약분은 실제 비용으로 정산(reconcile, `proxy_server.py:3730`). 예산 차단은 이 카운터를 읽는다(`get_current_spend`, `proxy_server.py:3162`), DB 컬럼이 아니다.
3. 같은 콜백에서 `DBSpendUpdateWriter.update_database`(P/db/db_spend_update_writer.py:554)가 **메모리 큐에 적재만** 한다: key/user/team/org/end_user/team_member 증분(`SpendUpdateQueue`), 일별 집계 6종 큐, SpendLogs 행 큐(`enqueue_spend_logs`, P/utils.py:7926). `asyncio.create_task(_batch_database_updates)`(`:663`)로 요청 경로를 막지 않는다.
4. **DB 반영은 배치**: APScheduler `update_spend_job` 이 `PROXY_BATCH_WRITE_AT`(10초, constants.py:1939) + 랜덤 0~5초 간격(`proxy_server.py:11212, 11245`)으로 `update_spend`(P/utils.py:8148) 실행. 테이블별 `UPDATE ... SET spend = spend + $inc` 를 id 정렬 순서로 한 트랜잭션에 묶음(`db_spend_update_writer.py:2138-`), 데드락·연결오류는 3회 재시도+지수 백오프(`:2108`). SpendLogs 는 1000행 단위 create_many, 한 번에 최대 10000행(`utils.py:8043, 8335`). 큐가 100건(`SPEND_LOG_QUEUE_SIZE_THRESHOLD`) 넘으면 `monitor_spend_logs_queue`(`utils.py:8532`)가 앞당겨 flush. 태그 일별 집계는 별도 job(주기 배수, `proxy_server.py:11258`). Redis 가 있으면 큐를 Redis 버퍼 경유(`_commit_spend_updates_to_db_with_redis`, `:1635`, 1K RPS 이상 데드락 회피용 — 주석 `:1855`).
5. 정확도 한계: (a) DB spend 컬럼·SpendLogs·일별 집계는 최대 약 10~15초(+쓰기 실패 재시도) 지연. (b) 프로세스가 죽으면 아직 flush 안 된 증분·로그가 사라진다(Redis 버퍼 없을 때). 재기동 후 카운터는 DB 값으로 reseed(`P/db/spend_counter_reseed.py`) → 유실분만큼 과소 계상. (c) 예약 껐거나 추정 불가(모델 단가 0/미등록)이면 동시 요청이 모두 통과해 한도를 넘는다(`_types.py:3055` 경고문). (d) 스트리밍은 완료 시점 비용으로 정산, 취소 시 예약 해제(`budget_reservation.py:379`). (e) 비교 연산 불일치: 키 `spend >= max_budget`(auth_checks.py:6697 근처), 팀 `spend > max_budget`(`:7186`), 글로벌 `>`(`:916`).

→ 새 설계("요청 후 배치 큐 + 인스턴스 1개 + 약간 늦은 차단 허용")는 litellm 의 DB 쪽 방식과 같다. 다만 litellm 은 **차단 판단용 카운터를 DB 와 분리해 메모리에서 즉시 갱신**하므로 차단 지연이 사실상 0 이고, 새 설계가 "DB 값으로 차단"이면 최대 배치 주기만큼 늦다. 인스턴스 1개면 메모리 카운터(AtomicReference/ConcurrentHashMap)로 같은 효과를 얻을 수 있어 Redis 는 불필요.

## 1. 놀라운 발견

1. **Ollama/로컬 모델은 비용 0 이라 예산이 아예 안 걸린다.** `cost_calculator.py:800-813` 은 단가표에 없으면 `0.0, 0.0` 반환(로그는 debug 한 줄). 단가표의 `ollama/*` 29개도 전부 0.0. 0 비용 모델은 `is_model_cost_zero`(P/auth/auth_checks.py:535, 1066-1073, user_api_key_auth.py:2936-2940)로 **모든 예산 검사를 건너뛴다**. 폐쇄망에서 $ 예산을 쓰려면 배포마다 `input_cost_per_token`/`output_cost_per_token` 커스텀 단가를 줘야 한다(`cost_calculator.py:221`). 실질 제어는 TPM/RPM/TPD 토큰 한도다 → 새 게이트웨이는 **토큰 기반 쿼터를 1순위, $ 예산은 단가표 기반 선택 기능**으로 두는 게 맞다.
2. **비용표를 기동 시 GitHub 에서 내려받는다.** `litellm/__init__.py:410-412,552` 기본 URL `raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`, `litellm_core_utils/get_model_cost_map.py:718-780` 이 5초 타임아웃으로 fetch, 실패 시 로컬 백업(2.9MB, 4245개 모델)으로 폴백, 재시도 스레드까지 띄운다. `LITELLM_LOCAL_MODEL_COST_MAP=true` 일 때만 호출 0. 주기 재조회 job·`POST /reload/model_cost_map`(`proxy_server.py:20851`)도 같은 URL. 폐쇄망 규칙(런타임 다운로드 0) 위반 경로.
3. **라이선스 검증이 외부 호출.** `P/auth/litellm_license.py` 가 서명 검증 실패 시 `https://license.litellm.ai/verify_license/` 를 호출(PREMIUM_WALL_REVIEW.md:60 도 확인). 내 영역은 아니지만 spend/audit 가 premium 에 묶여 있어 같이 걸린다.
4. **감사 로그(audit log)는 기본적으로 premium 전용.** `P/management_helpers/audit_logs.py:28-40`(`is_audit_logging_enabled` 기본값이 premium_user), `:277-290`(`premium_user is not True` 면 return). 즉 무료 상태에선 `LiteLLM_AuditLog` 에 아무것도 안 쌓인다. 조회 API `/audit` 은 `enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:63`. 감사 로그 보관 삭제 정책은 spend_log_cleanup 대상에 없음(확인 못함: 별도 정리 job 있는지).
5. **litellm 이 이미 "예약(reservation) 방식 하드 예산"을 갖고 있다**(v1.84.0 도입, `_types.py:3055`). 단순 `spend < max_budget` 보다 훨씬 복잡(`budget_reservation.py` 1703줄). 새 게이트웨이 1차 범위에서는 과함 — 단순 읽기 시점 검사 + 요청당 최대비용 상한(또는 인스턴스 1개의 메모리 카운터 원자 증가)이면 충분.
6. **RPM/TPM 은 "슬라이딩 윈도우"가 아니라 고정 윈도우.** `parallel_request_limiter_v3.py:219-245` Lua/인메모리: 첫 요청 시각부터 `window_size`(기본 60초, `LITELLM_RATE_LIMIT_WINDOW_SIZE`, `:915`) 지나면 리셋. 이름(`in_memory_cache_sliding_window` `:1310`)과 달리 고정 윈도우.
7. **spend 쓰기 경로가 계속 늘어나 있다**: 사이드카 collector(`LITELLM_COLLECTOR_ENABLED`, `spend_tracking/spend_event_producer.py`, unix socket, at-most-once) 가 기본 off 로 존재. 우리 규모엔 불필요.
8. 외부 호출형 기능이 spend 영역에 다수: Slack/MS Teams/webhook 알림, SMTP·Resend·SendGrid 이메일, PagerDuty, CloudZero/Vantage 내보내기, OpenAI 청구 API 대조(`spend_capture_rate.py`), ROI 계산기(GitHub OAuth), `SPEND_LOGS_URL`(별도 spend 서버로 POST, `utils.py:8065`). 전부 기본 off 이거나 설정해야 켜지지만, 새 게이트웨이에는 불필요.
9. `LiteLLM_SpendLogs` 는 `store_prompts_in_spend_logs` 기본 False(`_types.py:2821`) — 프롬프트·응답 본문은 기본적으로 DB 에 안 남는다. 저장 시 개인정보·용량 이슈.

## 2. 기능 표

### 2.1 spend 쓰기 경로(요청 후 기록)

| 기능 | 진입점/엔드포인트 | 구현 위치 | 동작 요약 | 사용 DB 테이블 | 외부 호출·유료 | 새 게이트웨이 권장 |
|---|---|---|---|---|---|---|
| 비용 콜백(성공) | 모든 LLM 응답 후 콜백 | P/hooks/proxy_track_cost_callback.py:333,348,556,989 | 응답에서 비용 계산→카운터 즉시 증가→DB 큐 적재. key/user/team/end_user/org/tag/project 귀속. 키·유저·팀·end_user 중 하나도 없으면 기록 안 함(`:911`) | (큐 경유) | 없음 | 필수 — 요청 후 비용 계산·기록은 핵심 |
| 실패 요청 spend/에러 기록 | `async_post_call_failure_hook` | proxy_track_cost_callback.py:376,533 (`_billed_failed_attempts :224`) | 실패해도 과금된 시도(폴백 등)는 spend 행으로 기록, 에러 로그 행 별도. `disable_error_logs` 로 끔(`:867`) | LiteLLM_SpendLogs, LiteLLM_ErrorLogs, LiteLLM_DailyRequestErrors | 없음 | 후순위 — 1차는 실패 요청 상태만 request_log 에 남기면 충분 |
| DB 증분 큐·배치 flush | 스케줄러 `update_spend_job` 10~15초 | P/db/db_spend_update_writer.py:554,1073,2138; P/utils.py:8148; proxy_server.py:11245 | 4~5절 서술대로. 메모리 큐 집계 후 `spend = spend + x` 일괄 UPDATE, 3회 재시도, 정렬 락 순서 | LiteLLM_VerificationToken, LiteLLM_UserTable, LiteLLM_TeamTable, LiteLLM_TeamMembership, LiteLLM_OrganizationTable, LiteLLM_EndUserTable, (project/tag/MAG) | 없음 | 필수 — 새 설계 그대로. 단 flush 실패 시 재큐잉·종료 시 drain 은 반드시 |
| Redis 버퍼 경유 쓰기 | Redis 설정 시 자동 | db_spend_update_writer.py:1635,1801; P/db/db_transaction_queue/redis_update_buffer.py, pod_lock_manager.py | 다중 pod 에서 증분을 Redis 에 모았다가 락 잡은 pod 가 DB 반영 | 위와 동일 | Redis(내부) | 제외 — 인스턴스 1개 |
| SpendLogs 행 큐·적재 | `enqueue_spend_logs` → `update_spend_logs` | P/utils.py:7926,8043-8100,8316; db_spend_update_writer.py:690,1575 | 요청별 1행(토큰·비용·모델·키 해시·팀·태그·세션·상태), 1000행 배치, 실패 시 Redis 에 보관 후 재시도(`:8187-`) | LiteLLM_SpendLogs, LiteLLM_SpendLogToolIndex | `SPEND_LOGS_URL` 설정 시 외부 서버로 POST(`:8065`) | 필수 — request_log 가 사용량 조회의 원천 |
| 일별 집계 upsert(6종) | 같은 flush | db_spend_update_writer.py:2484-2800; P/db/daily_spend_bulk_upsert.py; P/management_endpoints/common_daily_activity.py | user/team/org/end_user/agent/tag × (날짜,키,모델,provider,endpoint) 단위 요청수·토큰·비용·성공/실패·지연 합계 upsert | LiteLLM_DailyUserSpend, DailyGlobalSpend, DailyTeamSpend, DailyOrganizationSpend, DailyEndUserSpend, DailyAgentSpend, DailyTagSpend | 없음 | 후순위 — 1차는 request_log 의 GROUP BY 쿼리로 대체, 데이터가 커지면 일별 롤업 도입 |
| 도구·모델 사용량·게이트웨이 요청 롤업 | 같은 flush | db_spend_update_writer.py:794,824; P/db/spend_log_tool_index.py, model_usage_rollup.py, gateway_request_tracking.py, request_error_tracking.py; proxy_server.py:11264-11285 | 도구 호출/모델별/라우트별 일 집계 | LiteLLM_DailyToolSpend, DailyModelUsage, DailyGatewayRequests, DailyGatewayFailedRequests, DailyRequestErrors | 없음 | 제외 — 운영 필수 아님 |
| 오토라우터·baseline·shadow-eval 회계 | 같은 flush | db_spend_update_writer.py:838,901; P/spend_tracking/baseline_accounting.py, savings.py, compression_savings.py, P/db/autorouter_session_rollup.py | 라우팅 절감 효과·프롬프트 캐시 절감 추정치 롤업 | LiteLLM_AutoRouterDailySpend 등 (확인 못함: 정확한 테이블 목록) | 없음 | 제외 |
| 사이드카 collector | `LITELLM_COLLECTOR_ENABLED=true` | P/spend_tracking/spend_event_producer.py, spend_event.py; P/collector.py | 비용 파이프라인을 unix socket 로 별도 프로세스에 위임. 기본 off | 동일 | 없음 | 제외 |
| 배치·응답 비용 폴링 | 스케줄러 3600초 | proxy_server.py:11510-11580; enterprise/.../common_utils/check_batch_cost.py, check_responses_cost.py; P/db/db_spend_update_writer.py:705,754 | 매니지드 batch/responses 완료 후 비용을 provider 에 폴링해 정산 | SpendLogs 등 | provider 호출, 일부 enterprise 모듈 | 제외 — Ollama 에 배치 없음 |
| 비용 정산 PTU 롤업 | `is_ptu_cost_attribution_enabled` | P/spend_tracking/ptu_flat_cost_rollup.py(890줄), ptu_feature_flag.py, management_endpoints/ptu_consumption.py | PTU(예약 처리량) 고정비 귀속 | 확인 못함 | premium 여부 확인 못함 | 제외 |

### 2.2 비용 계산·가격표

| 기능 | 진입점 | 구현 위치 | 동작 요약 | 사용 DB 테이블 | 외부 호출·유료 | 권장 |
|---|---|---|---|---|---|---|
| 토큰 기반 비용 계산 | 콜백→`response_cost_calculator` | cost_calculator.py:2206(`completion_cost :1503`, `cost_per_token :410`) | usage(입력/출력/캐시/추론 토큰)×단가표. 단가표에 없으면 0. 이미지·오디오·rerank·OCR·비디오·realtime·배치 등 호출 유형별 분기 다수 | 없음 | 없음 | 필수(축소) — chat/completions/embeddings 의 입력·출력 토큰×단가만 |
| 모델 가격표 | `litellm.model_cost` | model_prices_and_context_window_backup.json(4245개, `ollama/*` 29개 모두 0.0), ../model_prices_and_context_window.json, litellm/cost.json(3개짜리 구버전), litellm_core_utils/get_model_cost_map.py:718 | 기본은 GitHub 에서 fetch 후 로컬 폴백 | 없음 | **기본 외부 호출**(위 1절 2번) | 필수 — 단, 새 게이트웨이는 `model_deployment` 에 단가 컬럼(입력/출력 USD per 1M 토큰) 직접 보관. 외부 가격표·fetch 제외 |
| 커스텀 단가(배포별) | model_list `litellm_params.input_cost_per_token` 등 | cost_calculator.py:221-270 | 배포에 준 단가가 단가표보다 우선, 초당 단가 지원 | LiteLLM_ProxyModelTable(params JSON) | 없음 | 필수 — 폐쇄망 비용의 실질 입력 경로 |
| 미등록 모델 차단 | `block_requests_for_models_without_pricing` | P/auth/auth_checks.py:814,1147; user_api_key_auth.py:893; P/management_endpoints/cost_tracking_settings.py:517-527 | 단가 없는 모델 요청 거부 | 설정(DB) | 없음 | 후순위 — 단가 필수 입력으로 대체 가능 |
| 할인/마진 설정 | `GET/PATCH /config/cost_discount_config`, `/config/cost_margin_config`; `POST /cost/estimate` | cost_tracking_settings.py:193,228,335,370,573; cost_calculator.py:1287,1326; litellm_core_utils/cost_discount.py | provider 별 할인율·마진을 비용에 곱·가산 | litellm_settings(DB) | 없음 | 제외 |
| 비용 계산기 엔드포인트 | `POST /spend/calculate` | P/spend_tracking/spend_management_endpoints.py:2305 | 메시지·모델로 비용 시뮬레이션 | 없음 | 없음 | 후순위 |
| 가격표 갱신 | `POST /reload/model_cost_map`, `/schedule/model_cost_map_reload`, `/model/cost_map/source` | proxy_server.py:20851,20919,20968,21008,21050; `_check_and_reload_model_cost_map :9033` | GitHub 에서 재조회·pod 간 동기화 | 설정/리비전 레코드 | **GitHub 호출** | 제외 |

### 2.3 예산(budget) 검사 — 인증 시점

공통: 모두 `budget` 초과 시 `BudgetExceededError`(HTTP 400 계열 litellm 예외). 카운터는 `spend:<엔티티>:<id>` 키(메모리/Redis), 냉시동 시 DB 로 reseed. 예산 필드는 키/팀/유저에 직접 컬럼, 또는 `LiteLLM_BudgetTable`(budget_id 로 공유).

| 기능 | 진입점 | 구현 위치 | 동작 요약 | 사용 DB 테이블 | 외부·유료 | 권장 |
|---|---|---|---|---|---|---|
| 키 max_budget | 인증(`user_api_key_auth`) | P/auth/auth_checks.py:6653; user_api_key_auth.py:3042-3060 | key.spend >= max_budget 이면 거부. 키 없는 유저 예산 별도 | LiteLLM_VerificationToken(spend, max_budget, budget_duration, budget_reset_at, soft_budget_cooldown) | 없음 | 필수 |
| 팀 max_budget | 인증 공통검사 | auth_checks.py:7164,1409; (멀티윈도우 `_team_multi_budget_check :7217`) | team.spend > max_budget 이면 거부 | LiteLLM_TeamTable | 없음 | 필수 |
| 유저 max_budget | 인증 | auth_checks.py:~1380-1460 (`apply_user_budget_to_team_keys` 설정에 따라 팀 키에도 적용) | 유저 개인 예산 | LiteLLM_UserTable | 없음 | 필수 |
| 팀 멤버 예산(팀 안의 유저별) | `/team/member_add`·`/team/member_update` 로 설정 | auth_checks.py:7009,1443; 기본값 `team.metadata.team_member_budget_id` (`get_team_member_default_budget :1745`) | 팀 내 개인별 한도, 0 은 명시적 차단 | LiteLLM_TeamMembership, LiteLLM_BudgetTable | 없음 | 필수 — "팀원별 한도"는 운영자가 가장 먼저 쓰는 기능 |
| 조직 예산 | 인증 | auth_checks.py:7495; P/auth/auth_checks_organization.py | 조직 단위 한도 | LiteLLM_OrganizationTable, LiteLLM_BudgetTable | 없음 | 제외 — 새 모델은 팀 단일 계층(설계 4.x) |
| 고객(end user) 예산 | 요청 `user` 필드 / `/customer/*` | auth_checks.py:1880,1678-1880; P/management_endpoints/customer_endpoints.py | 요청 바디의 `user` 로 식별한 최종 사용자 예산, 기본 예산 자동 부여 | LiteLLM_EndUserTable, LiteLLM_BudgetTable | 없음 | 후순위 |
| 프로젝트 예산 | 인증 | auth_checks.py:7328, `_project_soft_budget_check :7386` | 프로젝트(키 묶음) 한도 | LiteLLM_ProjectTable | 없음(프로젝트 엔드포인트는 enterprise 폴더에도 있음: enterprise/.../management_endpoints/project_endpoints.py) | 제외 |
| 태그 예산 | `tag_budget_config` / `/tag/*` | auth_checks.py:7597,7622; router_strategy/budget_limiter.py:960-995 | 요청 태그별 예산 | LiteLLM_TagTable | **premium**(budget_limiter.py:980, key_management_endpoints.py:1652 키 태그도 premium) | 제외 |
| 모델 접근 그룹 예산 | 인증 | auth_checks.py:7666,2380-2430 | 모델 묶음(access group)별 예산 | LiteLLM_ModelAccessGroupBudgetTable | 없음 | 제외 |
| 모델별 키 예산(model_max_budget) | 키/팀 생성 시 설정 | P/hooks/model_max_budget_limiter.py:363,587 (등록 proxy_server.py:2972); 설정 검증 P/management_endpoints/common_utils.py:83-92, key_management_endpoints.py:8387 | 키×모델 일/주 한도 | 키·팀 `model_max_budget` JSON, 소비는 spend_counter_cache | **premium**(키·팀 설정 시 거부) | 후순위 — 로컬 모델 몇 개뿐이면 토큰 쿼터로 충분 |
| 예산 멀티 윈도우(budget_limits) | 키·팀 생성/수정 | auth_checks.py:6736,7217; P/db/budget_window_spend_writer.py; spend_tracking/budget_reservation.py:1255; reset_budget_job.py:1558 | 한 키에 일/주/월 한도를 동시에 | LiteLLM_BudgetWindowSpend(entity_type, entity_id, window_duration) | 없음 | 제외 — 단일 `budget_duration` 으로 충분 |
| 글로벌(프록시 전체) 예산 | `litellm_settings.max_budget` | auth_checks.py:907; user_api_key_auth.py:1337,3192 | 전체 합산이 한도 초과 시 모든 LLM 요청 거부 | spend 합계 조회(`LiteLLM_VerificationToken`/SpendLogs 합) — 확인 못함: 정확한 쿼리 | 없음 | 후순위 |
| 소프트 예산(알림용) | 키 `soft_budget`, 팀 `soft_budget` | auth_checks.py:6780,7257 | 차단하지 않고 알림만 | VerificationToken, TeamTable | 알림 채널 설정 시 Slack/email | 후순위 — UI 경고 표시로 대체 |
| 임시 예산 증액 | 예산 테이블 `temp_budget_increase/expiry` | schema.prisma:13-30; auth_checks.py:7009- (`effective_max_budget`) | 기간 한정 증액 | LiteLLM_BudgetTable | 없음 | 제외 |
| 예산 초과 시 속도 낮춤(throttle) | 키 `throttle_on_budget_exceeded` + 전역 비율 | P/auth/budget_throttle.py; auth_checks.py:6635 | 차단 대신 TPM/RPM 을 비율로 축소 | 키 메타데이터 | 없음 | 제외 |
| 예산 폴백 | 키 `budget_fallbacks`, `enforce_fallback_budget` | P/auth/fallback_budget.py | 0원 모델→유료 폴백 시 예산 재검사 | VerificationToken | 없음 | 제외 |
| 0원 모델 예산 건너뛰기 | 자동 | auth_checks.py:535,1066-1073 | 모델 비용 0 이면 예산 검사 전부 skip | 없음 | 없음 | 필수(결정 사항) — 새 게이트웨이도 "단가 0 은 예산 미적용"을 명시적으로 정할 것 |
| 요청 전 예산 예약 | 인증 직후 | P/spend_tracking/budget_reservation.py:231(1703줄), carried_budget_state.py; user_api_key_auth.py:3925 | 5절 1번. 동시 요청 초과를 막는 낙관적 예약, 임대(lease) 갱신, 취소 시 해제 | 카운터(메모리/Redis), 윈도우는 LiteLLM_BudgetWindowSpend | 없음 | 후순위 — 1차는 "최대 비용 상한 한 건만 선차감" 정도 |
| 예산 변경 규칙 | 키 `/key/update` | P/management_endpoints/key_budget_change.py; `_types.py:2815` | 소유자가 예산을 낮추기만/올리기까지 허용 | VerificationToken | 없음 | 제외 |

### 2.4 예산 관리 API·리셋

| 기능 | 진입점 | 구현 위치 | 동작 요약 | 사용 DB 테이블 | 외부·유료 | 권장 |
|---|---|---|---|---|---|---|
| 예산 템플릿 CRUD | `POST /budget/new`, `/budget/update`, `/budget/info`, `GET /budget/settings`, `/budget/list`, `POST /budget/delete` | P/management_endpoints/budget_management_endpoints.py:38,122,213,242,311,339 | 예산(max/soft/tpm/rpm/duration)을 재사용 가능한 행으로 만들고 키·고객·팀원이 budget_id 참조 | LiteLLM_BudgetTable | 없음 | 후순위 — 키·팀에 한도 컬럼 직접 두는 것을 먼저 |
| 예산 주기 리셋 job | 스케줄러 ≈600초(597~605, constants.py:1899,1938; proxy_server.py:11226-11240) | P/common_utils/reset_budget_job.py:480,552(키·유저·팀·예산표 순), 1121,1232,1347,958 | `budget_duration`(예 "30d","1mo") 지난 행의 spend 0 으로 + `budget_reset_at` 갱신, 카운터·캐시 무효화, 연결된 end user·태그·조직 cascade, Redis 있으면 리더 1대만 실행(`:519`), 롤오버(이월) 옵션(`:136`) | VerificationToken, UserTable, TeamTable, BudgetTable, TeamMembership, EndUser, Org, Tag, Project, MAG, BudgetWindowSpend | 없음 | 필수 — 월/일 단위 리셋은 운영 필수. 인스턴스 1개라 `@Scheduled` + 단일 UPDATE 로 충분(약 600초 간격이라 리셋이 최대 10분 늦는 점 주의) |
| 수동 spend 리셋 | `POST /global/spend/reset`, `/global/spend/refresh` | spend_management_endpoints.py:3695,3730 | 전 키·팀 spend 0 (로그 보존) / 글로벌 뷰 새로고침 | VerificationToken, TeamTable | 없음 | 후순위 |
| 고객 차단 | `POST /customer/block`·`unblock` | customer_endpoints.py:151-213 | end user 차단 목록 | LiteLLM_EndUserTable(blocked) | 없음 | 제외 |

### 2.5 속도 제한(limiter) — 사전 호출 훅

기본 등록: `parallel_request_limiter` = v3(`P/hooks/__init__.py:38-50`). v1 은 `LEGACY_MULTI_INSTANCE_RATE_LIMITING=true` 일 때만. 한도값은 키/팀/유저 컬럼(rpm_limit, tpm_limit, max_parallel_requests, tpd_limit)과 예산표에서 온다.

| 기능 | 진입점 | 구현 위치 | 동작 요약 | 사용 DB 테이블 | 외부·유료 | 권장 |
|---|---|---|---|---|---|---|
| 키 RPM/TPM/동시요청 | 사전 호출 훅 | P/hooks/parallel_request_limiter_v3.py:4201(pre_call), 3316(descriptor 생성), 1666(should_rate_limit), 1880(동시요청 gauge) | RPM=요청수 카운터, TPM=토큰 카운터(호출 전 추정치 예약 `reserve_tpm_tokens :2681`, 호출 후 실사용으로 정산 `:5359`), max_parallel=현재 진행 수 gauge. 고정 60초 윈도우. 429 + `x-ratelimit-*` 헤더 | 한도: VerificationToken. 카운터: 메모리/Redis | 없음 | 필수 — 단 고정 윈도우 RPM/TPM + 동시요청 상한만 |
| 팀 / 팀 멤버 / 유저 / end user 한도 | 같은 훅 | v3:3316-3470 (team :63 근처, team_member, user, end_user 블록) | 엔티티별 RPM/TPM 카운터 | TeamTable, TeamMembership/BudgetTable, UserTable, EndUserTable | 없음 | 필수(팀·유저), 후순위(end user) |
| 모델별 키 RPM/TPM | 키 `model_rpm_limit`/`model_tpm_limit` 메타 | v3:2944(`_add_model_per_key_rate_limit_descriptor`), 3535-3600 | 키×모델 한도, 팀 모델 한도 상속 | VerificationToken(JSON) | 없음 | 후순위 |
| 태그별 한도 | 요청 태그 | v3:2987,3305,750(`resolve_tag_rate_limits_from_db`) | 태그 단위 RPM/TPM | LiteLLM_TagTable | 없음 | 제외 |
| MCP 서버 한도 | MCP 호출 | v3:3024,3061,3113 | MCP 서버별 rpm | 키/팀 메타 | 없음 | 제외 |
| 에이전트·세션·프로젝트·조직·PTU 한도 | 같은 훅 | v3:3253(agent), 2899(org), 3621,3651(project io tokens), 3596(team PTU ceiling), 2854 | 에이전트·프로젝트 입출력 토큰 쿼터 등 | 각 엔티티 테이블 | PTU 는 premium 여부 확인 못함 | 제외 |
| 토큰 추정(입력 카운트) | 훅 내부 | v3:1134-1296, 3876-3964(`_estimate_precise_input_tokens`) | tiktoken/모델별 토크나이저로 입력 토큰 선계산, max_tokens 없으면 출력 상한 가정(`:1081`) | 없음 | tokenizer 파일은 로컬 번들 확인 못함 | 필수(단순화) — 글자수/4 근사 + 실사용 정산으로 시작 |
| 일일 토큰 한도(TPD) | 훅 `daily_token_limiter` | P/hooks/daily_token_limiter.py:110(pre_call :119, success :151) | 키·팀·팀멤버의 하루(UTC) 토큰 합 한도. 사전 검사는 "이미 쓴 양"만 보므로 마지막 요청은 한도를 넘어 통과 | 한도: VerificationToken/TeamTable.tpd_limit, BudgetTable.tpd_limit. 카운터: 메모리/Redis 일 단위 키(DB 미기록) | 없음 | 필수 — 폐쇄망·로컬 모델 환경에서 $ 대신 쓰는 핵심 쿼터. 단 새 구현은 DB 에 일 카운터를 남기는 게 재기동에 안전(litellm 은 캐시만이라 재기동 시 초기화) |
| 동적 레이트리미터 v1/v3 | `litellm_settings.callbacks: dynamic_rate_limiter(_v3)` | P/hooks/dynamic_rate_limiter.py:80; dynamic_rate_limiter_v3.py:68; README.dynamic_rate_limiter_v3.md; litellm_core_utils/litellm_logging.py:5577,5595 | 모델 포화도(80% 기준) 따라 우선순위별 TPM/RPM 분배 | 없음(카운터) | 없음 | 후순위 — 로컬 GPU 병행성 한도(설계의 per-deployment semaphore)가 먼저 |
| 배치 요청 한도 | 배치 API | P/hooks/batch_rate_limiter.py(1347줄) | 배치 입력 파일 크기로 요청수·토큰 추정 | 없음 | 없음 | 제외 |
| 세션 예산(per session) | 에이전트 설정 `max_budget_per_session` | P/hooks/max_budget_per_session_limiter.py:60(pre_call :88, success :133) | 세션 id 별 누적 $ 상한, 초과 시 429 | 캐시(1시간 TTL), DB 없음 | 없음 | 제외 |
| 최대 반복(agent loop) | `max_iterations` | P/hooks/max_iterations_limiter.py:52,85 | 세션별 호출 횟수 상한 | 캐시 | 없음 | 제외 |
| 프로바이더·배포 예산 | `provider_budget_config`, 배포 `max_budget` | litellm/router_strategy/budget_limiter.py:1-60,960; `GET /provider/budgets` spend_management_endpoints.py:4318 | 라우터가 예산 초과 배포를 후보에서 제외 | 캐시(Redis 동기화 `DEFAULT_REDIS_SYNC_INTERVAL=1`) | 없음 | 제외 |
| 레이트리밋 저장소 | 자동 | v3:1310-1390(인메모리), 215-540 Lua, 186-205(fail_closed) | Redis 있으면 Lua 원자 증가, 없으면 단일 프로세스 락 + 메모리. Redis 장애 시 기본 통과(fail-open), `fail_closed_rate_limit_enforcement` 로 반대 | 없음 | Redis(내부) | 필수(메모리 구현). Redis 불필요 |

### 2.6 사용량 조회·분석 엔드포인트

일별 활동 API 는 엔티티마다 같은 라우트 세트를 등록(P/management_endpoints/daily_activity_routes.py:315-654: `{prefix}/daily/activity`, `/aggregated`, `/aggregated/keys`, `/aggregated/search`, `/aggregated/model_top_keys`, `/export`, `/aggregated/users`, `/aggregated/cache_leakage_keys`). prefix = user, team(team_endpoints.py:7030), organization(organization_endpoints.py:567), tag(tag_management_endpoints.py:728), customer/end_user(customer_endpoints.py:912,918), agent. 읽기 원천은 위 Daily* 테이블.

| 기능 | 진입점 | 구현 위치 | 동작 요약 | 사용 DB 테이블 | 외부·유료 | 권장 |
|---|---|---|---|---|---|---|
| 개인/팀 일별 활동 | `GET /user/daily/activity`(internal_user_endpoints.py:2846), `/team/daily/activity`(team_endpoints.py:7030) | daily_activity_routes.py, common_daily_activity.py | 날짜·모델·키별 비용·토큰·요청수 | LiteLLM_DailyUserSpend / DailyTeamSpend | 없음 | 필수 — 운영자가 사용량을 직접 보는 기본 화면(1차는 request_log 집계 쿼리로) |
| 그 외 엔티티 일별 활동(org/tag/customer/agent) | 위 prefix | 위 | 동일 | DailyOrganizationSpend, DailyTagSpend, DailyEndUserSpend, DailyAgentSpend | 없음 | 제외(org/tag/agent), 후순위(customer) |
| 게이트웨이 일별 요청·에러 | `GET /gateway/daily/activity`, `/gateway/errors/activity` | management_endpoints/gateway_request_endpoints.py:124; request_error_endpoints.py:221 | 라우트별 성공/실패 요청수 | DailyGatewayRequests, DailyGatewayFailedRequests, DailyRequestErrors | 없음 | 후순위 |
| 키·유저·태그별 spend 요약 | `GET /spend/keys`(:419), `/spend/users`(:481), `/spend/tags`(:570), `/global/spend`(:3937), `/global/spend/keys`(:4029), `/global/spend/teams`(:4076), `/global/spend/models`(:4281), `/global/spend/provider`(:1309), `/global/spend/end_users`(POST :4197), `/global/all_end_users`(:4165), `/global/spend/tags`(:2158), `/global/spend/all_tag_names`(:2106) | P/spend_tracking/spend_management_endpoints.py (5086줄) | 합계·상위 N, 대시보드 위젯 | LiteLLM_SpendLogs, LiteLLM_VerificationToken, 뷰 | 없음 | 필수(키·팀·모델별 합계 3~4개: 설계 문서의 `/api/v1/spend/summary?by=`) / 나머지 제외 |
| 활동 통계 | `GET /global/activity`(:682), `/global/activity/model`(:824), `/global/activity/exceptions`(:1123), `/global/activity/exceptions/deployment`(:976) | spend_management_endpoints.py | 요청·토큰·예외 일별 | SpendLogs, ErrorLogs | 없음 | 제외 |
| 비용 리포트 | `GET /global/spend/report`(:1438), `/key/spend/report`(:1932), `/user/spend/report`(:1979), `/team/spend/report`(:2021), `/organization/spend/report`(:2063) | spend_management_endpoints.py (`_spend_report_prereqs :1833`) | 기간·그룹별 리포트 | SpendLogs | **premium**(`:1525,1840`) | 후순위 — 어차피 유료 기능이라 우리가 같은 걸 무료로 제공하는 셈. 필요하면 CSV export |
| 스펜드 로그 조회 | `GET /spend/logs`(:3485), `/spend/logs/v2`(:2493), `/spend/logs/ui`(:2501), `/spend/logs/ui/{request_id}`(:3390), `/spend/logs/session/ui`(:4536), `/global/spend/logs`(:3847) | spend_management_endpoints.py; P/management_endpoints/management_v1/spend_logs.py | 요청 단위 로그 페이지네이션·필터(키·팀·모델·기간·세션·요청 id) | LiteLLM_SpendLogs | 없음 | 필수 |
| 세션 로그아웃 | `POST /session/logout` | management_endpoints/session_endpoints.py:117 | UI 세션 로그아웃(spend 와 무관, 파일명상 오인 주의) | 없음 | 없음 | 내 범위 밖(참고) |
| 태그 DAU/WAU/MAU, user-agent 분석 | `GET /tag/dau|wau|mau|summary|distinct`, `/tag/user-agent/per-user-analytics` | management_endpoints/user_agent_analytics_endpoints.py:164-654 | user-agent 태그로 활성 사용자 | DailyTagSpend | 없음 | 제외 |
| 모델 인사이트·ROI·usage chat | `GET /model-insights`, `/roi-calculator/*`, `/usage/ai/chat` | model_insights_endpoints.py; roi_calculator_endpoints.py, roi_observed_endpoints.py; usage_endpoints/endpoints.py:35 | 분석·ROI(GitHub OAuth)·LLM 에게 사용량 질의 | 확인 못함 | ROI 는 GitHub 외부 호출, usage chat 은 LLM 호출 | 제외 |
| 컴플라이언스 | `POST /compliance/eu-ai-act`, `/compliance/gdpr` | management_endpoints/compliance_endpoints.py:26,55 | 컴플라이언스 점검 | 확인 못함 | 확인 못함 | 제외 |
| 비용 외부 내보내기 | `/cloudzero/*`, `/vantage/*` | P/spend_tracking/cloudzero_endpoints.py, vantage_endpoints.py | 외부 FinOps SaaS 로 spend 전송 | DailySpend 등 | **외부 SaaS 호출** | 제외 |
| 청구 대조 | 스케줄러 job | P/spend_tracking/spend_capture_rate.py:304줄 | OpenAI 청구 API 로 spend 대조 | DailyUserSpend | **OpenAI 외부 호출** | 제외 |
| Prometheus 예산·spend 지표 | `/metrics` | litellm/integrations/prometheus.py (내 영역 밖, spend 지표 포함) | 키·팀 spend·잔여 예산 gauge | 없음 | 없음(스크래핑 내부) | 후순위 — 설계 문서의 `gateway_spend_total` 정도 |

### 2.7 감사 로그·보관·삭제·알림

| 기능 | 진입점 | 구현 위치 | 동작 요약 | 사용 DB 테이블 | 외부·유료 | 권장 |
|---|---|---|---|---|---|---|
| 감사 로그 기록 | 키/팀/유저/모델/설정 변경 시 | P/management_helpers/audit_logs.py:28,277,310; P/hooks/key_management_event_hooks.py:54-147; P/proxy_server.py:19906(시스템 설정 변경); P/agent_endpoints/kill_switch.py:157 | 변경 전/후 JSON, 변경자·키 해시를 기록, 외부 콜백 디스패치(`:102`), 비동기 insert | LiteLLM_AuditLog | **premium**(`audit_logs.py:35-40, 290`). 콜백 설정 시 외부 | 필수(재설계) — 폐쇄망 운영·보안 감사에 필요. 우리는 무료·단순 INSERT 로(actor, action, target, before/after) |
| 감사 로그 조회 | `GET /audit`(+ `/audit/{id}`로 추정, 확인 못함) | enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:63,186 | 필터(변경자·테이블·객체·기간)·정렬·페이지 | LiteLLM_AuditLog | enterprise 패키지 | 필수(위와 함께) |
| 스펜드 로그 보관기간 삭제 | 스케줄러 cron/주기 | P/proxy_server.py:5529-5563,8072-8160; P/db/db_transaction_queue/spend_log_cleanup.py:391(`cleanup_old_spend_logs`, 배치 삭제 `:812`); 파티션 방식 `spend_logs_partition_manager.py:149`; 설정 `maximum_spend_logs_retention_period`(_types.py:2865), `_cleanup_cron`, `_retention_interval` | N일 지난 행을 1000행씩 DELETE, 파티션 테이블이면 파티션 DROP. 락·타임아웃·실패 백오프 | LiteLLM_SpendLogs, SpendLogToolIndex, DailyTagSpend, AutoRouterDailySpend 등 | 없음 | 필수 — 운영 필수(디스크). 단순 `DELETE ... WHERE created_at < now()-N` 스케줄 job 하나 |
| 스펜드 로그 용량 기준 삭제 | `maximum_spend_logs_size`, 확인 주기 | proxy_server.py:5557,8103-8125; spend_log_cleanup.py:101(`SizeCleanupState`),681 | 테이블 총 용량이 한도 넘으면 오래된 것부터 삭제 | SpendLogs | 없음 | 제외 |
| 에러 로그·헬스체크 보관 | `maximum_health_check_retention_period` 등 | _types.py:2870-2880; spend_log_cleanup.py | 헬스체크·오토라우터 롤업 정리 | LiteLLM_HealthCheckTable(확인 못함) | 없음 | 제외 |
| 프롬프트/응답 본문 저장 | `store_prompts_in_spend_logs`, `disable_spend_logs`, `disable_requester_ip_address_logging`, `spend_logs_metadata_fields` | _types.py:2821-2835; P/spend_tracking/spend_tracking_utils.py:574(get_logging_payload),156(키 마스킹) | 기본은 본문 미저장, IP 저장 | SpendLogs.messages/response/proxy_server_request | 없음 | 필수(옵션 1개) — 본문 저장 off 가 기본, 켜면 보관기간 필수 |
| 예산 알림(Slack/Teams/webhook) | `budget_alerts` | P/utils.py:3488; litellm/integrations/SlackAlerting/slack_alerting.py:528, ms_teams.py, budget_alert_types.py; auth_checks.py:6863(max_budget_alert) | 키/유저/팀/조직/프록시 예산 임박·초과 시 전송. 기본 `alerting=None` 이면 아무것도 안 함(`utils.py:1610`) | VerificationToken.soft_budget_cooldown, 캐시 TTL(`budget_alert_ttl`) | **외부 호출**(Slack/Teams/webhook URL) | 제외 — 폐쇄망 내부 채널이 있으면 후순위로 webhook 1개 |
| 예산 알림 이메일 | `alerting: ["email"]` 또는 팀 `soft_budget_alerting_emails` | litellm/integrations/email_alerting.py; enterprise/.../send_emails/{smtp,resend,sendgrid}_email.py, base_email.py:730-762 | SMTP/Resend/SendGrid 로 전송, 템플릿 브랜딩 커스텀은 premium | 없음 | **SMTP 면 내부 가능**, Resend/SendGrid 는 외부 SaaS | 제외(후순위: 내부 SMTP) |
| PagerDuty 등 장애 알림 | `alerting` 설정 | enterprise/.../enterprise_callbacks/pagerduty/pagerduty.py; SlackAlerting(hanging_request_check.py) | 응답 지연·실패 알림 | 없음 | **외부 SaaS** | 제외 |
| 스펜드 캡처율 알림 | job | P/spend_tracking/spend_capture_rate.py | 위 청구 대조 | — | OpenAI | 제외 |

### 2.8 키 등과 연결되는 회계 부가 기능(참고)

| 기능 | 진입점 | 구현 위치 | 동작 요약 | 사용 DB 테이블 | 외부·유료 | 권장 |
|---|---|---|---|---|---|---|
| 사용량 기반 키 메타 복구 | 자동 | P/spend_tracking/key_metadata_recovery.py(575줄) | 키 삭제·불일치 시 spend 메타 복구 | 확인 못함 | 없음 | 제외 |
| 백그라운드 인터랙션 정산 | 자동 | P/spend_tracking/background_interaction_settlement.py | 장기 실행 interactions 응답 비용 정산 | SpendLogs | 없음 | 제외 |
| 스트림/배치 연동 비용 클레임 | 자동 | db_spend_update_writer.py:705-790 | 배치 비용 중복 과금 방지 | SpendLogs | 없음 | 제외 |
| 마스터키·키 해시 저장 | 자동 | spend_tracking_utils.py:156-170 | SpendLogs.api_key 에 해시만 저장 | SpendLogs | 없음 | 필수 — 로그에 원문 키 금지(프로젝트 규칙 3·4번과 일치) |

## 3. 새 게이트웨이 1차 범위 제안(필수만)

1. 비용 계산: 배포별 입력/출력 단가(USD/1M 토큰, 기본 0) × usage. 외부 가격표·fetch 제외. 단가 0 = 예산 비적용 규칙을 문서화.
2. 예산: 키·팀·팀멤버 `max_budget` + `budget_duration` 리셋(스케줄러 1개). 차단 판단은 메모리 카운터 우선, DB 는 기동 시 reseed. 요청 전 "최대비용 선차감"은 후순위.
3. 제한: 키/팀/유저 RPM·TPM(고정 윈도우)·동시요청·일일 토큰(TPD). 메모리 구현, 429 + `x-ratelimit-*`.
4. 기록: 요청 후 큐 → N초 배치로 `request_log` INSERT + 키·팀 spend 증분 UPDATE. 종료 훅에서 큐 drain, 실패 시 재큐잉. SpendLogs 본문 저장은 기본 off.
5. 조회: 요청 로그 페이지, 키·팀·모델별 합계, 일별 활동(원천은 request_log 집계).
6. 보관: `retention_days` 스케줄 삭제 1개. 감사 로그: 무료로 재구현(필수).
7. 알림은 1차 제외, 소프트 예산은 UI 표시로.

## 4. 확인 못한 것

- 글로벌 예산(`litellm.max_budget`)의 spend 합계 조회 SQL, PTU 롤업의 테이블·premium 여부, `/audit/{id}` 존재 여부, 감사 로그 삭제 정책, 컴플라이언스·ROI 테이블, HealthCheck 테이블 이름, tokenizer(tiktoken) 데이터가 이미지에 번들되어 폐쇄망에서 쓸 수 있는지.
- 스트리밍 중 클라이언트 연결 끊김 시 정확한 부분 과금 동작(예약 해제 코드는 확인했으나 부분 usage 정산은 확인 못함).
