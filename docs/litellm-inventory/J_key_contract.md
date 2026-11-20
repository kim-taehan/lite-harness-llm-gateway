# J. 가상 키(Virtual Key) 관리·검증 HTTP 계약 (litellm 원본 기준)

수정하지 않은 litellm 대시보드가 우리 서버를 그대로 호출한다. 그래서 이 문서는 litellm 이 키 관리 API 와 요청 인증에서 **실제로** 주고받는 모양을 적는다. 문서가 아니라 코드 동작을 따랐다.
근거는 모두 `파일:줄`로 달았다. 코드로 확인하지 못한 것은 "미확인"으로 적었다. 오류 본문 모양 (A)~(D) 와 역할 문자열은 `H_team_user_contract.md` §2.1·§2.3 의 정의를 그대로 쓴다.

## 0. 약어 (모든 경로는 `<workspace>/` 기준)

| 약어 | 파일 |
|---|---|
| KM | `gateway/litellm/proxy/management_endpoints/key_management_endpoints.py` |
| KR | `gateway/litellm/types/proxy/management_endpoints/key_requests.py` |
| RB | `gateway/litellm/types/proxy/management_endpoints/request_base.py` |
| T | `gateway/litellm/proxy/_types.py` |
| VT | `gateway/litellm/models/verification_token.py` |
| UAKT | `gateway/litellm/types/proxy/auth/user_api_key_auth.py` |
| ROLES | `gateway/litellm/types/proxy/auth/user_roles.py` |
| UAK | `gateway/litellm/proxy/auth/user_api_key_auth.py` |
| AC | `gateway/litellm/proxy/auth/auth_checks.py` |
| RC | `gateway/litellm/proxy/auth/route_checks.py` |
| AEH | `gateway/litellm/proxy/auth/auth_exception_handler.py` |
| AU | `gateway/litellm/proxy/auth/auth_utils.py` |
| RES | `gateway/litellm/proxy/auth/resolvers/exceptions.py` |
| STORE | `gateway/litellm/proxy/auth/resolvers/store.py` |
| MAD | `gateway/litellm/proxy/auth/model_access_denied.py` |
| TMP | `gateway/litellm/proxy/management_helpers/team_member_permission_checks.py` |
| MH | `gateway/litellm/proxy/management_helpers/utils.py` |
| AZ | `gateway/litellm/proxy/management/teams/authz.py` |
| CU | `gateway/litellm/proxy/management_endpoints/common_utils.py` |
| TZ | `gateway/litellm/proxy/common_utils/timezone_utils.py` |
| UKC | `gateway/litellm/proxy/common_utils/user_api_key_cache.py` |
| PU | `gateway/litellm/proxy/utils.py` |
| PS | `gateway/litellm/proxy/proxy_server.py` |
| DP | `gateway/litellm/litellm_core_utils/duration_parser.py` |
| CONST | `gateway/litellm/constants.py` |
| INIT | `gateway/litellm/__init__.py` |
| EXC | `gateway/litellm/exceptions.py` |
| SP | `gateway/schema.prisma` |
| NW | `gateway/ui/litellm-dashboard/src/components/networking.tsx` |
| SD | `gateway/ui/litellm-dashboard/src/lib/http/schema.d.ts` |
| UI/… | `gateway/ui/litellm-dashboard/src/…` |

---

## 1. 요약 표

### 1.1 대시보드가 부르는 키 엔드포인트 (먼저 전부 나열)

| 엔드포인트 | 대시보드 호출 위치 | 비고 |
|---|---|---|
| `POST /key/generate` | NW:952 `keyCreateCall` (호출부 UI/components/organisms/create_key_button.tsx:482), NW:989 `keyCreateForAgentCall` | |
| `POST /key/service-account/generate` | NW:894 `keyCreateServiceAccountCall` (create_key_button.tsx:481) | 키 소유자 "service_account" 선택 시 |
| `POST /key/update` | NW:2780 `keyUpdateCall` (UI/components/templates/key_info_view.tsx:398) | |
| `POST /key/delete` | NW:1080 `keyDeleteCall` (key_info_view.tsx:419) | |
| `GET /key/info` | NW:2411 `keyInfoV1Call` (UI/app/(dashboard)/hooks/keys/useKeyInfo.ts:31) | `?key=<해시>` |
| `GET /key/list` | UI/app/(dashboard)/hooks/keys/useKeys.ts:84 (주 화면), NW:2455 `keyListCall` | 삭제 키 화면도 같은 호출에 `status=deleted` (useKeys.ts:157) |
| `GET /key/aliases` | NW:2502 `keyAliasesCall` | 필터 드롭다운 |
| `POST /key/block` / `POST /key/unblock` | UI/app/(dashboard)/hooks/keys/useSetKeyBlockedState.ts:23 | 본문 `{key}` |
| `POST /key/{key}/regenerate` | NW:1562 `regenerateKeyCall` (UI/components/organisms/RegenerateKeyModal.tsx:98) | `/key/regenerate`(본문 key) 경로도 있으나 대시보드는 경로형만 씀 |
| `POST /key/{key}/reset_spend` | UI/app/(dashboard)/hooks/keys/useResetKeySpend.ts:20 | 실제 경로는 `/key/{key}/reset_spend` (KM:6732-6736). `/key/reset_spend` 는 없다 |
| (`GET /key/spend/report`) | UI/components/liteadmin/operations.ts:330 | 키 화면 아님. 범위 밖 |

`/v2/key/info`(KM:4717), `/key/bulk_update`(KM:4089), `/team/key/bulk_update`(KM:4284), `/key/health`(KM:8076)는 대시보드 키 화면이 부르지 않는다(UI/components/TeamSSOSettings.tsx:37-49 와 team/permission_definitions.tsx 는 권한 이름 목록일 뿐이다).

### 1.2 엔드포인트별 계약 요약

| 엔드포인트 | 성공 응답 최상위 모양 | 라우트 단계(비관리자) | 핸들러 단계 권한 | 오류 본문 |
|---|---|---|---|---|
| `POST /key/generate` (KM:2267-2478) | 평면 `GenerateKeyResponse`: **`key`=평문**, `token`=`token_id`=해시, `key_name`=`"sk-...abcd"` (T:1205-1243, KM:1797-1799) | internal_user ○, internal_user_viewer 401, viewer 403 | 개인 키: 본인 것만. 팀 키: 팀 멤버이고 role=admin 이거나 팀이 `/key/generate` 를 멤버에게 허용 | (A) `internal_server_error` (KM:2478) + 일부 ProxyException 그대로 |
| `POST /key/service-account/generate` (KM:2481-2650) | 위와 같음 | internal_user ○ | 팀 키 검사와 같음, `user_id` 강제 null | **(B)** — try 없음 |
| `POST /key/update` (KM:3805-4088) | `{"key": <요청 key 그대로>, ...갱신된 Prisma 키 행}` (KM:4068) | internal_user ○ | 작성자이자 소유자, 또는 팀 키(멤버 권한), 아니면 proxy/팀/조직 admin | (A) 이지만 **`type="auth_error"`, `param="None"`** (KM:4069-4088) |
| `POST /key/delete` (KM:4538-4644) | `{"deleted_keys": [요청 값 그대로]}` (KM:4641) | internal_user ○ | 소유자 또는 (팀 키면) 팀 admin | (A) `internal_server_error` |
| `GET /key/info` (KM:4815-4957) | `{"key": <쿼리 값>, "info": {...행(token 제외)..., "status"}}` (KM:4947) | info_route → 모두 통과 | admin·viewer, 소유자, 같은 팀 멤버 | (A) `internal_server_error` |
| `GET /key/list` (KM:7043-7240) | `{"keys":[...], "total_count","current_page","total_pages"}` (T:4412-4416, KM:7774-7779) | info_route → 모두 통과 | admin·viewer 전체, 나머지는 본인·작성·팀 범위 | (A) `internal_server_error` (KM:7225-7240) |
| `GET /key/aliases` (KM:7278-7397) | `{"aliases","total_count","current_page","total_pages","size"}` | internal_user ○, internal_user_viewer 401 | 비관리자는 본인·소속 팀 범위 | (A) |
| `POST /key/block` / `unblock` (KM:7848-8074) | 갱신된 키 행 (`LiteLLM_VerificationToken` 모양, KM:7858) | internal_user ○ | **proxy/팀/조직 admin 만** — 소유자도 불가 (KM:7797-7845) | 섞임: (A) ProxyException + **(B)** HTTPException |
| `POST /key/{key}/regenerate` (KM:6250-6522) | `GenerateKeyResponse`: `key`=새 평문, `token_id`=새 해시, **`token`=null** | internal_user ○ | 팀 멤버 권한 + 소유자 또는 팀 admin. **premium 전용** | (A) `internal_server_error` |
| `POST /key/{key}/reset_spend` (KM:6732-6836) | `{"key_hash","spend","previous_spend","max_budget","budget_reset_at"}` (KM:6824-6830) | internal_user ○ | proxy admin 또는 팀 admin 만 | **(B)** (HTTPException 은 그대로 다시 던짐, KM:6831-6832) |

---

## 2. 공통 사항

### 2.1 키 형식·해시·저장

| 항목 | 값 | 근거 |
|---|---|---|
| 생성 형식 | `"sk-" + secrets.token_urlsafe(16)` → 16바이트 base64url(패딩 없음) = **22자**, 전체 25자. 문자 집합 `A-Z a-z 0-9 - _` | KM:5106, CONST:1981 (`LENGTH_OF_LITELLM_GENERATED_KEY` 기본 16, 환경변수로 변경 가능) |
| 사용자 지정 키(`key`) | `sk-` 로 시작, 길이 ≥ 16(`MINIMUM_CUSTOM_KEY_LENGTH`) | KM:1726-1739, CONST:1982 |
| 해시 | **SHA-256 hex(소금 없음)**, `hashlib.sha256(token.encode()).hexdigest()` | PU:7837-7843, PU:5322-5326 |
| 저장 | `LiteLLM_VerificationToken.token`(PK) 에 **해시만** 저장. 평문은 어디에도 남지 않는다 | PU:6011-6029, SP:502-503 |
| `key_name` | `"sk-..." + 평문 마지막 4자`. 평문이 16자 미만이면 `"sk-..."` | AU:2366-2369, KM:5231-5236 (`DISABLE_KEY_NAME=true` 면 저장 안 함) |
| 해시/평문 구분 | 대부분 `sk-` 로 시작하면 해시, 아니면 이미 해시로 본다(`hash_token_if_needed`) | PU:7906-7915 |
| 예외: regenerate·reset_spend | `"sk" not in key` 로 판정(접두어가 아니라 **부분 문자열**) | KM:6416-6419, KM:6761-6764 |
| 예외: block/unblock | `is_valid_api_key`: `^sk-[A-Za-z0-9_-]+$` 또는 `^[a-fA-F0-9]{64}$`, 길이 3..100. 아니면 400 `"Invalid key format."` | PU:9219-9235, KM:7893-7899 |

- **같은 사용자 지정 키로 두 번 generate 하면 오류가 나지 않는다.** 키 삽입이 `upsert(create=…, update={})` 라서 기존 행을 그대로 돌려준다(PU:6020-6029). 응답의 `key` 는 요청 평문이다. (alias 를 함께 보내면 alias 중복 검사에서 먼저 400 이 날 수 있다.)
- 키 테이블에는 `key_alias` 에 unique 제약이 없다(SP:505, 인덱스도 없음). 고유성은 앱이 검사한다(§3.1 오류).

### 2.2 기간 문자열(duration) → `expires`

- 파서: 정규식 `(\d+)(mo|[smhdw]?)` 를 `re.match` 로 적용한다. 단위는 s, m, h, d, w, mo(달력 기준 개월)이다. 단어 별칭 `hourly=1h, daily=24h, weekly=7d, monthly=30d` 도 받는다(DP:17-22, DP:29-37, DP:51-82).
  - 숫자로 시작하지 않으면 `ValueError("Invalid duration format")` (DP:33-34).
  - 단위가 없으면(예: `"30"`) `ValueError("Unsupported duration unit, passed duration: 30")` (DP:81-82).
  - `re.match` 라서 뒤에 붙은 문자는 무시된다(예: `"30dx"` → 30일). 미확인: 의도된 동작인지.
- `/key/generate`: `expires = now(UTC) + duration초` (정확한 시각, 표준화 없음, KM:5108-5113). `duration=null` 이면 만료 없음.
  - 잘못된 duration 은 **500**, (A) `type="internal_server_error"`, message 는 ValueError 문자열 그대로다. ValueError 에 status_code 가 없어서 그렇다(KM:5112 → KM:2478 → PU:9062-9071).
- `/key/update`·regenerate: `duration` 이 `null` 이나 `"-1"` 이면 `expires=null`(만료 해제), `""` 이면 무시(변경 없음), 값이 있으면 now+기간(KM:2853-2861).
- `budget_duration` 은 별도 검증이 있다: 400 `{'error': "Invalid budget_duration '<v>'. Use a format like '1h', '24h', '7d', or '30d'."}` (CU:28-42, TZ:96-105). 리셋 시각은 표준화된 경계에 맞춘다(`get_budget_reset_time`, KM:5118). 예산 상세는 이 문서 범위 밖이다.

### 2.3 키 행의 응답 직렬화 차이 (주의)

| 경로 | 조직 필드 | token |
|---|---|---|
| `/key/list` (`return_full_object=true`) | **`org_id`** (UserAPIKeyAuth 생성자가 `organization_id`→`org_id` 로 옮김, UAKT:124-125) | 해시 |
| `/key/info` | `organization_id`(Prisma 행 그대로) | **제거됨** (KM:4924-4925) |
| `/key/update` | `organization_id`(Prisma 행) | 해시(`token`) + 요청 값(`key`) |
| `/key/generate` | `organization_id` | 해시 |
| `/key/block` | 반환 주석 `LiteLLM_VerificationToken`(KM:7858)이 response_model 로 쓰인다(FastAPI 동작). VT 에는 `org_id` 만 있고 `organization_id` 는 버려진다 → `org_id: null` (미확인: 실제 직렬화 결과) | 해시 |

대시보드는 두 이름을 모두 읽는다: `row.original.organization_id || row.original.org_id` (UI/components/VirtualKeysPage/keyTableColumns.tsx:283), 조직 열 accessor 는 `org_id`(keyTableColumns.tsx:173).

### 2.4 대시보드 공통 오류 처리 중 키와 관련된 것

- **응답 문자열에 `"Authentication Error - Expired Key"` 가 있으면 대시보드가 로그아웃한다**(쿠키 삭제 후 새로고침, NW:434-450). 만료 메시지 문구를 정확히 맞춰야 한다(§5.2).
- 키 생성 실패는 `simplifyKeyGenerateError` 를 거친다. 오류 문자열에 `/key/generate` 또는 `KeyManagementRoutes.KEY_GENERATE` 가 있고, `team_member_permission_error` 또는 `"Team member does not have permissions"` 가 있으면 고정 문구로 바꾼다. 나머지는 `"Error creating the key: <원문>"` 이다(UI/components/organisms/utils.ts:7-56).
- regenerate·list·reset_spend 는 `deriveErrorMessage(errorData)` 로 메시지를 꺼낸다(NW:1574-1578, useKeys.ts:95-98, useResetKeySpend.ts:30-34). 파서 순서는 H §2.3 을 따른다.

### 2.5 premium(유료) 검사 지점 — 우리는 강제하지 않는다. 기록만 남긴다 ("premium, 적용 안 함")

| 위치 | 내용 |
|---|---|
| KM:6367-6375 | **`/key/regenerate` 전체가 premium 전용**. 비 premium 이면 `ValueError("Regenerating Virtual Keys is an Enterprise feature, …")` → **500** (A). 마스터 키 교체(`new_master_key`)만 예외 |
| UI/components/templates/key_info_view.tsx:599 | 대시보드도 `premiumUser` 가 아니면 Regenerate 버튼을 비활성화한다. 우리가 regenerate 를 열려면 대시보드가 premium 으로 인식해야 한다 (premium 플래그 출처는 미확인) |
| KM:1647-1653 | `tags` 지정은 premium (`ValueError` → 500) |
| KM:5150, KM:8372-8403 `validate_model_max_budget` | `model_max_budget` 은 enterprise 라이선스 필요 (ValueError) |
| KM:4972-4994 | 와일드카드 라우트의 모델 액세스 그룹 지정은 premium (403) |
| KM:5245-5246 | `permissions.get_spend_routes` 는 enterprise |
| KM:1544-1555 | `apply_enterprise_key_management_params` (enterprise 패키지 있을 때만) |
| RC:413-420 | `admin_only_routes` 설정은 enterprise |

---

## 3. 키 관리 엔드포인트

### 3.1 `POST /key/generate` (KM:2267-2478, 본체 `_common_key_generation_helper` KM:1414-1811, 저장 `generate_key_helper_fn` KM:5028-5339)

**요청 본문** `GenerateKeyRequest`(KR:57-77) ← `KeyRequestBase`(KR:20-43) ← `GenerateRequestBase`(RB:24-67). 모르는 필드는 무시한다(pydantic 기본 `extra=ignore`, 미확인: 전역 설정이 바뀌었는지).

| 필드 | 타입 | 기본값 | 대시보드 전송 |
|---|---|---|---|
| key_alias | str\|null | null | ○ 폼 (create_key_button.tsx 필드) |
| duration | str\|null | null | ○ 빈 문자열이면 `null` 로 보냄 (UI/components/organisms/createKeyPayload.ts:210) |
| models | list | `[]` (= 모든 모델) | ○ |
| spend | float | 0 | × |
| max_budget | float\|null (`""` → null, RB:62-67) | null | ○ |
| user_id | str\|null | null | ○ **항상 넣는다**: `{user_id: userID, ...formValues}` (NW:957-960). 소유자 "you" 면 로그인 사용자, 다른 사용자 지정 시 폼 값 (createKeyPayload.ts:207) |
| team_id | str\|null (`""` → null, KR:72-77) | null | ○ |
| organization_id, project_id | str\|null (`""`→null) | null | ○ null 이면 키를 뺌 (createKeyPayload.ts:205-206) |
| agent_id | str\|null | null | 에이전트 키일 때 (NW:989-997) |
| metadata | dict | `{}` | ○ **JSON 문자열을 만들었다가 `keyCreateCall` 이 다시 `JSON.parse`** 해서 객체로 보냄 (createKeyPayload.ts:211, NW:941-950, jsonFields = metadata·config·enforced_params·aliases, UI/components/common_components/check_openapi_schema.tsx:41). 서비스 계정이면 `metadata.service_account_id = key_alias` (createKeyPayload.ts:95-97) |
| tpm_limit, rpm_limit, tpd_limit, max_parallel_requests | int\|null | null | tpm·rpm·tpd ○ |
| budget_duration | str\|null | null | ○ "never resets" 선택 시 `null` (createKeyPayload.ts:219) |
| budget_limits | list\|null | null | 있을 때만 [extra] |
| allowed_cache_controls | list | `[]` | × |
| config, permissions, aliases | dict | `{}` | aliases 는 있을 때만 (JSON 문자열 → parse) |
| model_max_budget | dict | `{}` | 있을 때만 [premium] |
| budget_fallbacks, model_rpm_limit, model_tpm_limit, mcp_rpm_limit, tag_rpm_limit | dict\|null | null | tag_rpm_limit·budget_fallbacks 있을 때만 [extra] |
| guardrails, policies, prompts, tags | list\|null | null | ○ 폼에 있음 [extra/tags=premium] |
| blocked | bool\|null | null | × |
| object_permission | 객체\|null | null | 있을 때만 [extra: MCP·vector store 등] |
| key | str\|null | null | 사용자 지정 키 (`disable_custom_api_keys` 면 폼에서 숨김, create_key_button.tsx:1877) |
| key_type | `"llm_api"\|"management"\|"read_only"\|"default"\|null` | **`"default"`** (KR:60-63) | ○ |
| allowed_routes | list | `[]` | × (비관리자가 보내면 403) |
| allowed_passthrough_routes, denied_passthrough_routes | list\|null | null | allowed ○ [extra] |
| auto_rotate | bool | false | 자동 교체 켜면 `true` + `rotation_interval` (createKeyPayload.ts:209) |
| soft_budget | float\|null | null | × |
| send_invite_email, budget_id, end_user_budget_id, rpm_limit_type, tpm_limit_type, router_settings, access_group_ids, enforced_params, disable_global_guardrails, enable_prompt_caching, throttle_on_budget_exceeded, default_estimated_output_tokens(_per_model), allowed_vector_store_indexes | | null | 일부 ○ [extra] |

헤더 `litellm-changed-by`(선택)는 감사 로그에만 쓴다(KM:2277-2280).

**성공 응답**: 200, `response_model=GenerateKeyResponse`(KM:2271, T:1205-1243). 평면 객체다.
```json
{"key": "sk-<22자>",            // 평문. 이 응답에서만 한 번 나온다
 "token": "<sha256 hex>", "token_id": "<sha256 hex>",
 "key_name": "sk-...abcd", "key_alias": "...", "expires": "<ISO>"|null,
 "user_id", "team_id", "organization_id", "project_id", "agent_id",
 "models": [], "spend": 0.0, "max_budget": null, "budget_duration": null,
 "metadata": {}, "tpm_limit", "rpm_limit", "tpd_limit", "max_parallel_requests",
 "allowed_cache_controls": [], "allowed_routes": [], "key_type": "default",
 "config": {}, "permissions": {}, "aliases": {}, "model_max_budget": {}, "budget_fallbacks": {},
 "blocked": null, "budget_id": null, "litellm_budget_table": null,
 "created_by", "updated_by", "created_at", "updated_at",
 ... KeyRequestBase/GenerateRequestBase 의 나머지 필드(대부분 null) ... }
```
- 순서: `generate_key_helper_fn` 이 `key_data["token"]` 에 **평문**을 담는다(KM:5188). `GenerateKeyResponse` 의 before 검증기가 `token` 을 `key` 로 복사한다(T:1221-1224). 그 뒤 핸들러가 `token = token_id`(해시)로 덮는다(KM:1799). `token_id` 는 DB 삽입 결과의 해시다(KM:5301).
- `max_budget`·`budget_duration` 은 키 자신의 값이다. 요청 `max_budget` 을 `key_max_budget` 으로 이름을 바꿔 넘긴다(KM:1632-1642, KM:5195).
- `soft_budget` 은 응답 dict 에 넣지만(KM:1795) `GenerateKeyResponse` 에 그 필드가 없어서 응답에서 빠진다. 미확인: 직렬화 결과.
- 대시보드가 읽는 필드: `response["key"]` 를 모달에 한 번 보여 준다(create_key_button.tsx:492, 1915-1921). `addKey(response)` 로 표에 즉시 넣고(create_key_button.tsx:486) 목록을 다시 불러온다(:490).

**오류**: 핸들러가 `handle_exception_on_proxy` 로 감싼다(KM:2476-2478). HTTPException 은 (A) `type="internal_server_error"`, `param=null`, message 는 detail(dict 면 repr 문자열)이다(PU:9052-9059). 이미 ProxyException 인 것은 그대로 나간다(PU:9060-9061).

| 상황 | 상태·본문 | 근거 |
|---|---|---|
| max_budget 음수·NaN·inf | 400 `{'error': 'max_budget must be a non-negative finite number. Received: …'}` | KM:2382-2388 |
| 비관리자가 없는 team_id | 400 `Team not found for team_id=<id>. Non-admin users cannot create keys for non-existent teams.` | KM:2438-2444 |
| 팀 키인데 팀 객체 없음(설정 있을 때 관리자 포함) | 400 `Unable to find team object in database. Team ID: <id>` | KM:845-857 |
| 팀 키: 지정한 user_id 가 팀 멤버 아님 | 400 `User=<uid> not assigned to team=<tid>` | KM:679-688 |
| 팀 키: 호출자가 팀 멤버 아님 | 400 `User=<caller> not assigned to team=<tid>` | KM:697-702 |
| 팀 키: 멤버(role=user)인데 팀이 `/key/generate` 를 허용 안 함(**기본값**) | **401**, ProxyException 그대로: `type="team_member_permission_error"`, `param="/key/generate"`, message `"Team member does not have permissions for endpoint: <route>. You only have access to the following endpoints: [...] for team <tid>. To create keys for this team, …"` | TMP:112-139, KM:734-760 |
| 개인 키에 타인 user_id | 403 `User can only create keys for themselves. Got user_id=<x>, Your ID=<y>` | KM:648-671, KM:907-931 |
| 서비스 계정 키로 다른 팀 키 생성 | 403 `Service account keys can only create keys for their own team. team_id=…` | KM:833-838 |
| 비관리자가 `allowed_routes` 지정 | 403 `{'error': 'Only proxy admins can set `allowed_routes` on a key. Use `key_type` to pick a preset route bucket instead.'}` | KM:1053-1093 |
| 비관리자가 `key_type="management"` | 403 (위와 같은 문구. 프리셋 `llm_api_routes`·`info_routes` 만 허용) | KM:1615-1626, KM:1004 |
| 비관리자가 `throttle_on_budget_exceeded=true` | 403 `{'error': 'Only proxy admins can enable throttle_on_budget_exceeded on a key.'}` | KM:1456-1461 |
| budget_duration 형식 오류 | 400 (§2.2) | KM:1454 |
| duration 형식 오류 | **500** (§2.2) | KM:5112 |
| key_alias 에 제어문자나 `..` 경로 조각 | 400 (A) `type="bad_request_error"`, `param="key_alias"`, `"Invalid key_alias: must not contain control characters (such as tab or newline) or a \"..\" path segment"` | KM:8299-8323 |
| key_alias 형식(설정 `key_alias_pattern` 또는 `enable_key_alias_format_validation` 켰을 때만) | 400 bad_request_error, param key_alias. 기본은 **검사 안 함** | KM:8266-8296, KM:8325-8336 |
| **key_alias 중복 — 전체 키에서 전역 고유** | 400 `{"error":{"message":"Key with alias '<a>' already exists. Unique key aliases across all keys are required.","type":"bad_request_error","param":"key_alias","code":"400"}}` | KM:8339-8369, 호출 KM:1717-1720 |
| `disable_custom_api_keys` 인데 key 지정 | 403 `{'error': 'Custom API key values are disabled by your administrator. Keys must be auto-generated.'}` | KM:550-563 |
| 사용자 지정 key 가 `sk-` 로 시작 안 함 | 400 `{'error': "Invalid key format. LiteLLM Virtual Key must start with 'sk-'. Received: abcd****wxyz"}` | KM:1726-1731 |
| 사용자 지정 key 길이 < 16 | 400 `{'error': 'Invalid key format. LiteLLM Virtual Key must be at least 16 characters long.'}` | KM:1733-1739 |
| organization_id 가 없는 조직 | 400 `Organization not found for organization_id=…` | KM:1766-1774 |
| `auto_rotate=true` + `store_virtual_keys` 설정 + alias 없음 | 400 bad_request_error param key_alias | KM:619-632 |
| DB 미연결 | 500 | KM:2374-2378 |
| 라우트 단계 거부 | 401/403 (D) | §6 |

- 대시보드는 **같은 팀 안**에서만 alias 중복을 미리 막는다(`Key alias <a> already exists for team with ID <t>, …`, createKeyPayload.ts:166-176, create_key_button.tsx:461-465). 서버는 전역으로 막는다. 다른 팀에 같은 alias 가 있으면 서버 400 이 난다.

**권한**
- 라우트: `/key/generate` 는 `key_management_routes` → `internal_user_routes` 에 들어 있다(T:594-619, T:788-816). 사용자 행 없는 팀 서비스 계정 키도 `team_service_account_key_routes` 로 통과한다(T:621-624, RC:389-394).
- 핸들러:
  - **user_id 기본값**: 호출자가 proxy_admin 이 아니고 `user_id` 가 없으면 **호출자 user_id 로 채운다**(KM:2414-2425). proxy_admin 은 null 로 둘 수 있다.
  - 개인 키(team_id 없음): `key_generation_settings.personal_key_generation.allowed_user_roles` 설정이 없으면 누구나 자기 키를 만든다(KM:772-822).
  - 팀 키: proxy_admin 은 통과. 그 외는 호출자가 팀 roster 에 있어야 하고, 기본 허용 팀 역할은 `["admin","user"]` 다(KM:740-746). 그다음 `does_team_member_have_permissions_for_endpoint`: role=admin 이면 통과, role=user 면 팀 `team_member_permissions` 에 해당 라우트가 있어야 한다(TMP:112-141). **기본 권한은 `/key/info`, `/key/health` 뿐이다**(TMP:18-23, TMP:28-45, SP:206 기본 `[]`). 즉 팀 일반 멤버는 기본적으로 팀 키를 못 만든다.
  - 팀 admin 이 다른 멤버 명의 키를 만들려면 `team_admin_editable_team_fields` 에 `create_member_keys` 가 있어야 한다(KM:1428-1452, 독스트링 KM:2293) [설정 의존].
  - org_admin 은 `disable_key_generate_for_org_admin` 이면 403 (rbac_utils.py:71-96) [조직, 범위 밖].

**부수 효과·기본값**
- 저장 행(KM:5181-5217): `token`=해시, `key_name`, `key_alias`, `expires`, `models`, `spend`, `max_budget`(키), `user_id`, `team_id`, `agent_id`, `project_id`, `metadata`(rpm/tpm 모델 한도·guardrails·policies·prompts 를 metadata 안에 합침, KM:5140-5149), `budget_duration`·`budget_reset_at`, `allowed_routes`(`[]`), `key_type`, `blocked`(**요청 없으면 null**, false 아님), `created_by`·`updated_by`=호출자 user_id(KM:1644-1646), `organization_id`, `router_settings`(`"{}"`).
- `key_type` 은 요청에서 빼도 모델 기본값 `"default"` 가 저장된다(KR:60-63 + KM:957-969). `llm_api`/`management`/`read_only` 는 `allowed_routes` 를 `["llm_api_routes"]`/`["management_routes"]`/`["info_routes"]` 로 채운다(KM:945-951).
- `default_key_generate_params`(설정)가 있으면 null 필드를 채운다(KM:1498-1522). `upperbound_key_generate_params` 가 있으면 넘는 값을 400 으로 막는다(KM:1321-1380) [설정 의존].
- `soft_budget` 이 있으면 `LiteLLM_BudgetTable` 행을 만들고 `budget_id` 로 연결한다(KM:1569-1590).
- **사용자 행을 만들지 않는다**(`table_name="key"`, KM:5262-5263). 없는 user_id 로 키를 만들 수 있다.
- **키 models ⊆ 팀 models 검사는 생성 시점에 없다.** 팀 관련 생성 검사는 `guaranteed_throughput` 한도 초과 할당뿐이다(KM:1964-1998). 모델 제한은 요청 시점에 팀 모델 검사로 걸린다(§5). 팀 변경(update) 때만 `can_team_access_model` 로 검사한다(KM:4472-4500).
- 캐시에는 쓰지 않는다(첫 요청 때 DB 에서 읽어 캐시).

### 3.2 `POST /key/service-account/generate` (KM:2481-2650)

- 본문은 `GenerateKeyRequest` 와 같다. 대시보드는 `/key/generate` 와 같은 payload 를 보낸다(NW:865-920).
- `team_id` 가 필수다. `validate_team_id_used_in_service_account_request` 가 검사한다(KM:2527-2530, KM:1285-1318). 정확한 문구는 미확인.
- `metadata.service_account_id` 가 없으면 `key_alias` 나 uuid4 로 채운다(KM:2531-2538). **`user_id` 는 강제로 null** 이다(KM:2644).
- 권한 검사는 `key_generation_check(route=/key/service-account/generate)` 다. 팀 일반 멤버는 이 라우트도 팀 권한에 있어야 한다(KM:2636-2641).
- 핸들러에 try/except 가 없다. 그래서 HTTPException 은 **(B) `{"detail": …}`** 로 나간다(management_endpoint_wrapper 는 다시 던질 뿐이다, MH:813-844).
- 성공 응답은 3.1 과 같다(같은 helper, KM:2645-2650). 다만 `response_model` 이 선언되어 있지 않다(KM:2481-2485). 미확인: 직렬화 차이.

### 3.3 `POST /key/update` (KM:3805-4088)

**요청 본문** `UpdateKeyRequest`(KR:84-127): 모든 필드 기본값이 null 이다.
- `key`(평문이나 해시) 또는 `key_alias` 중 하나가 필수다. 없으면 **422** (C) 다(KR:123-127 model_validator).
- `temp_budget_increase` 와 `temp_budget_expiry` 는 함께 보내야 한다. 아니면 422 (KR:116-121).
- `team_id: ""` 는 필드를 뺀 것으로 취급한다(KR:102-107). `organization_id: ""` 는 null 이다(KR:109-114).
- `project_id` 는 null(분리)만 된다. 다른 값으로 바꾸면 400 `Project reassignment is not supported. Use null to detach the key.` (KM:3546-3549).
- **보낸 필드만** 반영한다(`model_dump(exclude_unset=True)`, KM:2823). 키 컬럼은 명시적 null 이면 지운다. 메타데이터 기반 필드(guardrails 등)는 null 이면 유지한다. `metadata` 자체는 통째로 바꾼다(독스트링 KM:3818-3821).
- 대시보드 전송: 편집 폼 값 전체 + `key = formValues.token`(**해시**, key_info_view.tsx:211-212). `key_alias` 를 항상 보낸다. 그래서 alias 고유성 검사에서 자기 자신은 뺀다(KM:8352-8355). metadata 는 객체로 만들고 tags·guardrails·logging·disabled_callbacks 를 metadata 안에 넣는다(key_info_view.tsx:318-361). `max_budget, tpm_limit, rpm_limit, tpd_limit, max_parallel_requests` 의 `""` 는 null 로 바꾼다(:311-315). `soft_budget`·`disable_global_guardrails` 는 바뀐 경우에만 보낸다(:234-260). `model_tpm_limit`·`model_rpm_limit` 문자열은 JSON.parse 한다(NW:2762-2778).

**성공 응답**: 200, response_model 없음.
```json
{"key": "<요청에 보낸 key 그대로(대시보드는 해시)>", "token": "<해시>", ...갱신된 Prisma 키 행 전체(object_permission include)...}
```
(KM:4068, Prisma `update_data` 가 `{"token","data": row.model_dump()}` 를 돌려준다, PU:6166-6184.) 대시보드는 이 응답을 현재 키 상태에 병합한다(key_info_view.tsx:398-405).

**오류** — 형태 주의: HTTPException 은 **(A) `type="auth_error"`, `param="None"`(문자열)** 로 감싼다. ProxyException 은 그대로 나간다. 그 밖의 예외는 400 `"Authentication Error, <msg>"` 다(KM:4069-4088).

| 상황 | 상태·본문 | 근거 |
|---|---|---|
| key 로 찾았는데 없음 | 404 `{"error":{"message":"Key not found.","type":"not_found_error","param":"key","code":"404"}}` | KM:2989-3004 |
| key_alias 로 찾았는데 없음 | 404 `"Key not found. No key with key_alias='<a>'."`, param `key_alias` | KM:3015-3025 |
| key_alias 가 여러 키에 걸림 | 400 bad_request_error `"Multiple keys share key_alias='<a>', so it cannot be used as an identifier."` | KM:3027-3033 |
| 다른 키와 alias 중복 | 400 bad_request_error (3.1 과 같은 문구) | KM:3951-3955 |
| 비관리자가 user_id 를 지우려 함 | 403 auth_error `"Non-admin users cannot remove the user_id from a key."` | KM:1036-1041 |
| 비관리자가 user_id 를 바꾸려 함 | 403 `"Non-admin caller is not allowed to rebind the key from user=<a> to user=<b>"` | KM:1042-1050 |
| 팀 키 + 팀 일반 멤버 + 팀이 `/key/update` 미허용 | 401 team_member_permission_error (3.1 과 같은 문구, route `/key/update`) | KM:3569-3575, TMP:57-102 |
| 팀 키 + 팀 멤버 아님 | 401 team_member_permission_error `"User <uid> does not belong to team <tid>. Team-scoped key management endpoints can only be used for keys in your own team."` | TMP:94-102 |
| admin 검사 실패 | 403 `{'error': 'Only proxy admins, team admins, or org admins can call /key/update. user_role=…, user_id=…'}` (예산 변경이면 route 가 `/key/update (max_budget/spend)`) | KM:3670-3680, KM:7838-7845 |
| 비관리자가 없는 team_id 로 옮김 | 400 `Team not found for team_id=…. Non-admin users cannot set keys to non-existent teams.` | KM:3699-3703 |
| 팀 변경: 키 모델을 새 팀이 못 씀 / tpm·rpm 초과 / 키 소유자가 팀 멤버 아님 / 호출자가 팀 admin 아님 | 403 각 문구 | KM:4472-4535 |
| 팀 변경인데 라우터 없음 | 400 | KM:3772-3778 |
| max_budget 음수·NaN | 400 | KM:3909-3915 |

**권한** (`_validate_update_key_data`, KM:3505-3803)
- proxy_admin 은 모두 통과한다.
- 팀 키면 먼저 `can_team_member_execute_key_management_endpoint` 를 거친다(KM:3569-3575). 팀 admin 은 통과하고, 일반 멤버는 팀 권한에 `/key/update` 가 있어야 하며, 멤버가 아니면 401 이다.
- 그다음 `can_skip_admin_check = (caller_is_creator 또는 팀 키) 이고 예산 변경이 허용되고 프로젝트 변경이 아님`(KM:3669-3671). `caller_is_creator` = 키의 `created_by == 호출자 == 키의 user_id`(KM:3647-3651). 건너뛰지 못하면 `_check_key_admin_access`(proxy/팀/조직 admin, KM:7797-7845)를 요구한다.
  - 결과: **관리자가 만들어 준 개인 키는 소유자가 직접 수정할 수 없다(403).** `created_by` 가 소유자가 아니기 때문이다. 소유자가 만든 개인 키는 예산 외 필드를 수정할 수 있다.
  - 예산 변경(max_budget, budget_limits, soft_budget, spend 존재 자체)은 admin 검사가 원칙이다. 예외로 `general_settings.self_serve_budget_policy` 가 소유자에게 허용할 수 있다(KM:3584-3606, KM:3449-3503) [설정 의존].

**부수 효과**
- 키 캐시를 지운다(`delete_cache_key_object(hash)`, KM:4023-4027). 다음 요청부터 바로 반영된다.
- `spend` 를 보내면 cross-pod spend 카운터도 그 값으로 60초 TTL 로 맞춘다(KM:4038-4050).
- `budget_duration` 이 바뀌면 `budget_reset_at` 을 다시 계산한다(KM:2863-2873).

### 3.4 `POST /key/delete` (KM:4538-4644)

**요청 본문** `KeyRequest`: `{"keys": [str]|null, "key_aliases": [str]|null}`. 둘 다 비면 **422** (`"At least one of 'keys' or 'key_aliases' must be provided."`, T:1269-1278). `keys` 원소는 평문이나 해시다(KM:5481). 대시보드는 `{keys: [token 해시]}` 를 보낸다(NW:1080, key_info_view.tsx:419).

**성공 응답**: 200 `{"deleted_keys": <요청의 keys 또는 key_aliases 그대로>}` (KM:4641). 대시보드는 읽지 않는다(key_info_view.tsx:419-425).

**오류** ((A) `type="internal_server_error"`, KM:4642-4644)

| 상황 | 상태·message | 근거 |
|---|---|---|
| 하나도 못 찾음 | 404 `"{'error': 'No keys found'}"` | KM:5489-5494 |
| 일부만 못 찾음 | **오류 없음.** 찾은 것만 지우고 응답은 요청 목록을 그대로 돌려준다. "Not all keys passed in were deleted" 검사(KM:4617-4625)는 같은 길이끼리 비교라서 실패하지 않는다 | KM:4616-4625 |
| 권한 없는 키가 하나라도 있음 | 403 `"{'error': 'You are not authorized to delete this key'}"` (전체 거부) | KM:5499-5512 |

**권한**: 라우트는 internal_user 통과다. 핸들러 `can_modify_verification_token`(KM:5380-5449): proxy_admin, 내부 작업 서비스 계정, 팀 키면 그 팀 admin 이나 키 소유자, 개인 키면 소유자만이다. **팀 멤버 권한(`/key/delete` 허용)은 검사하지 않는다**(`_team_key_deletion_check` KM:5342-5377 은 호출되는 곳이 없다).

**부수 효과 — 하드 삭제 + 감사 사본**
- 키 행 전체를 `LiteLLM_DeletedVerificationToken` 에 복사한다(`deleted_at`, `deleted_by`=호출자 user_id, `deleted_by_api_key`, `litellm_changed_by` 추가, KM:5582-5636, SP:604-). 그 뒤 `LiteLLM_VerificationToken` 에서 삭제한다(KM:5537-5546).
- 캐시에서 평문 키와 해시 키를 모두 지운다(KM:5560-5564).
- 삭제된 키는 `/key/list?status=deleted` 와 `/key/info`(§3.5)로 계속 조회된다.

### 3.5 `GET /key/info` (KM:4815-4957)

**쿼리**: `key: str|null`. 평문이나 해시를 받는다. 해시를 권장한다(KM:4818-4824). 없으면 **호출자 자신의 키**를 쓴다(`user_api_key_dict.api_key` = 해시, KM:4886). 대시보드는 `?key=<해시>` 를 보낸다(NW:2411-2412).

**성공 응답**: 200
```json
{"key": "<쿼리 값 그대로>",
 "info": { ...키 행(token 제거, litellm_budget_table include)...,
           "status": "active"|"expired"|"revoked"|"deleted",
           "model_max_budget_usage": {...}?, "budget_limits_usage": {...}?,
           "object_permission": {...}|null } }
```
(KM:4922-4947.)
- `status` 는 `blocked=true` → `revoked`, `expires < now` → `expired`, 나머지 → `active` 다(KM:7033-7040). 라이브 행이 없고 **삭제 사본이 있으면 200 으로 그 사본을 `status="deleted"` 와 함께 돌려준다**(KM:4888-4898, KM:4960-4969). 이때 `deleted_at`, `deleted_by` 등이 포함된다.
- 대시보드 사용: `info` 를 펼치고 `token`·`api_key` 를 쿼리 값으로 덮는다. `info.user_id` 가 있으면 `/v2/user/info` 로 소유자를 붙인다(useKeyInfo.ts:24-45).

**오류** ((A), `handle_exception_on_proxy`, KM:4948-4957)
- 없음(라이브·삭제 둘 다): 404 `{"error":{"message":"Key not found in database","type":"not_found_error","param":"key","code":"404"}}` (KM:4904-4910).
- 접근 불가: 403 `"You are not allowed to access this key's info. Your role=<role>"`, `type="internal_server_error"` (KM:4911-4922).
- 그 밖: 500 `type="internal_error"`, `"Internal server error from /key/info. Details are in the proxy server logs."` (KM:4950-4957).

**권한**: info_route 라 라우트 단계는 모든 역할이 통과한다(T:560, RC:329-332). 핸들러 `_can_user_query_key_info`(KM:8166-8187): proxy_admin, proxy_admin_viewer, 호출 키 자신, **키의 user_id == 호출자**, 키의 팀 멤버(`user_belongs_to_keys_team`, 미확인: 내부 조건).

### 3.6 `GET /key/list` (KM:7043-7240, helper KM:7597-7782)

**쿼리**

| 파라미터 | 기본값 | 의미 | 대시보드 전송 (useKeys.ts:57-81) |
|---|---|---|---|
| page | 1 (≥1) | | ○ |
| size | **10** (1..100, 범위 밖 422) | | ○ |
| user_id | null | 정확히 일치. `substring_matching=true` 이고 **admin/viewer** 일 때만 부분 일치(대소문자 무시) | ○ 필터 |
| team_id | null | 전역 AND 필터 | ○ |
| organization_id | null | "본인 키" 조건 안에만 들어간다(KM:7509) | ○ |
| key_hash | null | `token` 정확히 일치 | ○ |
| key_alias | null | 정확히 일치. `substring_matching=true` 면 누구나 부분 일치 | ○ |
| search | null | `token == search` OR `key_alias contains search`(대소문자 무시) | ○ 검색창 |
| return_full_object | false | false 면 `keys` 가 **해시 문자열 배열** | ○ `"true"` |
| include_team_keys | false | admin 인 팀(+`/key/list` 권한 팀)의 모든 키 포함 | ○ `"true"` |
| include_created_by_keys | false | 본인이 만든 키 포함(현재 소속 팀이거나 팀 없는 것만) | ○ `"true"` |
| substring_matching | false | | ○ `"true"` |
| sort_by | null → `created_at desc, token desc` | 허용: spend, max_budget, created_at, updated_at, token, key_alias. 그 외 400 | ○ 기본 `created_at` (UI/components/VirtualKeysPage/VirtualKeysTable.tsx:73,128). 대시보드 정렬 가능 열도 이 6개뿐이다(keyTableColumns.tsx:43-49, :122-355) |
| sort_order | "desc" | asc\|desc, 그 외 400 | ○ |
| expand | null | `user` 면 각 키에 `user`(사용자 행 전체)와 `created_by_user`({user_id,user_email,user_alias})를 붙이고 full object 로 반환 | ○ `"user"` (VirtualKeysTable.tsx:130) |
| status | null | active\|expired\|revoked\|deleted. 그 외 400. `deleted` 는 삭제 사본 테이블을 읽는다 | ○ 필터, 삭제 키 화면은 `deleted` |
| expires | null | active\|expired | × |
| project_id, access_group_id, agent_id | null | 전역 AND 필터 | project·agent ○ |

**성공 응답**: 200 `KeyListResponseObject`(T:4412-4416)
```json
{"keys": [ <UserAPIKeyAuth 직렬화> | "<해시>" | <DeletedVerificationToken> ],
 "total_count": 123, "current_page": 1, "total_pages": 13}
```
- `total_pages = ceil(total_count/size)` 이다. 0 건이면 0 이다(KM:7720).
- full object 원소는 `UserAPIKeyAuth(**행)` 이다(KM:7768). 그래서 VT 필드에 더해 `team_spend, team_alias, team_models:[], team_blocked:false, …, api_key:null, user_role:null, user, created_by_user, object_permission, litellm_budget_table` 같은 View 필드가 기본값으로 붙는다(UAKT:56-118, :130-). **조직은 `org_id` 로 나간다**(§2.3). `token` 은 해시다.
- 대시보드 UI 세션 키(`team_id="litellm-dashboard"`)는 항상 뺀다(KM:7785-7794, CONST:1804).
- 대시보드가 읽는 필드: `keys[].key_alias, key_name, token, token_id(테스트 id 용), team_id, org_id/organization_id, user(.user_email 등), user_id, created_at, created_by, created_by_user, updated_at, last_active, expires, spend, max_budget, total_spend, budget_reset_at, models, allowed_routes, key_type, blocked, metadata.scim_blocked, deleted_at, deleted_by, tpm/rpm 한도` (keyTableColumns.tsx:51-79, :104-360, UI/components/key_team_helpers/key_list.tsx:34-114). 최상위는 `keys, total_count, current_page, total_pages` 다(useKeys.ts:15-20, :138-139).

**가시성(비관리자)** — `_build_key_filter_conditions`(KM:7460-7594). 대시보드처럼 세 플래그를 켜면 아래 OR 이다.
1. `user_id == 본인` (user_id 를 안 보내면 본인으로 강제, KM:7191-7193).
2. `created_by == 본인` AND (팀 없음 OR 현재 roster 에 있는 팀) (KM:7515-7540).
3. 본인이 admin 인 팀의 모든 키, 그리고 `team_member_permissions` 에 `/key/list` 가 있는 팀의 모든 키 (KM:7542-7543, KM:7147-7173).
4. 본인이 멤버인 팀(admin 팀 제외)의 **user_id 가 NULL 인 키(서비스 계정)** (KM:7545-7560).

여기에 team_id·key_alias·search·status 등이 전역 AND 로 붙는다(KM:7569-7592). proxy_admin·proxy_admin_viewer 는 user_id 없이 부르면 전체를 본다(KM:7182-7193, KM:6847-6848).

**오류** — HTTPException 은 (A) `type="internal_server_error"`, `param="None"`(문자열)로 감싼다. ProxyException 은 그대로, 그 밖은 500 `"Authentication Error, …"` 다(KM:7225-7240).

| 상황 | 상태·본문 | 근거 |
|---|---|---|
| 비관리자 키에 user_id 없음, 또는 사용자 행 없음 | 403 bad_request_error `"You are not authorized to access this endpoint. No 'user_id' is associated with your API key."`, param user_id | KM:6850-6868 |
| 타인 user_id | 403 `"You are not authorized to check another user's keys"` | KM:6872-6880 |
| 소속 아닌 team_id (사용자 행 `teams` 배열 기준) | 403 `"You are not authorized to check this team's keys"` | KM:6882-6889 |
| 소속 아닌 organization_id | 403 | KM:6891-6900 |
| key_hash 없음 | 403 `"Key Hash not found."` | KM:6902-6921 |
| key_hash 접근 불가 | 403 `"You are not allowed to access this key's info. Your role=…"` | KM:6922-6932 |
| status·expires·sort 값 오류 | 400 `{'error': "Invalid status value. Supported: 'active', 'expired', 'revoked', 'deleted'."}` 등 | KM:7123-7133, KM:7400-7428 |

### 3.7 `GET /key/aliases` (KM:7278-7397)

- 쿼리: `page`(1), `size`(50, 1..100), `search`(ILIKE `%s%`), `team_id` (KM:7286-7290). 대시보드는 page·size·search·team_id 를 보낸다(NW:2491-2511).
- 응답: `{"aliases": [str], "total_count", "current_page", "total_pages", "size"}`. alias 오름차순이다. null·빈 alias 와 UI 세션 키는 뺀다(KM:7318-7370).
- 비관리자(admin·viewer 아님)는 `user_id = 본인` OR `team_id ∈ 사용자 행 teams` 로 범위를 좁힌다(KM:7244-7275).
- 오류 처리는 /key/list 와 같은 래핑이다(KM:7381-7397).

### 3.8 `POST /key/block` / `POST /key/unblock` (KM:7848-8074)

- 본문 `BlockKeyRequest`: `{"key": str}` 필수(T:2048-2049). 대시보드는 `{key: keyToken(해시)}` 를 보낸다(useSetKeyBlockedState.ts:23-26).
- 응답: 갱신된 키 행이다(`blocked: true|false`). 대시보드는 `response?.blocked ?? 요청값` 만 읽는다(useSetKeyBlockedState.ts:27).
- **권한이 존재 확인보다 먼저다**(KM:7905-7924).
  - 비관리자 + 키 없음: **(B)** 404 `{"detail":{"error":"Key not found: <hash>"}}` (KM:7820-7826).
  - 권한 없음: **(B)** 403 `{"detail":{"error":"Only proxy admins, team admins, or org admins can call /key/block. user_role=…, user_id=…"}}` (KM:7838-7845).
  - proxy_admin + 키 없음: (A) 404 `not_found_error` `"Key not found."` (KM:7915-7924).
  - 형식 오류: (A) 400 bad_request_error `"Invalid key format."` (KM:7893-7899).
- 허용: proxy_admin, 키 팀의 팀 admin, 그 팀 조직의 org_admin 이다(`TEAM_OR_ORG_ADMIN`, KM:7829-7836). **개인 키는 소유자라도 block 할 수 없다.**
- 부수 효과: `blocked` 와 `settings_updated_at` 을 바꾸고(KM:7926-7929), 키 캐시를 지운다(KM:7953-7957). 감사 로그 설정 시 기록한다.
- 막힌 키로 요청하면 401 이다(§5.2).

### 3.9 `POST /key/{key}/regenerate` (KM:6250-6522, 실행 KM:6037-6232) — premium, 적용 안 함

- 경로 `"/key/{key:path}/regenerate"` 와 `"/key/regenerate"` 둘 다 같은 핸들러다(KM:6250-6259). 본문 `data.key` 가 경로 값보다 우선한다(KM:6378).
- **본문** `RegenerateKeyRequest`(KR:130-138) = GenerateKeyRequest + `new_key`, `duration`, `spend`, `metadata`, `new_master_key`, `grace_period`. 대시보드는 `{key_alias, max_budget(소수 2자리 반올림), tpm_limit, rpm_limit, duration, grace_period}` 를 보낸다. **duration·grace_period 는 빈 문자열 `""` 일 수 있다**(UI/components/organisms/regenerateKeyPayload.ts:3-30, RegenerateKeyModal.tsx:24-31). `""` duration 은 무시된다(KM:2853-2861).
- **응답**: `GenerateKeyResponse`(KM:6222). 갱신된 행 + `key`=새 평문, `token_id`=새 해시다(KM:6185-6188). `token` 은 pop 되어서 **null** 이다(generate 와 다르다). `key_name` 은 새 평문의 축약이다(KM:6102, KM:6154).
  - 대시보드: `response.key` 를 보여 주고, `token: response.token_id || response.token || 기존`, `expires: response.expires ?? 기존` 으로 갱신한다(RegenerateKeyModal.tsx:98-116).
- **부수 효과**
  - 같은 행의 PK(`token`)를 새 해시로 바꾼다(UPDATE, KM:6180-6184). 그래서 user_id·team_id·spend 등이 이어진다.
  - **옛 행을 `LiteLLM_DeletedVerificationToken` 에 사본으로 남긴다**(KM:6165-6170). 그래서 삭제 키 목록에 옛 키가 나타난다.
  - `grace_period`(또는 env `LITELLM_KEY_ROTATION_GRACE_PERIOD`)가 양수면 `LiteLLM_DeprecatedVerificationToken(token=옛 해시, active_token_id=새 해시, revoke_at)` 을 upsert 한다(KM:5976-6034, SP:591-602). 인증 시 본 테이블에 없으면 이 표를 찾아서 **옛 키가 revoke_at 까지 새 키로 동작한다**(PU:5898-5915, PU:4881-4925, 양성 결과 캐시 60초 PU:4876). 형식이 잘못된 grace_period 는 **조용히 무시한다**(경고 로그만, KM:5997-6004).
  - 옛 해시 캐시를 지운다(KM:6197-6202).
- **오류** ((A) `internal_server_error`, KM:6520-6522)

| 상황 | 상태·message | 근거 |
|---|---|---|
| 비 premium | **500** `"Regenerating Virtual Keys is an Enterprise feature, …"` [premium, 적용 안 함] | KM:6370-6375 |
| key 없음(경로·본문 모두) | 400 `{'error': 'No key passed in.'}` | KM:6378-6380 |
| 키 없음 | 404 `{'error': 'Key <입력값> not found.'}` | KM:6424-6428 |
| 팀 일반 멤버 + 팀이 `/key/regenerate` 미허용 | 401 team_member_permission_error | KM:6437-6443 |
| 소유자도 팀 admin 도 아님 | 403 `{'error': 'You are not authorized to regenerate this key'}` | KM:6446-6455 |
| new_key 가 `sk-` 아님 / 16자 미만 | 400 | KM:5954-5970 |
| 비관리자가 user_id 변경 | 403 (3.3 과 같음) | KM:6055-6059 |

- 권한: 3.4 의 `can_modify_verification_token` + 팀 멤버 권한 검사다. 즉 팀 키의 일반 멤버 소유자는 팀 권한이 없으면 401 이다.

### 3.10 `POST /key/{key}/reset_spend` (KM:6732-6836)

- 본문 `ResetSpendRequest`: `{"reset_to": float}` 필수. bool 이면 422 (T:1255-1266). 대시보드는 항상 `{reset_to: 0}` 을 보낸다(useResetKeySpend.ts:28).
- 응답: `{"key_hash": "<해시>", "spend": <reset_to>, "previous_spend", "max_budget", "budget_reset_at"}` (KM:6824-6830). 대시보드 타입과 같다(useResetKeySpend.ts:8-14).
- **값 검증이 권한 검사보다 먼저다**(KM:6777-6784).
- 오류는 **(B)** 다(HTTPException 을 그대로 다시 던짐, KM:6831-6832).
  - 키 없음: 404 `{"detail":{"error":"Key <입력값> not found."}}` (KM:6770-6774).
  - `reset_to` 검증: 400 `"reset_to must be a float"`, `"reset_to must be >= 0"`, `"reset_to (x) must be <= current spend (y)"`, `"reset_to (x) must be <= budget (z)"` (KM:6554-6590).
  - 권한: 403 `{"detail":{"error":"You must be a proxy admin or team admin to reset key spend"}}` (KM:6525-6551). **소유자도 불가하고, 개인 키는 proxy_admin 만 가능하다.**
- 부수 효과: DB `spend` 를 바꾸고, spend 카운터와 budget_limits 창을 초기화하고, 마지막에 캐시를 지운다(KM:6786-6819).

---

## 4. 저장 모델 (요약)

- `LiteLLM_VerificationToken`(SP:502-560): PK `token`(해시). `key_alias` 는 unique 가 아니다. `blocked Boolean?`(기본 없음 → null), `models String[]`, `spend` 기본 0, `expires DateTime?`, `user_id`, `team_id`, `organization_id`, `created_by` 등이 있다. 인덱스는 `(user_id, team_id)`, `team_id`, `(budget_reset_at, expires)` 다.
- `LiteLLM_DeletedVerificationToken`(SP:604-): 키 필드 + `id`(uuid PK), `deleted_at`, `deleted_by`, `deleted_by_api_key`, `litellm_changed_by` 다(VT:69-79). `token` 은 unique 가 아니다. regenerate 를 여러 번 하면 같은 해시 행이 여럿 생길 수 있다.
- `LiteLLM_DeprecatedVerificationToken`(SP:591-602): `token`(옛 해시, unique), `active_token_id`, `revoke_at` 이다.
- 키 행은 요청 인증 시 팀·사용자 등을 조인한 View 로 읽는다(`LiteLLM_VerificationTokenView`, UAKT:56-118. SQL 은 PU `get_data` 안, 미확인: 정확한 조인 목록).

---

## 5. 모든 요청에서의 가상 키 검증 (Part 2)

### 5.1 검사 순서

진입점은 `user_api_key_auth`(UAK:4216) → `user_api_key_auth_builder`(UAK:2088) → `validate_resolved_virtual_key`(UAK:2838) → `_authorize_authenticated_request`(UAK:4018) → `run_centralized_common_checks`(UAK:3459) → `common_checks`(AC:1076) 이다. 키·마스터 키 경로만 적는다(JWT·custom auth·OAuth2 는 B_auth.md 참고).

| # | 단계 | 실패 시 | 근거 |
|---|---|---|---|
| 1 | 키 추출 (헤더 우선순위는 B_auth.md §1) | 없음: 401 `"Authentication Error, No api key passed in."`. `Bearer ` 없음: 401 `"Authentication Error, Malformed API Key passed in. Ensure Key has `Bearer ` prefix."` | UAK:2513-2517, AEH:77-82 |
| 2 | `master_key` 미설정이면 어떤 키든 `internal_user` 로 통과 (무인증 모드) | — | UAK:2501-2512 |
| 3 | 캐시 조회(키 = `sha256(api_key)`). sk- 가 아니면 UI 세션 암호문 복호도 시도 | — | UAK:2596-2632 |
| 4 | 캐시에서 나온 토큰의 role 이 `proxy_admin` 이면 만료만 보고 즉시 반환 | 만료: 401 expired_key | UAK:2641-2672 |
| 5 | **마스터 키 비교**: `secrets.compare_digest(api_key, master_key)` (평문 비교. 해시 아님) | — | UAK:2705-2708 |
| 5a | 일치 → `user_role=proxy_admin`, `user_id="default_user_id"`(`litellm_proxy_admin_name`), `api_key="litellm_proxy_master_key"`(별칭) | — | UAK:2717-2748, CONST:1778, CONST:1817 |
| 6 | 마스터 키 아님 + `master_key_only_routes`(`/global/spend/reset` 등) | 401 `"Authentication Error, Tried to access route=…, which is only for MASTER KEY"` | UAK:2751-2753, T:588-592 |
| 7 | 캐시에 없고 **`sk-` 로 시작하지 않으면 거부** (해시를 그대로 키로 쓰는 것을 막음) | 401 auth_error `"LiteLLM Virtual Key expected. Received=abcd****wxyz, expected to start with 'sk-'."` (JWT 모양이면 안내 문구가 더 붙음) | UAK:2768-2785, CONST:1696 |
| 8 | DB 조회(해시). 없으면 grace 기간 표(§3.9)에서 찾음 | **키 없음**: 401 `{"error":{"message":"Authentication Error, Invalid API key provided.","type":"token_not_found_in_db","param":"key","code":"401"}}` | UAK:2789-2799, STORE:107-126, RES:25-40, CONST:1697 |
| 9 | **blocked** | 401 `{"error":{"message":"Authentication Error, Key is blocked. Update via `/key/unblock` if you're an admin.","type":"auth_error","param":"None","code":"401"}}` | UAK:2880-2881, AEH:77-82 |
| 10 | **키 모델 접근**(+클라이언트 fallbacks). `config` 가 있거나 models 에 `all-team-models` 가 있으면 건너뜀 | 403 §5.3 (`key_model_access_denied`) | UAK:2882-2889, UAK:4488-4548 |
| 11 | 키의 사용자 행 조회. **없으면 오류 없이 `user_obj=None`** | — | UAK:2895-2911 |
| 11a | 사용자 `metadata.scim_active == false` | 401 `"Authentication Error, User=<uid> has been deactivated via SCIM. …"` | UAK:2919-2926 |
| 12 | 팀 멤버 예산 | 예산 초과(§5.4) | UAK:2947-3014 |
| 13 | **만료**: `expires < now(UTC)`(naive 면 UTC 로 간주) | 401 `{"error":{"message":"Authentication Error - Expired Key. Key Expiry time <expires> and current time <now>","type":"expired_key","param":"sk-...<4자>","code":"401"}}` | UAK:3016-3034 |
| 14 | 키 예산(LLM API 라우트일 때만), soft budget, 모델별 예산, 사용자 모델 예산 | 예산 초과(§5.4) | UAK:3037-3128 |
| 15 | 팀 객체 조회, 프로젝트, 글로벌 예산 | — | UAK:3131-3213 |
| 16 | 역할 결정 → `UserAPIKeyAuth` 생성 (§5.5) | — | UAK:3247-3253, UAK:4321-4366 |
| 17 | 키 `allowed_routes`(비어 있지 않을 때) | 403 auth_error `"Virtual key is not allowed to call this route. Only allowed to call routes: [...]. Tried to call route: …"` | UAK:4033, RC:111-251 |
| 18 | common_checks: 가격 없는 모델 차단(설정 시) | 403 model_cost_map_missing | AC:1145-1156 |
| 19 | **팀 blocked** | 401 `"Authentication Error, Team=<tid> is blocked. Update via `/team/unblock` if you're an admin."` | AC:1158-1160 |
| 20 | **팀 모델 접근**(키의 access_group 으로 구제 가능) → 팀 멤버별 모델 범위 → (팀 없는 개인 키면) **사용자 모델 접근** → 고객 모델 | 403 `team_model_access_denied` / `user_model_access_denied` | AC:1162-1290 |
| 21 | 팀·프로젝트·사용자·팀멤버·end_user·글로벌 예산 | 예산 초과 | AC:1300-1466 |
| 22 | 조직 RBAC → **라우트 권한(§6)** | 401/403 (D) | AC:1471-1498 |
| 23 | vector store, tool allowlist | 403 | AC:1500-1517 |

- **순서 주의 1**: 모델 접근 검사(10)가 만료 검사(13)보다 먼저다. 만료된 키로 허용되지 않은 모델을 부르면 403 이 먼저 나온다.
- **순서 주의 2**: 라우트 권한(22)은 common_checks 의 끝이다. 그래서 **팀이 막힌 키는 관리 API(`/key/list` 등)도 401** 이다(19 가 먼저).
- 예외는 모두 `UserAPIKeyAuthExceptionHandler` 를 거친다(AEH:137-271). 일반 Exception 은 401 + `"Authentication Error, "` 접두어, HTTPException 은 그 상태 + `type="auth_error"` + `param="None"`(문자열), ProxyException 은 그대로다(AEH:49-82).
- 만료 메시지의 `param` 은 `abbreviate_api_key(api_key)` 다. DB 경로에서는 api_key 가 이미 해시라서 **해시의 끝 4자**가 나오고, 캐시 경로에서는 평문 끝 4자가 나온다(UAK:2790, UAK:3033). 미확인: 의도 여부.

### 5.2 대표 오류 본문 (그대로 맞출 것)

| 상황 | HTTP | `error.type` | `error.message` | `error.param` |
|---|---|---|---|---|
| 키 없음 | 401 | `token_not_found_in_db` | `Authentication Error, Invalid API key provided.` | `"key"` |
| `sk-` 아닌 키(해시 포함) | 401 | `auth_error` | `LiteLLM Virtual Key expected. Received=<앞4>****<뒤4>, expected to start with 'sk-'.` | `"None"` |
| 만료 | 401 | `expired_key` | `Authentication Error - Expired Key. Key Expiry time <py datetime str> and current time <py datetime str>` | `"sk-...<4>"` |
| 키 blocked | 401 | `auth_error` | `Authentication Error, Key is blocked. Update via `/key/unblock` if you're an admin.` | `"None"` |
| 팀 blocked | 401 | `auth_error` | `Authentication Error, Team=<tid> is blocked. Update via `/team/unblock` if you're an admin.` | `"None"` |
| 키 모델 불가 | 403 | `key_model_access_denied` | `The requested model '<m>' is not available for this API key, or the model name is invalid. Check the models available to you and try again.` | `"model"` |
| 팀 모델 불가 | 403 | `team_model_access_denied` | (위와 같은 문구) | `"model"` |
| 사용자 모델 불가(개인 키) | 403 | `user_model_access_denied` | (위와 같은 문구) | `"model"` |
| 키 예산 초과 | **422** (기본값) | `budget_exceeded` | `Budget has been exceeded! Key=<alias 또는 "key">[ (sk-...abcd)] Current cost: <spend>, Max budget: <max>` | `null` |

- `code` 는 문자열이다(T:3829). 본문은 `{"error": {...}}` 다(PS:2156-2174).
- 만료 메시지의 datetime 은 파이썬 `str(datetime)` 형식이다(예: `2026-11-20 10:00:00+00:00`). 대시보드는 앞부분 `Authentication Error - Expired Key` 만 찾는다(NW:440).
- 모델 거부 메시지는 객체 종류와 무관하게 같은 클라이언트 문구다(customer 만 다름, MAD:5-21). 내부 문구 `"<obj> not allowed to access model. This <obj> can only access models=[…]. Tried to access <m>"` 는 로그에만 남는다(AC:4973-4986, T:4037-4056).
- 모델 허용 판정: `models` 가 비어 있으면 전체 허용이다. `"*"`, `all-proxy-models`, 와일드카드 패턴, 모델 액세스 그룹, 팀 alias 도 허용된다(AC:4809-4855). 키 `access_group_ids` 로 구제될 수 있다(AC:5491-5513).

### 5.3 예산 초과 모양 (세부는 범위 밖)

- `litellm.BudgetExceededError` → `ProxyException(type="budget_exceeded", param=None, code=e.status_code)` (AEH:51-57).
- **상태 코드 기본값은 422** 다(`litellm.budget_exceeded_status_code = 422`, INIT:393, EXC:1092). 이 포크의 설정이고, 429 로 되돌릴 수 있다.
- 메시지에 `"Authentication Error, "` 접두어가 붙지 않는다(e.message 그대로).

### 5.4 역할·user_id 결정

- 키 테이블에는 역할 컬럼이 **없다**. 역할은 **키의 `user_id` 로 찾은 사용자 행의 `user_role`** 이다. 없으면 `internal_user` 다: `user_role or get_user_role(user_obj) or INTERNAL_USER` (UAK:4343). 사용자 행 role 이 null 이나 모르는 값이어도 `internal_user` 다(AC:1522-1534). 사용자 행이 proxy_admin 이면 proxy_admin 이다(UAK:4360-4364).
- **팀 키도 역할은 `team` 이 아니다.** `team` 역할은 JWT 전용이다. 팀 키는 소유자 사용자 행의 역할을 따른다. user_id 가 없으면 `internal_user` 다.
- user_id 없는 키(서비스 계정, 관리자가 user_id 없이 만든 키)는 `user_id=null`, `user_role=internal_user`(객체 기준)다. 그러나 **라우트 판정은 `user_obj` 의 역할로 한다**(AC:1549, RC:301-411). 사용자 행이 없으면 역할이 `None` 이라 internal_user 허용 목록에 해당하지 않는다(§6.2 마지막 열). 401 메시지에는 `Your role=unknown` 이 찍힌다(RC:286-297).
- 팀 서비스 계정 판정: `user_id is None and team_id is not None and metadata.service_account_id` 가 있음(UAKT:318-325).
- 마스터 키: `user_id="default_user_id"`, `user_role=proxy_admin` 이다. common_checks 에서는 토큰 역할을 우선해서 가짜 proxy_admin 사용자 객체를 만든다(UAK:3725-3740). 같은 user_id 의 DB 행이 비관리자여도 강등되지 않는다.

### 5.5 캐시

- 키 객체 캐시 키는 **해시 토큰**이다(UAK:2606, AC:3353-3371).
- 인메모리 TTL 은 기본 **60초**(`DEFAULT_MANAGEMENT_OBJECT_IN_MEMORY_CACHE_TTL`, CONST:2017, UKC:403-415)다. `general_settings.user_api_key_cache_ttl` 로 바꾸면 Redis TTL 도 같이 바뀐다(PS:6994-7004).
- 키를 바꾸는 엔드포인트(update, block, unblock, regenerate, reset_spend, delete)는 즉시 캐시를 지운다(KM:4023, 7953, 8067, 6198, 6815, 5557-5561). 그래서 차단·삭제는 그 파드에서 바로 반영된다(멀티 파드 전파는 Redis 설정 의존, 미확인).
- grace 기간 옛 키 조회 결과는 별도 인메모리 캐시에 60초 둔다(PU:4876).
- 팀 멤버십 예산 조회 캐시는 5초다(UAK:2972).

---

## 6. 라우트 단계 권한 (Part 3)

### 6.1 판정 순서 (비 proxy_admin, RC:301-411)

1. `admin_only_routes` 설정 [premium, 적용 안 함] (RC:313-316).
2. LLM API 라우트 → 통과.
3. **info_routes** → 통과(`/key/info`, `/key/list`, `/key/health`, `/v2/key/info`, `/team/info`, `/team/list`, `/v2/team/list`, `/user/info`, `/v2/user/info` 등, T:559-584). 단 `/user/info?user_id=타인` 은 viewer 가 아니면 403 이다(RC:334-344).
4. proxy_admin_viewer → `_check_proxy_admin_viewer_access`(RC:977-1095): GET/HEAD/OPTIONS 는 허용한다. 쓰기는 차단 목록(`/key/generate, /key/update, /key/delete, /key/regenerate, /key/service-account/generate, /key/block, /key/unblock, /key/bulk_update, …`, RC:28-64)과 `/key/*/regenerate`·`/key/*/reset_spend` 접미어(RC:70, RC:1024-1030)면 403 이다. 나머지 POST 는 `admin_viewer_routes` 에 없으면 403 이다(RC:1086-1095).
5. 통과 조건(OR, RC:371-386): (internal_user 이고 `internal_user_routes`) / (org_admin 이고 org_admin 허용 목록) / (internal_user_viewer 이고 `internal_user_view_only_routes`) / **`self_managed_routes`(역할 무관)**.
6. 팀 서비스 계정 키는 `/key/generate`, `/key/update` 를 통과한다(RC:389-394, T:621-624).
7. 그 외 → **401** (D) `"Authentication Error, Only proxy admin can be used to generate, delete, update info for new keys/users/teams. Route=<route>. Your role=<role>. Your user_id=<마스킹>"`, `type=auth_error`, `param="None"` (RC:274-298, RC:396-411, AEH:77-82). 키 `allowed_routes` 에 맞는 것이 있으면 통과한다.

`internal_user_routes` = 일부 activity·tag·guardrail 목록 + spend_tracking_routes + **key_management_routes 전체**(`/key/generate, update, delete, info, regenerate, service-account/generate, /key/{key_id}/regenerate, list, block, unblock, bulk_update, /team/key/bulk_update, /key/{key_id}/reset_spend, /key/aliases, /team/daily/activity…, /spend/logs…`) + compliance (T:594-619, T:788-816).
`internal_user_view_only_routes` 에는 **키 관리 라우트가 하나도 없다**(spend_tracking·compliance·tag 만, T:818-832).

### 6.2 라우트 × 역할 표

○ = 라우트 단계 통과(핸들러 권한 없음), H = 라우트 통과 후 **핸들러가 판정**(해당 절 참고), 401 = 라우트 단계 (D) 401, 403 = viewer 쓰기 차단 403. proxy_admin(마스터 키 포함)은 모든 행이 ○ 이다.

| 라우트 | proxy_admin_viewer | internal_user | internal_user_viewer | 사용자 행 없는 키 (팀 SA / user_id 없음) |
|---|---|---|---|---|
| `POST /key/generate` | 403 | H (§3.1) | **401** | SA: H / 그 외 401 |
| `POST /key/service-account/generate` | 403 | H (§3.2) | 401 | 401 |
| `POST /key/update` | 403 | H (§3.3) | 401 | SA: H / 그 외 401 |
| `POST /key/delete` | 403 | H (§3.4) | 401 | 401 |
| `GET /key/info` | ○ → H(viewer 는 전부 열람) | H | H | H |
| `GET /key/list` | ○ (전체) | H (§3.6 범위) | H (§3.6 범위) | H → user_id 없으면 403 (KM:6850-6856) |
| `GET /key/aliases` | ○ (GET 허용, 전체) | H (범위) | **401** | 401 |
| `POST /key/block`·`/key/unblock` | 403 | H (팀/조직 admin 만) | 401 | 401 |
| `POST /key/{k}/regenerate` | 403 | H [premium] | 401 | 401 |
| `POST /key/{k}/reset_spend` | 403 | H (proxy/팀 admin 만 → 사실상 팀 admin) | 401 | 401 |
| `POST /team/new` | 403 | 401 | 401 | 401 |
| `POST /team/update` | 403 | H (H 문서 §3.2) | H | H |
| `POST /team/delete`·`/team/block`·`/team/unblock` | 403 | 401 | 401 | 401 |
| `POST /team/member_add`·`member_delete`·`member_update` | 403 (POST 기본 거부) | H | H | H |
| `GET /team/info`·`/team/list`·`/v2/team/list` | ○ | H | H | H |
| `POST /user/new`·`/user/delete` | 403 | 401 | 401 | 401 |
| `POST /user/update` | `user_email` 만 허용, 그 외 403 | 401 | 401 | 401 |
| `GET /user/info` | ○ | 본인만(타인 403) | 본인만 | user_id 없음 → 타인 지정 시 403 |
| `GET /v2/user/info` | ○ | H | H | H |
| `GET /user/list` | ○ | H → 403 | H → 403 | H → 403 |

근거: info_routes T:559-584, management_routes T:626-684, self_managed_routes T:834-927(`/team/update`·`member_*` T:841-846, `/user/list` T:892), admin_viewer_routes T:958-, viewer 차단 RC:28-70·RC:950-972·RC:1013-1095, 팀·사용자 핸들러 판정은 `H_team_user_contract.md` §3·§4.

- org_admin(조직 멤버십 역할)은 `org_admin_allowed_routes` 로 management_routes 를 통과한다(H 문서 §2.4). 조직은 이 문서 범위 밖이다.
- `UserAPIKeyAuth.user_role` 이 핸들러에서 `internal_user` 로 보이더라도, 사용자 행이 없으면 라우트 단계 역할은 None 이다(§5.4). 그래서 같은 키가 핸들러 기준으로는 internal_user 인데 라우트 단계에서 401 이 날 수 있다.

### 6.3 키 관리에서 역할별로 실제 할 수 있는 일 (핸들러까지 합친 결과)

| 동작 | internal_user (자기 개인 키) | internal_user (남의 키) | 팀 admin (자기 팀 키) | 팀 일반 멤버 (자기 팀 키, 기본 권한) |
|---|---|---|---|---|
| 생성 | ○ (user_id 자동=본인) | 403 | ○ (멤버 명의는 설정 필요) | **401** team_member_permission_error |
| 조회 info | ○ | 403 | ○ | ○ (`/key/info` 는 기본 권한) |
| list | 본인·본인 작성 | × | 팀 전체 | 본인 키 + 팀 서비스 계정 키 |
| update | 본인이 **작성**한 키만 ○, 관리자가 만들어 준 키는 403 | 403 | ○ | **401** |
| delete | ○ | 403 | ○ | ○ (소유자면. 팀 권한 검사 없음) |
| block/unblock | **403** | 403 | ○ | 403 |
| regenerate | ○ [premium] | 403 | ○ | 401 |
| reset_spend | **403** | 403 | ○ | 403 |

---

## 7. 미확인 항목

- `validate_team_id_used_in_service_account_request`(KM:1285-1318)의 정확한 오류 문구.
- `/key/block` 응답이 반환 주석 `LiteLLM_VerificationToken` 으로 직렬화될 때 `organization_id`→`org_id` 누락이 실제로 일어나는지(FastAPI 버전 동작).
- `GenerateKeyResponse` 직렬화에서 `expires` 등 datetime 형식(`Z` 접미어 여부). pydantic v2 기본값으로 추정한다.
- 팀 멤버 권한 오류 메시지의 `{route}` 가 `/key/generate` 로 찍히는지 `KeyManagementRoutes.KEY_GENERATE` 로 찍히는지는 파이썬 버전에 따라 다르다(str-mixin Enum 의 `format`). 대시보드는 두 문자열을 모두 찾는다(UI/components/organisms/utils.ts:17).
- `user_belongs_to_keys_team`(TMP)의 정확한 조건(roster 기준인지 사용자 `teams` 배열 기준인지).
- 키 인증 시 DB 에서 읽는 조인 View 의 정확한 SQL(PU `get_data` 의 `sql_query`).
- 대시보드가 premium 여부(`premiumUser`)를 어느 응답에서 받는지. regenerate 버튼 활성화에 필요하다.
- duration 파서가 뒤에 붙은 문자를 무시하는 것(`"30dx"`)이 의도인지.
