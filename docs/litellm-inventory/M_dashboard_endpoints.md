# M. 대시보드가 부르는 HTTP 엔드포인트 전체 목록과 분류 (IMPLEMENTED·MUST·STUB)

수정하지 않은 litellm 대시보드(`gateway/ui/litellm-dashboard/src`)의 모든 화면이 우리 서버에서 떠야 한다. 그러려면 대시보드가 부르는 엔드포인트를 모두 등록해야 한다. 각 엔드포인트는 실제로 구현하거나, 화면을 깨뜨리지 않는 안전한 빈 응답으로 답한다. 이 문서는 그 목록이다. 엔드포인트마다 메서드, 경로 패턴, 호출 위치, 화면, 분류를 적었다. STUB 인 것에는 화면이 버티는 최소 응답 모양도 적었다.

근거는 모두 `파일:줄`로 달았다. 코드로 확인하지 못한 것은 "미확인"으로 적었다. 계약(요청·응답 전체 모양)은 이미 정리된 문서를 따른다. 팀·사용자는 H·I, 키는 J, 관리 API 전반은 C 에 있다. 이 문서는 "무엇을 등록해야 하는가"와 "비어 있을 때 어떤 모양이어야 안 깨지는가"만 다룬다.

## 0. 약어와 조사 방법

| 약어 | 뜻 (모두 `<workspace>/gateway/ui/litellm-dashboard/` 기준) |
|---|---|
| NW | `src/components/networking.tsx` (8000줄. 대부분의 호출 함수가 여기 있다) |
| D/… | `src/app/(dashboard)/…` |
| H/… | `src/app/(dashboard)/hooks/…` |
| C/… | `src/components/…` |
| LUI/… | `node_modules/@litellm/lens-ui/src/…` |
| schema.d.ts | `src/lib/http/schema.d.ts` (openapi 타입. 응답 필드 이름의 근거) |

**호출 경로 네 가지** (모두 찾았다)
1. `apiClient`·`gatewayApiClient`·`silentApiClient` 의 `.get/.post/.put/.patch/.delete/.getBlob` (`src/lib/http/client.ts` 의 `createApiClient`). `gatewayApiClient` 는 `gateway_base_url` 로 간다(NW:245-250, NW:530-540)
2. 타입 클라이언트 `fetchClient`·`backgroundFetchClient` 의 `.GET/.POST/…` 와 `$api.useQuery/useInfiniteQuery` (`src/lib/http/api.ts`)
3. 직접 `fetch(url, …)`. url 은 `proxyBaseUrl ? \`${proxyBaseUrl}/x\` : "/x"` 꼴이다(NW 곳곳, contexts/ThemeContext.tsx, D/ui-theme, D/playground/llm_calls)
4. OpenAI·Anthropic SDK(`baseURL` 지정)와 WebSocket. 경로가 문자열로 드러나지 않는다. SDK 규칙(`baseURL + "/chat/completions"` 등)으로 풀었다(C/llm_calls/gateway_client.ts, C/liteadmin/agent.ts, D/playground/llm_calls/*)

**방법**: 테스트·`.d.ts` 를 뺀 `.ts/.tsx` 전부에서 경로 모양의 문자열·템플릿 리터럴을 뽑았다(1차 741개, 2차로 중첩 템플릿·`encodeURIComponent(...)` 조각 71개). `${…}` 는 `{이름}` 으로 바꿔 경로 패턴으로 만들었다. 화면 배정은 import 도달성으로 했다. 각 `D/<folder>/page.tsx`, 부팅 루트(`app/layout.tsx`, `D/layout.tsx`), 대시보드 밖 페이지(`app/login`, `app/onboarding`, `app/chat`, `app/model_hub` 등)에서 import 를 따라가 닿는 파일을 구했다. NW 함수는 그 함수 이름을 쓰는 파일로 화면을 정했다. 도달성은 실제로 렌더되는 범위보다 넓다. 그래서 보장 화면과 부팅 경로의 호출은 렌더 조건(어느 컴포넌트가 언제 부르는가)을 직접 확인해서 적었다. 엔드포인트가 아닌 문자열(UI 경로, 표시용 예시 URL, 에셋 경로)은 뺐다. 뺀 목록은 9장에 있다.

**분류 기준**
- **IMPLEMENTED**: 지금 우리 서버에 메서드·경로가 똑같이 있는 것. 우리 구현이 대시보드가 보내는 쿼리·본문을 다 다루는지 장담할 수 없으면 비고에 "계약 확인 필요"를 붙였다
- **MUST**: 보장 화면의 핵심 기능, 또는 로그인·부팅에 실제 데이터가 필요한 것. 보장 화면은 home, api-keys, teams, users, models-and-endpoints, model-hub-table, usage, logs, playground, change-password, ui-theme, api-reference 다. 로그인·부팅의 기본값 객체(`{"values":{}}` 등)도 반드시 등록해야 하므로 MUST 로 쳤다
- **STUB**: 그 밖의 모든 것. 보장 화면에서 불리더라도 우리에게 없는 부가 기능(조직, 가드레일, MCP, 에이전트, 정책, 프롬프트, 태그, 예산표, 벡터 스토어, 프로젝트, 접근 그룹, 검색 도구, 스킬 등)의 드롭다운·탭이면 STUB 이다. "최소 응답" 열이 가장 중요하다. 컴포넌트가 응답을 실제로 읽는 방식(`data.data`, `res.guardrails`, `.map`, `Object.values` …)을 따라가 정했다

**응답 실패 시 공통 동작**(STUB 에 404 를 줘도 되는지 판단하는 근거)
- `apiClient` 의 비 2xx 는 `ApiError` 를 던진다. 전역 처리기 `handleError` 는 메시지에 `"Authentication Error - Expired Key"` 가 있을 때만 로그아웃시키고, 다른 오류는 아무것도 하지 않는다(NW:434-451). 토스트는 호출부가 각자 띄운다
- react-query 에는 전역 오류 토스트가 없다. `ApiError` 4xx 는 재시도하지 않는다. 그 밖의 오류(직접 `fetch` 에서 던진 `Error` 등)는 3번까지 재시도한다(contexts/ReactQueryProvider.tsx `shouldRetry`)
- 직접 `fetch` 하는 함수 중 상당수가 오류 때 `await response.json()` 으로 본문을 읽는다. 본문이 JSON 이 아니면 메시지 대신 파싱 오류가 뜬다. **미구현·스텁의 오류 본문은 JSON(`{"detail":{"error":"…"}}`)으로 준다**
- 일부 함수는 `response.ok` 를 보지 않고 `response.json()` 을 그대로 돌려준다(NW:589 `getUiConfig`, NW:603 `getPublicModelHubInfo`, NW:610 `getOpenAPISchema`, NW:615-630 `modelCostMap` 등). 이런 곳에 404 를 주면 오류 본문이 정상 응답처럼 화면에 들어간다
- **404 와 "빈 200" 은 다르다.** 404 면 react-query 의 `data` 가 `undefined` 가 되고, 대부분의 화면이 `data?.x ?? []` 로 버틴다. 반대로 필드가 빠진 200(`{}`)은 `undefined.length` 같은 렌더 예외를 일으키는 곳이 많다(2.2절). 확신이 없으면 "모양을 다 갖춘 빈 값" 또는 "JSON 404" 중 하나를 고른다. 반쯤 찬 객체는 주지 않는다

## 1. 요약

| 분류 | 행 수 |
|---|---|
| IMPLEMENTED | 32 |
| MUST | 59 |
| STUB | 330 |
| **합계** | **421** |

- 셈 방법: 3~8장 표의 행을 (메서드, 경로 표기)로 중복 제거해 셌다. 한 행이 메서드 둘(`GET·PUT`)이나 하위 경로 묶음(`/team/daily/activity/aggregated` + `/keys`·`/search`…, Lens 경로 약 30개를 묶은 1행)을 담기도 한다. 그래서 엔드포인트 수로 풀면 약 470개다(미확인, 묶음 행을 손으로 센 추정). 같은 엔드포인트가 두 화면에 다른 표기로 나온 경우가 몇 개 있다(예: `/chat/completions` 는 home 의 LiteAdmin 과 playground 에 따로 있다)
- IMPLEMENTED 32 행 가운데 다수에 "계약 확인 필요"가 붙어 있다. 대시보드가 보내는 쿼리(정렬·페이지·`expand`·`status=deleted` 등)를 우리 구현이 다 받는지는 이 문서에서 확인하지 않았다
- STUB 330 행 가운데 렌더 예외 위험이 있는 것은 2.2절에 모았다(각 장 "위험" 열 57행)

### 1.1 화면별 행 수 (소유 화면 기준. 한 엔드포인트는 처음 나온 화면에만 센다)

| 절 | IMPLEMENTED | MUST | STUB |
|---|---|---|---|
| 부팅·레이아웃 (app/layout.tsx, D/layout.tsx — 모든 화면에서 먼저 불림) | 0 | 8 | 15 |
| 로그인 (app/login) | 1 | 1 | 2 |
| D/home | 1 | 1 | 3 |
| D/change-password | 0 | 1 | 0 |
| D/ui-theme | 0 | 1 | 0 |
| app/onboarding (초대 링크로 비밀번호 설정) | 0 | 2 | 0 |
| 다른 화면에서 쓰는 3장 소유 엔드포인트 | 0 | 0 | 4 |
| D/api-keys | 7 | 3 | 3 |
| D/teams | 10 | 10 | 23 |
| D/users | 6 | 6 | 0 |
| D/budgets (행은 4장에 적음. 화면은 budgets) | 0 | 0 | 4 |
| 4장 추가 발견 | 1 | 0 | 1 |
| D/models-and-endpoints | 2 | 7 | 27 |
| D/model-hub-table (+ app/model_hub, app/model_hub_table) | 1 | 5 | 10 |
| D/playground (5장에서 다룬 행만) | 0 | 0 | 2 |
| 5장 추가 발견 | 0 | 0 | 1 |
| D/usage | 0 | 8 | 21 |
| D/logs | 1 | 3 | 5 |
| D/old-usage (사용 중단 배너가 붙은 옛 사용량 화면) | 0 | 0 | 10 |
| D/cost-optimization | 0 | 0 | 8 |
| D/caching (6장에서 다룬 행만) | 0 | 0 | 1 |
| D/guardrails-monitor (`viewGuardrailUsage` 권한일 때만, D/guardrails-monitor/page.tsx:12) | 0 | 0 | 3 |
| D/tool-policies (`viewToolPolicies` 권한, C/ToolPoliciesView.tsx:15) | 0 | 0 | 6 |
| D/tag-management | 0 | 0 | 4 |
| D/model-insights | 0 | 0 | 1 |
| D/lens (@litellm/lens-ui 패키지가 직접 부른다) | 0 | 0 | 5 |
| 6장 추가 발견 | 0 | 0 | 1 |
| D/playground | 0 | 3 | 12 |
| D/mcp-servers | 1 | 0 | 31 |
| D/agents | 0 | 0 | 8 |
| D/vector-stores | 0 | 0 | 7 |
| D/search-tools | 0 | 0 | 5 |
| D/memory | 0 | 0 | 3 |
| D/skills | 0 | 0 | 1 |
| D/prompts | 0 | 0 | 6 |
| D/workflows | 0 | 0 | 3 |
| app/chat · app/connect · app/moyai · app/mcp/oauth/callback (대시보드 밖 페이지) | 0 | 0 | 4 |
| 7장 추가 발견 | 1 | 0 | 1 |
| D/admin-panel | 0 | 0 | 19 |
| D/logging-and-alerts | 0 | 0 | 8 |
| D/caching | 0 | 0 | 7 |
| D/router-settings | 0 | 0 | 1 |
| D/cost-tracking | 0 | 0 | 7 |
| D/guardrails | 0 | 0 | 14 |
| D/policies | 0 | 0 | 19 |
| D/organizations | 0 | 0 | 6 |
| D/access-groups | 0 | 0 | 3 |
| D/projects | 0 | 0 | 3 |
| D/transform-request | 0 | 0 | 1 |
| (화면 밖) 8장에 넣은 기타 행 | 0 | 0 | 1 |

## 2. 먼저 알아야 할 것 (화면을 가장 쉽게 깨뜨리는 지점)

### 2.1 우리 구현과 어긋나는 곳

1. **대시보드는 우리가 만든 `/user/daily/activity` 와 `/spend/logs` 를 부르지 않는다.** 이 두 문자열은 주석과 권한 라벨에만 나온다(D/usage/_components/components/UsagePageView.tsx:4, C/team/permission_definitions.tsx:26). usage 화면은 `GET /{entity}/daily/activity/aggregated` 묶음(`/keys`, `/search`, `/model_top_keys`, `/export`)과 `/user/daily/activity/aggregated/users` 를 부른다(NW:1388-1455). logs 화면은 `GET /spend/logs/ui`, `/spend/logs/ui/{request_id}`, `/spend/logs/session/ui` 를 부른다(NW:1996, 4783, 5572). 지금 있는 두 경로는 데스크탑 계정 화면용이다. 대시보드용 경로는 따로 만들어야 한다(6장)
2. **SDK 로 부르는 추론 경로에는 `/v1` 이 없다.** 플레이그라운드와 LiteAdmin 은 OpenAI SDK 의 `baseURL` 을 프록시 주소(= `gateway_base_url` 또는 관리 주소, NW:245-250)로 둔다. 그래서 실제 요청은 `POST /chat/completions`, `/responses`, `/embeddings`, `/images/generations`, `/audio/speech` 처럼 `/v1` 없이 간다(C/llm_calls/gateway_client.ts:15, C/liteadmin/agent.ts:95,118). 우리 서버에는 `/v1/…` 만 있다. 그래서 `/v1` 없는 별칭이 필요하다. Anthropic 은 `/v1/messages`, realtime 은 WebSocket `/v1/realtime` 으로 간다(7장)
3. **플레이그라운드는 기본적으로 UI 로그인 토큰을 추론 키로 쓴다**(D/playground/components/chat/PlaygroundChat.tsx:134). 추론 경로가 UI 세션 키를 받아들여야 한다
4. **모델 표의 데이터 공급원은 `GET /v2/model/info` 다.** 우리 서버에 없다(MUST). 행마다 `litellm_params`·`model_info` 객체가 있어야 한다(D/models-and-endpoints/utils/modelDataTransformer.ts:47-52). 모델 편집·일시정지는 `PATCH /model/{model_id}/update` 다. 이것도 없다(5장)
5. **플레이그라운드·LiteAdmin·모델 허브의 모델 목록은 `GET /model_group/info` 다**(C/llm_calls/fetch_models.tsx:54, NW:1782). `/v1/models` 가 아니다. IMPLEMENTED 지만 행마다 `providers` 배열이 없으면 모델 허브 표가 깨진다(C/AIHub/ModelHubTableColumns.tsx:119-124, C/model_filters.tsx:51)
6. **IMPLEMENTED(빈 값)인 `/v1/mcp/server` 는 반드시 맨 배열 `[]` 이어야 한다.** `{"data":[]}` 면 mcp-servers 화면 전체와 팀 상세 MCP 선택기가 깨진다(C/mcp_tools/MCPServerSelector.tsx:61,71, D/mcp-servers/_components/mcp_servers.tsx:195). `/guardrails/list` 의 빈 값은 `{"guardrails":[]}` 여야 한다(C/Teams.tsx:309, H/guardrails/useGuardrails.ts:41)
7. **로그인은 `POST /v2/login` 이다.** 우리 목록에 없다(MUST). 응답의 `token` 은 대시보드가 직접 디코드하는 JWT 다. 필드 `key`·`user_id`·`user_role`·`exp` 등을 읽는다(contexts/AuthContext.tsx:107-128). 부팅 직후 `GET /management/v1/users/me` 가 실패하면 대시보드는 곧바로 로그아웃한다(D/layout.tsx:250-254)

### 2.2 비거나 모양이 틀리면 화면이 깨지는 응답

아래는 각 장 "위험" 열에서 모은 것이다. **굵게** 표시한 것은 그 화면 전체 또는 모든 화면의 렌더를 멈춘다.

| 엔드포인트 | 깨지는 조건 → 안전한 응답 | 근거 |
|---|---|---|
| GET `/management/v1/users/me` | **비 2xx 나 빈 본문이면 곧바로 로그아웃**. 응답은 `{"user_id","user_email","user_role"}` | D/layout.tsx:250-254, H/currentUser/useCurrentUser.ts:15 |
| GET `/telemetry/settings` | **`{}` 면 모든 화면 본문 위 배너에서 예외**(proxy admin). 404 를 주거나 `environment_variables:[]` 를 포함한 전체 모양 | D/telemetry/_components/TelemetryEnvBanner.tsx:8 |
| GET `/get/config/callbacks` | **`alerts` 가 없으면 logging-and-alerts 화면 전체가 깨짐**. 응답은 `{"callbacks":[],"available_callbacks":{},"alerts":[],"router_settings":{}}` | C/settings.tsx:397-413 |
| GET `/v2/model/info` | **행에 `litellm_params` 가 없으면 모델 표가 깨짐** | D/models-and-endpoints/utils/modelDataTransformer.ts:47,52 |
| GET `/spend/logs/ui` | **`data` 가 없으면 로그 표가 깨짐**(`rows.length`). `/spend/logs/session/ui` 도 `data` 필요 | C/logs/request/RequestLogsPanel.tsx:175-178 |
| GET `/public/v1/model_hub`, GET `/management/v1/budgets` | **`meta` 가 없으면 깨짐**(`data?.meta.total_count`). 응답은 `{"data":[],"meta":{"total_count":0,…}}` | H/common/useResourceList.ts:125 |
| GET `/v1/mcp/server`, `/v1/mcp/server/health`, `/v1/mcp/toolset`, `/v1/access_group`, `/project/list`, `/budget/list`, `/v1/agents`, `/public/agents/fields`, `/customer/list`, `/global/spend/provider`, `/global/spend/end_users`, `/config/list`, `/alerting/settings`, `/policy/templates`, `/user/filter/ui` | **맨 배열 `[]` 이어야 한다.** 객체면 `.map`·`.find`·`.filter` 에서 예외 | 각 장 표 |
| GET `/tag/list` | 객체 맵 `{}` 여야 한다(`Object.values`) | 7장 playground |
| GET `/prompts/list`, `/claude-code/plugins` | `{"prompts":[]}`, `{"plugins":[]}` | 4장·7장 |
| GET `/model/cost_map/source` | `{}` 면 `model_count.toLocaleString()` 에서 예외. 404 를 주거나 전체 모양 | C/price_data_reload.tsx:367 |
| GET `/gateway/errors/activity`, `/global/activity/cache_hits`, `/auto_router/benchmarks`, `/lens`, `/tag/dau`, `/tag/user-agent/per-user-analytics` | 일부 필드만 있는 200 이면 렌더 예외. 404 가 안전 | 6장 |
| GET `/v1/mcp/server/submissions`, `/v1/mcp/sessions`, `/guardrails/submissions` | `items:[]`, `total_sessions:0`·`by_client:[]`·`sessions:[]`, `summary:{total:0,…}` 가 필요 | 7장·8장 |
| GET `/get/sso_settings`, `/coordination_redis/settings`, `/email/event_settings` | `values`·`settings` 가 없으면 예외 | 8장 |
| GET `/model-insights` | `start_date`·`end_date` 가 없으면 `toISOString` RangeError. 4xx 가 안전 | 6장 |
| GET `/get/latest_release_info`, `/get/user_banner` | `version` 없는 객체, 또는 `enabled:true` 인데 `message` 가 없는 응답이면 예외. 404 가 안전 | 3장 |
| GET `/team/{team_id}/members/me` | 멤버가 아니면 **404** 여야 한다(다른 코드면 탭 오류) | 4장 |

### 2.3 MUST 인데 우리 서버에 아직 없는 것

부팅·로그인: GET `/litellm/.well-known/litellm-ui-config`, `/management/v1/users/me`, `/get/ui_settings`, `/get/ui_theme_settings`, `/get_favicon`, `/get_image`, `/sso/get/ui_settings`, POST `/session/logout`, POST `/v2/login`, GET `/onboarding/get_token`, POST `/onboarding/claim_token`.
화면: POST `/user/password/change`, PATCH `/update/ui_theme_settings`, POST `/key/service-account/generate`, POST `/key/{key}/regenerate`, GET `/user/filter/ui`, 팀 쪽 11개(`/team/available`, `/team/metadata_schema`, `/get|update/default_team_settings`, `/team/permissions_list|update`, `/team/{id}/members/me`, `/team/{id}/member/{uid}/reset_spend|reset_budget`, `/management/v1/teams/{id}/members/bulk_update`), 사용자 쪽 6개(`/user/available_roles`, `/user/bulk_update`, `/team/bulk_member_add`, `/invitation/new`, `/get|update/internal_user_settings`), 모델 쪽 7개(`/v2/model/info`, `/v1/model/info`, PATCH `/model/{id}/update`, `/public/litellm_model_cost_map`, `/public/providers/fields`, `/health`, `/health/test_connection`), 모델 허브 5개(`/model_group/make_public`, `/model_hub/update_useful_links`, `/public/model_hub/info`, `/public/v1/model_hub`, `/public/v1/model_hub/{facet}`), 사용량 8개(`/user|team/daily/activity/aggregated` 묶음, `/user/daily/activity/aggregated/users`, `/team/spend/by_user`), 로그 3개(`/spend/logs/ui`, `/spend/logs/ui/{id}`, `/spend/logs/session/ui`), 추론 별칭(`/chat/completions`, `/responses`, `/embeddings`).


---

## 3. 부팅·로그인·home·change-password·ui-theme·api-reference·onboarding

### 부팅·레이아웃 (app/layout.tsx, D/layout.tsx — 모든 화면에서 먼저 불림)

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/litellm/.well-known/litellm-ui-config` | NW:588 (`getUiConfig`) ← contexts/AuthContext.tsx:61, D/hooks/uiConfig/useUIConfig.ts(`useAuthorized`, 로그인 화면) | 부팅·로그인·온보딩 전부 | MUST | 인증 없이 부른다(`fetch(url)`, 헤더 없음 NW:589). 읽는 필드: `server_root_path`·`proxy_base_url`·`gateway_base_url`(NW:593-595), `admin_ui_disabled`(D/hooks/useAuthorized.ts:25, app/login/LoginPage.tsx:90), `auto_redirect_to_sso`(LoginPage.tsx:137), `sso_configured`(LoginPage.tsx:322,358), `hide_default_credentials_hint`(LoginPage.tsx:239), `is_control_plane`·`workers`(LoginPage.tsx:117, hooks/useWorker.ts). 최소: `{"server_root_path":"/","proxy_base_url":null,"auto_redirect_to_sso":false,"admin_ui_disabled":false,"sso_configured":false,"is_control_plane":false,"workers":[]}`. `gateway_base_url` 를 주면 추론 SDK 호출(`/models`·플레이그라운드)이 그 주소로 간다(NW:245-250) | `response.ok` 를 보지 않고 `response.json()` 한다(NW:589-590). 본문이 JSON 이 아니면 AuthContext 는 무시하고 넘어가지만(AuthContext.tsx:62-64) `useUIConfig` 는 오류 상태가 된다. `admin_ui_disabled:true` 면 로그인 불가 |
| GET | `/ui/litellm-runtime-config.json` | NW:569 (`getUiConfig`) | 부팅 | STUB | UI 정적 파일. 빌드 시 `NEXT_PUBLIC` 기본 주소가 없을 때만 읽는다(NW:566). 404 면 `null` 로 넘어간다(NW:570). 응답한다면 `{"backend_url":"https://…"}` 만 읽는다(NW:573-579) | — |
| GET | `/management/v1/users/me` | D/hooks/currentUser/useCurrentUser.ts:12 ← D/layout.tsx:227, D/hooks/useAuthorized.ts:22 | 부팅(모든 대시보드 화면) | MUST | 스키마 `CurrentUserResponse` = `{"user_id":"…","user_email":…|null,"user_role":"proxy_admin"|…}` (lib/http/schema.d.ts:31218-31224). `user_role` 이 화면 권한을 정한다(useAuthorized.ts:23,45-48) | **위험: 실패(비 2xx)·빈 본문이면 쿠키를 지우고 로그인으로 돌려보낸다** (D/layout.tsx:250-254, useAuthorized.ts:25,32-35; 빈 본문은 useCurrentUser.ts:15 에서 예외). 대시보드 전체가 이 한 줄에 걸려 있다 |
| GET | `/api/plugins` | contexts/PluginModeContext.tsx:64 | 부팅 | STUB | `[]` (`Array.isArray(data) ? data : []`, PluginModeContext.tsx:66). 실패도 `.catch` 로 무시(:68) | — |
| GET | `/api/plugins/auth-token` | D/layout.tsx:60 | 부팅(플러그인 모드일 때만) | STUB | 플러그인 목록이 비면 불리지 않는다(layout.tsx:57 `activePluginName` 없음). 응답한다면 `{"session_claim":"…"}`. 실패 무시(:63) | — |
| GET | `/get/ui_settings` | NW:7196 (`getUiSettings`, 인증 헤더 없음) ← D/hooks/uiSettings/useUISettings.ts ← D/components/SidebarProvider.tsx:12, C/CommandPalette/useVisibleMenuGroups.ts:15, C/Navbar/ViewSwitcher.tsx:38; NW:3665(`getUISettings`, 인증 헤더 있음)도 같은 경로 | 부팅(사이드바 메뉴), api-keys·teams·models-and-endpoints·model-hub-table 등 | MUST | `{"values":{}}`. 모든 읽기가 `settings?.values?.x` 이고 값이 없으면 기본 동작(SidebarProvider.tsx:13-22, useVisibleMenuGroups.ts:16-27, C/Settings/AdminSettings/UISettings/UISettings.tsx:70) | 오류면 본문을 `response.json()` 으로 읽으므로(NW:7200-7203) 오류 본문도 JSON 이어야 메시지가 나온다. 404 여도 사이드바는 기본 메뉴로 뜬다 |
| GET | `/get/ui_theme_settings` | contexts/ThemeContext.tsx:37 | 부팅 | MUST | `{"values":{}}` 또는 `{"values":{"logo_url":null,"logo_url_dark":null,"favicon_url":null}}`. `data.values?.logo_url` 만 읽는다(ThemeContext.tsx:44-52). 인증 없이 부른다 | 404·오류는 `response.ok` 검사와 catch 로 무시(:43,54) |
| GET | `/get_favicon` | app/layout.tsx:18 (`metadata.icons`) | 부팅 | MUST | 이미지(`image/*`). 없으면 탭 아이콘만 빠진다 | — |
| GET | `/get_image` (`?theme=dark`, `?variant=monogram`) | C/leftnav.tsx:94, C/navbar.tsx:50-51 | 부팅(사이드바·상단바 로고) | MUST | 이미지. 테마의 `logo_url` 이 없을 때 쓴다(leftnav.tsx:695-697). 없으면 로고 이미지만 깨진다(어두운 로고만 onError 대체, :710) | — |
| GET | `/sso/get/ui_settings` | NW:3651 (`getProxyUISettings`) ← D/hooks/proxySettings/useProxySettings.ts:20 ← D/hooks/useLogout.ts, C/liteadmin/LiteAdmin.tsx:144, D/api-reference/page.tsx:10 등 | 부팅(로그아웃), api-reference, 여러 화면 | MUST | `{"PROXY_BASE_URL":"","PROXY_LOGOUT_URL":"","LITELLM_UI_API_DOC_BASE_URL":null}` (타입 useProxySettings.ts:5-11). 로그아웃 후 `PROXY_LOGOUT_URL` 로 이동(useLogout.ts), API 참조 화면은 `LITELLM_UI_API_DOC_BASE_URL`→`PROXY_BASE_URL` 순으로 예시 주소를 만든다. `DISABLE_EXPENSIVE_DB_QUERIES`, `NUM_SPEND_LOGS_ROWS` 도 읽는 곳이 있다(4~8장 표 참고) | 실패해도 `EMPTY_PROXY_SETTINGS` 로 대체(useProxySettings.ts:32-34) |
| POST | `/session/logout` | NW:1539 (`sessionLogoutCall`) ← D/hooks/useLogout.ts | 부팅(사이드바·상단 로그아웃), change-password | MUST | `{"message":"…"}`. 3초 시간 제한, 실패 무시(NW:1541, useLogout.ts `catch {}`) | — |
| GET | `/health/readiness/details` | D/hooks/healthReadiness/useHealthReadinessDetails.ts:19 ← C/leftnav.tsx:554, C/navbar.tsx, C/Navbar/NotificationsBell, C/SidebarAccountMenu | 부팅(버전 표시) | STUB | `{"status":"healthy","litellm_version":"…"}`. `healthData?.litellm_version` 만 읽는다(leftnav.tsx:558). 비 2xx 면 오류 상태로 두고 재시도 안 함(:55) | — |
| GET | `/get/latest_release_info` | D/hooks/latestRelease/useLatestReleaseInfo.ts:9 ← C/Navbar/NotificationsBell/NotificationsBell.tsx | 부팅(알림 종) | STUB | 404 권장(데이터 `undefined` → 알림 없음, NotificationsBell.tsx:54). 응답한다면 `LatestReleaseInfo` 전체(`version`·`release_url`·`new_features`·`bug_fixes`·`other_updates`, schema.d.ts:34041-34052) | 위험: `{}` 처럼 `version` 없는 객체를 주면 `isNewerVersion(current, undefined)` 로 간다(NotificationsBell.tsx:54, utils/versionUtils.ts:12). 404 가 안전 |
| GET | `/public/litellm_blog_posts` | D/hooks/blogPosts/useBlogPosts.ts:18 ← D/home/_components/HomePage.tsx:26, C/Navbar/BlogDropdown | 부팅(블로그 메뉴), home | STUB | `{"posts":[]}` (`blog.data?.posts ?? []`, HomePage.tsx:27). 인증 없음 | — |
| GET | `/health/deployment_warnings` | D/hooks/deploymentWarnings/useDeploymentWarnings.ts:16 ← C/DeploymentHealth/DeploymentHealth.tsx:27,55 | 부팅(proxy admin 계열만, :24-27) | STUB | `{"warnings":[]}` (`data?.warnings ?? []`, DeploymentHealth.tsx:30,56). 실패 시 재시도 안 함 | — |
| PATCH | `/health/deployment_warnings/{warning_id}` | useDeploymentWarnings.ts:48 ← C/DeploymentHealth/DeploymentHealthPanel.tsx | 부팅(경고 무시 버튼) | STUB | 경고가 없으면 불리지 않는다. 오류 응답 가능(onError 토스트, :55) | — |
| GET | `/get/user_banner` | NW:7248 (`getUserBanner`) ← D/hooks/userBanner/useUserBanner.ts ← C/UserBanner.tsx | 부팅 | STUB | `{"enabled":false,"message":"","severity":"info","revision":""}`. 404 도 안전(`!banner?.enabled` 이면 렌더 안 함, UserBanner.tsx:44) | 위험: `enabled:true` 인데 `message` 가 없으면 `banner.message.trim()` 에서 예외(UserBanner.tsx:44) |
| GET | `/health/license` | NW:6580 (`getLicenseInfo`) ← D/hooks/license/useLicenseInfo.ts ← C/SidebarUsageCard.tsx:71 | 부팅(사이드바 하단 카드), admin-panel | STUB | 404 → `null` 로 처리(NW:6587-6589) → 카드 숨김(SidebarUsageCard.tsx:77). 응답한다면 `{"has_license":false,"license_type":null,"expiration_date":null,"allowed_features":[],"limits":{"max_users":null,"max_teams":null}}` | — |
| GET | `/user/available_users` | NW:6540 (`getRemainingUsers`) ← C/SidebarUsageCard.tsx:50,72 | 부팅, admin-panel | STUB | 404 → `null`(NW:6549-6551) → 카드 숨김. 응답한다면 `{"total_users":null,"total_users_used":0,"total_users_remaining":null,"total_teams":null,"total_teams_used":0,"total_teams_remaining":null}` (SidebarUsageCard.tsx:56-60) | 404 외 오류는 throw(NW:6552-6555) — 카드만 영향 |
| GET | `/telemetry/settings` | D/hooks/telemetry/useTelemetrySettings.ts:20 ← D/telemetry/_components/TelemetryEnvBanner.tsx:27 (D/layout.tsx:204, 모든 화면 본문 위) | 부팅(proxy admin 계열만, useTelemetrySettings.ts:26), telemetry | STUB | **404 권장**(데이터 `undefined` → 배너 없음, TelemetryEnvBanner.tsx:28). 응답한다면 `TelemetrySettingsResponse` 를 채워야 한다: 최소 `{"environment_variables":[],"vetoed":false,"set_by_environment":false,…}` (schema.d.ts:46872-) | **위험: `{}` 를 주면 `settings.environment_variables.length` 에서 예외 → 레이아웃 본문이 깨진다**(TelemetryEnvBanner.tsx:8, 모든 화면) |
| PUT | `/telemetry/settings` | useTelemetrySettings.ts:37 | telemetry | STUB | telemetry 화면 저장. 오류 응답 가능 | — |
| GET | `/telemetry/ui_events/enabled` | lib/telemetry/uiEvents.ts:12 ← D/layout.tsx:120 (`recordUiEvent`, 화면 이동마다) | 부팅 | STUB | `{"enabled":false}` (`data?.enabled ?? false`). 404 도 catch(uiEvents.ts:24) | — |
| POST | `/telemetry/ui_events` | uiEvents.ts:22 | 부팅 | STUB | `enabled:false` 면 불리지 않는다 | — |

**부팅 때 함께 불리는 다른 소유 엔드포인트**(행은 해당 절): GET `/team/list`(v1, C/leftnav.tsx:551 `useTeams()` → D/networking.ts:11,13), GET `/organization/list`(C/leftnav.tsx:550 `useIsOrgAdmin` → D/hooks/organizations/useOrganizations.ts:25, JWT `premium_user===true` 일 때만), GET `/key/list`(C/CommandPalette/CommandPalette.tsx:65, 키 검색 범위를 고를 때만).

### 로그인 (app/login)

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/v2/login` | NW:7114 (`loginCall`, `useV3` 거짓) ← D/hooks/login/useLogin.ts ← app/login/LoginPage.tsx:159 | 로그인 | MUST | 본문 `{"username","password"}`, `credentials:"include"`. 응답 `{"redirect_url":"…","token":"<JWT>"}` — `token` 을 쿠키에 저장(NW:7158-7160, utils/cookieUtils.ts:86), `redirect_url` 로 이동(LoginPage.tsx:172). JWT 에서 읽는 필드: `key`(=UI 세션 키), `user_id`, `user_email`, `user_role`, `login_method`, `premium_user`, `auth_header_name`, `disabled_non_admin_personal_key_creation`, `password_reset_required`, `exp`(contexts/AuthContext.tsx:107-128, utils/jwtUtils.ts:4) | 오류 본문을 `response.json()` 으로 읽는다(NW:7131-7134). 오류 본문이 JSON 이 아니면 메시지 대신 파싱 오류가 화면에 뜬다 |
| POST | `/v3/login` | NW:7114 (`useV3` 참) | 로그인 | STUB | 컨트롤 플레인 워커를 골랐을 때만(LoginPage.tsx:159, `!!selectedWorker`). `workers:[]` 면 불리지 않는다. 응답 `{"code":"…"}` 또는 `{"token":"…","redirect_url":"…"}` | — |
| POST | `/v3/login/exchange` | NW:7141, NW:7176 (`exchangeLoginCode`) ← LoginPage.tsx:107 | 로그인 | STUB | 로그인 URL 에 `?code=` 가 있을 때만(LoginPage.tsx:97-113, 워커 SSO 복귀). 응답 `{"token":"…"}` | — |
| GET | `/sso/key/generate` | app/login/LoginPage.tsx:139 (`auto_redirect_to_sso`), :348 (SSO 버튼, `?return_to=`) | 로그인 | IMPLEMENTED | 브라우저 이동(`router.push`)이지 fetch 가 아니다. 버튼은 `sso_configured` 일 때만(LoginPage.tsx:322). 쿼리 이름이 두 가지다: `redirect_to`(:141), `return_to`(:347) — 계약 확인 필요 | — |

### D/home

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/global/spend/report` | C/liteadmin/operations.ts:318 | home·부팅(LiteAdmin 도우미) | STUB | LiteAdmin 은 LLM 이 도구로 고를 때만 부른다. 실패하면 "The gateway could not complete this lookup." 을 모델에 돌려준다(operations.ts:167-172) — 화면은 안 깨진다. 404 가능 | — |
| GET | `/team/spend/report` | C/liteadmin/operations.ts:324 | home·부팅(LiteAdmin) | STUB | 위와 같음 | — |
| GET | `/key/spend/report` | C/liteadmin/operations.ts:330 | home·부팅(LiteAdmin) | STUB | 위와 같음 | — |
| GET | `/v2/team/list` | C/liteadmin/operations.ts:229 | home·부팅(LiteAdmin) | IMPLEMENTED | LiteAdmin 도구. 팀 화면 쪽 호출은 4장 표 | — |
| POST | `/chat/completions` (`/v1` 없음) | C/liteadmin/agent.ts:95,118 (OpenAI SDK `chat.completions.runTools`, `baseURL` = `/sso/get/ui_settings` 의 `LITELLM_UI_API_DOC_BASE_URL`→`PROXY_BASE_URL`→관리 주소, LiteAdmin.tsx:144-148) | home·부팅(LiteAdmin) | MUST | 도구 호출(`tools`)이 있는 스트리밍 아님 채팅. SDK 가 `baseURL + "/chat/completions"` 로 보낸다 — 우리 서버의 `/v1/chat/completions` 와 경로가 다르다(`/v1` 없는 별칭 필요, 미확인: `PROXY_BASE_URL` 에 `/v1` 을 넣으면 피할 수 있음). LiteAdmin 은 home 화면 본문에도 들어간다(D/home/_components/HomePage.tsx:11 `LiteAdminHome`). 모델 목록은 GET `/model_group/info`(C/llm_calls/fetch_models.tsx:54, `data` 배열만 읽음 :55-56) | 실패해도 LiteAdmin 패널 안 오류로 끝난다 |
| (나머지 LiteAdmin 도구) | `/key/list`·`/key/info`·`/key/generate`·`/key/update`·`/key/delete`·`/key/block`·`/key/unblock`·`/team/info`(`key_limit=20`)·`/team/new`·`/team/update`·`/team/delete`·`/team/member_add`·`/team/member_update`·`/team/member_delete`·`/user/list`·`/v2/user/info`·`/user/new`·`/user/update`·`/user/delete`·`/management/v1/budgets`·`/budget/info`·`/budget/new`·`/budget/update`·`/budget/delete`·`/spend/logs/ui` | C/liteadmin/operations.ts:186-341 | home·부팅(LiteAdmin) | (각 소유 표) | 모두 LLM 도구 호출. 실패는 모델에 문자열로 돌아간다 | — |

home 화면 본문이 부르는 나머지(행은 각 소유 절): GET `/user/daily/activity/aggregated`(D/home/_components/useHomeUsage.ts:49, `ENTITY_API.user.aggregated`, 관리자는 전체·아니면 본인 `user_id`), GET `/gateway/daily/activity`(useHomeUsage.ts:58, 관리자만, 실패 시 `null`), GET `/v2/user/info`(useHomeUsage.ts:32 → D/hooks/users/useCurrentUser.ts, `max_budget` 만 읽음 :78), GET `/public/litellm_blog_posts`(위 표).

### D/change-password

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/user/password/change` | NW:1550 (`changePasswordCall`) ← D/change-password/ChangePasswordForm.tsx:47 | change-password | MUST | 본문 `{"current_password","new_password"}`. 응답 `{"user_id":"…","message":"…"}`(값은 읽지 않음). JWT `password_reset_required:true` 세션이면 성공 후 `/session/logout` 하고 로그인으로 보낸다(:48-55). 오류는 `extractProxyErrorMessage` 로 폼에 표시(:59) | — |

### D/ui-theme

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/get/ui_theme_settings` | D/ui-theme/UIThemeSettings.tsx:33 | ui-theme | MUST | 부팅 표와 같은 경로·모양(`data.values?.logo_url` 등 :43-48). 여기서는 인증 헤더를 붙인다 | — |
| PATCH | `/update/ui_theme_settings` | UIThemeSettings.tsx:59 (저장), :98 (초기화) | ui-theme | MUST | 본문 `{"logo_url","logo_url_dark","favicon_url"}`(값은 null 가능). `response.ok` 만 보고 본문은 읽지 않는다(:72,111) | — |

### D/api-reference

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/sso/get/ui_settings` | D/api-reference/page.tsx:10 (`useProxySettings`) | api-reference | MUST | 부팅 표 참고. 이 화면은 이것 하나만 부르고 나머지는 정적 예제 코드다(D/api-reference/_components/APIReferenceView.tsx) | — |

### app/onboarding (초대 링크로 비밀번호 설정)

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/onboarding/get_token?invite_link={id}` | NW:1484 (`getOnboardingCredentials`) ← app/onboarding/OnboardingForm.tsx:20 | onboarding | MUST | 인증 없음. 응답 `{"token":"<JWT>"}` — JWT 에서 `user_email`·`user_id`·`key` 를 꺼낸다(OnboardingForm.tsx:29-32). 사용자 화면의 초대(`/invitation/new`)와 한 묶음이다. 초대를 쓰지 않으면 STUB(404 → 오류 화면 `OnboardingErrorView`, :58) | 위험: `token` 이 JWT 가 아니면 `jwtDecode` 가 렌더 중 예외(OnboardingForm.tsx:29) |
| POST | `/onboarding/claim_token` | NW:1517 (`claimOnboardingToken`) ← OnboardingForm.tsx:38 | onboarding | MUST | 본문 `{"invitation_link","user_id","password"}`. 응답 `{"token":"<JWT>"}` 없으면 "Failed to start session"(:43-46) | — |

### 다른 화면에서 쓰는 3장 소유 엔드포인트

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/.well-known/litellm-ui-config` (`/litellm` 없음) | D/mcp-servers/_components/StdioAvailability.tsx:13 | mcp-servers | STUB | `/litellm/.well-known/litellm-ui-config` 와 같은 본문을 주면 된다. `?.mcp_stdio_enabled !== false` 만 읽는다 — `{}` 면 stdio 허용으로 본다 | — |
| GET | `/openapi.json` | NW:609 (`getOpenAPISchema`) ← C/common_components/check_openapi_schema.tsx:128 ← C/organisms/create_key_button.tsx:1860 | api-keys(키 생성 모달 "Optional Settings" 접힘 영역), 키 생성 모달을 쓰는 화면 | STUB | `{"components":{"schemas":{"GenerateKeyRequest":{"properties":{}}}}}` → 추가 필드 없이 빈 영역(check_openapi_schema.tsx:129,272-273). 실제 OpenAPI 를 내면 litellm 처럼 필드가 그려진다 | `components.schemas` 가 없으면 예외지만 try/catch 로 그 영역에 "Error: …" 문구만 뜬다(:129-145,268-269). `response.ok` 검사 없음(NW:610) |
| PATCH | `/update/ui_settings` | NW:7211 (`updateUiSettings`) ← D/hooks/uiSettings/useUpdateUISettings.ts ← C/Settings/AdminSettings/UISettings/UISettings.tsx:77 | admin-panel | STUB | 오류 응답 가능(호출부 onError 토스트, UISettings.tsx:83-85). 성공 본문은 읽지 않는다 | — |
| PATCH | `/update/user_banner` | NW:7252 (`updateUserBanner`) ← C/Settings/AdminSettings/UserBannerSettings/UserBannerSettings.tsx | admin-panel | STUB | 성공 시 `data.banner` 를 돌려준다(NW:7256) → `{"message":"…","banner":{"enabled":false,"message":"","severity":"info","revision":""}}`. 오류 응답 가능(호출부 처리 미확인) | — |

#### 추가 발견
- `/api/plugins`, `/api/plugins/auth-token`, `/get_favicon`, `/get_image`, `/chat/completions`(LiteAdmin) 은 1차 스캔에서 빠졌던 것을 위 표에 넣었다.

#### 미확인
- LiteAdmin 이 `/v1` 없는 `/chat/completions` 를 부르는지는 SDK 규칙(`baseURL + "/chat/completions"`)과 `resolveInferenceTarget`(C/liteadmin/agent.ts:36-58)이 경로를 덧붙이지 않는다는 점으로 판단했다. 실제 요청은 브라우저로 확인하지 않았다.
- `/sso/key/generate` 의 `redirect_to` 와 `return_to` 를 우리 서버가 둘 다 받는지.

---

## 4. api-keys·teams·users (키·팀·사용자 폼의 공통 드롭다운 포함)

이 묶음의 줄 번호는 I·J 문서가 인용한 줄과 일치함을 표본 확인했다(C/Teams.tsx:290,308,322,386,558, C/team/TeamInfo.tsx:728,762,777,802,831,961, NW:1178,1187,1208,1228). 따라서 I·J 의 계약을 그대로 참조한다.

**렌더 중 예외(화면 전체가 깨짐)와 try/catch 안 예외(콘솔만)를 구분한다.** 아래 "위험" 열은 렌더 중 예외 또는 저장 차단만 적었다. try/catch 안에서 `.map` 이 터지는 것은 드롭다운이 빌 뿐이므로 비고에만 적었다.

### D/api-keys

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/key/list` | D/hooks/keys/useKeys.ts:84 (주 표), NW:2455 `keyListCall` ← D/agents/_components/add_agent_form.tsx:188 | api-keys, teams(키 탭 C/team/TeamVirtualKeysTable.tsx), agents·policies 폼 | IMPLEMENTED | 계약 확인 필요: 쿼리(`return_full_object`, `include_team_keys`, `expand=user`, 정렬, `status=deleted`)와 `{"keys","total_count","current_page","total_pages"}` 모양은 J 3.6 | — |
| GET | `/key/info` | NW:2411 `keyInfoV1Call` ← D/hooks/keys/useKeyInfo.ts:31 | api-keys 키 상세, logs(C/logs/request/RequestLogsPanel.tsx:126), usage(TopKeyView.tsx:73) | IMPLEMENTED | J 3.5 | — |
| POST | `/key/generate` | NW:952 `keyCreateCall` ← C/organisms/create_key_button.tsx:482 | api-keys 생성 모달(+ playground AgentBuilderView.tsx:408, admin-panel C/SCIM.tsx:67) | IMPLEMENTED | 응답 `key` 를 읽어 표시(create_key_button.tsx:493), 목록에 `addKey(response)`. J 3.1 | — |
| POST | `/key/service-account/generate` | NW:894 `keyCreateServiceAccountCall` ← create_key_button.tsx:481 | api-keys 생성 모달(소유자 "Service Account") | MUST | 응답 모양은 `/key/generate` 와 같다(J 3.2). 없으면 이 소유자 선택만 실패(토스트) | — |
| POST | `/key/update` | NW:2780 `keyUpdateCall` ← C/templates/key_info_view.tsx:398 | api-keys 키 상세 편집 | IMPLEMENTED | 응답을 새 키 값으로 씀(key_info_view.tsx:398). J 3.3 | — |
| POST | `/key/delete` | NW:1080 `keyDeleteCall` ← key_info_view.tsx:419 | api-keys | IMPLEMENTED | 본문 `{keys:[token]}`. J 3.4 | — |
| POST | `/key/block` · `/key/unblock` | D/hooks/keys/useSetKeyBlockedState.ts:23 | api-keys | IMPLEMENTED | J 3.8 | — |
| POST | `/key/{key}/regenerate` | NW:1562 `regenerateKeyCall` ← C/organisms/RegenerateKeyModal.tsx:98 (C/chat/KeysPanel.tsx:142) | api-keys 키 상세 | MUST | 우리 목록에 없음. 대시보드는 `premiumUser` 가 아니면 버튼을 막는다(J 2.5, key_info_view.tsx:599). 응답 `key`=새 평문(J 3.9) | — |
| POST | `/key/{key}/reset_spend` | D/hooks/keys/useResetKeySpend.ts:20 | api-keys 키 상세 | IMPLEMENTED | J 3.10 | — |
| GET | `/budget/list` | D/hooks/budgets/useBudgetOptions.ts:9,14 ← C/key_team_helpers/EndUserBudgetSelect.tsx:36 ← create_key_button.tsx:1145, key_edit_view.tsx:516 | api-keys 생성·편집("Default Customer Budget") | STUB | **맨 배열 `[]`**. `(budgets ?? []).map` (EndUserBudgetSelect.tsx:37) | 위험: 객체(`{"data":[]}`)를 주면 렌더 중 `.map is not a function` (EndUserBudgetSelect.tsx:37). 404 면 `data` undefined 라 안전 |
| GET | `/project/list` | D/hooks/projects/useProjects.ts:49 (`canReadProjects` 일 때, :77) ← create_key_button.tsx:234, C/templates/KeyProjectField.tsx:19, key_info_view.tsx:111 | api-keys 생성·상세, projects | STUB | **맨 배열 `[]`**. `projects?.find(...)` (create_key_button.tsx:515,558; KeyProjectField.tsx:20) | 위험: 객체를 주면 렌더 중 `.find is not a function` (KeyProjectField.tsx:20). 404 면 throw → query error, `projects` undefined 라 안전 |
| GET | `/prompts/list` | NW:4568 `getPromptsList` ← create_key_button.tsx:362, key_edit_view.tsx:184 | api-keys(`canViewPrompts` 일 때), prompts | STUB | `{"prompts":[]}`. `response.prompts.map` 은 try 안(create_key_button.tsx:363) | — |
| GET | `/user/filter/ui` | NW:1931 `userFilterUICall` ← create_key_button.tsx:593 (소유자=다른 사용자 검색), C/common_components/user_search_modal.tsx:98 (팀 멤버 추가) | api-keys, teams | MUST | **맨 배열** `[{"user_id","user_email"}]` (I 3.2). `data.map` (create_key_button.tsx:596)은 try 안 | — |

### D/teams

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v2/team/list` | D/hooks/teams/useTeams.ts:77 (`teamListCall`) ← :113 `useTeamsTable`, :209 `useInfiniteTeams`; 삭제 팀 :256 `status=deleted`; C/liteadmin/operations.ts:229 | teams 목록 표·CSV, 사용자 생성 팀 드롭다운 | IMPLEMENTED | I 2.1 | — |
| GET | `/team/list` (v1) | NW:1208 `teamListCall` ← D/networking.ts:11,13 ← D/hooks/teams/useTeams.ts:125 | users(팀 필터·일괄 편집), 사용자 상세 "팀에 추가" | IMPLEMENTED | **맨 배열** (policies add_attachment_form.tsx:135 주석도 같음). H 참조 | — |
| GET | `/team/info` | NW:1187 `teamInfoCall` ← C/team/TeamInfo.tsx:728; useTeams.ts:174; D/hooks/teams/useTeamMemberBudgets.ts:35 | teams 상세, 모델 선택, users 상세 | IMPLEMENTED | `team_info.organization_id`·`policies`·`access_group_ids`·`object_permission` 을 읽음(TeamInfo.tsx:756,789,1133,1156) | — |
| POST | `/team/new` | NW:2590 ← C/Teams.tsx:558 | teams | IMPLEMENTED | H | — |
| POST | `/team/update` | NW:2812 ← TeamInfo.tsx:944 | teams 상세 | IMPLEMENTED | H | — |
| POST | `/team/delete` | NW:1098 ← C/Teams.tsx:386 | teams | IMPLEMENTED | H | — |
| POST | `/team/member_add` | NW:2899 ← TeamInfo.tsx:831, C/team/AvailableTeamsPanel.tsx:52, D/users/_components/view_users/user_info_view.tsx:205 | teams, users | IMPLEMENTED | H | — |
| POST | `/team/member_update` | NW:3002 ← TeamInfo.tsx:879 | teams | IMPLEMENTED | H | — |
| POST | `/team/member_delete` | NW:3079 ← TeamInfo.tsx:917, user_info_view.tsx:245 | teams, users | IMPLEMENTED | H | — |
| GET | `/team/available` | NW:1228 `availableTeamListCall` ← C/team/AvailableTeamsPanel.tsx:28 | teams "Available Teams" 탭(C/Teams.tsx:654-657) | MUST | **맨 배열 `[]`** 을 그대로 `setAvailableTeams(response)` (AvailableTeamsPanel.tsx:30). 가입 가능 팀 설정이 없으면 `[]` 가 litellm 과 같다(I 2.2) | 위험: 객체를 주면 표 데이터가 배열이 아니어서 렌더 실패 가능(AvailableTeamsPanel.tsx:30, 표 렌더 미확인). 404 는 catch(:32) |
| GET | `/team/metadata_schema` | D/hooks/teams/useTeamMetadataSchema.ts:20 ← C/Teams.tsx:221, TeamInfo.tsx:651 | teams 생성·편집 | MUST | `{"fields":[]}`. 배열이 아니면 `[]` 로 보고(useTeamMetadataSchema.ts:21) 별도 무음 클라이언트(:14-17)라 404 도 안전 | — |
| GET | `/get/default_team_settings` | NW:5489 ← C/Teams.tsx:290(모달 열 때, `retry:false`), C/TeamSSOSettings.tsx:131 | teams 생성 모달 placeholder, "Default Team Settings" 탭(proxy admin) | MUST | `{"values":{}}` 로 충분. `data.values || {}` 를 기본값과 합침(TeamSSOSettings.tsx:132), `values?.budget_duration`(Teams.tsx:296). 정식 모양 I 4.1 | — |
| PATCH | `/update/default_team_settings` | NW:5499 ← TeamSSOSettings.tsx:152 | teams 설정 탭 저장 | MUST | 응답 `settings || {}` 를 다시 읽음(TeamSSOSettings.tsx:153). 실패는 토스트(:160) | — |
| GET | `/team/permissions_list` | NW:5510 `getTeamPermissionsCall` ← C/team/member_permissions.tsx:28 | teams "Member Permissions" 탭(TeamInfo.tsx:1547) | MUST | `{"team_member_permissions":[],"all_available_permissions":[]}`. 둘 다 `|| []` (member_permissions.tsx:29,31). 실패는 토스트(:35). 정식 값 I 2.4 | — |
| POST | `/team/permissions_update` | NW:5538 ← member_permissions.tsx:58 | 같은 탭 저장 | MUST | 응답을 읽지 않음(I 2.5) | — |
| GET | `/team/{team_id}/members/me` | C/team/useMyTeamMember.ts:31 ← C/team/MyUserTab.tsx:34 | teams "My User" 탭(TeamInfo.tsx:1521) | MUST | **404 면 `null` 로 빈 상태**(useMyTeamMember.ts:46-48). 그 밖의 실패 코드는 탭 오류. 정식 모양 I 2.6 | 위험: 멤버 아님을 404 가 아닌 다른 코드(403 등)로 주면 탭 오류(useMyTeamMember.ts:50-52) |
| POST | `/team/{team_id}/member/{user_id}/reset_spend` | D/hooks/teams/useResetTeamMemberSpend.ts:10 ← C/team/TeamMemberTab.tsx:276 | teams 멤버 탭 | MUST | I 2.7 | — |
| POST | `/team/{team_id}/member/{user_id}/reset_budget` | D/hooks/teams/useResetTeamMemberBudget.ts:10 ← TeamMemberTab.tsx:291 | teams 멤버 탭 | MUST | I 2.8 | — |
| POST | `/management/v1/teams/{team_id}/members/bulk_update` | C/team/TeamInfo.tsx:961 | teams 상세(팀 기본 멤버 예산 변경 후 개별 예산 해제) | MUST | `{"data":[]}` 이면 안전: `data?.data ?? []` (TeamInfo.tsx:965). 정식 I 2.9 | — |
| POST | `/budget/info` | C/TeamsPage/teamsCsvExport.ts:91 | teams CSV 내보내기 | STUB | 팀 `metadata.team_member_budget_id` 가 있을 때만 호출(:89-90). 우리 팀엔 없으므로 사실상 미호출. 주면 **맨 배열 `[]`** | — |
| GET | `/organization/info` | NW:1259 `organizationInfoCall` ← TeamInfo.tsx:762; D/hooks/organizations/useOrganizations.ts:41 | teams 상세 | STUB | 팀에 `organization_id` 가 있을 때만(TeamInfo.tsx:756), `useOrganization` 은 premium 일 때만(useOrganizations.ts:35). 404 → catch 후 null(TeamInfo.tsx:764-766) | — |
| GET | `/policy/info/{policy_name}` | NW:4075 ← TeamInfo.tsx:802, C/templates/key_info_view.tsx:165 | teams·api-keys 상세 | STUB | 팀/키에 `policies` 가 있을 때만. 404 → `[]` 로 처리(TeamInfo.tsx:804-806). 주면 `{"resolved_guardrails":[]}` | — |
| GET | `/config/pass_through_endpoint` · `/config/pass_through_endpoint/team/{team_id}` | NW:3409-3415 `getPassThroughEndpointsCall` ← C/common_components/PassThroughRoutesSelector.tsx:43 (C/Teams.tsx:1126, TeamInfo.tsx:2135, key_edit_view.tsx:733, create_key_button.tsx:1519) | teams·api-keys 폼; models-and-endpoints Pass-Through 패널(C/PassThroughSettings/PassThroughSettings.tsx:54) | STUB | `{"endpoints":[]}`. 선택기는 `if (response.endpoints)` 로 막음(PassThroughRoutesSelector.tsx:44). **팀 경로는 스캔이 `/team/{teamId}` 로 잘못 잡았다. 실제는 `/config/pass_through_endpoint/team/{team_id}`**(NW:3411-3412) | 위험: Pass-Through 패널은 `data["endpoints"]` 를 그대로 표 데이터로 넣음(PassThroughSettings.tsx:55 → C/PassThroughSettings/PassThroughEndpointsTable.tsx:43). `endpoints` 가 없으면 `undefined` 가 표로 감 |
| POST | `/config/pass_through_endpoint` | NW:3459 `createPassThroughEndpoint` | models-and-endpoints Pass-Through 추가 | STUB | 오류 응답 가능(호출부 catch·토스트는 미확인) | — |
| DELETE | `/config/pass_through_endpoint?endpoint_id={id}` | NW:3509-3513 | 같은 패널 | STUB | 오류 응답 가능 | — |
| GET | `/router/settings` | NW:3318 `getRouterSettingsCall` ← C/common_components/RouterSettingsAccordion.tsx:151 (C/Teams.tsx:40, TeamInfo.tsx:114, key_edit_view.tsx:24, create_key_button.tsx:47) | teams·api-keys 폼의 라우터 설정 아코디언; router-settings, models-and-endpoints | STUB | `{"fields":[],"routing_strategy_descriptions":{}}`. `if (data.fields)` 로 막음(:152) | 비고: `.then` 에 catch 가 없어 404 면 처리 안 된 Promise 거부(콘솔). 화면은 그대로 |
| GET | `/policies/list` | NW:3978 ← C/Teams.tsx:322, TeamInfo.tsx:777, create_key_button.tsx:352, C/policies/PolicySelector.tsx:65 | teams·api-keys(`canViewPolicies` 일 때), playground, policies | STUB | `{"policies":[]}`. `response.policies.map` 은 try 안(Teams.tsx:323) | — |
| GET | `/v2/guardrails/list` | NW:3825 `getGuardrailsList` (실패 시 `/guardrails/list` 로 재시도 NW:3842) ← C/Teams.tsx:308, create_key_button.tsx:342, D/hooks/guardrails/useGuardrails.ts:38 ← TeamInfo.tsx:627 | teams·api-keys·users 폼, playground, guardrails, policies, projects | STUB | `{"guardrails":[]}`. v2 가 404 면 매번 v1 을 한 번 더 부른다(NW:3834-3842). v2 도 같은 빈 값을 주면 호출이 반으로 준다 | — |
| GET | `/guardrails/list` | NW:3842 (v2 실패 시만) | 위와 같음 | IMPLEMENTED(빈 값) | 계약 확인 필요: 반드시 `{"guardrails":[]}` 여야 함. `response.guardrails.map` (Teams.tsx:309, create_key_button.tsx:343)은 try 안, `data?.guardrails ?? []` (useGuardrails.ts:41) | — |
| GET | `/v1/access_group` | D/hooks/accessGroups/useAccessGroups.ts:20 (admin 역할일 때, :48) ← C/common_components/AccessGroupSelector.tsx:35 (Teams.tsx:30, TeamInfo.tsx:65, key_edit_view.tsx:18), TeamInfo.tsx:646 | teams·api-keys 폼, agents, access-groups | STUB | **맨 배열 `[]`** | 위험: 객체를 주면 렌더 중 `(accessGroups ?? []).map` 실패(AccessGroupSelector.tsx:52). 404 이고 본문이 JSON 이 아니면 `response.json()` 이 throw(useAccessGroups.ts:31) → isError → **TeamInfo 의 MCP 도구 권한 저장이 막힘**(TeamInfo.tsx:660-668,1162-1168, 도구 권한을 제출했을 때만) |
| POST | `/v1/access_group` | D/access-groups/_components/access-group-create/AccessGroupCreateDialog.tsx:32 | access-groups | STUB | 오류 응답 가능(생성 실패) | — |
| GET | `/v1/mcp/server` | NW:4869 `fetchMCPServers` ← D/hooks/mcpServers/useMCPServers.ts:14 ← C/mcp_server_management/MCPServerSelector.tsx:55, MCPToolPermissions.tsx:71, TeamInfo.tsx:644, user_info_view.tsx:119, key_info_view.tsx:114 | teams·api-keys·users 폼, playground, mcp-servers, chat | IMPLEMENTED(빈 값) | 계약 확인 필요: **맨 배열 `[]`** 이어야 함(`useQuery<MCPServer[]>`, useMCPServers.ts:12). 쿼리 `team_id`, `user_id`, `connected_app_view` 를 보낸다(NW:4871-4875) | 위험: 객체를 주면 `serversWithinAccessGroups(mcpServers, …)` 와 `.map` 이 렌더 중 실패(MCPServerSelector.tsx:61,71). 실패(isError)면 TeamInfo 도구 권한 저장 차단(TeamInfo.tsx:663) |
| POST · PUT | `/v1/mcp/server` | NW:4935 `createMCPServer`, NW:4960 `updateMCPServer` | mcp-servers | STUB | 오류 응답 가능 | — |
| GET | `/v1/mcp/toolset` | NW:4978 `fetchMCPToolsets` ← D/hooks/mcpServers/useMCPToolsets.ts:13 ← MCPServerSelector.tsx:57, TeamInfo.tsx:645 | teams·api-keys·users 폼, playground(`.catch(() => [])`), mcp-servers | STUB | **맨 배열 `[]`** | 위험: 객체를 주면 렌더 중 `toolsets.map` 실패(MCPServerSelector.tsx:76). 404 면 isError → TeamInfo 도구 권한 저장 차단(TeamInfo.tsx:664) |
| POST · PUT | `/v1/mcp/toolset` | NW:4987, NW:4996 | mcp-servers | STUB | 오류 응답 가능 | — |
| GET | `/v1/mcp/access_groups` | NW:4900 `fetchMCPAccessGroups` ← D/hooks/mcpServers/useMCPAccessGroups.ts:11 ← MCPServerSelector.tsx:56, MCPToolPermissions.tsx:72 | teams·api-keys·users 폼 | STUB | `{"access_groups":[]}`. 함수가 `data.access_groups || []` 로 배열을 만든다(NW:4902) | — |
| GET | `/mcp-rest/tools/list?server_id={id}` | NW:5224 `listMCPTools` ← C/mcp_server_management/MCPToolPermissions.tsx:120 | teams·api-keys 도구 권한, playground, mcp-servers | STUB | 서버가 선택됐을 때만 부름 → 서버 목록이 비면 미호출. 주면 `{"tools":[]}`. 함수가 throw 하지 않는다(mcp_tools.tsx:162 주석) | — |
| GET | `/v1/agents` | NW:6105 `getAgentsList` ← C/agent_management/AgentSelector.tsx:41 | teams·api-keys 폼, playground(D/playground/llm_calls/fetch_agents.tsx:44), agents | STUB | **맨 배열 `[]`**. `data.map(toAgent)` (NW:6110) | 비고: 객체를 주면 `data.map` 실패지만 AgentSelector 는 try 안(:54). 다른 호출부는 7장 범위 |
| POST | `/v1/agents` | NW:4694 `createAgentCall` | agents | STUB | 오류 응답 가능 | — |
| GET | `/vector_store/list` | NW:5629 ← C/vector_store_management/VectorStoreSelector.tsx:31, C/permissions/VectorStorePermissions.tsx:25 | teams·api-keys 폼, playground, vector-stores | STUB | `{"data":[]}`. `if (response.data)` (VectorStoreSelector.tsx:32), `response.data || []` (D/vector-stores/_components/index.tsx:52) | — |
| GET | `/search_tools/list` | NW:5123 ← C/search_tools/SearchToolSelector.tsx:44 | teams 폼, admin-panel, search-tools | STUB | `{"search_tools":[]}`. 배열 검사 후 읽음(SearchToolSelector.tsx:45-49), `res.search_tools || []` (D/search-tools/_components/SearchTools.tsx:58) | — |
| GET | `/claude-code/plugins?enabled_only=` | NW:7270 `getClaudeCodePluginsList` ← C/skills/SkillSelector.tsx:60 (Teams.tsx:57, TeamInfo.tsx:112, KeyEditViewControls.tsx:12, create_key_button.tsx:31), C/AIHub/ModelHubTable.tsx:274 | teams·api-keys 폼, model-hub-table 스킬 탭, skills | STUB | `{"plugins":[],"count":0}`. 빈 본문이면 throw(NW:7274). SkillSelector 는 배열 검사(SkillSelector.tsx:33-34) | 위험: `plugins` 가 없으면 `setSkillHubData(response.plugins)` 로 undefined 가 들어감(ModelHubTable.tsx:275,671,1221; D/skills/_components/ClaudeCodePluginsPanel.tsx:47). 스킬 표가 undefined 를 견디는지 미확인 |
| POST | `/claude-code/plugins` | NW:7287 `registerClaudeCodePlugin` | skills | STUB | 오류 응답 가능(409 모양은 7장 범위) | — |
| GET | `/organization/list` | NW:1244 ← D/hooks/organizations/useOrganizations.ts:24 (premium 일 때만, :26) ← C/Teams.tsx:219, TeamInfo.tsx:650, C/TeamSSOSettings.tsx:121, C/VirtualKeysPage/VirtualKeysTable.tsx:92, C/CreateUserButton.tsx:168; C/key_team_helpers/filter_helpers.ts:168 | teams·api-keys·users, organizations, usage | STUB | **맨 배열 `[]`** (I 4.7). JWT `premium_user:false` 면 훅은 아예 부르지 않는다 | 위험: filter_helpers.ts:171 은 `[...allOrganizations, ...response]` 로 펼침 → 객체를 주면 TypeError(호출부 try 여부는 :162 의 try 안이라 목록만 빔) |

### D/users

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/user/list` | NW:1122 `userListCall` ← D/users/_components/view_users.tsx:245; D/hooks/users/useUsers.ts:22,50,64 | users 표, 사용자 드롭다운(C/common_components/UserDropdown.tsx) | IMPLEMENTED | `data?.users ?? []`, `data?.total ?? 0` (view_users.tsx:275-276). H 참조 | — |
| GET | `/v2/user/info` | NW:1178 `userGetInfoV2` ← user_info_view.tsx:130,209; D/hooks/users/useCurrentUser.ts:13; D/hooks/keys/useKeyInfo.ts:11 | users 상세, 모델 선택, api-keys 키 소유자 | IMPLEMENTED | I 3.1 | — |
| POST | `/user/new` | NW:1049 ← C/CreateUserButton.tsx:209, C/bulk_create_users_button.tsx:367 | users | IMPLEMENTED | H | — |
| POST | `/user/update` | NW:3179 ← user_info_view.tsx:331 | users 상세 | IMPLEMENTED | H | — |
| POST | `/user/delete` | NW:1089 ← view_users.tsx:179, user_info_view.tsx:301 | users | IMPLEMENTED | 본문 `{user_ids:[…]}` (NW:1089) | — |
| GET | `/user/available_roles` | NW:2565 `getPossibleUserRoles` ← view_users.tsx:269, create_key_button.tsx:384 | users(역할 열·역할 드롭다운), api-keys(서비스 계정/역할) | MUST | 역할→`{ui_label,description}` 객체(I 3.3). `{}` 를 주면 역할 열이 `-` (UsersTableColumns.tsx:160, 옵셔널 체이닝). create_key_button 은 결과를 sessionStorage 에 캐시(:380-386) | — |
| POST | `/user/bulk_update` | NW:3222 ← D/users/_components/BulkEditUsers.tsx:137,140 | users 일괄 편집 | MUST | I 3.4 | — |
| POST | `/team/bulk_member_add` | NW:2946 `teamBulkMemberAddCall` ← BulkEditUsers.tsx:163 | users 일괄 편집 "팀에 추가" | MUST | 응답 `successful_additions`, `failed_additions` 를 읽음(BulkEditUsers.tsx:173-174), 팀마다 try(:177) | — |
| POST | `/invitation/new` | NW:839 ← view_users.tsx:165(비밀번호 재설정 링크), user_info_view.tsx:289, C/CreateUserButton.tsx:221(SSO 꺼짐일 때 자동), bulk_create_users_button.tsx:378 | users | MUST | 응답으로 초대 링크 모달을 띄움(view_users.tsx:166-167). 모양 I 3.5. 링크 목적지 `/ui/onboarding` 의 `/onboarding/*` 는 3장 | — |
| GET | `/get/internal_user_settings` | D/users/_components/default-user-settings/DefaultUserSettingsForm.tsx:54 | users "Default User Settings"(proxy admin, view_users.tsx:396) | MUST | `{"values":{}}`. I 4.2 | — |
| PATCH | `/update/internal_user_settings` | DefaultUserSettingsForm.tsx:62 | 같은 폼 저장 | MUST | I 4.2 | — |
| GET | `/models` (gateway 기본 URL) | NW:1853 `modelAvailableCall` (gatewayApiClient) ← D/hooks/models/useModels.ts:257,267,279,303; view_users.tsx:97; user_info_view.tsx:156; C/CreateUserButton.tsx:178; C/key_team_helpers/fetch_available_models_team_key.tsx:14; create_key_button.tsx:189,212; key_edit_view.tsx:168 | 모든 모델 드롭다운(api-keys·teams·users·models-and-endpoints·playground 등) | IMPLEMENTED | **계약 확인 필요**: 대시보드는 `include_model_access_groups=True` 를 **항상** 보낸다(NW:1855). 그 밖에 `return_wildcard_routes=True`(useModels.ts:257, fetch_available_models_team_key.tsx:14, create_key_button.tsx:189), `team_id`(create_key_button.tsx:189, useModels.ts:303, C/llm_calls/fetch_models.tsx:41), `only_model_access_groups=True`(useModels.ts:279, C/add_model/AddModelForm.tsx:163), `scope=expand`(useModels.ts:257; litellm 은 다른 값이면 400). 값은 `"True"` 문자열. 읽는 것은 `data[].id` 뿐(I 4.3). 요청은 관리 API 가 아니라 `getGatewayBaseUrl` 로 간다(NW:536-540) | — |

### D/budgets (행은 4장에 적음. 화면은 budgets)

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/management/v1/budgets?page=&page_size=&…` | D/hooks/budgets/useBudgets.ts:19,31 ← D/budgets/_components/BudgetPanel.tsx:42; C/liteadmin/operations.ts:294 | budgets | STUB | `{"data":[],"links":{"first":"","last":""},"meta":{"page":1,"page_size":50,"total_count":0,"total_pages":0}}`. 읽기는 `data?.data ?? []`, `data?.meta.total_count ?? 0` (D/hooks/common/useResourceList.ts:121,125) | 위험: `meta` 가 없으면 `data?.meta.total_count` 가 렌더 중 TypeError(useResourceList.ts:125). `{"data":[]}` 만 주면 깨짐 |
| POST | `/budget/new` | NW:801 ← useBudgets.ts(`useCreateBudget`) ← D/budgets/_components/BudgetModal.tsx:16 | budgets | STUB | 오류 응답 가능(mutation 오류 처리는 미확인) | — |
| POST | `/budget/update` | NW:820 ← D/budgets/_components/EditBudgetModal.tsx:29 | budgets | STUB | 오류 응답 가능(미확인) | — |
| POST | `/budget/delete` | NW:783 ← BudgetPanel.tsx:43 | budgets | STUB | 오류 응답 가능(미확인) | — |

- 위 budgets·key·team·user 경로가 스캔에서 `(boot)`·home 으로도 잡힌 것은 C/liteadmin/operations.ts(관리 도우미 패널의 도구 실행) 때문이다. 화면이 뜰 때 부르는 것이 아니라 사용자가 도우미에게 시킬 때만 부른다(operations.ts:188-337).

#### 추가 발견

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/config/pass_through_endpoint/team/{team_id}` | NW:3411-3412 ← PassThroughRoutesSelector.tsx:43 (`teamId` 가 있을 때) | teams 상세·api-keys 폼 | STUB | `{"endpoints":[]}`. 스캔의 `/team/{teamId}` 행은 이 경로의 조각이다 | — |
| GET | `/models` | 위 D/users 표 | 전 화면 | IMPLEMENTED | 스캔 누락(부모 지시로 추가) | — |

#### 미확인
- `/budget/new|update|delete` 실패 시 budgets 모달이 토스트로 끝나는지(useBudgets.ts 의 mutation `onError` 를 읽지 않음).
- C/team/AvailableTeamsPanel 표가 배열이 아닌 값을 받으면 렌더 중 깨지는지(표 구현을 읽지 않음).
- ModelHubTable·ClaudeCodePluginsPanel 의 스킬 표가 `undefined` 를 견디는지.
- `/config/pass_through_endpoint` POST·DELETE 호출부의 오류 처리.
- `/v1/mcp/server` 의 현재 우리 빈 응답이 맨 배열인지 객체인지(우리 구현 확인 필요). `/guardrails/list` 빈 응답이 `{"guardrails":[]}` 인지도 같다.

---

## 5. models-and-endpoints·model-hub-table (자격 증명·헬스·비용 맵·자동 라우터 포함)

약어: NW=`components/networking.tsx`, D/=`app/(dashboard)/`, C/=`components/`. 경로는 `gateway/ui/litellm-dashboard/src/` 기준.

공통 관찰
- NW 의 모델·비용 맵·헬스 함수 다수는 `apiClient` 가 아니라 `fetch` 를 직접 쓴다. 그래서 `!response.ok` 검사가 함수마다 다르다. `modelCostMap`(NW:615-630), `getPublicModelHubInfo`(NW:601-606), `makeModelGroupPublic`(NW:547-560), `reloadModelCostMap`·`schedule…`(NW:632-688)는 상태 코드를 보지 않고 `response.json()` 을 그대로 돌려준다. 이 함수들에 404 를 주면 본문이 JSON 일 때는 그 본문을 정상 데이터로 받고, HTML 이면 `json()` 에서 예외가 난다. STUB 은 404 보다 **모양을 갖춘 200 JSON** 이 안전하다.
- `/public/*` 경로는 인증 헤더 없이 부른다(NW:458, 617, 602, 1720-1765, C/publicModelHub/usePublicModelHubList.ts:42). 우리 서버에서 인증 없이 열어 두어야 한다.

### D/models-and-endpoints

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v2/model/info` | NW:1613 (`modelInfoCall`) ← D/hooks/models/useModels.ts:73 `useModelsInfo` ← D/models-and-endpoints/components/AllModelsTab.tsx:142, useModelDashboardData.ts:12(page.tsx:38), panels/HealthStatusPanel.tsx:22, C/model_info_view.tsx:96. `useDeployments`(useModels.ts:216, size=1000 으로 `total_pages` 만큼 반복, :172-190) ← AutoRoutersPanel.tsx:39, C/shared/table_cells/AutoRouterTag.tsx:15(← C/logs/detail/DrawerHeader.tsx:165) | models-and-endpoints(모델 표·상세·헬스·자동 라우터 탭), logs(로그 상세 헤더), cost-optimization | MUST | 우리 서버에 없음. 쿼리: `include_team_models=true&page&size` 와 선택적 `search, model, modelId, teamId, sortBy, sortOrder, exclude_auto_routers, access_group, wildcard_only, blocked`(NW:1614-1651). 응답 `{"data":[{"model_name","litellm_params":{"model",…},"model_info":{"id","access_groups","db_model",…}}],"total_count","current_page","total_pages","size"}`(useModels.ts:19-25). 실패하면 토스트를 띄우고 예외(NW:1660-1675) | 위험: 행마다 `litellm_params` 객체가 있어야 한다. `litellmParams.model` 을 옵셔널 체이닝 없이 읽는다(D/models-and-endpoints/utils/modelDataTransformer.ts:47,52). `model_info.id` 도 마찬가지(AllModelsTab.tsx:258) |
| GET | `/v1/model/info?litellm_model_id={id}` | NW:1693 (`modelInfoV1Call`) ← C/model_info_view.tsx:193 | models-and-endpoints(모델 상세. `/v2/model/info` 결과가 아직 없을 때만 부르는 대체 경로) | MUST | `{"data":[<행 1개>]}`. 실패하면 예외(NW:1703-1708) | 위험: `modelInfoResponse.data[0]`(C/model_info_view.tsx:194). `data` 가 없으면 TypeError 가 처리되지 않은 rejection 으로 남는다(useEffect 안, try 없음) |
| POST | `/model/new` | NW:742 ← C/add_model/handle_add_model_submit.tsx:228, C/add_model/handle_add_auto_router_submit.tsx:45 | models-and-endpoints(모델 추가·자동 라우터 추가), playground(AgentBuilderView.tsx:341) | IMPLEMENTED | 응답은 읽지 않는다(NW:749-755, 토스트만). 계약 확인 필요: 자동 라우터 생성도 같은 경로로 `auto_router/…` 모델을 보낸다 | — |
| POST | `/model/delete` | NW:764 (body `{"id"}`) ← AllModelsTab.tsx:265, C/model_info_view.tsx:491, AutoRoutersPanel.tsx:63 | models-and-endpoints, playground | IMPLEMENTED | 응답은 읽지 않음 | — |
| PATCH | `/model/{model_id}/update` | NW:2855 (`modelPatchUpdateCall`, fetch 직접) ← AllModelsTab.tsx:283(`{blocked}` 일시정지 토글), C/model_info_view.tsx:386(상세 편집), C/update_model_credentials_modal.tsx:54, C/edit_auto_router/edit_auto_router_modal.tsx:573,618 | models-and-endpoints, playground(AgentBuilderView.tsx:376) | MUST | 우리 서버에 없음. 성공 응답을 `response.json()` 으로 파싱한다(NW:2873). 그래서 본문은 반드시 JSON 이어야 한다(최소 `{}`). 실패하면 예외 | — |
| GET | `/model/{model_id}/kubernetes_pods` | C/model_info/KubernetesPodsCard.tsx:57-62 ($api, 10초마다) | models-and-endpoints 상세. `litellm_params.kubernetes_pod_discovery === true` 이고 관리자일 때만(C/model_info_view.tsx:684) | STUB | 우리 모델에는 이 플래그가 없으므로 실제로는 부르지 않는다. 응답을 준다면 `{"service_host":"","port":null,"pod_ips":[],"error":null}`. 404 면 카드에 오류 문구만 나온다 | 위험: 데이터가 있는데 `pod_ips` 가 없으면 `.length` 에서 깨진다(KubernetesPodsCard.tsx:22-23) |
| GET | `/public/litellm_model_cost_map` (`?catalog_only=true`) | NW:617 (`modelCostMap`, fetch 직접, ok 검사 없음) ← D/hooks/models/useModelCostMap.ts:11 ← AllModelsTab.tsx:85, AddModelPanel.tsx:31, HealthStatusPanel.tsx:19, C/model_info_view.tsx:97, C/add_model/DecisionModelSelect.tsx:23, C/AIHub/DecisionModelGarden.tsx:92, C/Settings/RouterSettings/Fallbacks/Fallbacks.tsx:143 | models-and-endpoints, model-hub-table, router-settings, cost-optimization | MUST | `{"<litellm 모델명>":{"litellm_provider":"…","mode":"…","supported_endpoints":[…]},…}`. 최소 `{}` 이면 화면은 뜬다. 다만 제공자 열이 모두 "openai" 가 되고(AllModelsTab.tsx:164-169), 모델 추가 폼의 제공자별 모델 드롭다운이 빈다(C/provider_info_helpers.tsx:503-522). 폐쇄망이라 litellm 에 묶인 맵을 그대로 서빙하는 것이 맞다 | — (`{}`·불완전 항목은 C/provider_info_helpers.tsx:511 과 lib/decisionModels.ts:44-45 의 zod safeParse 가 걸러 준다) |
| GET | `/public/providers/fields` | NW:458 (`getProviderCreateMetadata`, !ok 면 예외) ← D/hooks/providers/useProviderFields.ts:10 ← C/add_model/AddModelForm.tsx:101, C/add_model/provider_specific_fields.tsx:134, AddModelPanel.tsx:37, C/AIHub/DecisionModelGarden.tsx:93 | models-and-endpoints(모델 추가), model-hub-table(관리자 기본 보기의 Decision Garden) | MUST | 배열 `[{"provider":"OpenAI","provider_display_name":"OpenAI","litellm_provider":"openai","credential_fields":[…]}]`. `[]` 면 제공자 드롭다운이 비어 모델을 추가할 수 없다 | 위험: 항목마다 `credential_fields` 배열(provider_specific_fields.tsx:146,195 `.map`)과 `provider_display_name` 문자열(AddModelForm.tsx:173 `localeCompare`)이 꼭 있어야 한다 |
| GET | `/credentials` | NW:2637 (`credentialListCall`) ← D/hooks/credentials/useCredentials.ts:11 ← AllModelsTab.tsx:88(관리자만), AddModelPanel.tsx:32, C/model_add/CredentialsPanel.tsx:29; C/model_info_view.tsx:237 | models-and-endpoints(모델 표 자격 증명 라벨, 모델 추가, LLM Credentials 탭), vector-stores | STUB | `{"credentials":[]}`. 모든 호출부가 `?.credentials ?? []`/`|| []` 로 읽는다(AllModelsTab.tsx:90, AddModelPanel.tsx:104, CredentialsPanel.tsx:30, model_info_view.tsx:238) | — |
| POST | `/credentials` | NW:2618 ← C/model_add/CredentialsPanel.tsx:63, C/add_model/AddModelForm.tsx:120, C/model_info_view.tsx:261 | models-and-endpoints | STUB | 오류 응답 가능(4xx `{"detail":{"error":"…"}}`). AddModelForm·CredentialsPanel 은 잡아서 토스트를 띄운다 | 낮음: C/model_info_view.tsx:261 `handleReuseCredential` 은 try 가 없다. 처리되지 않은 rejection 이 되지만 화면은 유지된다 |
| PATCH | `/credentials/{credential_name}` | NW:2744 ← C/model_add/CredentialsPanel.tsx:45 | models-and-endpoints | STUB | 오류 응답 가능 | — |
| DELETE | `/credentials/{credential_name}` | NW:2667 ← C/model_add/CredentialsPanel.tsx:78 | models-and-endpoints | STUB | 오류 응답 가능 | — |
| GET | `/credentials/by_model/{model_id}` | NW:2653 (`credentialGetCall`) ← C/model_info_view.tsx:181 | models-and-endpoints 상세(모델이 기존 자격 증명을 쓰지 않을 때 상세를 열 때마다) | STUB | `{}` (읽는 필드는 `credential_name`·`credential_values`·`credential_info` 이고 undefined 여도 된다. model_info_view.tsx:182-186) | 낮음: try 가 없어서(model_info_view.tsx:178-187) 404 면 처리되지 않은 rejection 이 콘솔에 남는다. 200 `{}` 가 낫다 |
| GET | `/credentials/user_connections` | NW:2708 ← D/hooks/credentials/useUserConnections.ts:11 ← C/model_add/UserConnectionsPanel.tsx:25 | models-and-endpoints "LLM Credentials" 탭(항상 렌더, panels/LlmCredentialsPanel.tsx:13) | STUB | `{"connections":[]}` (`data?.connections ?? []`, UserConnectionsPanel.tsx:27). 오류면 "Could not load" 와 재시도 버튼 | — |
| POST | `/credentials/{credential_name}/user_connection/start` | NW:2714 ← C/model_add/GithubCopilotConnectModal.tsx:42 | models-and-endpoints(GitHub Copilot 연결) | STUB | 오류 응답 가능. 목록이 비면 연결 버튼에 닿지 않는다 | — |
| POST | `/credentials/{credential_name}/user_connection/poll` | NW:2721 ← GithubCopilotConnectModal.tsx:60 | models-and-endpoints | STUB | 오류 응답 가능 | — |
| GET | `/credentials/{credential_name}/jwks` | C/model_add/InternalIssuerJwks.tsx:8 ($api) ← C/model_add/CredentialModal.tsx:271 | models-and-endpoints(내부 발급자 페더레이션 자격 증명 편집) | STUB | 오류면 메시지를 보여 준다(InternalIssuerJwks.tsx:13-18). 200 이면 본문을 그대로 출력 | — |
| GET | `/health?model_id={id}` | NW:3561 (`individualModelHealthCheckCall`) ← C/model_dashboard/HealthCheckComponent.tsx:261,357 | models-and-endpoints "Health Status" 탭(개별·전체 검사 버튼) | MUST | `{"healthy_endpoints":[],"unhealthy_endpoints":[{"error":"…"}],"healthy_count":0,"unhealthy_count":1}`. `{}` 를 주면 "healthy" 로 표시된다(HealthCheckComponent.tsx:264-288). 오해를 부르므로 404·오류가 차라리 낫다. 오류면 "unhealthy" 와 메시지(:310-322) | — |
| GET | `/health/latest` | NW:3622 ← HealthCheckComponent.tsx:208,292,409 | models-and-endpoints Health Status | STUB | `{"latest_health_checks":{}}`. try/catch 로 감싸 실패해도 기본 상태로 진행한다(:207-238, :310) | — |
| POST | `/health/test_connection` | NW:2260 (fetch 직접) ← C/add_model/model_connection_test.tsx:58(모델 추가 "Test Connect"), C/model_info_view.tsx:456 | models-and-endpoints | MUST | 성공 `{"status":"success"}`, 실패 `{"status":"error","result":{"error":"…"}}`. `content-type` 이 JSON 이 아니면 예외(NW:2276-2283). 읽는 필드는 `status`, `result.error`, `message`, `result.raw_request_typed_dict`(model_connection_test.tsx:60-67) | — |
| GET | `/model/cost_map/source` | NW:691 (!ok 면 예외) ← C/price_data_reload.tsx:181 (30초마다) | models-and-endpoints "Price Data" 탭 | STUB | **404 가 가장 안전하다**(잡아서 카드를 숨김, :179-184). 200 을 준다면 전체 모양 `{"source":"local","model_count":0,"url":null,"is_env_forced":false,"fallback_reason":null,"source_revision":null,"etag":null,"loaded_at":null}` | 위험: `{}` 를 주면 `sourceInfo.model_count.toLocaleString()` 에서 렌더가 깨진다(C/price_data_reload.tsx:367) |
| GET | `/schedule/model_cost_map_reload/status` | NW:716 ← C/price_data_reload.tsx:169 | Price Data 탭 | STUB | `{"scheduled":false,"interval_hours":null,"last_run":null,"next_run":null}`. 404 면 EMPTY_RELOAD_STATUS 로 대신한다(:171-173) | — |
| POST | `/reload/model_cost_map` | NW:635 (ok 검사 없음) ← C/price_data_reload.tsx:213 | Price Data 탭 | STUB | `{"status":"error"}`. `status==="success"` 가 아니면 실패 토스트(:215-224). 본문은 JSON 이어야 한다 | — |
| POST | `/schedule/model_cost_map_reload?hours={n}` | NW:654 ← C/price_data_reload.tsx:245 | Price Data 탭 | STUB | 위와 같음(:247-256) | — |
| DELETE | `/schedule/model_cost_map_reload` | NW:673 ← C/price_data_reload.tsx:270 | Price Data 탭 | STUB | 위와 같음 | — |
| GET | `/access_group/list` | D/hooks/modelAccessGroups/useModelAccessGroups.ts:14 ← panels/AccessGroupBudgetsPanel.tsx:39 | models-and-endpoints "Model Access Group Budgets" 탭(관리자) | STUB | `{"access_groups":[]}` (`data?.access_groups ?? []`, :15). 모델 `model_info.access_groups` 로 이름은 만들 수 있지만 예산이 없다([차이:예산표]) | — |
| PUT | `/access_group/{access_group}/budget` | D/hooks/modelAccessGroups/useSetModelAccessGroupBudget.ts:18 | 같은 탭 | STUB | 오류 응답 가능(mutation 오류) | — |
| DELETE | `/access_group/{access_group}/budget` | D/hooks/modelAccessGroups/useDeleteModelAccessGroupBudget.ts:11 | 같은 탭 | STUB | 오류 응답 가능 | — |
| POST | `/auto_router/availability` | C/add_model/AutoRouterAvailability.tsx:31 | models-and-endpoints 자동 라우터 추가·편집(C/add_model/add_auto_router_tab.tsx, C/edit_auto_router/edit_auto_router_modal.tsx) | STUB | `{"allowances":[],"error":null}`. 오류면 저장이 막힌다("Could not check availability", :52). 자동 라우터는 우리에게 없는 기능이라 그것으로 충분하다 | 위험: 200 인데 `allowances` 가 없으면 `.find` 에서 깨진다(AutoRouterAvailability.tsx:73) |
| GET | `/auto_router/classifier/default_prompt` | NW:36 ← C/add_model/ClassifierPromptEditor.tsx:41 | 자동 라우터 폼 | STUB | `{"system_prompt":""}`. 오류면 토스트를 띄우고 편집기를 닫는다(:50-52) | — |
| POST | `/auto_router/classifier/default_prompt` | NW:67 ← C/add_model/OpeningPromptEditor.tsx:109 | 자동 라우터 폼 | STUB | `{"system_prompt":""}` 또는 오류 | 미확인(OpeningPromptEditor 의 catch 는 확인하지 않음) |
| POST | `/auto_router/test_routing` | NW:2374 ← C/add_model/AutoRouterRoutingTest.tsx:41, auto_router_connection_test.tsx:41 | 자동 라우터 폼 | STUB | 오류 응답 가능. NW:2376-2382 가 `{status:"error"}` 로 바꿔 준다 | — |
| POST | `/auto_router/validate_complexity_router_config` | NW:2398 ← add_auto_router_tab.tsx:468, edit_auto_router_modal.tsx:552 | 자동 라우터 폼 | STUB | 404 도 된다. 오류면 `{valid:true}` 로 통과시킨다(NW:2403-2406) | — |
| GET | `/public/autorouter_presets` | NW:494 ← D/hooks/autoRouter/useAutoRouterPresets.ts:11 ← add_auto_router_tab.tsx:270 | 자동 라우터 폼 | STUB | `{}` (`Object.entries`, lib/autorouter_presets.ts:44-45) | 낮음: `null`·배열이 아닌 값이면 queryFn 에서 예외가 나 쿼리 오류가 된다 |
| GET | `/public/complexity_router/scorer_defaults` | NW:485 ← D/hooks/autoRouter/useComplexityScorerDefaults.ts:11 ← C/add_model/HeuristicScoringConfig.tsx:95, ClassificationMethodConfig.tsx:97 | 자동 라우터 폼 | STUB | `{"tier_boundaries":{},"token_thresholds":{},"dimension_weights":{}}`. 읽을 때 모두 `defaults?.` 로 읽는다(HeuristicScoringConfig.tsx:105,115,182). 404 면 isError | — |
| GET | `/public/complexity_router/fuse_presets` | C/add_model/FuseProfilePresets.tsx:170-175 ($api) | 자동 라우터 폼(복잡도 라우터 fuse 설정) | STUB | `{"version":"","harnesses":[],"models":[]}`. 404 면 "could not be loaded" 를 띄우고 직접 편집은 된다(:186-190) | 미확인(FuseProfilePresetField 가 `presets` 를 읽는 방식) |

### D/model-hub-table (+ app/model_hub, app/model_hub_table)

`D/model-hub-table/page.tsx:11-12`: 관리자가 아니면 `PublicModelHub`(C/public_model_hub.tsx), 관리자면 `ModelHubTable`(C/AIHub/ModelHubTable.tsx)을 그린다. `app/model_hub/page.tsx` 는 PublicModelHub 이고, `app/model_hub_table/page.tsx` 는 ModelHubTable 에 `publicPage=true` 를 준다.

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/model_group/info` | NW:1782 (`modelHubCall`) ← C/AIHub/ModelHubTable.tsx:168(`.data`); D/hooks/models/useModels.ts:240 `useModelHub` ← C/model_info_view.tsx:98; D/playground/llm_calls/system_one.ts:53; D/guardrails/_components/add_guardrail_form.tsx:299 | model-hub-table(관리자), models-and-endpoints 상세, playground, guardrails | IMPLEMENTED | 계약 확인 필요: 행마다 `providers` 문자열 배열이 꼭 있어야 한다. `model_group` 문자열, `mode`(null 가능, system_one.ts:28-30 의 zod `parse` 가 엄격하다)도 필요하다 | 위험: `providers` 가 없으면 `row.original.providers.join`/`.slice`(C/AIHub/ModelHubTableColumns.tsx:119-124), `model.providers.forEach`(C/model_filters.tsx:51,92)에서 깨진다 |
| GET | `/config/field/info?field_name=enable_public_model_hub` | NW:3427 (`getConfigFieldSetting`) ← ModelHubTable.tsx:171 | model-hub-table(관리자), admin-panel | STUB | `{"field_name":"enable_public_model_hub","field_value":false}`. `.catch` 로 무시한다(:177-179) | — |
| POST | `/model_group/make_public` | NW:548 (fetch 직접, ok 검사 없음) ← C/AIHub/forms/MakeModelPublicForm.tsx:123 | model-hub-table(관리자 "Make Public") | MUST | body `{"model_groups":[…]}`. 공개 허브(`/public/v1/model_hub`)에 무엇을 보일지 정한다. 응답은 읽지 않는다 | 낮음: 상태 코드를 보지 않아서 4xx 라도 JSON 이면 "성공" 토스트가 뜬다 |
| POST | `/model_hub/update_useful_links` | NW:1829 ← C/AIHub/UsefulLinksManagement.tsx:101 | model-hub-table(관리자) | MUST | body `{"useful_links":{name: url 또는 {url,index}}}`. 저장값은 `/public/model_hub/info` 의 `useful_links` 로 돌아온다 | — |
| GET | `/public/model_hub/info` | NW:602 (fetch 직접, ok 검사 없음) ← C/public_model_hub.tsx:155, C/AIHub/UsefulLinksManagement.tsx:35 | model-hub-table(비관리자·관리자 링크 관리), app/model_hub | MUST | `{"docs_title":"…","custom_docs_description":null,"litellm_version":"…","useful_links":{}}` (public_model_hub.tsx:156-159) | 위험: public_model_hub.tsx:154-160 에 try 가 없다. HTML 404 면 `json()` 예외가 처리되지 않은 rejection 이 된다. JSON 404 면 제목 등이 undefined 가 된다 |
| GET | `/public/v1/model_hub` | C/publicModelHub/usePublicModelHubList.ts:42 ← C/public_model_hub.tsx:291 | model-hub-table(비관리자), app/model_hub | MUST | `{"data":[<ModelGroupInfo>],"meta":{"page":1,"page_size":50,"total_count":0,"total_pages":0}}`. 쿼리는 page, page_size, 정렬·필터(C/publicModelHub/publicModelHubFilters.ts) | 위험: `data?.meta.total_count`(D/hooks/common/useResourceList.ts:125). `meta` 가 없는 200 이면 렌더가 깨진다 |
| GET | `/public/v1/model_hub/{providers\|modes\|features}?page_size=100` | C/publicModelHub/usePublicModelHubFacets.ts:26 | 위와 같음(필터 드롭다운) | MUST | `{"data":[],"links":{},"meta":{"has_more":false,"page":1,"page_size":100}}` (schema.d.ts:32356 FacetListResponse). `result.data?.data ?? []` 로 읽는다(:40) | — |
| GET | `/public/model_hub` | NW:1720 ← C/AIHub/ModelHubTable.tsx:191(`publicPage=true` 일 때만) | app/model_hub_table | STUB | `[]`. !ok 면 함수가 `[]` 를 돌려준다(NW:1726-1729) | — |
| GET | `/public/agent_hub` | NW:1735 ← C/public_model_hub.tsx:133 | model-hub-table(비관리자), app/model_hub | STUB | `[]` (Array.isArray 검사, :134). !ok 면 `[]` | — |
| GET | `/public/mcp_hub` | NW:1750 ← C/public_model_hub.tsx:145 | 위와 같음 | STUB | `[]` (:146) | — |
| GET | `/public/skill_hub` | NW:1765 ← C/public_model_hub.tsx:165 | 위와 같음 | STUB | `{"plugins":[]}` (`response.plugins ?? []`, :166). !ok 면 `{plugins:[]}` | — |
| POST | `/v1/agents/make_public` | NW:5908 ← C/AIHub/forms/MakeAgentPublicForm.tsx:97 | model-hub-table(관리자 에이전트 탭) | STUB | 오류 응답 가능. 에이전트 목록(`/v1/agents`)이 비면 닿지 않는다 | — |
| POST | `/v1/mcp/make_public` | NW:5937 ← C/AIHub/forms/MakeMCPPublicForm.tsx:116 | model-hub-table(관리자 MCP 탭) | STUB | 오류 응답 가능 | — |
| POST | `/claude-code/plugins/{plugin_name}/enable` | NW:7327 ← C/claude_code_plugins/MakeSkillPublicForm.tsx:85 | model-hub-table(관리자 스킬 탭) | STUB | 오류 응답 가능 | — |
| POST | `/claude-code/plugins/{plugin_name}/disable` | NW:7362 ← MakeSkillPublicForm.tsx:88 | 같음 | STUB | 오류 응답 가능 | — |
| GET | `/guardrails/ui/add_guardrail_settings` | NW:5993 ← D/hooks/providers/useProxyDecisionProviders.ts:11 ← C/AIHub/DecisionModelGarden.tsx:94(관리자 model-hub-table 기본 보기, ModelHubTable.tsx:120,543); D/guardrails/_components/add_guardrail_form.tsx:297, guardrail_info.tsx:216 | model-hub-table, guardrails | STUB | `{"supported_entities":[],"supported_actions":[],"pii_entity_categories":[],"decision_model_providers":[],"supported_modes_by_provider":{},"content_filter_settings":{"content_categories":[]}}`. 허브는 zod safeParse 가 실패하면 null 로 둔다(lib/decisionModels.ts:74-77, `retry:false`). 그래서 404 도 된다. 가드레일 화면은 8장 범위 | 미확인: add_guardrail_form.tsx:948 `guardrailSettings.supported_entities` 가 null 일 때의 보호 장치 |

### D/playground (5장에서 다룬 행만)

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/v1/systemone` | D/playground/llm_calls/system_one.ts:90 (gateway base URL, :73) ← D/playground/components/systemOneUI/SystemOneUI.tsx:89 | playground "System One" 탭 | STUB | 오류 응답 가능. 응답이 zod 스키마에 안 맞으면 예외 메시지를 띄운다(system_one.ts:77-80) | — |
| POST | `/v1/decisions` | system_one.ts:103-110 ← SystemOneUI.tsx:98 | playground "System One" 탭 | STUB | 같음 | — |

#### 추가 발견
| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| DELETE | `/credentials/{credential_name}/user_connection` | NW:2726-2727 (`userConnectionDeleteCall`) ← C/model_add/UserConnectionsPanel.tsx:40 | models-and-endpoints LLM Credentials | STUB | 오류 응답 가능(try/catch 토스트, :39-46) | — |

- 1차 스캔의 `/start`·`/poll` 은 실제로 `/credentials/{credential_name}/user_connection/start|poll` 이다(NW:2701-2722 `userConnectionPath`). 위 표에 그 경로로 적었다.
- 1차 스캔의 `/by_name/{credentialName}` 은 실제로 `/credentials/by_name/{name}` 이다. 그런데 `credentialGetCall` 의 유일한 호출부(C/model_info_view.tsx:181)가 `credentialName=null` 을 넘긴다. 그래서 by_name 은 **호출되지 않는다**(표에서 뺌).
- 1차 스캔의 `/{facet}` 는 `/public/v1/model_hub/{facet}` 이다(위 표).
- 범위 밖이지만 이 화면들이 부르는 것(다른 장 소유): models-and-endpoints 는 `/get/config/callbacks`(panels/ModelRetrySettingsPanel.tsx:5, ModelGroupAliasPanel.tsx:5)와 `/router/settings`(ModelGroupAliasPanel.tsx:5)를 부른다. 관리자 model-hub-table 은 `/v1/agents`(ModelHubTable.tsx:229-235, `response.agents.map` 에 `litellm_params.is_public` 이 필요하므로 `{"agents":[]}` 필수), `/v1/mcp/server`(:257-258, 응답을 배열로 그대로 저장), `/claude-code/plugins`(:274-275, `response.plugins`), `/get/ui_settings`(:139)를 부른다. 모델 표·추가 폼은 gatewayApiClient 로 `/models`(NW:1853)를 부르고 팀 목록도 부른다.

#### 미확인
- `/v2/model/info` 가 api-keys·teams·users·usage 화면에서도 렌더 경로로 불리는지. import 도달성만 확인했다. 실제 렌더 경로로 확인한 것은 models-and-endpoints, logs(DrawerHeader), cost-optimization 이다.
- C/add_model/OpeningPromptEditor.tsx 의 오류 처리, FuseProfilePresetField 의 `presets` 읽기, add_guardrail_form.tsx:948 의 null 보호.
- 우리 서버의 `/model_group/info` 응답에 `providers` 배열이 들어가는지(이 문서는 대시보드 쪽만 봤다).

---

## 6. usage·logs·old-usage·cost-optimization·guardrails-monitor·tool-policies·tag-management·model-insights·lens

약어: NW=`components/networking.tsx`, D/=`app/(dashboard)/`, C/=`components/`, LUI/=`node_modules/@litellm/lens-ui/src/` (모두 `gateway/ui/litellm-dashboard/` 기준).

### 먼저 짚을 것 — 우리 서버의 `/user/daily/activity`, `/spend/logs` 는 대시보드가 부르지 않는다

- 대시보드 어디에도 맨 `/user/daily/activity` 나 맨 `/spend/logs` 를 부르는 코드가 없다. 두 문자열은 주석(D/usage/_components/components/UsagePageView.tsx:4)과 팀 멤버 권한 라벨(C/team/permission_definitions.tsx:25-26, C/TeamSSOSettings.tsx:50)에만 나온다. 확인 방법: ``daily/activity[`"'?]``, ``/spend/logs[`"'?]`` 를 src 전체(테스트·schema.d.ts 제외)에서 찾았다.
- 사용량 화면이 실제로 부르는 것은 **`GET /{entity}/daily/activity/aggregated`**(와 `/keys`·`/search`·`/model_top_keys`·`/export`), **`GET /user/daily/activity/aggregated/users`** 이다. 로그 화면은 **`GET /spend/logs/ui`**, **`GET /spend/logs/ui/{request_id}`**, **`GET /spend/logs/session/ui`** 를 부른다.
- 즉 지금 구현된 `/user/daily/activity`·`/spend/logs` 는 데스크탑 계정 화면용이고, 대시보드 usage·logs 화면에는 위 경로를 따로 만들어야 한다.

### `{entity}` 의 실제 값과 화면

`DailyActivityEntity = "user" | "team" | "tag" | "organization" | "customer" | "agent"` (C/UsagePage/dailyActivityApi.ts:4). 엔티티별 ID 쿼리 이름은 NW:1347-1354(`user_id`, `team_ids`, `tags`, `organization_ids`, `end_user_ids`, `agent_ids`), 제외 쿼리는 NW:1356-1361(`exclude_team_ids` 등). 공통 쿼리: `start_date`, `end_date`(YYYY-MM-DD), `model`, `api_key`, `timezone`(분 단위 오프셋), user 만 `include_current_utc_day` (NW:1363-1386). `team` 은 항상 `exclude_team_ids=litellm-dashboard` 를 붙인다(D/usage/_components/components/EntityUsage/entityFetchFns.ts:46).

| entity | 부르는 곳 | 화면 |
|---|---|---|
| user | UsagePageView.tsx:225(전체/내 사용량, 기본 보기), :350-389(키 페이지·검색·모델별 상위 키·키 상세), D/home/_components/useHomeUsage.ts:49, D/cost-optimization/_components/useDailyActivityRange.ts:78, C/chat/UsagePanel.tsx:105, C/shared/ScopedSavingsTab(키 상세·사용자 상세 Savings 탭) | usage, home, cost-optimization, api-keys·users(상세 탭), @chat |
| team | EntityUsage `usageView === "team"` (UsagePageView.tsx:620, ENTITY_API.team) | usage(팀 보기) |
| tag | UsagePageView.tsx:237(관리자·global·사용자 미선택일 때 "Top agents"), EntityUsage tag 보기 | usage |
| organization | EntityUsage organization 보기 | usage |
| customer | EntityUsage customer 보기 | usage |
| agent | EntityUsage agent 보기(`viewAgentUsage` 권한), EntityUsage.tsx:216(에이전트 분해) | usage |

`/keys`·`/search`·`/model_top_keys`·`/export` 도 같은 ENTITY_API 로 엔티티마다 불린다(entityFetchFns.ts:36-43). 홈은 `user/aggregated` 만 부른다.

---

### D/usage

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/user/daily/activity/aggregated` | NW:1392 (dailyActivityAggregatedCall) ← UsagePageView.tsx:225 | usage, home, cost-optimization, api-keys·users(Savings 탭), @chat | MUST | `{"results":[],"metadata":{...}}`. 읽기: `toDailyData` 가 `results ?? []`(dailyActivityApi.ts:89), `metadata ?? EMPTY`(UsagePageView.tsx:299). 오류면 ErrorState(UsagePageView.tsx:421)와 빈 화면. 쿼리 `api_key_limit` 는 키 상세에서 1(UsagePageView.tsx:387). 응답 모양은 schema `SpendAnalyticsPaginatedResponse`. 우리 `/user/daily/activity` 와 경로가 다르다 | `results[i].metrics` 가 없으면 `day.metrics.prompt_tokens` 에서 예외(C/UsagePage/keyActivityData.ts:27). 행을 넣을 거면 `metrics` 는 반드시 |
| GET | `/user/daily/activity/aggregated/keys` | NW:1403 ← UsagePageView.tsx:368 → C/UsagePage/components/KeyActivityPanel.tsx:225 | usage("Key Activity" 탭) | MUST | `{"api_keys":[],"total_api_keys":0,"offset":0,"limit":N}` (KeyActivityPanel.tsx:230-233) | `api_keys` 가 없으면 `.length` 에서 예외. `.catch` 가 잡아 실패 표시로 바뀐다(:240). 화면은 안 깨진다 |
| GET | `/user/daily/activity/aggregated/search` | NW:1414 ← UsagePageView.tsx:359 → KeyActivityPanel.tsx:325 | usage(키 검색) | MUST | `{"api_keys":[]}` (KeyActivityPanel.tsx:328) | — (catch 있음 :334) |
| GET | `/user/daily/activity/aggregated/model_top_keys` | NW:1426 ← UsagePageView.tsx:352 → C/activity_metrics.tsx:149 | usage("Model Activity" 모델 펼침) | MUST | `{"api_keys":[],"model":"…","by_model_group":true}`. 행마다 `metadata`·`metrics` 가 필수(activity_metrics.tsx:155-161) | 행에 `metadata` 가 없으면 예외. catch 가 잡아 실패 표시(:167) |
| GET | `/user/daily/activity/export` | NW:1441 (getBlob) ← entityFetchFns.ts:42 ← C/EntityUsageExport | usage("Export" 모달) | MUST | Blob 응답. 쿼리 `export_type`, `format`=csv\|json. 빈 CSV(`text/csv`) 로 충분. JSON 검사를 하지 않는 getBlob 경로(lib/http/client.ts getBlob) | — |
| GET | `/user/daily/activity/aggregated/users` | NW:1451 ← UsagePageView.tsx:378 → C/UsagePage/components/TopUsersView.tsx:31, useUserActivityPage.ts | usage(Overview "Top users", "User Activity" 탭) | MUST | `{"users":[],"total_users":0,"offset":0,"limit":N}` (useUserActivityPage.ts:49-52, TopUsersView.tsx:34) | `users` 가 없으면 `.length` 예외. catch 가 잡아 실패 표시 |
| GET | `/team/daily/activity/aggregated` (+`/keys`, `/search`, `/model_top_keys`, `/export`) | entityFetchFns.ts:46 ← C/…/EntityUsage/EntityUsage.tsx:156,194 | usage(팀 보기) | MUST | user 와 같은 모양. 쿼리 `team_ids`(콤마), `exclude_team_ids=litellm-dashboard` | user 와 같음 |
| GET | `/tag/daily/activity/aggregated` (+하위 4개) | entityFetchFns.ts:45 ← UsagePageView.tsx:237(관리자 global "Top agents"), EntityUsage tag 보기 | usage | STUB | `{"results":[],"metadata":{}}`. `useAggregatedDailyActivity` 가 오류를 빈 응답으로 바꾼다(D/usage/_components/hooks/useAggregatedDailyActivity.ts:63-67). 404 도 안전 | — |
| GET | `/organization/daily/activity/aggregated` (+하위 4개) | entityFetchFns.ts:47 ← EntityUsage organization 보기 | usage | STUB | 위와 같음 [조직 없음] | — |
| GET | `/customer/daily/activity/aggregated` (+하위 4개) | entityFetchFns.ts:48 ← EntityUsage customer 보기 | usage | STUB | 위와 같음 | — |
| GET | `/agent/daily/activity/aggregated` (+하위 4개) | entityFetchFns.ts:49 ← EntityUsage agent 보기, EntityUsage.tsx:216 | usage | STUB | 위와 같음 | — |
| GET | `/gateway/daily/activity` | NW:2517-2537 (gatewayDailyActivityCall) ← UsagePageView.tsx:262, D/home/_components/useHomeUsage.ts:58 | usage, home (관리자만) | STUB | 404 로 충분. catch 가 `null` 로 바꾸고 일별 집계(metadata)로 되돌아간다(UsagePageView.tsx:267-270, useHomeUsage.ts:63). 응답을 주려면 `{"total_successful_requests":0,"total_failed_requests":0,"by_date":[],"by_route":[]}` (C/…/gatewayActivity.ts:13-19). `{}` 도 `?.`·`??` 로 버틴다(gatewayActivity.ts:70, overviewData.ts:245) | — |
| GET | `/gateway/errors/activity` | NW:2550 (requestErrorActivityCall) ← UsagePageView.tsx:280 → errors/ErrorsTab.tsx | usage("Errors" 탭, 관리자만) | STUB | `{"total_successful_requests":0,"total_failed_requests":0,"by_date":[],"by_status_code":[],"by_key":[],"by_team":[],"by_user":[],"by_model":[]}` (errors/errorsData.ts:19-28). 404 면 "불러오기 실패" 상태로 표시만 된다(UsagePageView.tsx:285-292) | **위험**: 200 으로 필드를 빼면 `[...activity.by_status_code]`, `activity.by_date.some` 에서 렌더 예외(errorsData.ts:118,123,204). 필드를 다 주거나 404 로 |
| GET | `/customer/list` | D/hooks/customers/useCustomers.ts:12 ← UsagePageView.tsx:122 | usage(customer 필터, 관리자) | STUB | `[]` (select `data ?? []` :16, `customers?.map` UsagePageView.tsx:647) | `{}` 를 주면 `.map` 예외. 배열로 |
| GET | `/tag/list` | NW:5424-5450 (tagListCall) ← UsagePageView.tsx:177 | usage, api-keys(C/organisms/create_key_button.tsx:236 useTags, C/templates/key_edit_view.tsx:214), models-and-endpoints(C/add_model/AddModelForm.tsx:104, C/model_info_view.tsx:227), playground(D/playground/components/chat/usePlaygroundCatalog.ts:142), tag-management | STUB | `{}` (태그 이름 → Tag 사전, `TagListResponse = Record<string, Tag>` C/tag_management/types.tsx:29). 모두 `Object.values(response)` 로 읽는다. 쿼리 `start_date`·`end_date` 는 선택 | `null` 을 주면 `Object.values(null)` 예외. 대부분 try/catch 안이지만 usePlaygroundCatalog.ts:143 은 react-query 오류가 된다. `{}` 로 |
| GET | `/tag/summary` | D/hooks/tags/useTagSummary.ts:15 ← UsagePageView.tsx:326(관리자 global "Top agents"); NW:7044 (userAgentSummaryCall) ← C/user_agent_activity.tsx:252 | usage | STUB | `{"results":[]}`. useTagSummary 는 `select: data => data.results`(:26), user_agent_activity 는 `summaryData.results \|\| []`(:459) | `null` 이면 select 에서 예외 → 쿼리 오류(화면은 TopAgents 가 `rows ?? []` 로 버팀, TopAgents.tsx:167) |
| GET | `/tag/distinct` | NW:7031 ← C/user_agent_activity.tsx:181 | usage("User Agent Activity" 보기) | STUB | `{"results":[]}` (:182 `data.results.map`) | try/catch 안 |
| GET | `/tag/dau` | NW:6950 ← user_agent_activity.tsx:195 | usage(같은 보기) | STUB | `{"results":[]}` | **위험**: 200 `{}` 면 렌더에서 `dauData.results.forEach`/`getAllTagsForData(dauData.results)` 예외(user_agent_activity.tsx:320,347). 404 는 catch 로 초기값 유지 |
| GET | `/tag/wau` | NW:6990 ← user_agent_activity.tsx:214 | usage | STUB | `{"results":[]}` | 위와 같음(:321,378) |
| GET | `/tag/mau` | NW:7017 ← user_agent_activity.tsx:233 | usage | STUB | `{"results":[]}` | 위와 같음(:322,414) |
| GET | `/tag/user-agent/per-user-analytics` | NW:7075 ← C/per_user_usage.tsx:56 | usage(같은 보기) | STUB | `{"results":[],"total_count":0}` (per_user_usage.tsx:141,146) | **위험**: 200 에 `results` 가 없으면 `perUserData.results.forEach` 예외(:163). 404 는 catch 로 무시 |
| GET | `/team/spend/by_user` | NW:1464-1476 ← C/…/EntityUsage/TeamUserSpendCard.tsx:79 | usage(팀 보기 "Spend Per User Within Team") | MUST | `{"results":[],"start_date":"…","end_date":"…"}`. `data?.results ?? []`(:82) | — |
| POST | `/usage/ai/chat` | NW:4243-4300 (usageAiChatStream, SSE `data: {"type":"chunk"\|"status"\|"tool_call"\|"done"…}`) ← D/usage/_components/components/UsageAIChatPanel.tsx:168 | usage("Ask AI" 패널) | STUB | 오류 응답 가능. 단 본문은 **JSON** 이어야 한다(`await response.json()` NW:4268). 4xx `{"detail":{"error":"not supported"}}` → 패널이 catch 로 표시(UsageAIChatPanel.tsx:206) | — |
| GET | `/cloudzero/settings` | C/cloudzero_export_modal.tsx:58(상대 경로 그대로 fetch), D/hooks/cloudzero/useCloudZeroSettings.ts:10 | usage(CloudZero 내보내기 모달), logging-and-alerts(C/CloudZeroCostTracking) | STUB | 200 `{}`. 훅은 `api_key_masked`·`connection_id` 가 없으면 "미설정"(null)으로 본다(useCloudZeroSettings.ts:45). 모달은 404 도 정상으로 보지만 훅은 404 를 오류 카드로 그린다(CloudZeroCostTracking.tsx:38-47). 둘 다 버티는 것은 `{}` | `null` JSON 이면 모달이 `settings.connection_id` 에서 예외 → catch 토스트(cloudzero_export_modal.tsx:70,77) |
| POST | `/cloudzero/init` | cloudzero_export_modal.tsx:92, D/hooks/cloudzero/useCloudZeroCreate.ts:16 | usage, logging-and-alerts | STUB | 오류 응답 가능(토스트) | — |
| PUT | `/cloudzero/settings` | cloudzero_export_modal.tsx:92(기존 설정이 있을 때), useCloudZeroSettings.ts:80 | usage, logging-and-alerts | STUB | 오류 응답 가능 | — |
| POST | `/cloudzero/export` | cloudzero_export_modal.tsx:141, D/hooks/cloudzero/useCloudZeroExport.ts:14 | usage, logging-and-alerts | STUB | 오류 응답 가능 | — |
| POST | `/cloudzero/dry-run` | D/hooks/cloudzero/useCloudZeroDryRun.ts:14 | logging-and-alerts | STUB | 오류 응답 가능 | — |
| DELETE | `/cloudzero/delete` | useCloudZeroSettings.ts:138 | logging-and-alerts | STUB | 오류 응답 가능 | — |

### D/logs

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/spend/logs/ui` | NW:1986-2040 (uiSpendLogsCall) ← C/logs/request/useLogFilterLogic.ts:174, RequestLogsPanel.tsx:143(URL `log_id` 로 한 건 찾기) | logs, guardrails-monitor·tool-policies·lens(LogDetailsDrawer·LensHost lookup, C/lens/EmbeddedLens.tsx:13), home·부팅(C/liteadmin/operations.ts:337 LiteAdmin 도구) | MUST | `{"data":[],"total":0,"page":1,"page_size":50,"total_pages":0}` + 선택 `has_more`, `next_session_cursor`, `total_is_capped` (useLogFilterLogic.ts:11-20). 쿼리: `start_date`·`end_date`(UTC "YYYY-MM-DD HH:mm:ss"), `page`, `page_size`, `api_key`, `team_id`, `request_id`, `search`, `session_id`, `user_id`, `end_user`, `status_filter`, `cache_hit_filter`, `used_client_oauth_token`, `span_type`, `model_id`, `model`, `key_alias`, `error_code`, `error_message`, `sort_by`, `sort_order`, `exclude_internal_health_checks`, `group_by_session=true`, `session_cursor` (:174-200). 시작 시간 정렬이면 `next_session_cursor` 로 다음 쪽을 연다(RequestLogsPanel.tsx:223) | **위험**: 200 에 `data` 가 없으면 `rows = filteredLogs.data` 가 undefined → `rows.length` 렌더 예외(RequestLogsPanel.tsx:175-178). 오류(4xx)는 표가 오류 상태로 그린다(:304) |
| GET | `/spend/logs/ui/{request_id}` | NW:4777-4800 (uiSpendLogDetailsCall, 쿼리 `start_date`) ← D/hooks/logDetails/useLogDetails.ts ← C/logs/detail/LogDetailsDrawer.tsx:248 | logs, guardrails-monitor, tool-policies, lens, @chat | MUST | `{}` 또는 404 도 안전. `messages`·`response`·`proxy_server_request` 만 읽고 없으면 목록 행 값을 쓴다(LogDetailsDrawer.tsx:260-262) | — |
| GET | `/spend/logs/session/ui` | NW:5559-5590 (sessionSpendLogsCall, 쿼리 `session_id`·`page`·`page_size`) ← LogDetailsDrawer.tsx:138 | logs(행의 `session_total_count > 1` 일 때만, RequestLogsPanel.tsx:167,256) | MUST(세션 묶음을 내보낼 때만. 아니면 호출 안 됨) | `{"data":[],"total":0,"total_pages":1}` | **위험**: 200 `{}` 면 `rows = firstPage.data \|\| firstPage` 가 객체 → `rows.map` 예외(LogDetailsDrawer.tsx:139,163) |
| GET | `/key/aliases` | NW:2490-2510 (keyAliasesCall, 쿼리 `page`·`size`·`search`·`team_id`) ← D/hooks/keys/useKeyAliases.ts ← C/logs/request/RequestLogsFilters.tsx:102 | logs(Key Alias 필터) | IMPLEMENTED | 계약 확인 필요: `{"aliases":[],"total_count":0,"current_page":1,"total_pages":1,"size":50}` (NW:2483-2489). 다음 쪽은 `current_page < total_pages`(useKeyAliases.ts:22) | `aliases` 가 없으면 `page.aliases.flatMap` 렌더 예외(RequestLogsFilters.tsx:108) |
| GET | `/management/v1/spend_logs/users` | D/hooks/spendLogs/useSpendLogUsers.ts:20 ← RequestLogsFilters.tsx:185 | logs(User ID 필터) | STUB(사용자 필터. 실제로 채우려면 MUST) | `{"data":[],"links":{"self":"…","next":null},"meta":{"has_more":false,"page":1,"page_size":50}}` (schema FacetListResponse). 쿼리 `filter[startTime][gte]`, `filter[startTime][lte]`, `page_size`, `q`, `page` | **위험**: `links` 가 없으면 `lastPage.links.next` 에서 예외(D/hooks/spendLogs/useSpendLogEndUsers.ts:15), `data` 가 없으면 `page.data.flatMap` 렌더 예외(RequestLogsFilters.tsx:194) |
| GET | `/management/v1/spend_logs/end_users` | D/hooks/spendLogs/useSpendLogEndUsers.ts:35 ← RequestLogsFilters.tsx:230 | logs(End User 필터) | STUB | 위와 같은 모양 | 위와 같음(:239) |
| GET | `/audit` | NW:6485-6520 (uiAuditLogsCall, 쿼리 `page`·`page_size`·`search`·`object_id`·`changed_by`·`object_key_hash`·`object_team_id`·`action`·`table_name`·`sort_by`·`sort_order`) ← C/logs/audit/AuditLogsPanel.tsx:65 | logs("Audit Logs" 탭. `viewAuditLogs` 권한 + premium 일 때만, D/logs/page.tsx:51, AuditLogsPanel.tsx:56) | STUB | `{"audit_logs":[],"total":0,"page":1,"page_size":50,"total_pages":0}`. `?.`·`??` 로 읽는다(AuditLogsPanel.tsx:132-133) | — |
| POST | `/compliance/eu-ai-act` | NW:7450 ← C/logs/detail/sections/GuardrailViewer/CompliancePanel.tsx:160 | logs·guardrails-monitor·lens·tool-policies 상세 서랍(로그에 가드레일 정보가 있을 때만, C/logs/detail/LogDetailContent.tsx:245) | STUB | 오류 응답 가능(카드에 "--"·오류 표시). 성공 모양은 `{"compliant":true,"checks":[]}` | 200 에 `checks` 가 없으면 카드를 펼칠 때 `data.checks.map` 예외(CompliancePanel.tsx:117). [가드레일 없음] 이라 사실상 안 불림 |
| POST | `/compliance/gdpr` | NW:7470 ← CompliancePanel.tsx:167 | 위와 같음 | STUB | 위와 같음 | 위와 같음 |

### D/old-usage (사용 중단 배너가 붙은 옛 사용량 화면)

`viewGlobalSpend` 권한일 때만 불린다(D/old-usage/_components/usage.tsx:106). 먼저 `getProxyUISettings`(3장 범위 `/sso/get/ui_settings`)를 읽고 **`DISABLE_EXPENSIVE_DB_QUERIES: true` 면 아래 호출을 하나도 하지 않고**(usage.tsx:204,491) 안내 화면을 그린다(:532). 가장 싼 스텁 방법이다. 그렇지 않으면 아래를 모두 부른다(:497-508).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/global/spend/logs` | NW:2099-2104 (adminSpendLogsCall) ← usage.tsx:317 | old-usage | STUB | `[]` (fillMissingDates 가 `data.map`, :266) | try/catch 안 |
| GET | `/global/spend/keys` | NW:2109 (adminTopKeysCall) ← usage.tsx:348 | old-usage | STUB | `[]` (`top_keys.map`, :349) | catch 안 |
| GET | `/global/spend/models` | NW:2226 (adminTopModelsCall) ← usage.tsx:365 | old-usage | STUB | `[]` | catch 안 |
| GET | `/global/spend/provider` | NW:2155-2172 (adminspendByProvider, 쿼리 start/end) ← usage.tsx:338 | old-usage | STUB | `[]` | **위험**: 받은 값을 그대로 state 에 넣고 렌더에서 `spendByProvider.map`(usage.tsx:666). 객체를 주면 예외. 배열로 |
| GET | `/global/activity` | NW:2174-2188 (adminGlobalActivity) ← usage.tsx:437 | old-usage | STUB | `{"daily_data":[],"sum_api_requests":0,"sum_total_tokens":0}` (:446, :694) | `sum_*` 가 없으면 `valueFormatterNumbers(undefined)` 로 넘어간다(:694). 미확인 |
| GET | `/global/activity/model` | NW:2190 (adminGlobalActivityPerModel) ← usage.tsx:463 | old-usage | STUB | `[]` (`data.map`, :470) | try/catch 안 |
| GET | `/global/spend/teams` | NW:1866-1876 (teamSpendLogsCall) ← usage.tsx:381 | old-usage(관리자) | STUB | `{"daily_spend":[],"teams":[],"total_spend_per_team":[]}` (:388-396) | catch 안 |
| GET | `/global/spend/all_tag_names` | NW:1916-1926 (allTagNamesCall) ← usage.tsx:407 | old-usage(관리자) | STUB | `{"tag_names":[]}` | **위험**: `tag_names` 가 없으면 state 가 undefined → 렌더에서 `...allTagNames.filter` 예외(usage.tsx:141) |
| GET | `/global/spend/tags` | NW:1879 (tagsSpendLogsCall, 쿼리 start/end/tags) ← usage.tsx:208,418 | old-usage(관리자) | STUB | `{"spend_per_tag":[]}` | `spend_per_tag` 가 없으면 BarChart 에 undefined(:939). 미확인. updateTagSpendData(:208)는 try/catch 밖 |
| POST | `/global/spend/end_users` | NW:2141-2153 (adminTopEndUsersCall, 본문 `{api_key?, startTime?, endTime?}`) ← usage.tsx:187,427 | old-usage(관리자) | STUB | `[]` | **위험**: 렌더가 `topUsers?.map`(:857). 객체를 주면 예외. :187 경로는 try/catch 밖 |

### D/cost-optimization

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/user/daily/activity/aggregated` | D/cost-optimization/_components/useDailyActivityRange.ts:78 | cost-optimization(Overall 등 모든 탭의 기반) | MUST(D/usage 행과 같은 엔드포인트) | 위 D/usage 참고 | — |
| GET | `/user/daily/activity/aggregated/cache_leakage_keys` | NW:1457 ← D/cost-optimization/_components/useCacheLeakageKeys.ts:43 ← CacheLeakageCard.tsx:96 | cost-optimization(Prompt Caching 탭) | STUB | `{"api_keys":[]}` (useCacheLeakageKeys.ts:46) | `api_keys` 가 없으면 rows 가 undefined. 미확인 |
| GET | `/cost_optimization/prompt_caching/requests` | D/cost-optimization/_components/PromptCachingRequestsTable.tsx:65 | cost-optimization(Prompt Caching 탭) | STUB | `{"requests":[],"has_more":false,"next_cursor":null}` | **위험**: 성공 시 `requests.data.requests.length` 를 바로 읽는다(:117,135). 4xx 면 "Could not load" 로 안전하게 그린다(:107) |
| GET | `/v1/tool/spend` | NW:7575-7590 (getToolSpend, 쿼리 start/end) ← D/cost-optimization/_components/UsageTab.tsx:60 | cost-optimization(Overall, `viewProxyWideCostData`) | STUB | `{"by_tool":[],"daily":[],"start_date":null,"end_date":null}`. 404 도 안전(catch 가 EMPTY_TOOL_SPEND 로 바꾼다, :65) | — |
| GET | `/auto_router/benchmarks` | D/cost-optimization/_components/useAutoRouterBenchmarks.ts:32-37 (쿼리 start_date·end_date·api_key·user_id, `retry:false`) ← AutoRouterBenchmarksTab.tsx:366 | cost-optimization(Auto-Router 탭), api-keys(C/templates/KeyAutoRouterUsageTab.tsx), users(user_info_view.tsx:48 AutoRouterUsageView) | STUB | **404 권장**("unavailable" 문구, :298). 403 이면 "proxy admin only" 문구(:295). 200 을 주려면 `groups`·`totals`(중첩 `cache` 포함)를 모두 채워야 한다(schema AutoRouterBenchmarksResponse) | **위험**: 200 에 `totals` 가 없거나 불완전하면 `stats.sessions.toLocaleString()` 등에서 렌더 예외(:321), `data.groups.find`(autoRouterBenchmarks.ts:26) |
| GET | `/auto_router/shadow_eval` | D/cost-optimization/_components/useShadowEval.ts:33-44 ← ShadowEvalSection.tsx:498 | cost-optimization(Auto-Router 탭 하단) | STUB | `[]`. 403 이면 섹션을 숨긴다(:507) | `jobs ?? []` 로 읽음. 객체를 주면 `.filter` 예외(:501) |
| GET | `/auto_router/shadow_eval/{job_id}` | useShadowEval.ts:46-58 ← ShadowEvalSection.tsx:425,483 | 같은 곳(작업이 있을 때만) | STUB | 목록이 비면 호출 안 됨 | — |
| POST | `/auto_router/shadow_eval/start` | useShadowEval.ts:70 | 같은 곳(시작 폼) | STUB | 오류 응답 가능(onError 토스트, :64) | — |
| POST | `/auto_router/shadow_eval/{job_id}/stop` | useShadowEval.ts:76 | 같은 곳 | STUB | 오류 응답 가능 | — |

### D/caching (6장에서 다룬 행만)

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/global/activity/cache_hits` | D/hooks/caching/useCacheActivity.ts:17-31 ← D/caching/_components/cache_dashboard.tsx:104 | caching | STUB | 4xx 가 가장 안전(전부 `activity?.` 로 읽음). 200 이면 `{"groups":[],"error_breakdown":[],"filter_options":{"key_aliases":[],"models":[]},"totals":{"api_requests":0,"cache_hits":0,"failed_requests":0,"cache_hit_ratio":0,"cached_completion_tokens":0}}` | **위험**: 200 에 `filter_options` 가 없으면 `activity?.filter_options.key_aliases` 예외(cache_dashboard.tsx:115-116) |

### D/guardrails-monitor (`viewGuardrailUsage` 권한일 때만, D/guardrails-monitor/page.tsx:12)

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/guardrails/usage/overview` | D/hooks/guardrails/useGuardrailsUsage.ts:19-25 ← D/guardrails-monitor/_components/GuardrailsOverview.tsx:129 | guardrails-monitor | STUB | `{"rows":[],"chart":[],"totalRequests":0,"totalBlocked":0,"passRate":0,"totalCost":null,"totalUntrackedUsageUnits":0}`. `rows ?? []`, `chart` 는 ScoreChart 가 빈 값을 처리(ScoreChart.tsx:14) | — |
| GET | `/guardrails/usage/detail/{guardrail_id}` | useGuardrailsUsage.ts:27-36 ← GuardrailDetail.tsx:41 | guardrails-monitor(목록에서 고르거나 URL 로 열 때만) | STUB | 404 안전(오류 문구) | — |
| GET | `/guardrails/usage/logs` | NW:3931-3960 (getGuardrailsUsageLogs) ← GuardrailDetail.tsx:42 | 같은 곳 | STUB | `{"logs":[],"total":0}` (`logsData?.logs ?? []`) | — |

### D/tool-policies (`viewToolPolicies` 권한, C/ToolPoliciesView.tsx:15)

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v1/tool/list` | NW:7537-7552 (fetchToolsList) ← C/ToolPolicies/toolPoliciesQueries.ts:13 ← ToolPoliciesPanel.tsx:58 | tool-policies | STUB | `{"tools":[]}` (`data.tools ?? []` 라 `{}` 도 안전). 오류면 `response.text()` 를 메시지로 던짐 | — |
| GET | `/v1/tool/policy/options` | NW:7521-7535 ← C/ToolDetail.tsx:82 | tool-policies(도구 상세, 목록이 비면 안 열림) | STUB | `{"input_policies":[],"output_policies":[]}` (`?.` 로 읽음, :237) | — |
| GET | `/v1/tool/{tool_name}/detail` | NW:7644 ← ToolDetail.tsx:76 | tool-policies(도구 상세) | STUB | 404 안전("Failed to load tool details", :218) | — |
| GET | `/v1/tool/{tool_name}/logs` | NW:7617 ← ToolDetail.tsx:96 | tool-policies(도구 상세) | STUB | `{"logs":[],"total":0}` (:106,413) | — |
| POST | `/v1/tool/policy` | NW:7661-7688 ← ToolDetail.tsx:138, ToolPoliciesPanel.tsx:79 | tool-policies | STUB | 오류 응답 가능 | — |
| DELETE | `/v1/tool/{tool_name}/overrides` | NW:7691-7715 ← ToolDetail.tsx:197 | tool-policies | STUB | 오류 응답 가능 | — |

### D/tag-management

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/tag/list` | NW:5424 ← D/tag-management/_components/index.tsx:47 | tag-management(외 D/usage 행 참고) | STUB | `{}` (`Object.values`, :48) | — |
| POST | `/tag/new` | NW:5377 (fetchClient) ← index.tsx:66 | tag-management | STUB | 오류 응답 가능(토스트 :82) | — |
| POST | `/tag/update` | NW:5384 ← D/tag-management/_components/tag_info.tsx:210 | tag-management(태그 상세) | STUB | 오류 응답 가능 | — |
| POST | `/tag/info` | NW:5390-5410 (본문 `{"names":[…]}`) ← tag_info.tsx:184 | tag-management(태그 상세) | STUB | `{}` (`response[tagId]` 가 없으면 무시, :185) | — |
| POST | `/tag/delete` | NW:5461 ← index.tsx:94 | tag-management | STUB | 오류 응답 가능 | — |

### D/model-insights

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/model-insights` | D/model-insights/_components/ModelInsightsView.tsx:80-81 (apiClient, 쿼리 `metric`=tokens\|spend\|requests) | model-insights | STUB | **4xx 권장**(Alert 로 "Could not load", :111). 200 이면 `{"start_date":"2026-01-01","end_date":"2026-01-01","daily":[],"daily_totals":[],"top_models":[]}` 처럼 **날짜가 유효해야** 한다 | **위험**: `start_date`/`end_date` 가 비거나 없으면 `rankModels`→`halfShares` 의 `isoDay(NaN)` 이 `toISOString` RangeError 로 렌더 예외(modelInsightsData.ts:55,109,136). 상위 배정 목록에서는 UI 경로로 보고 뺐지만 실제 API 호출이다 |

### D/lens (@litellm/lens-ui 패키지가 직접 부른다)

D/lens/page.tsx → C/lens/EmbeddedLens.tsx 는 `@litellm/lens-ui`(vendor tgz, package.json:34)의 `LensWorkspace` 를 그린다. HTTP 는 대시보드의 base URL·토큰을 넘겨받아(EmbeddedLens.tsx:16-24) 패키지 안에서 부른다. 대시보드 소스만 스캔하면 보이지 않는다. 첫 화면(트레이스 탭)에서 불리는 것만 정리했다. 나머지는 목록만 적는다.

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v1/traces` | LUI/components/lens/traces/api.ts:120 (`anyRecorded`, 쿼리 `start_ms=0`), LUI/lib/http/requests.ts:43 | lens | STUB | **404 또는 501 권장**. 둘 다 "tracing not enabled" 로 처리하고 재시도를 멈춘다(LUI/components/lens/traces/list/useAgentTraces.ts:21-27,117) | — |
| GET | `/v1/traces/agents` | LUI/components/lens/traces/api.ts:93 ← LUI/components/lens/agents/useAgents.ts:18 | lens | STUB | 404 안전(`data ?? []`, useAgents.ts:26) | — |
| GET | `/lens` | LUI/components/lens/data/service.ts:70 ← LensWorkspace.tsx:92, useLensReadiness.ts:23 (관리자 등급일 때만) | lens | STUB | `{"lenses":[],"workers":[],"tracing_enabled":false}` (schema LensList) | **위험**: 200 에 `lenses` 가 없으면 `list.data?.lenses.length` 예외(useLensReadiness.ts:37). 4xx 면 오류 문구로 버틴다 |
| GET | `/lens/activity/available` | service.ts:71 ← useLensReadiness.ts:24(`/lens` 성공 뒤) | lens | STUB | `{"requests":false,"traces":false}` (schema ActivityAvailability) | 미확인 |
| GET/POST/PUT/PATCH | `/lens/{lens_id}`, `/lens/{lens_id}/runs`, `/lens/{lens_id}/runs/{job_id}`, `/lens/{lens_id}/runs/{job_id}/reviews`, `/lens/{lens_id}/executions/{execution_id}`, `/lens/{lens_id}/findings/{finding_id}`, `/lens/{lens_id}/cancel`, `/lens/preview/sample`, `/lens/watch-all`, `/lens/signals`, `/lens/agents`, `/lens/models`, `/lens/model_group/info`, `/lens/datasets…`, `/lens/evals…`, `/lens/github/…`, `/lens/traces/findings`, `/lens/traces/signals`, `/lens/feedback`, `/lens/feedback/summary`, `/lens/tracing/keys`, `/lens/service`, `/v1/traces/{trace_id}…`, POST `/v1/traces`, `/public/litellm_model_cost_map` | LUI/components/lens/data/service.ts:70-134, LUI/components/lens/datasets/client.ts:44-71, LUI/components/lens/evals/runs/client.ts:25-41, LUI/components/lens/agents/githubConnection.ts:59-98, LUI/components/lens/traces/api.ts:93-120, LUI/components/lens/onboarding/tracing/TracingSetupCard.tsx:240,341,468, LUI/lib/http/requests.ts:23-81 | lens(조사·설정·데이터셋·평가·GitHub 하위 화면) | STUB | 첫 화면에서 `/lens` 가 빈 목록이면 하위 호출은 거의 일어나지 않는다. 개별 모양은 미확인 | 미확인 |

---

#### 추가 발견

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/model-insights` | ModelInsightsView.tsx:81 | model-insights | STUB | 위 D/model-insights 참고. 1차 스캔에서 UI 경로로 잘못 빠졌던 것이다 | 날짜 RangeError |
| GET | `/v1/traces`, `/v1/traces/agents`, `/lens…` 일체 | LUI(위 표) | lens | STUB | 상위 목록은 대시보드의 `agentTraceListCall`(NW:2060) 등을 호출처 없음(UNUSED)으로 봤다. 실제로는 lens-ui 패키지가 같은 경로를 부른다 | 위 표 |
| GET | `/{entity}/daily/activity/aggregated…` 의 실제 경로 30개 | entityFetchFns.ts:45-52 | usage | 위 표 | `{entity}` 를 user/team/tag/organization/customer/agent × (aggregated, /keys, /search, /model_top_keys, /export)로 풀면 30개다. 그중 user·team 10개만 MUST | — |
| GET | `/key/list?status=deleted`, `/v2/team/list?status=deleted` | D/hooks/keys/useKeys.ts:157 ← C/DeletedKeysPage/DeletedKeysPage.tsx:15; D/hooks/teams/useTeams.ts:295 ← C/DeletedTeamsPage/DeletedTeamsPage.tsx:18 | logs("Deleted Keys"·"Deleted Teams" 탭) | 4장 범위(IMPLEMENTED 경로) | `{"keys":[],"total_count":0}`, `{"teams":[],"total":0}` (`?.`·`??` 로 읽음) | — |
| GET | `/config/list?config_type=general_settings` | NW:3286 (getGeneralSettingsCall) ← D/cost-optimization/_components/PromptCachingTab.tsx:28 | cost-optimization | 8장 범위 | 응답을 `generalSettingsItem[]` 로 setState 하고 `prev.map`(:37) | 배열이 아니면 편집 때 예외 |
| GET | `/model_group/info` | NW:1782 (modelHubCall) ← UsageAIChatPanel.tsx:137 | usage(Ask AI 모델 목록) | IMPLEMENTED(5장 범위) | `data?.data?.length` 로 읽어 안전 | — |
| GET | `/v2/model/info`(무한 스크롤) | RequestLogsFilters.tsx:139 `useInfiniteModelInfo` | logs(모델 필터) | 5장 범위 | — | 미확인 |
| GET | `/user/list` | D/hooks/users/useUsers.ts:43 `useUserEmailLookup` ← C/logs/request/RequestLogsTable.tsx:93; `useUserLookup` ← LogDetailsDrawer.tsx:251 | logs | 4장 범위(IMPLEMENTED) | — | — |
| GET | `/key/info` | NW:2411 (keyInfoV1Call) ← C/logs/request/RequestLogsPanel.tsx:126 | logs(키 ID 를 누를 때) | 4장 범위(IMPLEMENTED) | — | — |

#### 미확인

- 사용량 응답 행(`results[i]`)의 필수 필드 전체: 스키마 `DailySpendData`·`BreakdownMetrics` 를 따르면 되지만 대시보드가 모든 필드를 읽는지는 다 확인하지 않았다(`metrics` 가 없으면 깨지는 것만 확인).
- `/spend/logs/ui` 행(`LogEntry`)의 필수 필드: 표·서랍이 읽는 필드 목록은 따로 정리하지 않았다(C/logs/columns 계열). 실제 구현 때 C/logs/request/RequestLogsTable.tsx 와 C/logs/detail 을 따라가야 한다.
- old-usage `/global/activity` 의 `sum_*` 누락, `/global/spend/tags` 의 `spend_per_tag` 누락 때 BarChart·포매터가 견디는지.
- `/user/daily/activity/aggregated/cache_leakage_keys` 의 `api_keys` 누락 때 CacheLeakageCard 동작.
- Lens 하위 화면(조사·데이터셋·평가·GitHub)의 응답 모양 전체.
- `/user/daily/activity/export` 가 JSON 형식일 때 모달이 Blob 을 어떻게 저장하는지(C/EntityUsageExport).

---

## 7. playground·mcp-servers·agents·vector-stores·search-tools·memory·skills·prompts·workflows·app/chat·connect·moyai

약어: NW=`components/networking.tsx`, D/=`app/(dashboard)/`, C/=`components/`. 경로는 대시보드 `src/` 기준.

**공통 전제(먼저 읽을 것)**
- 플레이그라운드의 추론 호출은 대부분 OpenAI/Anthropic SDK 를 쓴다. SDK `baseURL` 은 `customBaseUrl || getGatewayBaseUrl()` 이고(C/llm_calls/chat_completion.tsx:64-70 → C/llm_calls/gateway_client.ts:15, C/llm_calls/responses_api.tsx:106-115, D/playground/llm_calls/image_generation.tsx:23-27 등), `getGatewayBaseUrl()` 은 `gateway_base_url`(런타임 설정) 또는 프록시 origin 이다(NW:245-250, NW:597). **`/v1` 이 붙지 않는다.** 그래서 OpenAI SDK 는 `/chat/completions`, `/responses`, `/images/generations`, `/images/edits`, `/audio/speech`, `/audio/transcriptions` 로 보낸다. Anthropic SDK 는 자체적으로 `/v1/messages` 를 붙인다. 직접 fetch 하는 임베딩도 `${base}/embeddings` 다(D/playground/llm_calls/embeddings_api.tsx:38). 사용자가 설정에서 "Custom proxy base URL" 을 `…/v1` 로 넣으면 그때만 `/v1/…` 가 된다(D/playground/components/chat/playgroundSettings.ts:112).
- 기본 키 출처는 "session" 이다. 즉 **UI 로그인 토큰을 그대로 추론 키로 쓴다**(D/playground/components/chat/PlaygroundChat.tsx:134). 인증 헤더는 `Authorization: Bearer <UI 토큰>`(전역 헤더 이름, C/llm_calls/gateway_client.ts:11-19). 추론 경로가 UI 세션 토큰을 받아들여야 플레이그라운드가 동작한다.
- 플레이그라운드가 화면을 열 때 부르는 모델 목록은 **GET `/model_group/info`** 다(usePlaygroundModels → C/llm_calls/fetch_models.tsx:53 → NW:1782 `modelHubCall`). `/v1/models`·`/models` 가 아니다. 읽는 법: `fetchedModels?.data` 가 배열일 때만 씀(fetch_models.tsx:54-56), 항목은 `model_group || id || model_name`, `mode`(엔드포인트별 필터), `providers`.
- 추론 오류는 모두 대화창의 오류 말풍선으로 끝난다(D/playground/components/chat/usePlaygroundSend.ts:248-253). 404/501 이어도 화면은 깨지지 않는다.

### D/playground

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/chat/completions` | C/llm_calls/chat_completion.tsx:146 (`client.chat.completions.create`, SDK baseURL 에 /v1 없음) ← D/playground/components/chat/dispatchRequest.ts:91; Compare 탭 D/playground/components/compareUI/CompareUI.tsx(→chat_completion), Compliance 탭 D/playground/components/complianceUI/ComplianceUI.tsx:453 | playground(Chat 기본 엔드포인트·Compare·Compliance·Agent builder) | MUST | **IMPLEMENTED 아님: `/v1` 없는 별칭 필요.** 우리 서버는 `/v1/chat/completions` 만 있음. 스트리밍(SSE)·비스트리밍 둘 다(chat_completion.tsx:146-152). UI 세션 토큰으로 호출됨 | — (오류는 말풍선) |
| POST | `/responses` | C/llm_calls/responses_api.tsx:213-216 (`client.responses.create`) ← dispatchRequest.ts:132 | playground(엔드포인트 `/v1/responses` 선택 시), @chat(app/chat/page.tsx:194) | MUST | **IMPLEMENTED 아님: `/v1` 없는 별칭 필요**(우리는 `/v1/responses`). 본문 `tools` 에 `server_url: ${mcpBase}/mcp/{name}` 가 들어감(responses_api.tsx:167,181) — 대시보드가 부르는 게 아니라 게이트웨이가 쓸 값 | — |
| POST | `/embeddings` | D/playground/llm_calls/embeddings_api.tsx:38-40 (직접 fetch) | playground(엔드포인트 embeddings) | MUST | **IMPLEMENTED 아님: `/v1` 없는 별칭 필요.** 읽기: `responseData?.data?.[0]?.embedding` 없으면 오류(embeddings_api.tsx:56-60) | — |
| POST | `/v1/messages` | D/playground/llm_calls/anthropic_messages.tsx:88,102 (Anthropic SDK, baseURL=gateway origin → SDK 가 `/v1/messages` 붙임) | playground(엔드포인트 anthropic) | STUB | 미지원이면 오류 응답(4xx/501 JSON). 말풍선 오류로 끝남 | — |
| POST | `/images/generations` | D/playground/llm_calls/image_generation.tsx:33 (`client.images.generate`) | playground(엔드포인트 image) | STUB | 오류 응답 가능 | — |
| POST | `/images/edits` | D/playground/llm_calls/image_edits.tsx:45 (`client.images.edit`, multipart) | playground | STUB | 오류 응답 가능 | — |
| POST | `/audio/speech` | D/playground/llm_calls/audio_speech.tsx:37 | playground | STUB | 오류 응답 가능 | — |
| POST | `/audio/transcriptions` | D/playground/llm_calls/audio_transcriptions.tsx:38 (multipart) | playground | STUB | 오류 응답 가능 | — |
| POST | `/v1beta/interactions` | D/playground/llm_calls/interactions_api.tsx:34,51 | playground(엔드포인트 interactions) | STUB | 오류 응답 가능 | — |
| GET(WebSocket) | `/v1/realtime?model={m}[&guardrails=…]` | D/playground/components/chat_ui/RealtimePlayground.tsx:99-106 (`ws(s)://<gateway>/v1/realtime`, 서브프로토콜 `realtime`, `openai-insecure-api-key.<토큰>`) | playground(엔드포인트 realtime) | STUB | WS 업그레이드 거절(404)이면 onerror/onclose 로 상태 메시지만. HTTP 핸들러 불필요 | — |
| POST | `/a2a/{agentId}/message/send` | D/playground/llm_calls/a2a_send_message.tsx:123,149 (JSON-RPC) | playground(엔드포인트 A2A — 에이전트가 있어야 선택 가능) | STUB | 에이전트 목록이 비면 호출되지 않음. 오류 시 `await response.json()` 후 throw(a2a_send_message.tsx:168-170) — 본문이 JSON 이 아니면 SyntaxError 지만 catch 로 말풍선 | — |
| POST | `/a2a/{agentId}` | a2a_send_message.tsx:254,279 (스트리밍, Compare 탭 A2A) | playground(Compare A2A) | STUB | 위와 같음 | — |
| POST | `/mcp-rest/tools/call` | NW:5301 (`callMCPTool`) ← dispatchRequest.ts:271; D/playground/components/chat_ui/ChatUI.tsx(Agent builder) | playground(엔드포인트 MCP, 서버 직접 호출), mcp-servers(도구 테스트 D/mcp-servers/_components/ToolTestPanel.tsx) | STUB | MCP 서버 목록이 비면 호출 안 됨. 응답 `{content:[{type:"text",text}]}` 을 문자열화(dispatchRequest.ts:65-73) | — |
| POST | `/utils/test_policies_and_guardrails` | NW:4031 (`testPoliciesAndGuardrails`) ← D/playground/components/complianceUI/ComplianceUI.tsx:488,563 | playground(Compliance 탭, 게이트웨이 모드 아닌 경우) | STUB | `{"guardrail_errors":[],"results":[]}` (`guardrail_errors = []` 기본값 ComplianceUI.tsx:488, `response.results ?? []` :577). `{}` 도 안전 → 전부 "allowed" 로 표시 | — |
| GET | `/v1/containers/{container_id}/files/{file_id}/content` | D/playground/components/chat_ui/CodeInterpreterOutput.tsx:59-60,100-101 | playground(Responses + code interpreter 결과에 이미지 인용이 있을 때만) | STUB | `!response.ok` 면 조용히 무시(:68) | — |

플레이그라운드가 읽지만 다른 장 소유인 엔드포인트(모양만 적음):
- GET `/model_group/info`(IMPLEMENTED): 위 공통 전제 참조. `{"data":[]}` 면 모델 없음 표시.
- GET `/v1/mcp/server`(IMPLEMENTED 빈 값): 플레이그라운드는 `Array.isArray(r) ? r : r.data ?? []`(D/playground/components/chat/usePlaygroundCatalog.ts:22-23, ChatUI.tsx:304) 라 `[]`·`{"data":[]}` 둘 다 안전. **그러나 mcp-servers 화면은 배열만 받는다(아래).**
- GET `/v1/mcp/toolset`: 배열 아니면 `[]` 로 취급, 오류면 null → `isReady=false`(usePlaygroundCatalog.ts:44-49,57). `[]` 권장.
- GET `/mcp-rest/tools/list?server_id=`: `response.error` 있으면 throw, `response.tools ?? []`(usePlaygroundCatalog.ts:71-73). `{"tools":[]}`.
- GET `/v1/agents`: **배열을 바로 `.sort`**(D/playground/llm_calls/fetch_agents.tsx:60-63). A2A 엔드포인트 선택 시에만(PlaygroundChat.tsx:156). `[]` 이어야 함, `{"data":[]}` 면 TypeError(react-query 오류, 화면은 유지).
- GET `/guardrails/list`: `response.guardrails ?? []`(usePlaygroundCatalog.ts:96). `{"guardrails":[]}`. Compliance 는 실패 시 `{guardrails:[]}` 대체(ComplianceUI.tsx:174).
- GET `/policies/list`: `response.policies ?? []`(usePlaygroundCatalog.ts:108), `viewPolicies` 권한일 때만.
- GET `/vector_store/list`: `response.data ?? []`(usePlaygroundCatalog.ts:127-128).
- GET `/tag/list`: **`Object.values(response)`**(usePlaygroundCatalog.ts:142-143) — 객체 맵 `{}` 이어야 함. `null` 이면 TypeError(쿼리 오류만, 화면 유지).
- GET `/sso/get/ui_settings`: 페이지 진입 시(D/playground/page.tsx:28 → utils/proxyUtils.ts:7), 실패하면 null 로 무시.
- Agent builder 탭: GET `/v2/model/info`(D/playground/llm_calls/fetch_agents.tsx:86, `response?.data ?? []`), POST `/model/new`·`/model/{id}/update`(PATCH)·`/model/delete`, POST `/key/generate`(D/playground/components/chat_ui/AgentBuilderView.tsx:341,376,408,435).
- System One 탭: `/v1/systemone`, `/v1/decisions` 는 5장 범위.

### D/mcp-servers

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v1/mcp/server/health?include_reachability=true[&server_ids=…]` | NW:4885 (`fetchMCPServerHealth`) ← D/hooks/mcpServers/useMCPServerHealth.ts:19-23 ← D/mcp-servers/_components/mcp_servers.tsx:184 (30초마다) | mcp-servers | STUB | `[]` | 위험: `healthStatuses.map` (mcp_servers.tsx:191) — 배열이 아니면(예: `{}`) TypeError 로 목록 렌더 중단 |
| GET | `/v1/mcp/user-env-vars/status` | NW:7858 (`listMCPUserEnvVarStatus`) ← mcp_servers.tsx:233-236 | mcp-servers | STUB | `[]`. 실패하면 함수가 `[]` 로 삼킴(NW:7855-7862) | 위험: 200 으로 객체를 주면 `for (const status of envVarStatuses ?? [])`(mcp_servers.tsx:242) 가 not iterable 로 깨짐 — 배열 또는 오류만 |
| GET | `/get/mcp_semantic_filter_settings` | NW:3694 ← D/hooks/mcpSemanticFilterSettings/useMCPSemanticFilterSettings.ts:12 ← C/Settings/AdminSettings/MCPSemanticFilterSettings/MCPSemanticFilterSettings.tsx:123-124 | mcp-servers(설정 탭) | STUB | `{"field_schema":{},"values":{}}`. `data?.field_schema`, `data?.values ?? NO_STORED_VALUES` 로 읽어 `{}` 도 안전 | — |
| PATCH | `/update/mcp_semantic_filter_settings` | NW:3709 | mcp-servers | STUB | 오류 응답 가능(throw → 저장 실패 토스트) | — |
| POST | `/v1/responses` | NW:3769 (`testMCPSemanticFilter`, `${gateway}/v1/responses`) ← C/Settings/AdminSettings/MCPSemanticFilterSettings/semanticFilterTestUtils.ts:51 | mcp-servers(시맨틱 필터 테스트 버튼) | IMPLEMENTED | 응답 헤더(필터 정보)를 읽음. 헤더 없으면 "not enabled" 경고만(semanticFilterTestUtils.ts:55-57) | — |
| GET | `/get/mcp_tool_search_settings` | D/hooks/mcpToolSearchSettings/useMCPToolSearchSettings.ts:10,16 ← C/Settings/AdminSettings/MCPToolSearchSettings/MCPToolSearchSettings.tsx:51 | mcp-servers(설정 탭) | STUB | `{"values":{}}` 또는 `{}`. `data?.values` 없으면 기본값 유지(:57,67-70). 오류면 탭 안에 Alert 만 | — |
| PATCH | `/update/mcp_tool_search_settings` | useMCPToolSearchSettings.ts:11,22 (`apiClient.patch`) | mcp-servers | STUB | 오류 응답 가능(onError 토스트) | — |
| POST | `/mcp-rest/test/tools/list` | NW:6667 (`testMCPToolsListRequest`, POST) ← C/hooks/useTestMCPConnection.tsx:157, D/mcp-servers/_components/mcp_server_edit.tsx:581 | mcp-servers(생성·편집 폼 "연결 테스트") | STUB | content-type 이 JSON 아니면 throw(NW:6690-6697). `{"tools":[],"error":"not supported","message":"…"}` 이면 폼에 오류 문구 | — |
| GET | `/v1/mcp/openapi-registry` | NW:4811 ← D/mcp-servers/_components/OpenAPIQuickPicker.tsx:40 | mcp-servers(생성 폼 OpenAPI) | STUB | `{"apis":[]}` (`data.apis ?? []`, 실패 시 `[]`) | — |
| GET | `/v1/mcp/discover` | NW:4835 ← D/mcp-servers/_components/mcp_discovery.tsx:56 | mcp-servers("Discover" 모달) | STUB | `{"servers":[],"categories":[]}` (`data.servers || []`) | — |
| GET | `/v1/mcp/network/client-ip` | NW:4910 ← D/mcp-servers/_components/MCPNetworkSettings.tsx:234 | mcp-servers(네트워크 설정) | STUB | `{"ip":null}`. 실패해도 null | — |
| POST | `/v1/mcp/server/import` | NW:4951 ← D/mcp-servers/_components/ImportMCPServers.tsx:48 | mcp-servers(가져오기) | STUB | 오류 응답 권장. 200 이면 `response.imported.length` 필수(ImportMCPServers.tsx:50) | 위험: 200 에 `imported` 없으면 TypeError(catch 로 잡혀 토스트) |
| POST | `/v1/mcp/server/register` | NW:5014 ← D/mcp-servers/_components/CreateMCPServer.tsx:453 (비관리자 제출) | mcp-servers | STUB | 오류 응답 가능 | — |
| GET | `/v1/mcp/server/submissions` | NW:5023 ← D/mcp-servers/_components/MCPSubmissionsTab.tsx:515 | mcp-servers(Submissions 탭) | STUB | `{"total":0,"pending_review":0,"active":0,"rejected":0,"items":[]}` | 위험: `setSummary(res)` 후 `summary.items.filter`(MCPSubmissionsTab.tsx:519,552) — `items` 없으면 TypeError 로 탭 렌더 실패 |
| PUT | `/v1/mcp/server/{server_id}/approve` | NW:5076 ← MCPSubmissionsTab.tsx:568 | mcp-servers | STUB | 오류 응답 가능(토스트). 목록이 비어 호출 안 됨 | — |
| PUT | `/v1/mcp/server/{server_id}/reject` | NW:5098 ← MCPSubmissionsTab.tsx:581 | mcp-servers | STUB | 위와 같음 | — |
| DELETE | `/v1/mcp/server/{server_id}` | NW:4969 ← mcp_servers.tsx:411 | mcp-servers | STUB | 목록이 비어 호출 안 됨. 오류 응답 가능 | — |
| DELETE | `/v1/mcp/toolset/{toolset_id}` | NW:5005 ← D/mcp-servers/_components/MCPToolsetsTab.tsx:408 | mcp-servers(Toolsets 탭) | STUB | 위와 같음 | — |
| GET | `/v1/mcp/sessions` | NW:5045 ← D/mcp-servers/_components/MCPGatewaySessionsTab.tsx:264 | mcp-servers(Live connections 탭) | STUB | `{"total_sessions":0,"worker_pid":0,"by_client":[],"by_user":[],"sessions":[]}` (`total_sessions === 0` 이면 빈 안내 후 반환, :155) | 위험: `total_sessions` 가 0 이 아니거나 빠지면 `data.by_client.length`·`data.sessions.map`(:169-200) TypeError. 오류 응답이면 Alert 만 |
| DELETE | `/v1/mcp/sessions?{selector}` | NW:5051 ← MCPGatewaySessionsTab.tsx:270 | mcp-servers | STUB | 세션이 없어 호출 안 됨 | — |
| GET | `/v1/mcp/server/{server_id}/user-credentials` | NW:5057 ← D/mcp-servers/_components/MCPServerUserCredentialsPanel.tsx:133 | mcp-servers(서버 상세) | STUB | `[]` (`items.length`, :66) | 서버 목록이 비어 열리지 않음 |
| DELETE | `/v1/mcp/server/{server_id}/oauth-user-credential?user_id=` · `/v1/mcp/server/{server_id}/user-credential?user_id=` | NW:5068 (`revokeMCPServerUserCredential`, route 는 credential_type 로 결정 NW:5067) ← MCPServerUserCredentialsPanel.tsx:137 | mcp-servers | STUB | 호출 안 됨(목록 빔) | — |
| GET | `/v1/mcp/server/{server_id}/user-env-vars` | NW:7836 ← D/mcp-servers/_components/UserEnvVarsModal.tsx:123 | mcp-servers(사용자 env 입력 모달) | STUB | `{"server_id":"…","required":[],"missing_count":0}` (`status?.required ?? []` :163) | — |
| POST · DELETE | `/v1/mcp/server/{server_id}/user-env-vars` | NW:7844, 7851 ← UserEnvVarsModal.tsx:128,141 | mcp-servers | STUB | 오류 응답 가능 | — |
| GET | `/v1/mcp/server/{server_id}/oauth-user-credential/status` | NW:7809 ← D/mcp-servers/_components/mcp_tools.tsx:99, C/chat/MCPAppsPanel.tsx:185 | mcp-servers(OAuth 서버 도구 탭), @chat | STUB | `!ok` 면 `{has_credential:false,is_expired:false}` 로 대체(NW:7817-7819). 404 로 충분 | — |
| POST · DELETE | `/v1/mcp/server/{server_id}/oauth-user-credential` | NW:7743, 7778 ← CreateMCPServer.tsx:473, mcp_server_edit.tsx:799, C/hooks/useUserMcpOAuthFlow.tsx:218, C/chat/MCPCredentialsTab.tsx:81 | mcp-servers, @chat | STUB | 오류 응답 가능(`detail` 문자열 메시지로 throw) | — |
| POST | `/v1/mcp/server/{server_id}/user-credential` | D/playground/components/chat/ByokConnect.tsx:49; C/mcp_tools/ByokCredentialModal.tsx:53 | playground(BYOK 서버 선택 시), mcp-servers | STUB | 오류 응답 가능(토스트) | — |
| POST | `/v1/mcp/server/oauth/session` | NW:6733 (`cacheTemporaryMcpServer`) ← C/hooks/useMcpOAuthFlow.tsx:174 | mcp-servers(OAuth 서버 생성 시 "Authorize") | STUB | 오류 응답 가능. 200 이면 `server_id` 필수(없으면 throw :139-141) | — |
| POST | `/v1/mcp/server/oauth/{server_id}/register` | NW:6766 ← useMcpOAuthFlow.tsx:184, C/hooks/useUserMcpOAuthFlow.tsx:98, C/hooks/useToolsOAuthFlow.tsx:92 | mcp-servers, @chat, @connect | STUB | 오류 응답 가능 | — |
| GET(브라우저 이동) | `/v1/mcp/server/oauth/{server_id}/authorize?…` | NW:6805 (`buildMcpOAuthAuthorizeUrl`, URL 만 만들고 `window.location` 이동) | mcp-servers, @chat, @connect | STUB | API 호출 아님(페이지 이동). 앞 단계가 실패하면 도달 안 함 | — |
| POST | `/v1/mcp/server/oauth/{server_id}/token` | NW:6844 (form-urlencoded) ← useMcpOAuthFlow.tsx:346 등, 콜백 app/mcp/oauth/callback/page.tsx 경유 | mcp-servers, @chat, @mcp/oauth/callback | STUB | 위 흐름이 시작되지 않으면 호출 안 됨 | — |
| POST | `/v1/mcp/server` · PUT `/v1/mcp/server` | NW:4925-4940(create), 4960(update) ← CreateMCPServer.tsx:452, mcp_server_edit.tsx | mcp-servers | STUB | 오류 응답 가능(폼 토스트). GET 은 4장 범위 | — |
| POST · PUT | `/v1/mcp/toolset` | NW:4986, 4995 ← MCPToolsetsTab.tsx | mcp-servers | STUB | 오류 응답 가능 | — |

mcp-servers 가 읽는 다른 장 소유 엔드포인트:
- **GET `/v1/mcp/server`(IMPLEMENTED 빈 값)**: `useMCPServers` 가 응답을 그대로 `MCPServer[]` 로 받고(D/hooks/mcpServers/useMCPServers.ts:12-14) `mcpServers.map`(mcp_servers.tsx:195). **반드시 `[]`.** `{"data":[]}` 면 TypeError 로 mcp-servers 화면 전체가 깨진다. (플레이그라운드·@chat 은 두 모양 다 받음.)
- GET `/.well-known/litellm-ui-config`: `mcp_stdio_enabled !== false`(D/mcp-servers/_components/StdioAvailability.tsx:13) — 3장.
- GET `/mcp-rest/tools/list`: `Array.isArray(result) ? result : result?.tools ?? []`(MCPToolsetsTab.tsx:64-65).

### D/agents

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/public/agents/fields` | NW:502 (`getAgentCreateMetadata`, 인증 헤더 없는 fetch) ← D/agents/_components/agent_info.tsx:101, D/agents/_components/add_agent_form.tsx(fetchMetadata) | agents(생성 모달·상세) | STUB | `[]` | 위험: 응답을 그대로 `agentTypeMetadata` 로 두고 `.find`/`.map`(add_agent_form.tsx:251,773; agent_info.tsx:130) — 배열이 아닌 200(`{}`)이면 TypeError. 404 면 catch 로 빈 배열 유지 |
| GET | `/v1/agents/{agent_id}` | NW:6114 (`getAgentInfo`) ← agent_info.tsx:111 | agents(상세) | STUB | 목록이 비면 상세가 열리지 않음. 404 가능 | — |
| PATCH | `/v1/agents/{agent_id}` | NW:6175 (`patchAgentCall`) ← agent_info.tsx:247 | agents | STUB | 호출 안 됨 | — |
| DELETE | `/v1/agents/{agent_id}` | NW:5882 (`deleteAgentCall`) ← D/agents/_components/AgentsPanel.tsx:122 | agents | STUB | 호출 안 됨 | — |
| POST | `/v1/agents/{agent_id}/kill_switch` | NW:6128 ← D/agents/_components/AgentKillSwitchDangerZone.tsx:44 | agents | STUB | 호출 안 됨 | — |
| GET | `/v1/agents/{agent_id}/identity` | D/agents/_components/AgentIdentityDetails.tsx:29-31 (`apiClient.get`) | agents(상세, 관리자·identity 설정된 에이전트만) | STUB | 호출 안 됨(목록 빔). **목록에 없던 엔드포인트(추가 발견)** | — |
| GET | `/v1/agents/identity/providers` | D/agents/_components/AgentIdentityFields.tsx:34 | agents(생성 폼, provider=microsoft_entra 선택 시) | STUB | `[]` (문자열 배열에 `.flatMap`). 배열이 아니면 `.then` 안 TypeError → `.catch` 로 오류 문구만 | — |
| POST | `/v1/a2a/discover` | NW:4742 (`gatewayApiClient`) ← D/agents/_components/agent_card_discovery.tsx:139 | agents(생성 폼, URL 입력 시 자동) | STUB | 오류 응답 가능(폼에 오류 표시) | — |

agents 가 읽는 다른 장 소유: **GET `/v1/agents?health_check=`** 는 `data.map(toAgent)`(NW:6105-6110) — **`[]` 이어야 함**(객체면 TypeError, AgentsPanel.tsx:55-61 catch 로 빈 목록). POST `/v1/agents` 생성, GET `/key/list`(add_agent_form.tsx:188), GET `/models`(add_agent_form.tsx:205), GET `/key/list?agent_id=`(agent_info.tsx:84 `useKeys`, `keysData?.keys ?? []`).

### D/vector-stores

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v1/indexes` | NW:5658 ← D/vector-stores/_components/IndexesTab.tsx:54 | vector-stores(Indexes 탭) | STUB | `{"data":[]}` (`response.data || []`). 오류면 토스트 | — |
| POST | `/vector_store/new` | NW:5600 ← D/vector-stores/_components/CreateVectorStore.tsx | vector-stores | STUB | 오류 응답 가능 | — |
| POST | `/vector_store/info` | NW:5692 ← D/vector-stores/_components/vector_store_info.tsx:93 | vector-stores(상세) | STUB | 목록 빔 → 호출 안 됨. 200 이면 `vector_store` 없을 때 "load failed" 처리(:95-97) | — |
| POST | `/vector_store/update` | NW:5717 | vector-stores | STUB | 호출 안 됨 | — |
| POST | `/vector_store/delete` | NW:5667 | vector-stores | STUB | 호출 안 됨 | — |
| POST | `/rag/ingest` | NW:5751 (`${gateway}/rag/ingest`, multipart FormData) ← CreateVectorStore.tsx:189 | vector-stores(파일 업로드 생성) | STUB | 오류 응답 가능 | — |
| POST | `/v1/vector_stores/{vector_store_id}/search` | NW:6889 (`${gateway}/v1/…`) ← D/vector-stores/_components/VectorStoreTester.tsx:58 | vector-stores(상세 테스트) | STUB | 호출 안 됨 | — |

다른 장 소유: GET `/vector_store/list` → `response.data || []`(D/vector-stores/_components/index.tsx:51-52), GET `/credentials` → `response.credentials || []`(index.tsx:64-65).

### D/search-tools

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/search_tools/ui/available_providers` | NW:5174 ← D/search-tools/_components/SearchTools.tsx:67, CreateSearchTools.tsx:122 | search-tools | STUB | `{"providers":[]}` (`providersResponse?.providers || []` SearchTools.tsx:86) | — |
| POST | `/search_tools` | NW:5133 | search-tools | STUB | 오류 응답 가능 | — |
| PUT · DELETE | `/search_tools/{search_tool_id}` | NW:5148, 5163 | search-tools | STUB | 호출 안 됨(목록 빔) | — |
| POST | `/search_tools/test_connection` | NW:5202 ← D/search-tools/_components/SearchConnectionTest.tsx:30 | search-tools(생성 폼) | STUB | 오류 응답 가능(catch → 실패 결과 표시) | — |
| POST | `/v1/search/{search_tool_name}` | NW:6922 (`${gateway}/v1/search/…`) ← D/search-tools/_components/SearchToolTester.tsx:49 | search-tools | STUB | 호출 안 됨 | — |

다른 장 소유: GET `/search_tools/list` → `res.search_tools || []`(SearchTools.tsx:58).

### D/memory

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v1/memory?[search|key_prefix|key]&page&page_size` | NW:7911 (`${gateway}/v1/memory`) ← D/memory/_components/MemoryView.tsx:46 | memory | STUB | `{"memories":[],"total":0}` (`data?.memories ?? []`, `data?.total ?? 0` :55-56 — `{}` 도 안전) | — |
| POST | `/v1/memory` | NW:7943 | memory | STUB | 오류 응답 가능(toast.error) | — |
| PUT · DELETE | `/v1/memory/{key}` | NW:7970, 7988 (키는 세그먼트별 인코딩, 백엔드 `{key:path}` NW:7871-7880) | memory | STUB | 호출 안 됨 | — |

### D/skills

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| DELETE | `/claude-code/plugins/{plugin_name}` | NW:7397 ← D/skills/_components/ClaudeCodePluginsPanel.tsx:68 | skills | STUB | 호출 안 됨 | — |

다른 장 소유: **GET `/claude-code/plugins?enabled_only=`** → `setPluginsList(response.plugins)`(ClaudeCodePluginsPanel.tsx:46-47), 그 뒤 `pluginsList.find`(:111)·DataTable `data`. **`{"plugins":[]}` 필수** — `{}` 면 `pluginsList` 가 undefined 가 되어 표/검색이 깨질 위험. `data` 가 비면 NW:7274 가 throw(catch 로 무시).

### D/prompts

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/prompts` | NW:4629 | prompts | STUB | 오류 응답 가능 | — |
| PUT · DELETE | `/prompts/{prompt_id}` | NW:4639, 4649 | prompts | STUB | 호출 안 됨(목록 빔) | — |
| GET | `/prompts/{prompt_id}/info[?environment=]` | NW:4581 ← D/prompts/_components/prompt_info.tsx:60,176; prompt_editor_view/index.tsx:279 | prompts(상세·편집) | STUB | 호출 안 됨(목록 빔) | — |
| GET | `/prompts/{prompt_id}/versions[?environment=]` | NW:4597 ← prompt_info.tsx:86, prompt_editor_view/VersionHistorySidePanel.tsx:51 | prompts | STUB | 호출 안 됨. 쓸 때 `{"prompts":[]}` (`setVersions(response.prompts)` 는 기본값 없음) | — |
| POST | `/utils/dotprompt_json_converter` | NW:4668 (multipart) ← D/prompts/_components/add_prompt_form.tsx:92 | prompts(.prompt 파일 업로드) | STUB | 오류 응답 가능 | — |
| POST | `/prompts/test` | D/prompts/_components/prompt_editor_view/conversation_panel/useConversation.ts:92 (스트리밍 fetch) | prompts(편집기 대화 테스트) | STUB | 오류 응답 가능 | — |

다른 장 소유: **GET `/prompts/list?environment=`** → `setPromptsList(response.prompts)`(D/prompts/_components/index.tsx:62) 기본값 없음. **`{"prompts":[]}` 필수**(`{}` 면 표에 undefined 전달). 키 생성·편집 모달도 `response.prompts.map`(C/organisms/create_key_button.tsx:362, C/templates/key_edit_view.tsx:184 — try/catch 안이라 화면은 유지).

### D/workflows

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v1/workflows/runs?limit=100` | D/workflows/WorkflowRuns.tsx:397 | workflows | STUB | `{"runs":[]}` (`data.runs ?? []`). `!ok` 면 throw → console 만 | — |
| GET | `/v1/workflows/runs/{run_id}/events` | WorkflowRuns.tsx:421 | workflows(행 클릭) | STUB | `{"events":[]}`. `!ok` 면 `{events:[]}` 대체 | — |
| GET | `/v1/workflows/runs/{run_id}/messages` | WorkflowRuns.tsx:424 | workflows | STUB | `{"messages":[]}` | — |

### app/chat · app/connect · app/moyai · app/mcp/oauth/callback (대시보드 밖 페이지)

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v1/mcp/user-credentials` | NW:7822 ← C/chat/MCPCredentialsTab.tsx:74 | @chat(credentials) | STUB | `[]`. `!ok` 면 `[]`(NW:7827) | 위험: 200 에 배열 아닌 값이면 `credentials.map`(MCPCredentialsTab.tsx:173) TypeError |
| GET | `/authorize/flow?flow={handle}` | NW:4860 (`credentials:"include"`) ← C/chat/ConnectFlowSurface.tsx:33 | @connect, @chat(`connect_flow` 쿼리가 있을 때만) | STUB | 오류 응답이면 `failed` → "stale" 배너(ConnectFlowBanner.tsx:87). 쿼리 없으면 호출 안 됨 | — |
| POST(폼 제출) | `/authorize/complete` | C/chat/ConnectFlowBanner.tsx:86,119 (`<form method="POST" action>`) | @connect, @chat | STUB | 브라우저 폼 이동. 흐름이 없으면 도달 안 함 | — |
| POST | `/moyai/connect/start` | NW:7230 ← app/moyai/page.tsx:57 | @moyai(관리자 Quick connect 버튼) | STUB | 오류 응답 가능(단, 호출부에 catch 없음 → unhandled rejection, 화면은 유지). 200 이면 `connect_url` 로 이동 | — |

@chat 이 읽는 다른 장 소유(모양만): POST `/responses`(위 playground 행, app/chat/page.tsx:194), GET `/model_group/info`(app/chat/page.tsx:98), GET `/v1/mcp/server?connected_app_view=`(C/chat/MCPAppsPanel.tsx:208-212, 배열·`{data}` 둘 다 받음), GET `/mcp-rest/tools/list`(`Array.isArray(toolsData?.tools)`, MCPAppsPanel.tsx:171-174), GET `/key/list`·POST `/key/{key}/regenerate`(C/chat/KeysPanel.tsx:24), GET `/spend/logs/ui`·`/spend/logs/ui/{id}`(C/chat/LogsPanel.tsx:7), GET `/user/daily/activity/aggregated`(C/chat/UsagePanel.tsx:6), GET `/get/ui_settings`(app/chat/layout.tsx:24).

#### 추가 발견

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/chat/completions`, `/responses`, `/embeddings`, `/images/generations`, `/images/edits`, `/audio/speech`, `/audio/transcriptions` | SDK 경로(위 playground 표) | playground, @chat | 위 표 | 할당 목록에는 `chatConstants.ts` 의 라벨 문자열(`/v1/…`)만 있었음. 라벨은 화면 표시용이고 실제 경로는 `/v1` 없는 쪽 | — |
| GET | `/v1/agents/{agent_id}/identity` | D/agents/_components/AgentIdentityDetails.tsx:29-31 | agents | STUB | 위 agents 표 | — |
| POST | `/v1/chat/completions`, `/v1/embeddings` | NW:2327-2343 (`testModelGroupConnection`, `gatewayApiClient`) | models-and-endpoints(자동 라우터 연결 테스트) — **5장 범위, 참고로만** | IMPLEMENTED | 여기는 `/v1` 이 붙음 | — |
| POST · PUT | `/v1/mcp/server`, `/v1/mcp/toolset` (쓰기) | NW:4925-4995 | mcp-servers | STUB | 할당 목록에선 GET 과 묶여 4장 로 갔음. 쓰기는 위 mcp-servers 표에 적음 | — |

비 API 로 제외한 것: `chatConstants.ts:37-46`·`compareUI/endpoint_config.ts:10,37` 의 `/v1/...` 는 드롭다운 라벨, `/mcp`·`/toolset/{name}/mcp`·`${mcpBase}/mcp/{name}`(responses_api.tsx:167,181)은 화면에 보여 주거나 요청 본문에 넣는 URL 이다. 대시보드가 직접 부르지 않는다.

#### 미확인
- `/v1/realtime` WS 를 거절했을 때 RealtimePlayground 가 정확히 어떤 상태 문구를 띄우는지는 확인하지 않았다(onerror/onclose 처리부 미확인).
- `/prompts/test` 응답 스트림 형식과, 오류일 때 대화 패널이 어떻게 처리하는지는 미확인(useConversation.ts:92 이후 미독).
- 플레이그라운드 Compliance 의 "게이트웨이 모드" 분기가 `/chat/completions` 를 쓰는지 `/utils/test_policies_and_guardrails` 를 쓰는지 고르는 조건(ComplianceUI.tsx:453 vs 488) 세부는 미확인.
- `gateway_base_url` 을 우리 서버가 런타임 설정(`/litellm/.well-known/litellm-ui-config`, NW:597)으로 따로 주면 SDK 대상 origin 이 바뀐다. 이 값을 `…/v1` 로 주면 `/v1` 별칭 문제를 피할 수 있으나, 같은 base 로 `/v1/memory`·`/v1/vector_stores/…`·`/rag/ingest`·`/v1/realtime` 도 만들어지므로(`/v1/v1/…` 가 됨) 권하지 않는다 — 추정, 실제 영향 범위 미확인.

---

## 8. admin-panel·logging-and-alerts·caching·router-settings·cost-tracking·guardrails·policies·organizations·access-groups·budgets·projects·telemetry·transform-request

약어: NW=`components/networking.tsx`, D/=`app/(dashboard)/`, C/=`components/`, H/=`app/(dashboard)/hooks/`.
이 13개 화면은 모두 보장 화면이 아니다. 그래서 이 장의 행은 전부 STUB 이다(보장 화면에서도 불리는 행은 비고에 적음).

공통 관찰
- 탭은 대부분 `TabsContent ... keepMounted` 라서 **화면을 여는 순간 모든 탭의 조회가 한꺼번에 나간다**(logging-and-alerts C/settings.tsx:582-701, caching D/caching/_components/cache_dashboard.tsx:188-374, router-settings D/router-settings/_components/general_settings.tsx:313-325, guardrails D/guardrails/_components/GuardrailsPanel.tsx:124-204, policies D/policies/_components/index.tsx:458-607). 예외는 admin-panel 로, `keepMounted` 가 없어 첫 탭(SSO)만 마운트된다(D/admin-panel/_components/AdminPanel.tsx:423-432).
- 직접 `fetch` 하는 호출부는 오류 시 `await response.json()` 으로 본문을 읽는다(예: H/configOverrides/hashicorpVaultApi.ts:12-14, H/proxyConfig/useProxyConfig.ts:94-98). 404 본문이 JSON 이 아니면 SyntaxError 로 바뀔 뿐 화면이 깨지지는 않는다. 그래도 스텁·미구현 404 는 JSON 본문(`{"detail":"Not Found"}`)으로 주는 편이 메시지가 제대로 뜬다.
- "오류 응답 가능" = 호출부가 예외를 잡아 토스트만 띄우므로 4xx `{"detail":{"error":"이 게이트웨이는 지원하지 않습니다"}}` 를 돌려줘도 화면이 유지된다.

---

### D/admin-panel

화면 로드 시: `/sso/get/ui_settings`(D/admin-panel/page.tsx:6 → H/proxySettings/useProxySettings.ts:28) → 3장 소유. `/get/sso_settings` 2회(아래). 나머지 탭(SCIM·UI Settings·Logging·Vault·CyberArk·Plugins·Web Search)은 탭을 눌러야 마운트된다. UI Settings 탭의 `/get/ui_settings`·`/update/ui_settings`·`/get/user_banner`·`/update/user_banner` 는 3장 소유. Plugins 탭의 `/config/field/info` 는 5장 소유(응답은 `data?.field_value` 만 읽음, C/Settings/AdminSettings/PluginSettings/PluginSettings.tsx:44). Web Search 탭의 `/search_tools/list` 는 4장 소유(`search_tools` 또는 `data` 배열이 아니면 `[]` 로 처리, WebSearchInterceptionSettings.tsx:90-94).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/get/sso_settings` | NW:6416 (getSSOSettings) ← H/sso/useSSOSettings.ts:63 ← C/Settings/AdminSettings/SSOSettings/SSOSettings.tsx:56; AdminPanel.tsx:88 | admin-panel(로드 시) | STUB | `{"values":{},"field_schema":{}}`. AdminPanel 은 `ssoData.values` 를 검사하고 try/catch(AdminPanel.tsx:88-101) | 위험: 응답이 오면 `ssoSettings?.values.google_client_id` 로 `values` 를 옵셔널 없이 읽는다. `values` 가 없으면 렌더 중 TypeError (SSOSettings.tsx:60-64) |
| PATCH | `/update/sso_settings` | NW:6427 (updateSSOSettings) ← SSO 추가·편집·삭제 모달 | admin-panel | STUB | 오류 응답 가능(모달이 토스트). 성공 본문은 읽지 않음(미확인: 모달별 onSuccess 세부) | — |
| GET | `/get/allowed_ips` | NW:1794 (getAllowedIPs) ← AdminPanel.tsx:111 | admin-panel("Allowed IPs" 버튼, `premiumUser` 일 때만) | STUB | `{"data":[]}`. 함수가 `data.data` 를 돌려주고(NW:1795) 호출부가 `data && data.length` 로 검사 | 위험: 추가·삭제 직후 재조회는 `updatedIPs.length`(AdminPanel.tsx:154) 를 바로 읽는다. `data` 필드가 없으면 TypeError → catch 로 토스트(화면은 유지) |
| POST | `/add/allowed_ip` | NW:1805 (addAllowedIP) ← AdminPanel.tsx:128 | admin-panel | STUB | 오류 응답 가능(AdminPanel.tsx:133-136) | — |
| POST | `/delete/allowed_ip` | NW:1816 (deleteAllowedIP) ← AdminPanel.tsx:150 | admin-panel | STUB | 오류 응답 가능(AdminPanel.tsx:156-158) | — |
| GET | `/config_overrides/hashicorp_vault` | H/configOverrides/hashicorpVaultApi.ts:5 ← C/Settings/AdminSettings/HashicorpVault/HashicorpVault.tsx:40 | admin-panel(Vault 탭) | STUB | `{"values":{},"field_schema":{"properties":{}}}`. `data?.values ?? {}`(HashicorpVault.tsx:47), `data?.field_schema?.properties ?? {}`(EditHashicorpVaultModal.tsx:83). 404 면 isError 표시 | — |
| POST | `/config_overrides/hashicorp_vault` | hashicorpVaultApi.ts:23 | admin-panel | STUB | 오류 응답 가능(onError 토스트) | — |
| DELETE | `/config_overrides/hashicorp_vault` | hashicorpVaultApi.ts:44 | admin-panel | STUB | 오류 응답 가능(HashicorpVault.tsx:65-72) | — |
| POST | `/config_overrides/hashicorp_vault/test_connection` | hashicorpVaultApi.ts:62 | admin-panel | STUB | 오류 응답 가능(HashicorpVault.tsx:55-57). 성공 시 `result.message` 만 읽음 | — |
| GET | `/config_overrides/cyberark` | H/configOverrides/cyberArkApi.ts:26 ← C/Settings/AdminSettings/CyberArk/CyberArk.tsx:39 | admin-panel(CyberArk 탭) | STUB | `{"config_type":"cyberark","values":{},"field_schema":{"properties":{}}}`. `data?.values ?? {}`(CyberArk.tsx:46), `data?.field_schema?.properties ?? {}`(EditCyberArkModal.tsx:76) | — |
| POST | `/config_overrides/cyberark` | cyberArkApi.ts:32 | admin-panel | STUB | 오류 응답 가능 | — |
| DELETE | `/config_overrides/cyberark` | cyberArkApi.ts:35 | admin-panel | STUB | 오류 응답 가능 | — |
| POST | `/config_overrides/cyberark/test_connection` | cyberArkApi.ts:38 | admin-panel | STUB | 오류 응답 가능(CyberArk.tsx:54-56) | — |
| GET | `/get/websearch_interception_settings` | NW:3742 ← C/Settings/AdminSettings/WebSearchInterceptionSettings/WebSearchInterceptionSettings.tsx:265 | admin-panel(Web Search 탭) | STUB | `{"values":{},"field_schema":{},"active_on_this_pod":true}`. `data?.values ?? NO_STORED_VALUES`(:295), `schema?.properties?.…`(:171) 모두 옵셔널 | — |
| PATCH | `/update/websearch_interception_settings` | NW:3756 | admin-panel | STUB | 오류 응답 가능(훅이 토스트, 미확인: 훅 세부) | — |
| GET | `/config/list?config_type=general_settings` | H/proxyConfig/useProxyConfig.ts:82 (getProxyConfigCall) ← C/Settings/AdminSettings/LoggingSettings/LoggingSettings.tsx:234; NW:3289 (getGeneralSettingsCall) ← D/router-settings/_components/general_settings.tsx:240 | admin-panel(Logging 탭), router-settings(로드), models-and-endpoints(C/model_dashboard/ModelSettingsModal/ModelSettingsModal.tsx:36, 모달), cost-optimization·mcp-servers | STUB | **`[]`(맨 배열)**. 항목 모양 `{field_name, field_type, field_description, field_value, stored_in_db, field_default_value}`(useProxyConfig.ts:43-51) | 위험: `proxyConfigData?.find(...)`(LoggingSettings.tsx:237,241; ModelSettingsModal.tsx:53), `generalSettings.map/find`(general_settings.tsx:247,258, 렌더 :338) — 객체(`{"data":[]}` 등)를 주면 `.find/.map is not a function` 으로 렌더 오류 |
| POST | `/config/field/update` | NW:3480 (updateConfigFieldSetting), H/storeModelInDB/useStoreModelInDB.ts:18 | admin-panel(Plugins), router-settings, logging-and-alerts(alerting 설정 저장), models-and-endpoints(모델 설정 모달) | STUB | 오류 응답 가능. NW:3481 은 성공 시 스스로 토스트 | — |
| POST | `/config/field/delete` | NW:3496 (deleteConfigFieldSetting), H/proxyConfig/useProxyConfig.ts:121 | admin-panel(Logging), router-settings | STUB | 오류 응답 가능. 단 router-settings 는 `deleteConfigFieldSetting` 을 await 없이 부르고 로컬 상태를 먼저 바꾼다(general_settings.tsx:283-293) | — |
| POST | `/config/update` | NW:3541 (setCallbacksCall), H/storeRequestInSpendLogs/useStoreRequestInSpendLogs.ts:24 | admin-panel(Logging), logging-and-alerts, router-settings(라우팅 그룹·폴백·로드밸런싱 저장) | STUB | 오류 응답 가능(호출부 try/catch 토스트, 예: C/settings.tsx:466) | — |

### D/logging-and-alerts

`D/logging-and-alerts/page.tsx:7` → `C/settings.tsx`. 6개 탭이 모두 `keepMounted`(C/settings.tsx:582-701) 라서 로드 시 `/callbacks/configs`, `/get/config/callbacks`, `/alerting/settings`, `/email/event_settings`, CloudZero 조회가 모두 나간다. CloudZero(`/cloudzero/settings` 등)는 6장 소유.

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/callbacks/configs` | NW:14 (getCallbackConfigsCall) ← C/settings.tsx:333 | logging-and-alerts(로드) | STUB | **`[]`**. `setCallbackConfigs(data \|\| [])`(:335). 실패 시 토스트(:338) | 위험: 객체를 주면 콜백 추가·편집 모달에서 `callbackConfigs.find`(C/settings.tsx:263) 가 TypeError. 빈 배열은 안전 |
| GET | `/get/config/callbacks` | NW:3277 (getCallbacksCall) ← C/settings.tsx:396; C/router_settings/index.tsx:34; C/Settings/RouterSettings/Fallbacks/Fallbacks.tsx:155; D/models-and-endpoints/panels/ModelRetrySettingsPanel.tsx:40, ModelGroupAliasPanel.tsx:36; C/MSTeamsSettings.tsx:71 | logging-and-alerts·router-settings(로드), models-and-endpoints(재시도·별칭 패널) | STUB | `{"callbacks":[],"available_callbacks":{},"alerts":[],"router_settings":{},"active_alerting_destinations":[]}`. models-and-endpoints 의 두 패널은 `info.router_settings?.…` 처럼 옵셔널로 읽어 안전(ModelGroupAliasPanel.tsx:39, ModelRetrySettingsPanel.tsx:41,57) | 위험(가장 큼): `setCallbacks(data.callbacks)`·`setAlerts(data.alerts)`(C/settings.tsx:397,413) 에 기본값이 없다. `alerts` 가 undefined 면 keepMounted 된 Email·MS Teams 탭이 렌더 중 `alerts.filter(...)` 로 TypeError(C/email_settings.tsx:108, C/MSTeamsSettings.tsx:104) → **화면 전체 오류**. `callbacks` 가 undefined 면 DataTable `data` 가 undefined(LoggingCallbacksTable.tsx:63). router-settings 는 `"model_group_retry_policy" in router_settings`(C/router_settings/index.tsx:36, Fallbacks.tsx:157) 가 undefined 에서 TypeError(then 안이라 미처리 거부, 화면은 기본값으로 뜸). 네 필드를 반드시 넣을 것 |
| POST | `/config/callback/delete` | NW:6647 (deleteCallback) ← C/settings.tsx(삭제 확인) | logging-and-alerts | STUB | 오류 응답 가능 | — |
| GET | `/alerting/settings` | NW:858 (alertingSettingsCall) ← C/alerting/alerting_settings.tsx:33 | logging-and-alerts(로드, Alerting Settings 탭) | STUB | **`[]`**. 항목 `{field_name, field_type, field_value, field_default_value, field_description, stored_in_db, premium_field}`(alerting_settings.tsx:10-18) | 위험: `setAlertingSettings(data)` 후 DynamicForm 에 그대로 넘김(:109). 객체를 주면 `.map` TypeError. 오류(404)면 `.then` 만 있어 미처리 거부, 상태는 `[]` 로 남아 안전 |
| GET | `/email/event_settings` | NW:5801 (getEmailEventSettings) ← C/email_events/email_event_settings.tsx:30 | logging-and-alerts(로드, Email 탭) | STUB | `{"settings":[]}` | 위험: `setEventSettings(response.settings)`(:31) 뒤 렌더에서 `eventSettings.map`(:107). `settings` 가 없으면 렌더 TypeError. 404 는 catch 로 토스트만(안전) |
| PATCH | `/email/event_settings` | NW:5827 | logging-and-alerts | STUB | 오류 응답 가능 | — |
| POST | `/email/event_settings/reset` | NW:5854 | logging-and-alerts | STUB | 오류 응답 가능 | — |
| GET | `/health/services?service={name}` | NW:3245 (serviceHealthCheck) ← C/settings.tsx:595,679; C/email_settings.tsx:163; C/MSTeamsSettings.tsx:146 | logging-and-alerts("Test" 버튼) | STUB | 오류 응답 가능(호출부가 결과 본문을 읽지 않고 성공 토스트만, 미확인: 각 버튼의 catch) | — |

### D/caching

로드 시(모든 탭 keepMounted): `/global/activity/cache_hits`(H/caching/useCacheActivity.ts:19, 6장 소유), `/cache/settings`, `/coordination_redis/settings`, 임베딩 모델 목록 `fetchAvailableModels` → `/model_group/info`(D/caching/_components/cache_settings/index.tsx:66, 5장 소유).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/cache/settings` | NW:3328 (getCacheSettingsCall) ← D/caching/_components/cache_settings/index.tsx:46 | caching(로드) | STUB | `{"current_values":{}}`. `data.current_values ?? {}`(:47), 실패 시 토스트(:53) | 위험: 본문이 빈 200 이면 `request()` 가 undefined 를 돌려줘 `data.current_values` 가 TypeError → catch 토스트(화면 유지). JSON 객체로 줄 것 |
| POST | `/cache/settings` | NW:3353 (updateCacheSettingsCall) ← cache_settings/index.tsx:131 | caching | STUB | 오류 응답 가능 | — |
| POST | `/cache/settings/test` | NW:3338 ← cache_settings/index.tsx:103 | caching | STUB | `{"status":"failed","message":"지원하지 않음"}` 또는 오류 응답. `result.status === "success"` 검사(:106) | — |
| GET | `/cache/ping` | NW:3592 (cachingHealthCheckCall) ← cache_dashboard.tsx:130 | caching(Health 탭 버튼) | STUB | 오류 응답 가능. 오류 메시지를 JSON 으로 파싱 시도 후 표시(cache_dashboard.tsx:133-) | — |
| GET | `/coordination_redis/settings` | NW:3370 ← H/coordinationRedis/useCoordinationRedisSettings.ts ← D/caching/_components/coordination_redis_settings/index.tsx:29 | caching(로드) | STUB | `{"values":{}}` | 위험: `if (data) form.reset(buildInitialValues(data.values))`(:36-37) 에서 `values` 가 없으면 `values[field.name]`(coordinationRedisUtils.ts:63) TypeError(effect 안이라 화면 오류). 404 는 isError 토스트로 안전 |
| POST | `/coordination_redis/settings` | NW:3397 | caching | STUB | 오류 응답 가능 | — |
| POST | `/coordination_redis/settings/test` | NW:3382 ← coordination_redis_settings/index.tsx:66 | caching | STUB | `{"status":"unhealthy","error":"지원하지 않음"}` 또는 오류 응답. `result.status === "healthy"`(:67) | — |

참고: `/global/activity/cache_hits`(6장)는 `activity?.filter_options.key_aliases`(cache_dashboard.tsx:114-115) 로 `filter_options` 를 옵셔널 없이 읽는다. 스텁은 `{"groups":[],"filter_options":{"key_aliases":[],"models":[]}}` 이어야 한다(6장 에 전달).

### D/router-settings

`D/router-settings/_components/general_settings.tsx`. 5개 탭 keepMounted(:313-325). 로드 시 `/config/list`(위 admin-panel 행), `/get/config/callbacks` 2회(Loadbalancing·Fallbacks, 위 logging 행), `/router/settings` 3회(C/router_settings/index.tsx:47, Fallbacks.tsx:162, H/routingGroups/useRoutingGroups.ts:18 — 4장 소유), `/router/fields`, 모델 허브(`useModelHub`, C/routing_groups/index.tsx:23 — 5장 소유), 비용표(`useModelCostMap`, Fallbacks.tsx:143 — 5장 소유).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/router/fields` | H/router/useRouterFields.ts:36 ← C/routing_groups/index.tsx:22 | router-settings(로드), cost-optimization·mcp-servers(미확인: 렌더 경로) | STUB | `{"fields":[],"routing_strategy_descriptions":{}}`. 전부 옵셔널로 읽음(routing_groups/index.tsx:49-54) | — |

참고(4장 소유 `/router/settings`): `useRoutingGroups` 는 `data?.current_values ?? {}`, `Array.isArray(data?.fields)` 로 방어(useRoutingGroups.ts:19-20). C/router_settings/index.tsx:57-58 은 `data.fields` 가 있을 때만 `forEach`. 최소 `{"fields":[],"current_values":{},"source":{}}` 면 세 호출부 모두 안전.

### D/cost-tracking

`D/cost-tracking/_components/cost_tracking_settings.tsx:99-114` 에서 로드 시 discount·margin·block-unpriced 3개 + `fetchAvailableModels` → `/model_group/info`(C/llm_calls/fetch_models.ts:54 → NW:1782, 5장 소유, `fetchedModels?.data` 배열만 사용).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/config/cost_discount_config` | D/cost-tracking/_components/use_discount_config.ts:32 | cost-tracking(로드) | STUB | `{"values":{}}`. `data.values \|\| {}`(:42), `!response.ok` 면 콘솔만 | — |
| PATCH | `/config/cost_discount_config` | use_discount_config.ts:58 | cost-tracking | STUB | 오류 응답 가능(`response.ok` 검사 후 토스트) | — |
| GET | `/config/cost_margin_config` | use_margin_config.ts:38 | cost-tracking(로드) | STUB | `{"values":{}}`(:50) | — |
| PATCH | `/config/cost_margin_config` | use_margin_config.ts:64 | cost-tracking | STUB | 오류 응답 가능 | — |
| GET | `/config/block_requests_for_models_without_pricing` | use_block_unpriced_config.ts:29 | cost-tracking(로드) | STUB | `{"enabled":false}`. `Boolean(data?.enabled)`(:30). 404 면 오류 토스트가 매번 뜸 → 스텁 권장 | — |
| PATCH | `/config/block_requests_for_models_without_pricing` | use_block_unpriced_config.ts:42 | cost-tracking | STUB | 오류 응답 가능(:51-53) | — |
| POST | `/cost/estimate` | D/cost-tracking/_components/pricing_calculator/use_multi_cost_estimate.ts:49 | cost-tracking(가격 계산기 입력 시) | STUB | 오류 응답 가능(`response.ok` 가 아니면 항목에 오류 표시). 성공 본문은 `cost_per_request`, `daily_cost` 등 숫자 필드를 `formatCost` 로 null 안전하게 표시(multi_cost_results.tsx:19-64). 우리 모델 단가로 실제 구현도 쉬움 | — |

### D/guardrails

`D/guardrails/_components/GuardrailsPanel.tsx`. 로드 시(탭 keepMounted, `AddGuardrailForm` 은 `visible` prop 만 바뀌고 항상 마운트 :169): `/v2/guardrails/list` → 실패하면 `/guardrails/list`(NW:3825,3842 — 4장 소유, 우리 서버의 빈 `/guardrails/list` 로 떨어짐), `/guardrails/submissions`, `/guardrails/ui/add_guardrail_settings`(5장 소유)·`/guardrails/ui/provider_specific_params`·`/models`·`/model_group/info` 를 `Promise.all`(add_guardrail_form.tsx:295-300).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/guardrails/submissions` | NW:3899 (listGuardrailSubmissions) ← D/guardrails/_components/TeamGuardrailsTab.tsx:851 | guardrails(로드, Submitted 탭) | STUB | `{"submissions":[],"summary":{"total":0,"pending_review":0,"active":0,"rejected":0}}` | 위험: `setSummary(res.summary)`(:856) 후 렌더에서 `summary.total`(:894-897). `summary` 가 빠지면 렌더 TypeError → 화면 오류. 404 는 catch(:857) 로 안전 |
| POST | `/guardrails/submissions/{guardrail_id}/approve` | NW:3915 (approveGuardrailSubmission) ← TeamGuardrailsTab.tsx:957 | guardrails | STUB | 오류 응답 가능(추가 발견 행) | — |
| POST | `/guardrails/submissions/{guardrail_id}/reject` | NW:3925 (rejectGuardrailSubmission) ← TeamGuardrailsTab.tsx:970 | guardrails | STUB | 오류 응답 가능(추가 발견 행) | — |
| POST | `/guardrails/register` | H/guardrails/useRegisterGuardrail.ts:29 ← TeamGuardrailsTab.tsx:839 | guardrails | STUB | 오류 응답 가능 | — |
| GET | `/guardrails/ui/provider_specific_params` | NW:6021 ← add_guardrail_form.tsx:298; guardrail_info.tsx:206; guardrail_provider_fields.tsx:274 | guardrails(로드) | STUB | `{}`. `Object.entries(providerParamsResponse)`(guardrail_info_helpers.tsx:55,105) | 위험: `null`/비객체면 `Object.entries` TypeError → Promise.all catch 로 토스트 "Failed to load guardrail configuration"(add_guardrail_form.tsx:314-316). 404 도 같은 토스트가 **로드마다** 뜸 → 스텁 권장 |
| POST | `/guardrails` | NW:4752 (createGuardrailCall) ← add_guardrail_form.tsx:766; custom_code/CustomCodeModal.tsx:339; D/policies/_components/index.tsx:348 | guardrails, policies, cost-optimization | STUB | 오류 응답 가능 | — |
| PATCH | `/guardrails/{guardrail_id}` | NW:6211 (updateGuardrailCall) ← guardrail_info.tsx:494; TeamGuardrailsTab.tsx:905 | guardrails | STUB | 오류 응답 가능 | — |
| DELETE | `/guardrails/{guardrail_id}` | NW:5966 (deleteGuardrailCall) ← GuardrailDeleteModal.tsx:36 | guardrails | STUB | 오류 응답 가능 | — |
| GET | `/guardrails/{guardrail_id}/info` | NW:6134 ← guardrail_info.tsx:166 | guardrails(상세, 목록이 비면 도달 불가) | STUB | 오류 응답 가능. 성공 시 `litellm_params?.…` 옵셔널(:170) | — |
| POST | `/guardrails/apply_guardrail` | NW:6245 ← GuardrailTestPlayground.tsx:86 | guardrails(Playground 탭, 가드레일 선택 후) | STUB | 오류 응답 가능 | — |
| POST | `/guardrails/test_custom_code` | NW:6345 ← custom_code/CustomCodeModal.tsx:390 | guardrails | STUB | 오류 응답 가능 | — |
| GET | `/guardrails/ui/category_yaml/{category_name}` | NW:6051 ← content_filter/ContentCategoryConfiguration.tsx:107,142 | guardrails(콘텐츠 필터 폼) | STUB | 오류 응답 가능(미확인: catch 세부) | — |
| GET | `/guardrails/ui/major_airlines` | NW:6079 ← content_filter/CompetitorIntentConfiguration.tsx:87 | guardrails | STUB | 오류 응답 가능(미확인) | — |
| POST | `/guardrails/validate_blocked_words_file` | NW:6388 ← content_filter/ContentFilterConfiguration.tsx:186 | guardrails | STUB | 오류 응답 가능 | — |

참고(4장·5장 소유 행에 전달): `/v2/guardrails/list`·`/guardrails/list` 는 `{"guardrails":[]}` — GuardrailsPanel 은 `setGuardrailsList(response.guardrails)` 로 기본값 없이 넣고(GuardrailsPanel.tsx:54) DataTable(guardrail_table.tsx:48)·`.find`(:97) 에 쓴다. `guardrails` 키가 없으면 TypeError. `/guardrails/ui/add_guardrail_settings` 는 `{"supported_entities":[],"supported_actions":[],"pii_entity_categories":[],"content_filter_settings":{}}` 권장(옵셔널 접근 :279,385 과 비옵셔널 접근 :948-962 혼재, 비옵셔널은 PII·콘텐츠 필터 공급자를 골랐을 때만).

### D/policies

`D/policies/_components/index.tsx`. 4개 탭 keepMounted(:458-607). 로드 시 `/policy/templates`, `/policies/list`(4장 소유, `response.policies \|\| []` :126 로 안전), `/policies/attachments/list`, guardrails 목록(:154, `response.guardrails \|\| []`), 시뮬레이터 탭의 `/team/list`·`/key/list`·`/models`(policy_test_panel.tsx:115-131, 입력 시, 미확인: 정확한 시점).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/policy/templates` | NW:4085 ← D/policies/_components/policy_templates.tsx:157 | policies(로드) | STUB | **`[]`(맨 배열)** | 위험: `setTemplates(data)`(:158) 후 `templates.forEach/filter/length`(:116,129,235). 객체를 주면 렌더 TypeError. 404 는 catch 토스트(안전) |
| GET | `/policies/attachments/list` | NW:4435 ← index.tsx:140 | policies(로드) | STUB | `{"attachments":[]}`. `response.attachments \|\| []`(:141) | — |
| POST | `/policies` | NW:4313 (createPolicyCall) | policies | STUB | 오류 응답 가능 | — |
| GET | `/policies/{policy_id}` | NW:4423 (getPolicyInfo) | policies(상세) | STUB | 오류 응답 가능(목록이 비면 도달 불가) | — |
| PUT | `/policies/{policy_id}` | NW:4323 (updatePolicyCall) | policies | STUB | 오류 응답 가능 | — |
| DELETE | `/policies/{policy_id}` | NW:4413 (deletePolicyCall) ← index.tsx:196 | policies | STUB | 오류 응답 가능 | — |
| PUT | `/policies/{policy_id}/status` | NW:4401 (updatePolicyVersionStatus) | policies | STUB | 오류 응답 가능 | — |
| GET | `/policies/name/{policy_name}/versions` | NW:4338 (listPolicyVersions) | policies(상세) | STUB | 오류 응답 가능(미확인: 응답 모양 — 상세 진입 불가라 생략) | — |
| POST | `/policies/name/{policy_name}/versions` | NW:4370 (createPolicyVersion) | policies | STUB | 오류 응답 가능 | — |
| GET | `/policies/{policy_id}/resolved-guardrails` | NW:4501 | policies(상세) | STUB | 오류 응답 가능 | — |
| POST | `/policies/attachments` | NW:4445 | policies | STUB | 오류 응답 가능 | — |
| DELETE | `/policies/attachments/{attachment_id}` | NW:4456 ← index.tsx:214 (useDeletePolicyAttachment) | policies | STUB | 오류 응답 가능 | — |
| POST | `/policies/attachments/estimate-impact` | NW:4541 | policies(첨부 폼) | STUB | 오류 응답 가능(미확인: 폼이 결과를 옵셔널로 읽는지) | — |
| POST | `/policies/resolve` | NW:4531 ← policy_test_panel.tsx:145 | policies(시뮬레이터 실행) | STUB | 오류 응답 가능 | — |
| POST | `/policies/test-pipeline` | NW:4487 | policies(파이프라인 빌더) | STUB | 오류 응답 가능 | — |
| POST | `/policy/templates/enrich` | NW:4101 | policies(템플릿 사용) | STUB | 오류 응답 가능 | — |
| POST | `/policy/templates/enrich/stream` | NW:4180 (enrichPolicyTemplateStream) | policies | STUB | 오류 응답 가능(스트림 시작 전 `response.ok` 검사, 미확인) | — |
| POST | `/policy/templates/suggest` | NW:4136 ← ai_suggestion_modal | policies | STUB | 오류 응답 가능 | — |
| POST | `/policy/templates/test` | NW:4152 | policies | STUB | 오류 응답 가능 | — |

### D/organizations

`D/organizations/_components/OrganizationsPanel.tsx`. `premiumUser` 가 아니면 업그레이드 안내만 그리고(:98) `useOrganizations` 도 `enabled: premiumUser === true`(H/organizations/useOrganizations.ts:25) 라서 **아무 호출도 없다**. premium 이면 `/organization/list`(4장 소유) 와 `/models`(useUserModels, H/models/useModels.ts:267 — `response["data"].map` 으로 `data` 필수).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/organization/new` | C/organization/org-create/OrgCreateDialog.tsx:26 (fetchClient) | organizations | STUB | 오류 응답 가능(ApiError → 다이얼로그 토스트, 미확인) | — |
| PATCH | `/v2/organization/{organization_id}` | C/organization/org-settings/OrgSettingsForm.tsx:35 | organizations(상세) | STUB | 오류 응답 가능 | — |
| DELETE | `/organization/delete` | NW:1289 ← OrganizationsPanel.tsx:80 | organizations | STUB | 오류 응답 가능 | — |
| POST | `/organization/member_add` | NW:3105 ← C/organization/organization_view.tsx | organizations(상세) | STUB | 오류 응답 가능 | — |
| DELETE | `/organization/member_delete` | NW:3136 | organizations(상세) | STUB | 오류 응답 가능 | — |
| PATCH | `/organization/member_update` | NW:3155 | organizations(상세) | STUB | 오류 응답 가능 | — |

참고(4장 소유 `/organization/list`): **`[]`(맨 배열)**. `data: organizations = []`(OrganizationsPanel.tsx:45) 는 undefined 만 막고 객체는 못 막는다. 열 셀은 `row.original.members?.length ?? 0`(OrganizationsTableColumns.tsx:170) 로 옵셔널. [차이:조직]이므로 premium 판정을 거짓으로 두면 이 화면의 호출은 0개다.

### D/access-groups

`D/access-groups/_components/AccessGroupsPage.tsx`. 로드 시 `/v1/access_group` GET(H/accessGroups/useAccessGroups.ts:20, admin 역할일 때만 :48 — 4장 소유). 생성 다이얼로그·편집 모달은 `useAgents`(`/v1/agents`)·`useMCPServers`(`/v1/mcp/server`) 를 부른다(4장 소유).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/v1/access_group/{access_group_id}` | H/accessGroups/useAccessGroupDetails.ts:11 ← D/access-groups/_components/AccessGroupsDetailsPage.tsx:109 | access-groups(상세) | STUB | 오류 응답 가능(목록이 비면 도달 불가) | — |
| PUT | `/v1/access_group/{access_group_id}` | H/accessGroups/useEditAccessGroup.ts:32 | access-groups | STUB | 오류 응답 가능(추가 발견 행) | — |
| DELETE | `/v1/access_group/{access_group_id}` | H/accessGroups/useDeleteAccessGroup.ts:10 | access-groups | STUB | 오류 응답 가능(추가 발견 행) | — |

참고(4장 소유 `/v1/access_group` GET): **`[]`(맨 배열)**. `(groupsData ?? []).map(...)`(AccessGroupsPage.tsx:40) — 객체를 주면 `.map` TypeError 로 화면 오류. POST `/v1/access_group` 는 D/access-groups/_components/access-group-create/AccessGroupCreateDialog.tsx:32(4장 소유).

### D/budgets

`D/budgets/_components/BudgetPanel.tsx:42` → `useBudgetList` → GET `/management/v1/budgets`(H/budgets/useBudgets.ts:19,31 — 4장 소유). 생성·수정·삭제는 `/budget/new|update|delete`(4장 소유).
이 장의 행 없음. 전달할 모양: `{"data":[],"links":{"self":"","first":"","last":""},"meta":{"page":1,"page_size":50,"total_count":0,"total_pages":0}}`(schema.d.ts:34137-34142, 34095-34120). **위험**: `data?.meta.total_count`(H/common/useResourceList.ts:125) 가 `meta` 를 옵셔널 없이 읽는다 — `meta` 가 빠지면 렌더 TypeError. 이 공통 훅(`useResourceList`)을 쓰는 다른 목록도 같다. [차이:예산표].

### D/projects

`D/projects/_components/ProjectsPage.tsx:19-20` → `/project/list`(H/projects/useProjects.ts:49 — 4장 소유), `useTeams`(`/team/list`). 상세는 `/project/info`, `useTeam`(`/team/info`), 키 표 `useKeys`(`/key/list`).

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| GET | `/project/info?project_id={id}` | H/projects/useProjectDetails.ts:11 ← D/projects/_components/ProjectDetailsPage.tsx:28 | projects(상세, 목록이 비면 도달 불가) | STUB | 오류 응답 가능. 읽기는 모두 옵셔널(`project?.spend ?? 0` :36-46) | — |
| POST | `/project/new` | H/projects/useCreateProject.ts:27 | projects | STUB | 오류 응답 가능 | — |
| POST | `/project/update` | H/projects/useUpdateProject.ts:31 | projects | STUB | 오류 응답 가능 | — |

참고(4장 소유 `/project/list`): **`[]`(맨 배열)**. `projects ?? []`(ProjectsPage.tsx:41) 와 `data={projects}`(:80) — 객체면 필터 `.filter` TypeError. 삭제 엔드포인트는 대시보드에 없다(H/projects 에 delete 훅 없음).

### D/telemetry

`D/telemetry/_components/TelemetrySettingsPage.tsx:198` → GET `/telemetry/settings`(H/telemetry/useTelemetrySettings.ts:20, proxy admin 계열만 — 3장 소유). PUT 도 3장 소유. 이 장의 행 없음.
전달할 모양: 404(또는 오류)가 **가장 안전**하다 — `settings === undefined` 면 "Could not load telemetry settings" 문구만 그린다(:204-205). 200 을 줄 거면 전체 필드가 필요하다: `{"available_destinations":[],"destination":null,"destination_set_by_environment":false,"editable":false,"environment_variables":[],"flush_interval_seconds":3600,"groups":[],"report":null,"report_is_sample":false,"retention_days":0,"set_by_environment":false,"stored_destination":null,"stored_groups":null,"vetoed":false}`(schema.d.ts:46872-46901). **위험**: `{}` 를 주면 `settings.groups.filter`(:44)·`settings.available_destinations.includes`(:82) 가 렌더 TypeError.

### D/transform-request

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/utils/transform_request` | NW:1321 (transformRequestCall) ← D/transform-request/TransformRequestPanel.tsx:90 | transform-request(버튼) | STUB | 오류 응답 가능(:111-113 토스트). 성공 시 `raw_request_api_base`·`raw_request_body` 가 없으면 JSON 문자열을 그대로 보여 줌(:93-110). 로드 시 호출 없음 | — |

### (화면 밖) 8장에 넣은 기타 행

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/config/pass_through_endpoint/{endpoint_path}` | NW:6614 (updatePassThroughEndpoint) | models-and-endpoints(패스스루 편집) | STUB | 오류 응답 가능(미확인: 호출부 catch). 메서드는 POST(NW:6618) | — |

---

#### 추가 발견

| 메서드 | 경로 패턴 | 호출 위치 | 화면 | 분류 | 최소 응답(STUB) / 비고 | 위험 |
|---|---|---|---|---|---|---|
| POST | `/guardrails/submissions/{guardrail_id}/approve` | NW:3915 | guardrails | STUB | 위 guardrails 표에 포함 | — |
| POST | `/guardrails/submissions/{guardrail_id}/reject` | NW:3925 | guardrails | STUB | 위 guardrails 표에 포함 | — |
| PUT | `/v1/access_group/{access_group_id}` | H/accessGroups/useEditAccessGroup.ts:32 | access-groups | STUB | 위 access-groups 표에 포함 | — |
| DELETE | `/v1/access_group/{access_group_id}` | H/accessGroups/useDeleteAccessGroup.ts:10 | access-groups | STUB | 위 access-groups 표에 포함 | — |

- 원인: 1차 스캔 정규식이 `${encodeURIComponent(x)}` 처럼 괄호가 든 템플릿을 놓쳤다. 2차 스캔의 71줄을 모두 다시 확인해 `/credentials/{name}/user_connection`, `/guardrails/submissions/{id}/approve|reject`, `/v1/agents/{agent_id}/identity`, `/v1/access_group/{id}` 를 각 장에 넣었다. 남은 71줄은 모두 9장의 비엔드포인트다.
- `/get/config/callbacks` 는 models-and-endpoints(보장 화면)에서도 불린다(ModelRetrySettingsPanel.tsx:40, ModelGroupAliasPanel.tsx:36). 두 곳 모두 옵셔널이라 STUB 로 충분하지만, 모델 그룹 별칭·재시도 정책을 실제로 구현하면 MUST 로 올려야 한다.

#### 미확인

- admin-panel SSO 모달·Web Search 저장 훅의 오류 처리 세부(토스트로 끝나는지).
- `/guardrails/ui/category_yaml/*`, `/guardrails/ui/major_airlines`, `/policies/attachments/estimate-impact`, `/policies/name/*/versions`, `/policy/templates/enrich/stream` 의 응답 읽기 모양(목록이 비면 도달하지 않는 경로라 자세히 보지 않음).
- `/router/fields` 가 cost-optimization·mcp-servers 에서 실제로 렌더되는지(import 도달성만 확인).
- policies 시뮬레이터 탭의 `/team/list`·`/key/list`·`/models` 호출 시점(마운트 시인지 입력 시인지).

---

## 9. 엔드포인트가 아니라서 뺀 문자열

경로처럼 보이지만 서버에 보내는 요청이 아니다. 우리 서버에 등록하지 않아도 된다.
- UI 페이지 경로: `/ui`, `/ui/login`, `/login`, `/ui/api-reference`, `/ui/model_hub_table`, `/ui/onboarding`, `/onboarding`, `/change-password`, `/api-keys`, `/logs`, `/usage`, `/integrations`(app/chat 탭), `/mcp/oauth/callback`, `/ui/mcp/oauth/callback`(OAuth 복귀 주소로만 씀, hooks/mcpOAuthUtils.ts:31,35)
- 쿠키 경로: `/my-ui`, `/my-ui-tool/login`(utils/cookieUtils.ts:16)
- 에셋: `/litellm`·`/ui/assets/logos/*`(lib/assetPaths.ts), `/_next/static`, `/cloudzero.png`
- 화면에 보여 주기만 하는 예시 URL: `{base}/scim/v2`(C/SCIM.tsx:49), `{base}/fallback/login`(D/admin-panel/_components/AdminPanel.tsx:84), `{proxy}/claude-code/marketplace.json`(C/claude_code_plugins/helpers.ts:250, 설치 명령 문자열), `{gateway}/toolset/<name>/mcp`·`{proxy}/{server}/mcp`·`{base}/mcp/{name}`(MCP 연결 예시와 Responses API 의 `server_url` 값), `{agent}/.well-known/agent-card.json`(에이전트 발견 표시용, D/agents/_components/agent_discovery_utils.ts:144,154), `{base}/v1`·`{base}/v1/models`(C/shared/CreatedKeyDisplay.tsx:30-33 안내 문구), `{apiBase}/chat/completions`(D/prompts 코드 예시)
- 권한 라벨·도움말 문자열: C/team/permission_definitions.tsx 와 C/TeamSSOSettings.tsx:40-50 의 `/key/regenerate`, `/key/bulk_update`, `/key/{key_id}/reset_spend`, `/team/daily/activity`, `/spend/logs` 등. 팀 멤버 권한 이름이지 대시보드가 부르는 경로가 아니다
- 파일 경로 예시: `/var/run/secrets/…`(C/model_add/*_federation.ts), `/etc/passwd` 등(src/data/codeExecutionCompliancePrompts.ts)
- 주석: `/v1/memory/{key:path}`(NW:7871, 백엔드 라우트 설명)

`/v1/traces` 묶음(NW:2060-2094)은 대시보드 코드에는 호출처가 없다. 하지만 `@litellm/lens-ui` 패키지가 부른다(6장 lens 절).
