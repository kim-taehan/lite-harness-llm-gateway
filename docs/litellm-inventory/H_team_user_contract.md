# H. 팀·사용자 관리 API HTTP 계약 (litellm 원본 기준)

수정하지 않은 litellm 대시보드가 우리 서버를 그대로 호출한다. 그래서 이 문서는 litellm 이 **실제로** 주고받는 모양을 적는다. 문서에 적힌 내용이 아니라 코드 동작을 따랐다.
근거는 모두 `파일:줄`로 달았다. 코드로 확인하지 못한 것은 "미확인"으로 적었다.

## 0. 약어 (모든 경로는 `<workspace>/` 기준)

| 약어 | 파일 |
|---|---|
| TE | `gateway/litellm/proxy/management_endpoints/team_endpoints.py` |
| IU | `gateway/litellm/proxy/management_endpoints/internal_user_endpoints.py` |
| KM | `gateway/litellm/proxy/management_endpoints/key_management_endpoints.py` |
| T | `gateway/litellm/proxy/_types.py` |
| MT | `gateway/litellm/models/team.py` |
| MU | `gateway/litellm/models/user.py` |
| TT | `gateway/litellm/types/proxy/management_endpoints/team_endpoints.py` |
| UR | `gateway/litellm/types/proxy/management_endpoints/user_requests.py` |
| UL | `gateway/litellm/types/proxy/management_endpoints/internal_user_endpoints.py` |
| RB | `gateway/litellm/types/proxy/management_endpoints/request_base.py` |
| ROLES | `gateway/litellm/types/proxy/auth/user_roles.py` |
| MH | `gateway/litellm/proxy/management_helpers/utils.py` |
| BUD | `gateway/litellm/proxy/management_helpers/bulk_user_deletion.py` |
| AZ | `gateway/litellm/proxy/management/teams/authz.py` |
| RC | `gateway/litellm/proxy/auth/route_checks.py` |
| AC | `gateway/litellm/proxy/auth/auth_checks.py` |
| AEH | `gateway/litellm/proxy/auth/auth_exception_handler.py` |
| PS | `gateway/litellm/proxy/proxy_server.py` |
| PU | `gateway/litellm/proxy/utils.py` |
| SP | `gateway/schema.prisma` (`gateway/litellm/proxy/schema.prisma` 와 같음) |
| NW | `gateway/ui/litellm-dashboard/src/components/networking.tsx` |
| CL | `gateway/ui/litellm-dashboard/src/lib/http/client.ts` |
| SD | `gateway/ui/litellm-dashboard/src/lib/http/schema.d.ts` |
| UI/… | `gateway/ui/litellm-dashboard/src/…` |

---

## 1. 요약 표

| 엔드포인트 | 대시보드 호출 위치 | 성공 응답 최상위 모양 | 요청 경로(route) 단계 허용 역할 | 핸들러 단계 권한 | 오류 본문 모양 |
|---|---|---|---|---|---|
| `POST /team/new` | NW:2590 | 팀 행 객체 `LiteLLM_TeamTable`(TE:1491) | proxy_admin, org_admin | 조직 검사 | `{"error":{…}}` (TE:1964) |
| `POST /team/update` | NW:2812 | `{"team_id", "data": <팀 행>}` (TE:2777) | proxy_admin + self_managed(누구나 통과) | proxy/org/team admin (TE:2428-2430) | `{"error":{…}}` (TE:2779) |
| `POST /team/delete` | NW:1098 | `{"deleted_teams": [ids]}` (TE:4607) | proxy_admin, org_admin | TEAM_OR_ORG_ADMIN (TE:4594) | `{"detail": …}` (래핑 없음) |
| `POST /team/block` | 호출 안 함 | 팀 행 객체 (TE:5568) | proxy_admin, org_admin | TEAM_OR_ORG_ADMIN | `{"detail": …}` |
| `POST /team/unblock` | 호출 안 함 | 팀 행 객체 (TE:5568) | proxy_admin, org_admin | TEAM_OR_ORG_ADMIN | `{"detail": …}` |
| `POST /team/member_add` | NW:2899 | 팀 행 + `updated_users` + `updated_team_memberships` (T:4170) | self_managed(누구나 통과) | TEAM_OR_ORG_ADMIN 또는 가입 가능 팀 셀프 가입 (TE:3011) | 대부분 `{"detail":{"error"}}`, 중복은 `{"error":{…}}` |
| `POST /team/member_delete` | NW:3079 | 갱신된 팀 행 (TE:3807) | self_managed | TEAM_OR_ORG_ADMIN (TE:3843) | `{"detail":{"error"}}` |
| `POST /team/member_update` | NW:3002 | `TeamMemberUpdateResponse` 평면 객체 (TE:4135) | self_managed | TEAM_OR_ORG_ADMIN (TE:4044) | `{"detail":…}` |
| `GET /team/info` | NW:1187 | `{"team_id","team_info","keys","team_memberships"}` (T:4392) | info_route(누구나 통과) | 멤버·org admin·admin 계열 (TE:validate_membership) | `{"error":{"type":"auth_error"…}}` (TE:5394-5408) |
| `GET /team/list` | NW:1208 | **맨 배열** `[TeamListResponseObject…]` (TE:6334) | info_route(누구나 통과) | admin 계열 전체, org admin 은 자기 조직, 나머지는 `user_id=본인` 만 | `{"detail":{"error"}}` |
| `POST /user/new` | NW:1049 | `NewUserResponse` 평면 객체(`key` 포함) (IU:467) | proxy_admin, org_admin | 관리자 역할 생성은 proxy_admin 만 | `{"error":{…}}` (IU:667) |
| `POST /user/update` | NW:3179 | `{"user_id", "data": <사용자 행>}` (PU:6206) | proxy_admin, org_admin(+viewer 는 user_email 만) | 본인 또는 proxy_admin | `{"error":{"type":"auth_error"…}}` (IU:1732-1746) |
| `POST /user/delete` | NW:1089 | **정수**(삭제된 사용자 수) (IU:2557, BUD:618) | proxy_admin, org_admin | proxy_admin, 또는 대상 조직의 org_admin | `{"detail":{"error"}}` |
| `GET /user/info` | **호출 안 함**(`/v2/user/info` 를 씀, NW:1178) | `{"user_id","user_info","keys","teams"}` (T:3165) | info_route(본인 또는 viewer, RC:334-344) | admin 계열 또는 본인 (IU:758-784) | `{"error":{…}}` (IU:1000 `handle_exception_on_proxy`) |
| `GET /user/list` | NW:1122 | `{"users","total","page","page_size","total_pages"}` (IU:2373-2379) | self_managed + admin_viewer | admin 계열 전체, org admin 은 자기 조직 (IU:2128-2188) | `{"detail":{"error"}}` |

> 실제 팀 목록 화면은 `/team/list` 가 아니라 **`GET /v2/team/list`**(페이지 응답 `{teams,total,page,page_size,total_pages}`, TT:99-106)를 부른다. 위치는 UI/app/(dashboard)/hooks/teams/useTeams.ts:77,256 이다. 사용자 상세는 **`GET /v2/user/info`**(NW:1178)를 부른다. 두 엔드포인트는 이 문서 범위 밖이지만 같은 화면에 필요하므로 따로 정리해야 한다.

---

## 2. 공통 사항

### 2.1 역할 문자열 (정확한 철자)

`LitellmUserRoles` (ROLES:5-42):

| 상수 | 값 | UI 라벨(ROLES:66-75) |
|---|---|---|
| PROXY_ADMIN | `proxy_admin` | Admin (All Permissions) |
| PROXY_ADMIN_VIEW_ONLY | `proxy_admin_viewer` | Admin (View Only) |
| ORG_ADMIN | `org_admin` | — (사용자 행이 아니라 조직 멤버십에만 붙는다, IU:2443-2452) |
| INTERNAL_USER | `internal_user` | Internal User (Create/Delete/View) |
| INTERNAL_USER_VIEW_ONLY | `internal_user_viewer` | Internal User (View Only) |
| TEAM | `team` | JWT 전용 |
| CUSTOMER | `customer` | — |

- 사용자 행의 `user_role` 이 null 이거나 모르는 값이면 `internal_user` 로 취급한다(AC:1520-1524).
- `/user/new`·`/user/update` 의 `user_role` 에는 앞의 네 값(proxy_admin, proxy_admin_viewer, internal_user, internal_user_viewer)만 들어갈 수 있다(UR:21-30, UR:55-63). 다른 값은 422 가 된다.
- 팀 안 역할은 `"admin"` 과 `"user"` 두 가지뿐이다(MT:39-42).

### 2.2 Member 객체

```json
{"user_id": "string|null", "user_email": "string|null", "role": "admin"|"user"}
```
- 필드 정의는 MT:19-42 이고 대시보드 생성 타입은 SD:38174-38178 이다. `user_id`·`user_email` 둘 다 null 이면 검증 오류(422)다(MT:29-36).
- `members_with_roles` 는 팀 행의 **JSON 컬럼 하나**에 Member 배열로 저장된다. 각 원소는 `model_dump()` 결과 `{user_id, user_email, role}` 이다(TE:3249-3252). DB 기본값은 `"{}"`(빈 객체, SP:188)이고, 읽을 때 빈 dict 는 `[]` 로 바뀐다(MT:122-123).
- 누락된 식별자는 추가할 때 채운다. 이메일로 추가하면 user_id 가, user_id 로 추가하면 이메일이 기록된다(TE:3130-3159). 이 roster 값은 스냅샷이라 나중에 사용자 행이 바뀌어도 갱신되지 않는다(TE:3133).
- `/team/info` 응답의 멤버에는 `user_alias` 가 더 붙는다(`TeamInfoMember`, T:4338-4339). 비어 있는 `user_email` 은 사용자 테이블에서 채운다(TE:5155-5175).
- 레거시 컬럼 `admins: String[]`, `members: String[]` 도 남아 있다(SP:186-187, MT:67-68). 응답에는 `[]` 로 나간다.
- 팀별 예산·사용액은 Member 가 아니라 `LiteLLM_TeamMembership`(user_id, team_id, spend, total_spend, budget_id, SP:801-810) 테이블에 따로 둔다.

### 2.3 오류 본문 모양 (중요: 엔드포인트마다 다르다)

litellm 에서는 세 가지 모양이 섞여 나온다.

**(A) ProxyException 형태**: 핸들러가 `handle_exception_on_proxy` 나 `ProxyException` 으로 감싼 경우다.
```json
{"error": {"message": "...", "type": "...", "param": null|"None"|"...", "code": "400"}}
```
- 본문을 만드는 곳은 PS:2156-2174 이고 `to_dict` 는 T:3844-3854 다. **`code` 는 문자열**이다(T:3829). HTTP 상태 코드는 `int(code)` 다(PS:2162).
- `handle_exception_on_proxy` 는 HTTPException 을 `type="internal_server_error"` 로 바꾸고, `message=str(detail)`, `param=null` 로 둔다(PU:9052-9059). 상태 코드가 400 이든 404 든 403 이든 type 은 `internal_server_error` 다.
- detail 이 dict 이면 message 는 **파이썬 dict repr 문자열**이 된다. 예: `"{'error': 'Team id = x already exists. Please use a different team id.'}"` (T:3821 `str(message)`). 대시보드는 이 문자열에서 메시지를 다시 꺼낸다(CL:83-104 `unwrapProxyErrorMessage`, 정규식 `^\{'error':\s*(['"])…`). 그래서 우리 서버도 **같은 repr 문자열 형식을 내야** 대시보드가 메시지를 제대로 보여 준다.
- `/team/info`·`/user/update` 는 자체적으로 `type="auth_error"`, `param="None"`(**문자열 "None"**)으로 감싼다(TE:5394-5408, IU:1732-1746). 예외가 HTTPException 이 아니면 message 앞에 `"Authentication Error, "` 가 붙고 상태는 400 이다.
- 선택 설정 `include_call_id_in_error_body` 가 켜져 있으면 `error.litellm_call_id` 가 더 붙는다(`gateway/litellm/proxy/common_utils/error_body_call_id.py:11-20`). 기본값은 꺼짐이다.

**(B) FastAPI HTTPException 기본 형태**: 핸들러가 감싸지 않고 그대로 던진 경우다(PS:2188-2196 → starlette 기본 처리기).
```json
{"detail": {"error": "..."}}      // detail 이 dict 일 때
{"detail": "..."}                  // detail 이 문자열일 때
```
- 대시보드의 member_add·member_update 호출은 `parsedError?.detail?.error` 를 읽는다(NW:2924, NW:3059). TeamInfo 화면은 `error.raw.detail.error`(member_add)와 `error.raw.detail`(member_update, 문자열)을 검사한다(UI/components/team/TeamInfo.tsx:847, 893).

**(C) 요청 검증 오류(422)**: `{"detail": [ …pydantic 오류 배열… ]}` (PS:2359-2371). 필수 필드가 빠지면(예: `/team/update` 의 `team_id`, T:1957) 핸들러의 400 검사까지 가지 않고 여기서 422 가 난다.

**(D) 인증·라우트 단계 거부**: 핸들러에 들어가기 전에 `user_api_key_auth` 가 거부하는 경우다. 항상 (A) 형태이고 `type="auth_error"` 다(AEH:49-82).
- 일반 예외(예: 관리자 전용 라우트 거부)는 **401**, `message = "Authentication Error, Only proxy admin can be used to generate, delete, update info for new keys/users/teams. Route=<route>. Your role=<role>. Your user_id=<마스킹>"`, `param="None"` 이다(RC:275-298, AEH:77-82).
- HTTPException(예: viewer 쓰기 거부 403)은 그 상태 코드를 그대로 쓰고 message 는 detail 문자열이다(AEH:66-72).

대시보드 공통 파서(CL:71-80)는 `error.message` → `error`(문자열) → `message` → `detail` 순서로 메시지를 찾는다. 그래서 어떤 형태든 화면 표시는 된다. 다만 위 특수 검사(`detail.error`, repr 문자열 풀기)에 맞춰야 메시지가 원문 그대로 나온다.

### 2.4 권한 판정 두 단계

1. **라우트 단계**(RC:301-411): proxy_admin 이 아니면 아래 순서로 검사한다.
   - info_route(`/team/info`, `/team/list`, `/user/info` 등, T:559-584)는 통과한다. 단 `/user/info?user_id=타인`은 viewer 가 아니면 403 이다(RC:334-344).
   - `proxy_admin_viewer` 는 GET 은 모두 허용하고 쓰기는 차단한다(RC:977-1095). 차단 목록에 `/user/new`, `/user/delete`, `/team/new`, `/team/update`, `/team/delete`, `/team/block`, `/team/unblock` 이 있다(RC:28-64, RC:950-965). 목록에 없는 POST(예: `/team/member_add`)도 기본 거부다(RC:1086-1095). `/user/update` 는 본문에 `user_email` 만 있을 때 허용된다(RC:1042-1054).
   - 조직 admin(조직 멤버십 역할 `org_admin`)은 `management_routes` 전체를 통과한다(T:1042, T:626-680).
   - **self_managed_routes**(`/team/update`, `/team/member_add`, `/team/member_delete`, `/team/member_update`, `/user/list` 등, T:834-927)는 역할과 관계없이 통과하고 권한은 핸들러가 판정한다.
   - `internal_user` 가 통과하는 `internal_user_routes`(T:788-816)에는 이 문서의 팀·사용자 쓰기 라우트가 **하나도 없다**. 그래서 internal_user 가 `/team/new`, `/team/delete`, `/team/block`, `/user/new`, `/user/update`, `/user/delete` 를 부르면 **401 (D)** 이 난다(RC:405-411).
2. **핸들러 단계**: 팀은 `TeamAccess`(AZ:22-44)로 판정한다. `TEAM_OR_ORG_ADMIN` = proxy_admin, 그 팀의 admin 멤버, 팀이 속한 조직의 org_admin 이다(AZ:14). 팀 admin 은 `members_with_roles` 에 `role=="admin"` 이고 `user_id` 가 호출자와 같은 항목이 있는 사람이다(AZ:52-56).

### 2.5 premium(유료) 검사 지점 — 우리는 강제하지 않는다. 기록만 남긴다

| 위치 | 내용 |
|---|---|
| TE:1661-1667 | `/team/new` 라이선스 팀 수 초과 시 403 `"License is over limit…"` |
| TE:1631-1635, TE:2407-2411 (`common_utils.py:81-95`) | 팀 `model_max_budget`·`team_member_model_max_budget` 설정은 enterprise 전용(400) |
| TE:2904-2917, TE:2935-2940 | `/team/member_add` 에서 `role="admin"` 지정은 premium(400 `{"detail":{"error":"Assigning team admins is a premium feature. …"}}`) |
| TE:4018-4023 | `/team/member_update` 에서 `role="admin"` 은 premium(400, **detail 이 문자열**) |
| TE:4031 | `/team/member_update` 의 `model_max_budget` 은 premium |
| `management_helpers/team_metadata_validation.py:166-187` | 팀 메타데이터 스키마 검증은 premium 여부를 참조 |
| IU:554-560 | `/user/new` 라이선스 사용자 수 초과 시 403 |
| RC:413-420 | `admin_only_routes` 설정은 enterprise 전용 |

대시보드는 premium 오류 문구를 직접 찾아본다(TeamInfo.tsx:847, 893). 우리는 이 오류를 내지 않으므로 이 분기는 실행되지 않는다.

---

## 3. 팀 엔드포인트

### 3.1 `POST /team/new` (TE:1487-1964)

**요청 본문** `NewTeamRequest`(T:1881-1929, 기반 TeamBase MT:63-83)

| 필드 | 타입 | 기본값 | 대시보드 전송 |
|---|---|---|---|
| team_alias | str\|null | null | ○ (UI/components/Teams.tsx:735) |
| team_id | str\|null | null(빈 문자열도 null 로 취급, T:1925-1931) | ○ (Teams.tsx:892) |
| organization_id | str\|null | null(없으면 `default_team_params.organization_id`, TE:1689-1692) | ○ (Teams.tsx:749) |
| members_with_roles | Member[] | [] | × |
| admins, members | str[] | [] | × (레거시) |
| team_member_permissions | str[]\|null | null | × |
| metadata | dict\|null | null | ○ 문자열을 JSON.parse 해서 보냄 (NW:2584-2589, Teams.tsx:870) |
| tpm_limit, rpm_limit, tpd_limit | int\|null | null | ○ (Teams.tsx:843,848,855) |
| max_budget, soft_budget | float\|null | null | max_budget ○ (Teams.tsx:819) |
| budget_duration | str\|null | null | ○ (Teams.tsx:824) |
| budget_limits | BudgetLimitEntry[]\|null | null | × |
| models | str[] | [] | ○ (Teams.tsx:561 `normalizeTeamModelSelection`) |
| blocked | bool | false | × |
| router_settings | dict\|null | null | ○ 값이 있을 때만 (Teams.tsx:544-552) |
| access_group_ids | str[]\|null | null | ○ (Teams.tsx:1072) |
| default_team_member_models | str[]\|null | null | × |
| model_aliases | dict\|null | null | ○ 있을 때만 (Teams.tsx:536-538) |
| model_max_budget | dict\|null | null | ○ 있을 때만 (Teams.tsx:540-542) [premium] |
| tags, guardrails, policies, prompts | list\|null | null | guardrails·policies ○ (Teams.tsx:988,1050) [extra] |
| object_permission | {vector_stores, mcp_servers, mcp_access_groups, mcp_toolsets, mcp_tool_permissions, agents, agent_access_groups, search_tools, skills…} | null | ○ (Teams.tsx:466-532) [extra] |
| allowed_passthrough_routes, denied_passthrough_routes | list\|null | null | allowed ○ (Teams.tsx:1109) [extra] |
| disable_global_guardrails, require_trace_id | bool\|null | null | ○ (Teams.tsx:1010,1035, 556-559) [extra] |
| secret_manager_settings | dict\|null | null | ○ (Teams.tsx:967) [extra] |
| model_rpm_limit, model_tpm_limit, mcp_rpm_limit | dict\|null | null | × [extra] |
| rpm_limit_type, tpm_limit_type | "guaranteed_throughput"\|"best_effort_throughput"\|null | null | × [extra] |
| default_estimated_output_tokens(_per_model) | 양의 정수 / map | null | × [extra] |
| team_member_budget, team_member_rpm_limit, team_member_tpm_limit, team_member_key_duration, team_member_budget_duration | | null | budget·key_duration·rpm·tpm ○ (Teams.tsx:900,931,943,955) |
| team_member_model_max_budget | dict\|null | null | ○ 있을 때만 (Teams.tsx:557,560) [premium] |
| allowed_vector_store_indexes, enforced_*_expires_after | | null | × [extra] |

헤더 `litellm-changed-by`(선택)는 감사 로그에만 쓴다(TE:1498-1501).

**성공 응답**: 200, `response_model=LiteLLM_TeamTable`(TE:1491)이라 **팀 행 전체**를 돌려준다(TE:1959-1962).
```json
{"team_alias","team_id","organization_id","admins":[],"members":[],"members_with_roles":[Member…],
 "team_member_permissions","metadata","tpm_limit","rpm_limit","tpd_limit","max_budget","soft_budget",
 "budget_duration","budget_limits","models":[],"blocked":false,"router_settings","access_group_ids",
 "default_team_member_models","spend","max_parallel_requests","budget_reset_at","model_id","model_spend",
 "model_max_budget","policies","allow_team_guardrail_config","litellm_model_table","object_permission":null,
 "object_permission_id","updated_at","created_at"}
```
(필드 목록은 MT:63-101.)
- `members_with_roles` 는 멤버 추가가 끝난 뒤의 값이다. 행은 빈 roster 로 먼저 만들고(TE:1888-1891, TE:1908-1914), 그다음 `_add_team_members_to_team` 이 같은 객체의 `members_with_roles` 를 제자리에서 바꾼다(TE:3231, TE:3184-3186).
- `litellm_model_table` 은 포함되고(TE:475 include), `object_permission` 은 include 하지 않으므로 null 이다.
- 대시보드는 이 응답을 읽지 않는다. 성공하면 toast 를 띄우고 목록을 다시 불러온다(Teams.tsx:558-565).

**오류** (모두 (A) 형태, `type="internal_server_error"`, TE:1964)

| 상황 | 상태 | message(repr) | 근거 |
|---|---|---|---|
| max_budget·team_member_budget·soft_budget 이 음수나 비유한수 | 400 | `{'error': 'max_budget must be a non-negative finite number. Received: …'}` | TE:1608-1628 |
| soft_budget >= max_budget | 400 | `{'error': 'soft_budget (…) must be strictly lower than max_budget (…)'}` | TE:1638-1646 |
| team_id 가 `litellm-dashboard`(UI_TEAM_ID, T:4634) | 400 | reserved 안내 | TE:1672-1678 |
| team_id 중복 | 400 | `{'error': 'Team id = <id> already exists. Please use a different team id.'}` | TE:1680-1687 |
| organization_id 가 없는 조직 | 400 | `Organization not found for organization_id=…`(detail 문자열) | TE:1734-1738 |
| **team_alias 중복** | — | **검사하지 않는다**. 같은 별칭으로 여러 팀을 만들 수 있다(TE 안에 별칭 중복 검사 없음. SP:219 도 unique 가 아닌 index) | TE:1487-1964 |
| 권한 없음 | 401 (D) | 2.4 참고 | RC:405-411 |
| 라이선스 초과 | 403 | [premium] | TE:1661-1667 |

**권한**: 라우트 단계에서 proxy_admin 과 org_admin 만 통과한다(`/team/new` 는 management_routes 소속, self_managed 아님, T:646). org_admin 은 조직 멤버십이 있으면 `organization_id` 를 반드시 보내야 하고, 그 조직에서 org_admin 이어야 한다(`gateway/litellm/proxy/auth/auth_checks_organization.py:85-108`, 401). proxy_admin_viewer 는 403 이다(RC:37).

**부수 효과**
- team_id 를 보내지 않으면 `str(uuid.uuid4())` 로 만든다(TE:1669-1670).
- **호출자를 admin 으로 자동 추가한다.** 호출 키에 user_id 가 있고 roster 에 그 사람이 없으면 `Member(role="admin", user_id=호출자)` 를 덧붙인다(TE:1758-1764). proxy_admin 도 마찬가지다. `general_settings.disable_auto_add_proxy_admin_to_teams=true` 일 때만 빠진다(TE:1475-1483).
- roster 의 각 멤버마다 `add_new_member` 가 돈다(TE:1918-1929). 사용자 행이 없으면 만들고, 사용자 `teams` 배열에 team_id 를 넣고, TeamMembership 행을 upsert 한다(3.6 부수 효과와 같음).
- `budget_duration` 이 있으면 `budget_reset_at` 을 계산한다(TE:1868-1874). team_member_* 필드가 있으면 팀 기본 멤버 예산 행을 만들고 그 id 를 `metadata.team_member_budget_id` 에 둔다(TE:1824-1843) [extra].
- `model_aliases` 가 있으면 `LiteLLM_ModelTable` 행을 만들고 `model_id` 로 연결한다(TE:1797-1808).
- `default_team_params`(max_budget, budget_duration, tpm_limit, rpm_limit, team_member_permissions)를 null 필드에 채운다(TE:1696-1709) [설정 의존].

### 3.2 `POST /team/update` (TE:2280-2779)

**요청 본문** `UpdateTeamRequest`(T:1939-2003). **`team_id: str` 는 필수**이고 빠지면 422 다(T:1957). 나머지는 모두 선택이고 기본값 null 이다. 필드는 NewTeamRequest 와 거의 같고 `blocked: bool|null` 이 더 있다.
- 서버는 **보낸 필드만** 바꾼다(`data.json(exclude_unset=True)`, TE:2600 부근). 즉 명시적 null 은 "null 로 지운다"는 뜻이고, 필드를 빼면 "그대로 둔다"는 뜻이다.
- `organization_id: ""` 는 조직 해제다(TE:2559-2561).
- `model_max_budget: null` 은 `{}` 로 저장한다(TE:2603-2604 부근).
- `models` 등 배열은 통째로 바꾼다.
- 대시보드는 폼 값을 그대로 펼쳐 보낸다(NW:2812-2823). 편집 화면 TeamInfo.tsx 는 metadata·secret_manager_settings 등을 가공해서 보낸다(TeamInfo.tsx:983-1000 이후). 팀 admin 설정 저장 때는 `{team_id, …변경분}` 만 보낸다(TeamInfo.tsx:969).

**성공 응답**: 200, response_model 이 없다.
```json
{"team_id": "<id>", "data": <갱신된 Prisma 팀 행 전체>}
```
(TE:2777.) `data` 는 Prisma 행이라 DB 컬럼명 그대로 직렬화된다. `members_with_roles` 는 JSON 배열, `litellm_model_table`·`object_permission` 은 include 결과다(TE:1385-1390 `_TEAM_UPDATE_INCLUDE`). 대시보드는 `{data: Team; team_id}` 로 캐스팅만 하고 실제로는 읽지 않는다(NW:2833, TeamInfo.tsx:943-948).

**오류** ((A) 형태, TE:2779)

| 상황 | 상태 | 근거 |
|---|---|---|
| 팀 없음, 호출자가 proxy_admin | 404 `{'error': 'Team not found, passed team_id=…'}` | TE:2417-2425 |
| 팀 없음, 호출자가 proxy_admin 아님 | **403** `You do not have access to this team` (존재 여부를 숨긴다) | TE:2418-2421, AZ:60-61 |
| 권한 없음 | 403 `You do not have access to this team` | TE:2428-2430 |
| 팀 admin 이 허용되지 않은 필드를 수정 | 403 류 (`team_admin_request_or_raise`) — 정확한 문구는 미확인 | TE:2431-2449 |
| 다른 조직으로 옮기는데 대상 조직 admin 이 아님 | 403 `{'error': 'Relocating a team to a different organization requires PROXY_ADMIN or org-admin of the destination org.'}` | TE:2516-2550 |
| soft/max budget 대소 위반 | 400 | TE:2493-2514 |
| 쓰기 결과가 없음 | 400 `{'error': "Team doesn't exist. Got=…"}` | TE:2749-2753 |

**권한**: 라우트 단계는 self_managed 라 누구나 통과한다(T:841). 핸들러에서 proxy_admin, org_admin(그 팀의 조직), team_admin 을 판정한다(TE:2428). 팀 admin 은 `team_admin_editable_team_fields` 설정에 든 필드만 고칠 수 있다(TE:2431-2449) [extra]. viewer 는 라우트 단계에서 403 이다(RC:960).

**부수 효과**
- 팀 캐시를 갱신한다(TE:2761-2765).
- `budget_duration` 이 바뀌면 `budget_reset_at` 을 다시 계산한다(`_set_budget_reset_at`, TE:2631 부근).
- `blocked` 도 이 경로로 바꿀 수 있다(T:1967).

### 3.3 `POST /team/delete` (TE:4545-4608)

**요청 본문** `DeleteTeamRequest`: `{"team_ids": [str]}` 필수(T:2034-2041). 중복 id 는 하나로 합친다(T:2037-2041). 대시보드는 `{team_ids: [teamID]}` 를 보낸다(NW:1098).

**성공 응답**: 200 `{"deleted_teams": ["<요청한 id들>"]}` (TE:4607-4608, TE:383-384). 대시보드는 읽지 않는다(Teams.tsx:386-388).

**오류**: 핸들러가 감싸지 않으므로 (B) 형태다.

| 상황 | 상태·본문 | 근거 |
|---|---|---|
| 팀 하나라도 없음 | 404 `{"detail":{"error":"Team not found, passed team_id=<id>"}}` (전체 거부) | TE:4580-4590 |
| 권한 없음 | 403 `{"detail":"You do not have access to this team"}` | TE:4594-4595 |
| 라우트 단계 거부 | 401 (D) | RC:405-411 |

**권한**: 라우트 단계에서 proxy_admin 과 org_admin 만 통과한다(management_routes, T:649). 핸들러는 TEAM_OR_ORG_ADMIN 이다(TE:4594). 결국 **팀 admin 인 internal_user 는 라우트 단계에서 막힌다.** viewer 는 403 이다(RC:41).

**부수 효과 — 팀은 하드 삭제, 감사 기록은 남긴다**
- 팀의 **모든 키**를 `LiteLLM_DeletedVerificationToken` 에 기록한 뒤 삭제한다(TE:4634-4648).
- 팀 BYOK 모델을 삭제한다(TE:4665-4675).
- 팀 행 내용을 `LiteLLM_DeletedTeamTable` 에 기록한 뒤 `LiteLLM_TeamTable` 에서 삭제한다(TE:4838-4855 `_delete_locked_teams_tx`: `_persist_deleted_team_records` → `delete_many_in`).
- 모든 사용자의 `teams` 배열에서 team_id 를 빼고, 해당 TeamMembership 행을 삭제한다(`_sweep_deleted_team_references_tx`).
- `LiteLLM_DeletedTeamTable` 은 팀 필드 + `id, deleted_at, deleted_by, deleted_by_api_key, litellm_changed_by` 다(MT:140-149).

### 3.4 `POST /team/block` / 3.5 `POST /team/unblock` (TE:5571-5626)

**요청 본문** `BlockTeamRequest`: `{"team_id": str}` 필수(T:2044-2045). 대시보드는 **호출하지 않는다**(NW 에 호출 없음). 대신 `/team/update` 의 `blocked` 필드로 같은 일을 할 수 있다.

**성공 응답**: 200, 갱신된 Prisma 팀 행이다. include 관계(`litellm_model_table`, `object_permission`)는 null 로 비운다(TE:5568). `blocked` 는 true 또는 false 다.

**오류** ((B) 형태)
- 팀 없음: 404 `{"detail":{"error":"Team not found, passed team_id=<id>"}}` (TE:5540-5544).
- 권한 없음: 403 `{"detail":"You do not have access to this team"}` (TE:5546-5549).

**권한**: 라우트 단계에서 proxy_admin 과 org_admin 만 통과한다(management_routes, T:653-654). 핸들러는 TEAM_OR_ORG_ADMIN 이다. viewer 는 403 이다(RC:42-43).

**blocked 동작**: 팀 컬럼 `blocked Boolean @default(false)`(SP:200). 막힌 팀의 키로 호출하면 공통 검사에서 `Exception("Team=<id> is blocked. Update via `/team/unblock` if you're an admin.")` 이 나고(AC:1158-1160), 이것이 401 `auth_error` 가 된다(AEH:77-82). 차단 직후 팀 캐시를 갱신한다(TE:5562-5566). 대시보드는 `team_info.blocked` 를 읽는다(TeamInfo.tsx).

### 3.6 `POST /team/member_add` (TE:3578-3734)

**요청 본문** `TeamMemberAddRequest`(T:4127-4146, T:4192-4221)

| 필드 | 타입 | 기본값 | 대시보드 전송 |
|---|---|---|---|
| team_id | str | 필수 | ○ (NW:2909) |
| member | Member \| Member[] | 필수 | ○ 단일 객체 `{user_email, user_id, role}` (NW:2910, TeamInfo.tsx:825-829; 사용자 상세 화면은 `{role, user_id}`, UI/app/(dashboard)/users/_components/view_users/user_info_view.tsx:201-205) |
| max_budget_in_team | float\|null | null | × |
| budget_duration | str\|null | null | × |
| allowed_models | str[]\|null | null(없으면 팀 `default_team_member_models`, TE:3094-3097) | × |

**성공 응답**: 200, `TeamAddMemberResponse`(T:4170-4172).
```json
{ ...LiteLLM_TeamTable 전체 필드(갱신된 members_with_roles 포함)...,
  "updated_users": [LiteLLM_UserTable…],
  "updated_team_memberships": [LiteLLM_TeamMembership…] }
```
(TE:3728-3733.) `updated_users` 원소는 MU:19-48 필드이고 `password` 는 직렬화하지 않는다(MU:26 `exclude=True`). 대시보드는 응답을 읽지 않고 `/team/info` 를 다시 부른다(TeamInfo.tsx:831-843).

**오류** — 형태가 섞여 있다

| 상황 | 상태·본문 | 근거 |
|---|---|---|
| member 의 user_id·user_email 이 둘 다 null | 422 (C) | MT:29-36 |
| 팀 없음 | 404 `{"detail":{"error":"Team doesn't exist in db. Team=<id>. Create team via `/team/new` call."}}` (get_team_object 가 던짐) | TE:3626-3634, AC:3453-3466, AC:3653-3659 |
| **이미 멤버(단일 member)** | 400, **(A) 형태** `{"error":{"message":"User already in team. Member: user_id=…, user_email=…. Existing members=[…]","type":"team_member_already_in_team","param":"member","code":"400"}}` | TE:2982-2988 |
| 목록 전원이 이미 멤버 | 400 (A) `"All users are already in team. Existing members=…"`, 같은 type | TE:2975-2981 |
| 목록 일부만 중복 | 오류 없음. 중복은 건너뛴다 | TE:2989-2994, TE:3105-3106 |
| 권한 없음 | 403 `{"detail":{"error":"Call not allowed. User not proxy admin OR team admin. route=/team/member_add, team_id=<id>"}}` | TE:3011-3027 |
| proxy_admin 이 아닌데 아직 없는 user_id 를 추가 | 403 `{"detail":{"error":"Only proxy admins can add a user_id that does not exist yet: … Add the member by user_email to invite a new user, or ask a proxy admin to create the user first."}}` | TE:3349-3381 |
| user_id 와 user_email 이 다른 사람 | 400 `{"detail":{"error":"user_email '…' and user_id '…' do not belong to the same user."}}` | TE:3519-3528 |
| 같은 이메일 사용자가 여러 명 | 400 `{"detail":{"error":"Multiple users found with email '…'. Please use 'user_id' instead."}}` | TE:3507-3514, MH:475-479 |
| DB 쓰기 실패 | 500 `{"detail":{"error":"Unable to add user - …, to team - …, for reason - …"}}` | TE:3118-3122 |
| role="admin" | 400 [premium, 우리는 강제 안 함] | TE:2935-2940 |

대시보드는 오류 본문에서 `parsedError.detail.error` 를 꺼내 Error 메시지로 쓴다. 없으면 `"Failed to add team member"` 다(NW:2913-2927). 그래서 **중복 멤버 오류((A) 형태)는 대시보드에 "Failed to add team member" 로만 보인다.** 이 차이까지 맞추려면 형태를 바꾸지 말아야 한다.

**권한**: 라우트 단계는 self_managed 라 누구나 통과한다(T:842). viewer 는 POST 기본 거부로 403 이다(RC:1086-1095). 핸들러는 TEAM_OR_ORG_ADMIN 이고(TE:3011), 예외로 "가입 가능한 팀"(`_is_available_team`, TE:948)에는 일반 사용자가 **본인만 role=user 로** 셀프 가입할 수 있다(TE:3013-3060).

**부수 효과**
- **없는 사용자는 만든다.** 이것이 문서화된 동작이다(TE:3592).
  - user_id 로 추가하면 upsert 한다. 새 행의 기본값은 `get_new_internal_user_defaults`(MH:169-185)에서 온다: `user_role="internal_user"`, `models=[]`(또는 `default_internal_user_params.models`), max_budget·budget_duration 은 설정 기본값, `teams=[team_id]` (MH:446-455).
  - 이메일로 추가하고 그 이메일의 사용자가 없으면 user_id 를 `str(uuid.uuid4())` 로 새로 만든다(MH:458-469). 있으면 그 사용자를 쓴다(MH:470-473).
- 사용자 `teams` 배열에 team_id 를 넣는다. 이미 있으면 넣지 않는다(MH:400-417).
- `LiteLLM_TeamMembership(user_id, team_id)` 행을 upsert 한다. 기존 행이 있으면 건드리지 않는다(`"update": {}`). 예산 id 는 이렇게 정한다(MH:345-397): 명시한 한도가 있으면 새 예산 행, 없으면 팀 기본 멤버 예산(`metadata.team_member_budget_id`), 둘 다 없으면 null.
- 팀 advisory lock 을 잡고 roster 를 다시 읽은 다음 쓴다(TE:3224-3255).
- **키는 만들지 않는다.**

### 3.7 `POST /team/member_delete` (TE:3763-3985)

**요청 본문** `TeamMemberDeleteRequest`: `{"team_id": str, "user_id"?: str, "user_email"?: str}` (T:4224-4225, TT:21-30). user_id·user_email 이 둘 다 null 이면 422 다. 대시보드는 `{team_id, user_email?, user_id?}` 를 보낸다. 값이 undefined 인 키는 넣지 않는다(NW:3079-3090).

**성공 응답**: 200, response_model 은 없다. **갱신된 `LiteLLM_TeamTable`**(대상을 뺀 members_with_roles)을 돌려준다(TE:3807, TE:3877). 대시보드는 읽지 않는다(TeamInfo.tsx:917-924).

**오류** ((B) 형태)

| 상황 | 상태·본문 | 근거 |
|---|---|---|
| 팀 없음 | **400** `{"detail":{"error":"Team id=<id> does not exist in db"}}` (404 아님) | TE:3834-3838 |
| 권한 없음 | 403 `{"detail":{"error":"Call not allowed. User not proxy admin OR team admin. route=/team/member_delete, team_id=<id>"}}` | TE:3843-3851 |
| roster 에도 없고 사용자 teams 에도 없음 | 400 `{"detail":{"error":"User not found in team"}}` | TE:3903-3904 |

**권한**: self_managed 라 라우트 단계는 통과한다(T:843). 핸들러는 TEAM_OR_ORG_ADMIN 이다.

**부수 효과**
- roster 에서 해당 항목을 뺀다(TE:3872-3877, TE:3920-3924).
- 사용자 `teams` 배열에서 team_id 를 뺀다(TE:3926-3930).
- TeamMembership 행을 삭제한다(TE:3932-3933).
- **그 사용자가 이 팀에 가진 키를 모두 삭제한다.** 삭제 전에 `LiteLLM_DeletedVerificationToken` 에 기록한다(TE:3909-3954). 사용자 행 자체는 남긴다.

### 3.8 `POST /team/member_update` (TE:3988-4135)

**요청 본문** `TeamMemberUpdateRequest`(T:4228-4264)

| 필드 | 타입 | 대시보드 전송(NW:3008-3043) |
|---|---|---|
| team_id | str 필수 | ○ |
| user_id / user_email | str\|null (하나는 필수) | user_id 는 항상, user_email 은 undefined 가 아닐 때 |
| role | "admin"\|"user"\|null | ○ 항상 |
| max_budget_in_team | float\|null | 키가 있으면 전송. 빈 값은 null |
| tpm_limit, rpm_limit | int\|null | 위와 같음 |
| budget_duration | str\|null | 위와 같음 |
| allowed_models | str[]\|null (`[]` 는 제한 해제) | undefined 가 아닐 때 |
| temp_budget_increase(≥0) + temp_budget_expiry(datetime) | 둘은 함께 보내야 한다(아니면 422, T:4259-4264) | 키가 있으면 전송 [extra] |
| model_max_budget | dict\|null (`{}` 는 팀 기본값으로 되돌림) | undefined 가 아닐 때 [premium] |

대시보드는 "키가 있는지"로 전송 여부를 정한다(`"x" in formValues`). 그래서 우리도 **null(지움)과 누락(유지)을 구분해야 한다.**

**성공 응답**: 200 `TeamMemberUpdateResponse`(T:4186-4188, T:4267-4276). 저장 결과가 아니라 **요청 값을 되돌려 준다**(TE:4135-4147).
```json
{"user_id","user_email","team_id","max_budget_in_team","tpm_limit","rpm_limit","budget_duration",
 "allowed_models","temp_budget_increase","temp_budget_expiry","model_max_budget"}
```
`user_id` 는 이메일로 찾은 경우 roster 에서 해석한 값이다(TE:4064-4071). 대시보드는 응답을 읽지 않는다(TeamInfo.tsx:879-887).

**오류** ((B) 형태)

| 상황 | 상태·본문 | 근거 |
|---|---|---|
| user_id·user_email 둘 다 없음 | 422 (C) — 모델 검증이 먼저 막는다. 핸들러 400 검사(TE:4024-4028)까지 가지 않는다 | TT:25-30 |
| 팀 없음 | **400** `{"detail":{"error":"Team id=<id> does not exist in db"}}` | TE:4035-4039 |
| 권한 없음 | 403. 메시지의 route 가 **`/team/member_delete` 로 잘못 찍힌다**(원본 버그) | TE:4044-4052 |
| 이메일로 찾았는데 roster 에 없음 | 400 `{"detail":{"error":"User id doesn't exist in team table. Data=…"}}` | TE:4073-4077 |
| role="admin" | 400 `{"detail":"Assigning team admins is a premium feature. …"}` [premium] | TE:4018-4023 |

**권한**: self_managed(T:846). 핸들러는 TEAM_OR_ORG_ADMIN 이다(TE:4044). 내부에서 `team_info()` 를 호출하므로 `/team/info` 권한 검사도 함께 받는다(TE:4054-4059).

**부수 효과**
- role 변경은 roster JSON 을 다시 쓴다(TE:4097-4108, TE:3260 이후).
- 예산 필드는 멤버 예산 행을 upsert 한다. 팀 기본 예산을 공유하던 멤버라면 첫 수정 때 예산 행을 복제해서 따로 쓴다(TE:4109-4117, MH:350-358).

### 3.9 `GET /team/info` (TE:5240-5408)

**쿼리**: `team_id: str` (기본 None. 없으면 422 `{'message': 'Malformed request. No team id passed in.'}`, TE:5270-5274), `key_limit: int>0` (선택, TE:5245). 대시보드는 `team_id`, `key_limit`(선택)을 보낸다(NW:1187-1190). 멤버 예산 훅은 `keyLimit: 1` 을 쓴다(UI/app/(dashboard)/hooks/teams/useTeamMemberBudgets.ts:35).

**성공 응답**: 200 `TeamInfoResponseObject`(T:4392-4396).
```json
{
  "team_id": "<id>",
  "team_info": { ...LiteLLM_TeamTable 필드...,
     "members_with_roles": [{"user_id","user_email","role","user_alias"}],
     "team_member_budget_table": LiteLLM_BudgetTableFull|null,
     "access_group_models","access_group_mcp_server_ids","access_group_agent_ids","access_group_skill_names",
     "unresolved_access_group_ids","access_group_details",          // [extra]
     "organization_models": [..]|null,                               // 관리 권한자에게만
     "model_max_budget_usage": {...}|null,                           // [extra]
     "caller_edit_access": {"kind":"unrestricted"|"team_admin"|"team_admin_disabled"|"none", ...} },
  "keys": [LiteLLM_VerificationToken…],
  "team_memberships": [{ ...LiteLLM_TeamMembership..., "budget_source": "team_default"|"custom"|"none" }]
}
```
(T:4342-4396, 조립은 TE:5370-5385.)
- `team_info` 에는 `litellm_model_table`, `object_permission`, `litellm_organization_table` 이 include 된 상태로 들어간다(TE:5278-5284).
- **keys 에서 해시 토큰을 제거하는 코드가 사본에만 적용돼서 실제로는 효과가 없다**(TE:5318-5325: `key = key.model_dump(); key.pop("token")`). 즉 `keys[].token`(해시)이 그대로 나간다. 원본 버그로 보이며, 따라 할지는 우리가 판단할 일이다.
- 만료되지 않은 키만 조회한다(`expires=datetime.now()`, TE:5300-5306).
- `caller_edit_access.kind` 는 TE:`_caller_edit_access` 가 정한다. 팀 admin 이면 `editable_fields`, `may_raise_max_budget`, `proxy_controlled_metadata_keys` 가 붙는다(T:4346-4350).

**대시보드가 읽는 필드**(UI/components/team/TeamInfo.tsx, `info = teamData.team_info` TeamInfo.tsx:681):
- team_info: `team_id, team_alias, organization_id, models, max_budget, soft_budget, budget_duration, tpm_limit, rpm_limit, tpd_limit, max_parallel_requests, blocked, spend, created_at, metadata(가장 많이 씀), router_settings, object_permission, policies, litellm_model_table, team_member_budget_table, default_team_member_models, model_max_budget, model_max_budget_usage, access_group_ids, access_group_models, access_group_mcp_server_ids, access_group_agent_ids, access_group_skill_names, access_group_details, unresolved_access_group_ids, caller_edit_access, members_with_roles`
- 최상위: `keys`, `team_id`, `team_memberships`
- 사용자 상세 화면은 `team_info.team_alias` 만 쓴다(user_info_view.tsx:138-141).
- ModelSelect 는 `organization_models` 를 읽는다(UI/components/ModelSelect/ModelSelect.tsx:81,160).

**오류** ((A) 형태, `type="auth_error"`, `param="None"`, TE:5389-5408)
- 팀 없음: 404, message `"{'message': 'Team not found, passed team id: <id>.'}"` (TE:5286-5291).
- 접근 불가: 403, message `"{'error': 'User=<uid> not authorized to access this team=<id>'}"`. 팀 키가 다른 팀을 보면 `"Team key for team=… not authorized…"` 다(validate_membership).

**권한**: 라우트 단계는 info_route 라 누구나 통과한다(T:562, RC:354-355). 핸들러 `validate_membership` 은 proxy_admin, proxy_admin_viewer, 같은 팀의 팀 키, **roster 의 아무 멤버(role 무관)**, 팀 조직의 org_admin 을 허용한다. `organization_models` 는 `strongest_role` 이 있는 관리자(proxy/org/team admin)에게만 채운다(TE:5294-5297).

### 3.10 `GET /team/list` (TE:6260-6334)

**쿼리**: `user_id: str|null`, `organization_id: str|null`(`"default_organization"` 은 조직 없는 팀만, TE:6327-6333) (TE:6262-6266).
대시보드는 `user_id, organization_id, team_id, team_alias` 를 보낸다(NW:1208-1216). **`team_id`·`team_alias` 는 핸들러 시그니처에 없어서 무시된다**(TE:6262-6266, SD:73764-73770 도 user_id·organization_id 만 정의).
- 비관리자는 `user_id=본인` 을 붙여 부르고, Admin/Admin Viewer 는 붙이지 않는다(UI/app/(dashboard)/networking.ts:10-14).

**성공 응답**: 200, **맨 JSON 배열**이다(`{teams: …}` 로 감싸지 않음, TE:6334). `team_alias` 오름차순으로 정렬한다(null 은 `""` 로 취급, TE:6326).
```json
[ { ...LiteLLM_TeamTable 필드(litellm_model_table 포함)...,
    "team_memberships": [LiteLLM_TeamMembership…],
    "keys": [<그 팀의 모든 Prisma 키 행>] } ]
```
(T:4407-4409, TE:6297-6317.)
- `keys` 는 Prisma `LiteLLM_VerificationToken` 행을 그대로 담는다(TE:6309). **해시 `token` 이 포함되고 만료 키도 포함된다**(필터 없음).
- `user_id` 를 주면 team_memberships 도 그 사용자 것만 남긴다(TE:6299 `get_all_team_memberships(..., user_id=user_id)`).
- 검증에 실패한 팀은 로그만 남기고 빠진다(TE:6319-6325).
- 대시보드가 읽는 필드: `team_id, team_alias` (user_info_view.tsx:176-182). 정책 화면은 "plain array" 로 가정한다(UI/app/(dashboard)/policies/_components/add_attachment_form.tsx:135-139). 이전 `useTeams` 훅(`UI/app/(dashboard)/hooks/useTeams.tsx`)이 읽는 필드는 미확인이다.

**오류** ((B) 형태)
- 비관리자가 `user_id` 없이 또는 타인 user_id 로 부르고 org_admin 도 아니면: **401** `{"detail":{"error":"Only admin users can query all teams/other teams. Your user role=<role>"}}` (TE:6223-6232).

**권한**(TE:6187-6258)
- proxy_admin·proxy_admin_viewer(`user_api_key_has_admin_view`, `gateway/litellm/types/proxy/auth/user_api_key_auth.py:328-337`): 전체 팀. user_id 를 주면 그 사용자가 roster 에 있는 팀만.
- org_admin: 자기가 admin 인 조직의 팀만.
- 그 외: `user_id == 본인` 일 때만 본인이 roster 에 있는 팀. roster 판정은 `members_with_roles[].user_id` 로 한다. TeamMembership 이나 사용자 `teams` 배열이 아니다.

---

## 4. 사용자 엔드포인트

### 4.1 `POST /user/new` (IU:462-667)

**요청 본문** `NewUserRequest`(UR:18-46, 기반 GenerateRequestBase RB:24-90)

| 필드 | 타입 | 기본값 | 대시보드 전송 |
|---|---|---|---|
| user_id | str\|null | null → `str(uuid.uuid4())` (IU:204-205) | ○ **항상 `null`** (NW:1057, 호출부 CreateUserButton.tsx:209) |
| user_email | str\|null | null | ○ (CreateUserButton.tsx:248) |
| user_alias | str\|null | null | × (단건 폼에는 없음) |
| user_role | proxy_admin\|proxy_admin_viewer\|internal_user\|internal_user_viewer\|null | **null** | ○ 폼 기본값 `"internal_user_viewer"` (CreateUserButton.tsx:71,79,281) |
| teams | str[] \| {team_id, max_budget_in_team, user_role:"user"\|"admin"}[] \| null | null(없으면 `default_internal_user_params.available_teams` 등 기본 팀, IU:592-594) | × |
| team_id | str\|null | null | ○ (CreateUserButton.tsx:256) |
| organizations | str[]\|null | null | ○ 폼의 `organization_ids` 를 이름만 바꿔서 보냄 (CreateUserButton.tsx:102-106) |
| models | list | [] | ○. 비어 있고 role 이 proxy_admin 이 아니면 **`["no-default-models"]`** 를 보냄 (CreateUserButton.tsx:97-101) |
| metadata | dict | {} | ○. description 은 metadata 로 옮긴 뒤 JSON 으로 보냄 (NW:1026-1047) |
| auto_create_key | bool | **true** (UR:32) | ○ **항상 `false`** (NW:1038) |
| send_invite_email | bool\|null | null | ○ (CreateUserButton.tsx:273) |
| max_budget, budget_duration, tpm_limit, rpm_limit, max_parallel_requests, spend, key_alias, duration, … | GenerateRequestBase | | 일괄 생성 화면은 max_budget·budget_duration·metadata 를 보냄 (bulk_create_users_button.tsx:355-367) |
| sso_user_id | str\|null | null | × |
| password | — | 값이 있으면 **422** (`/invitation/new` 를 쓰라는 안내, UR:36-44) | × |

**성공 응답**: 200, `response_model=NewUserResponse`(IU:467, T:1712-1728, 기반 GenerateKeyResponse T:1205-1220 + KeyRequestBase + GenerateRequestBase). 평면 객체다. 주요 필드는 다음과 같다.
```json
{"key": "sk-…" | "",  "key_name", "expires", "user_id", "token_id", "token": null,
 "user_email", "user_role", "user_alias", "teams", "max_budget", "models", "spend", "team_id",
 "metadata", "tpm_limit", "rpm_limit", "budget_duration", "model_max_budget", "created_at", "updated_at",
 ... GenerateRequestBase 의 나머지 필드(대부분 null/기본값) ... }
```
- `key` 는 생성된 평문 키(`sk-` + token_urlsafe, KM:5106)다. `auto_create_key=false` 면 **빈 문자열 `""`** 이다. 이때 helper 가 사용자 데이터만 돌려주고 token 이 없어서 그렇다(IU:644, KM:5281-5285). `token`·`token_id` 는 일부러 응답에서 뺀다(IU:638-642).
- `teams` 는 team_id·teams 로 팀에 붙였을 때만 실제 소속 목록으로 채운다(IU:623-628, IU:645-646).
- 대시보드가 읽는 필드: `user_id`(`response.data?.user_id || response.user_id`, CreateUserButton.tsx:212). 일괄 생성은 `response.key || response.user_id` 로 성공을 판정한다(bulk_create_users_button.tsx:370-372). 받은 `user_id` 로 `/invitation/new` 를 부른다(CreateUserButton.tsx:220-224).

**오류** ((A) 형태, `type="internal_server_error"`, IU:665-667)

| 상황 | 상태·message | 근거 |
|---|---|---|
| user_id 중복 | **409** `"{'error': 'User with id <id> already exists'}"` | IU:294-303, IU:270-278 |
| user_email 중복(**대소문자 무시**) | **409** `"{'error': 'User with email <기존 이메일> already exists'}"` | IU:281-291, IU:266-268 |
| 비 proxy_admin 이 proxy_admin·proxy_admin_viewer 사용자 생성 | 403 `Only proxy admins can create administrative users (proxy_admin, proxy_admin_viewer). Attempted to create user with role: … Your role: …` | IU:565-573 |
| budget_duration 형식 오류 | 400 (validate_budget_duration) — 정확한 문구 미확인 | IU:548 |
| 라이선스 초과 | 403 [premium] | IU:554-560 |
| 라우트 거부 | 401 (D), viewer 403 | RC:405-411, RC:31 |

**권한**: 라우트 단계에서 proxy_admin 과 org_admin 만 통과한다(management_routes, T:629). org_admin 의 조직 범위 검사는 `auth_checks_organization.py` 에 있다(미확인: `/user/new` 전용 분기는 없다. `organization_role_based_access_check` 의 일반 분기만 해당될 수 있다).

**부수 효과**
- `user_role` 이 null 이면 **null 로 저장한다**(KM:5161-5168). 서버 기본값은 없다. 인증 때 null 은 internal_user 로 해석한다(AC:1520-1524). 반면 member_add 로 생긴 사용자는 `internal_user` 로 저장된다(MH:178). 두 경로의 차이를 맞춰야 한다.
- role 이 proxy_admin 이 아니면 `litellm.default_internal_user_params` 로 빈 필드를 채운다(IU:212-224). `internal_user` 면 `max_internal_user_budget`·`internal_user_budget_duration` 기본값을 쓴다(IU:227-232) [설정 의존].
- **키 생성**: `auto_create_key=true`(기본)면 사용자와 함께 키를 하나 만들어 평문을 `key` 로 돌려준다. 대시보드는 false 를 보내므로 키를 만들지 않는다(IU:207-210).
- `team_id` 가 있으면 그 팀에 `role="user"` 로 추가한다(IU:604-613). `teams` 가 있으면 기본 팀 추가 경로를 탄다(IU:614-621). `organizations` 가 있으면 각 조직에 `internal_user` 로 추가하고, 실패해도 무시한다(IU:306-339, `return_exceptions=True`).

### 4.2 `POST /user/update` (IU:1659-1746, 본체 IU:1455-1641)

**요청 본문** `UpdateUserRequest`(UR:49-78): `user_id` 또는 `user_email` 중 하나가 필수다(없으면 422, UR:72-78). 나머지는 GenerateRequestBase 필드 + `password, spend, metadata, user_alias, user_role, max_budget` 이고 모두 기본 null 이다.
- **보낸 필드만** 반영한다(`model_dump(exclude_unset=True)`, IU:1481).
- 대시보드 편집 폼 필드: `user_id, user_email, user_alias, user_role, models, max_budget, tpm_limit, rpm_limit, budget_duration, metadata, model_max_budget`, 그리고 `object_permission`(MCP 권한) (UI/app/(dashboard)/users/_components/user_edit_view.tsx:226-395, user_info_view.tsx:331-335).
- `userUpdateUserCall` 의 세 번째 인자가 null 이 아니면 `user_role` 을 덧붙인다(NW:3174-3178).

**성공 응답**: 200, response_model 없음.
- user_id 로 갱신하면: `{"user_id": "<id>", "data": <Prisma 사용자 행>}` (IU:1558-1564, PU:6206). `password` 는 뺀다(IU:1640).
- **user_email 만 주고 그 이메일의 사용자가 없으면 새 사용자를 만들고, 감싸지 않은 Prisma 행을 그대로 돌려준다**(IU:1582-1587). 두 모양이 다르다.
- 대시보드 타입은 `{user_id, data: UserInfo}` 다(NW:3179-3182). 화면은 응답 대신 로컬 상태를 고친다(user_info_view.tsx:337-350).

**오류** ((A) 형태, `type="auth_error"`, `param="None"`, IU:1729-1746)

| 상황 | 상태·message | 근거 |
|---|---|---|
| 비 proxy_admin 이 user_role 변경 | 403 `Only proxy admins can modify user roles.` | IU:1377-1378 |
| 타인 수정(비 proxy_admin) | 403 `{'error': 'User does not have permission to update this user. Only PROXY_ADMIN can update other users.'}` | IU:1380-1388, IU:1644-1656 |
| 대상 없음 + 비 proxy_admin | 404 `{'error': 'User not found. Only PROXY_ADMIN can create users via /user/update; use /user/new instead.'}` | IU:1389-1396 |
| 비관리자가 자기 max_budget·model_max_budget·soft_budget·spend·object_permission 수정 | 403 `{'error': "Non-admin users cannot modify '<field>' on their own record. Contact your proxy admin."}` | IU:1509-1524 |
| 갱신 결과 없음 | 400 `{'error': 'Failed to update user'}` | IU:1635-1639 |

**권한**: 라우트 단계에서 proxy_admin 과 org_admin 은 통과한다(management_routes, T:631). viewer 는 `user_email` 만 보낼 때 허용된다(RC:1014-1023, RC:1042-1054). **internal_user 는 라우트 단계에서 401 이다**(internal_user_routes·self_managed 에 없음). 그래서 핸들러의 "본인 수정 허용" 분기(IU:1651-1655)는 사실상 viewer·org_admin·`allowed_routes` 키에서만 의미가 있다.

**부수 효과**
- **proxy_admin 이 없는 user_id 로 update 하면 upsert 로 사용자를 만든다**(PU:6196-6202 `upsert(create=…, update=…)`). 404 가 아니다.
- password 를 바꾸면 해시해서 저장하고 그 사용자의 UI 세션 키를 폐기한다(IU:1483-1488, IU:1590-1605).
- 사용자 캐시를 비운다(IU:1617-1620).

### 4.3 `POST /user/delete` (IU:2383-2557)

**요청 본문** `DeleteUserRequest`: `{"user_ids": [str]}` 필수(T:1753-1754). 대시보드는 `{user_ids: [...]}` 를 보낸다(NW:1089).

**성공 응답**: 200, **JSON 정수 하나**(삭제된 사용자 수)다. 예: `1` (IU:2547-2557, BUD:592-618). 대상 사용자가 하나도 없으면 `0` 이다(BUD:605-606). 다만 앞 단계 존재 검사(IU:2471-2479)에서 404 가 먼저 난다. 대시보드는 응답을 읽지 않고 목록 캐시에서 해당 행을 지운다(UI/app/(dashboard)/users/_components/view_users.tsx:179-184).

**오류** ((B) 형태)

| 상황 | 상태·본문 | 근거 |
|---|---|---|
| user_ids 중 하나라도 없음 | 404 `{"detail":{"error":"User not found, passed user_id=<id>"}}` | IU:2471-2479 |
| proxy_admin 이 아니고 어느 조직의 org_admin 도 아님 | 403 `{"detail":{"error":"Only PROXY_ADMIN or ORG_ADMIN users may delete users."}}` | IU:2439-2457 |
| org_admin 인데 대상이 범위 밖 조직 소속이거나 조직이 없음 | 403 (`…administered organizations.`) | IU:2482-2496 |
| 내부 삭제 실패 | 500 `{"detail":{"error":"<사유>"}}` | IU:2555-2556 |

**권한**: 라우트 단계에서 proxy_admin 과 org_admin 이 통과한다(T:633). viewer 는 403 이다(RC:33).

**부수 효과 — 하드 삭제**
- 사용자의 **모든 키**를 `delete_many` 로 지운다. `/team/delete` 와 달리 **DeletedVerificationToken 에 기록하지 않는다**(IU:2525-2533). 감사 로그 훅은 부른다(IU:2534-2539).
- 사용자를 모든 팀 roster(`members_with_roles`)에서 빼고, TeamMembership 행과 사용자 행을 삭제한다(BUD:592-618, BUD:457).
- 감사 로그를 쓰도록 설정돼 있으면 사용자마다 "deleted" 감사 행을 남긴다(IU:2500-2521).

### 4.4 `GET /user/info` (IU:922-1003)

> 대시보드는 이 엔드포인트를 **부르지 않는다**. 사용자 상세는 `/v2/user/info` 를 쓴다(NW:1145-1183, user_info_view.tsx:130). 아래는 호환을 위한 참고다.

**쿼리**: `user_id: str|null` (IU:931). 값에 공백이 있으면 원본 쿼리에서 `+` 를 살려 다시 읽는다(IU:751-755).

**성공 응답**: 200 `UserInfoResponse`(T:3165-3169)
```json
{"user_id": "<id>|null", "user_info": {사용자 행, password 제외, model_max_budget_usage 추가}|null,
 "keys": [키 dict + "team_alias"], "teams": [TeamListResponseObject…]}
```
(IU:891-919.)
- **관리자 계열이 user_id 없이 부르면** 특수 경로를 탄다. `teams` 에는 **모든 팀**, `keys` 에는 대시보드 세션 키를 뺀 **모든 키**를 담고, `user_info` 는 호출자 본인 행이다(IU:957-958, IU:1176-1237).
- 키에서 대시보드 세션 키(`team_id="litellm-dashboard"`)를 빼고 `team_alias` 를 붙인다(IU:1240-1275).

**오류** ((A) 형태, `handle_exception_on_proxy`, IU:1000)
- 사용자 없음: 404 `"User <id> not found"` (IU:967-971).
- 타인 조회: 403 `key not allowed to access this user's info. user_id=…, key's user_id=…` (IU:758-784; 라우트 단계 RC:334-344 도 같은 문구).

**권한**: info_route 다. 본인, proxy_admin, proxy_admin_viewer 만 볼 수 있다.

### 4.5 `GET /user/list` (IU:2210-2380)

**쿼리**(IU:2216-2237)

| 파라미터 | 타입 | 기본값 | 의미 | 대시보드 전송(NW:1122-1140) |
|---|---|---|---|---|
| role | str | null | `user_role` 정확히 일치 | ○ |
| user_ids | str(쉼표 구분) | null | **1개면 부분 일치(contains, 대소문자 무시)**, 여러 개면 in | ○ `join(",")` |
| sso_user_ids | str(쉼표 구분) | null | in | ○ (`sso_user_id` 를 그대로 넣음) |
| user_email | str | null | 부분 일치, 대소문자 무시 | ○ |
| search | str | null | user_id 또는 user_email 부분 일치(대소문자 무시) | ○ |
| team | str | null | `teams` 배열에 포함 | ○ |
| page | int ≥1 | 1 | | ○ |
| page_size | int 1..100 | **25** | 범위 밖이면 422 | ○ |
| sort_by | user_id\|user_email\|created_at\|spend\|user_alias\|user_role | null → `created_at desc` | 다른 값이면 400 | ○ |
| sort_order | asc\|desc | "asc" | 다른 값이면 400 | ○ |
| organization_ids | str(쉼표 구분) | null | 조직 멤버십 some-in | ○ |

`undefined` 인 파라미터는 대시보드가 보내지 않는다.

**성공 응답**: 200 `UserListResponse`(UL:32-42)
```json
{"users": [ { ...LiteLLM_UserTable 필드(password 제외)..., "key_count": 0 } ],
 "total": 123, "page": 1, "page_size": 25, "total_pages": 5}
```
(IU:2335-2380.)
- `total_pages = ceil(total / page_size)` 다. total 이 0 이면 0 이다(IU:2361).
- `metadata` 의 SCIM enterprise 정보는 가린다(IU:2367).
- `organization_memberships`·`object_permission` 은 include 하지 않으므로 null 이다.
- 대시보드가 읽는 필드: `users[].user_id, user_email, user_role, user_alias, spend, max_budget, sso_user_id, key_count, created_at, updated_at` (UI/app/(dashboard)/users/_components/view_users/UsersTableColumns.tsx:105-256), `total` (view_users.tsx:275-276). 대시보드 타입은 `components["schemas"]["UserListResponse"]` 다(NW:1104-1105, SD:49782).

**오류** ((B) 형태, 이 핸들러에는 `management_endpoint_wrapper` 도 try 도 없다)
- 관리자도 org_admin 도 아님: 403 `{"detail":{"error":"Only proxy admins and organization admins can list users."}}` (IU:2128-2178).
- org_admin 이 권한 없는 조직을 요청: 403 `{"detail":{"error":"You do not have org_admin access to the requested organization(s)."}}` (IU:2182-2188).
- sort 값 오류: 400 `{"detail":{"error":"Invalid sort column. Must be one of: …"}}` 또는 `"Invalid sort order. Must be 'asc' or 'desc'"` (IU:2096-2125).

**권한**: 라우트 단계는 self_managed(T:892)와 admin_viewer(T:964)다. 핸들러에서 proxy_admin·proxy_admin_viewer 는 전체를 보고, org_admin 은 자기 조직으로 범위가 좁혀진다. **internal_user 는 403 이다.**

---

## 5. 미확인 항목

- 팀 admin 이 허용되지 않은 필드를 `/team/update` 할 때의 정확한 상태 코드와 문구(`team_admin_request_or_raise` 내부, TE:2436).
- `validate_budget_duration` 이 내는 오류 문구와 상태(`gateway/litellm/proxy/common_utils/timezone_utils.py` 를 읽지 않았다).
- `/user/new` 를 org_admin 이 부를 때 조직 범위 검사가 따로 있는지(`auth_checks_organization.py` 의 `/user/new` 전용 분기는 보지 못했다).
- 이전 `useTeams` 훅(`UI/app/(dashboard)/hooks/useTeams.tsx`)이 `/team/list` 응답에서 읽는 필드 전체.
- `public_validation_errors`(PS:2361)가 422 배열을 어떻게 가공하는지.
