# N. 대시보드 Usage·Logs 화면 HTTP 계약 (litellm 원본 기준)

수정하지 않은 litellm 대시보드의 **Usage**(사용량)·**Logs**(요청 로그) 화면을 우리 Go 게이트웨이가 받치려면 어떤 응답을 내야 하는지 정리한다. 문서가 아니라 코드를 따랐다. 근거는 모두 `파일:줄`로 달았고, 코드로 확인하지 못한 것은 "미확인"이라고 적었다.
- 엔드포인트 목록과 STUB 판단·충돌 위험은 `M_dashboard_endpoints.md` §6 에 있다. 이미 구현한 `/user/daily/activity`·`/spend/logs` 는 `K_sso_cli_contract.md` §9 에 있다. 이 문서는 그 내용을 되풀이하지 않는다.
- 오류 본문 모양 (A)·(B)·(C)·(D)는 `H_team_user_contract.md` §2.3 의 정의를 그대로 쓴다.

## 0. 약어

서버 쪽 경로는 `gateway/litellm/` 기준이고, 대시보드 쪽 경로는 `gateway/ui/litellm-dashboard/src/` 기준이다.

| 약어 | 파일 |
|---|---|
| DAR | `proxy/management_endpoints/daily_activity_routes.py` |
| DAS | `proxy/management_endpoints/daily_activity_scopes.py` |
| CDA | `proxy/management_endpoints/common_daily_activity.py` |
| CDAT | `types/proxy/management_endpoints/common_daily_activity.py` |
| DRP | `repositories/daily_activity_repository.py` |
| DSQL | `repositories/daily_activity_sql.py` |
| DTY | `types/repositories/daily_activity.py` |
| TE | `proxy/management_endpoints/team_endpoints.py` |
| TET | `types/proxy/management_endpoints/team_endpoints.py` |
| IU | `proxy/management_endpoints/internal_user_endpoints.py` |
| CU | `proxy/management_endpoints/common_utils.py` |
| SME | `proxy/spend_tracking/spend_management_endpoints.py` |
| KMR | `proxy/spend_tracking/key_metadata_recovery.py` |
| AUZ | `proxy/auth/authorization.py` |
| AUZD | `proxy/auth/authorization_dependencies.py` |
| MV1 | `proxy/management_endpoints/management_v1/spend_logs.py` |
| LAC | `proxy/list_api/common.py` |
| MV1T | `types/proxy/management_endpoints/management_v1.py` |
| T | `proxy/_types.py` |
| UAKT | `types/proxy/auth/user_api_key_auth.py` |
| PU | `proxy/utils.py` |
| DSW | `proxy/db/db_spend_update_writer.py` |
| RLR | `proxy/route_llm_request.py` |
| CONST | `constants.py` |
| SP | `gateway/schema.prisma` |
| NW | (대시보드) `components/networking.tsx` |
| DAA | (대시보드) `components/UsagePage/dailyActivityApi.ts` |
| UPV | (대시보드) `app/(dashboard)/usage/_components/components/UsagePageView.tsx` |
| EU/ | (대시보드) `app/(dashboard)/usage/_components/components/EntityUsage/` |
| LG/ | (대시보드) `components/logs/` |

---

## 1. 요약 — 가장 중요한 사실

1. **Usage 는 일별 집계 표만 읽는다.** 요청 로그는 읽지 않는다(DSQL:31-40). 예외는 `/team/spend/by_user` 로, `LiteLLM_SpendLogs` 를 요청 단위로 묶는다(TE:7114-7134). 우리는 둘 다 `request_log` 를 그때그때 묶어서 만든다(§10).
2. 날짜 버킷은 startTime 의 **UTC 날짜**다. 양 끝을 포함하고 정확한 `YYYY-MM-DD` 만 받는다. user aggregated 를 뺀 나머지는 400일 제한이 있다(§2.3). `timezone` 은 `include_current_utc_day` 와 함께일 때만 효과가 있다. usage 화면은 그 플래그를 보내지 않는다.
3. 범위:
   - user 는 관리자 2역할만 전체를 보고, 나머지는 자기 것으로 고정된다(다른 id 를 주면 403).
   - team 은 비관리자면 내 팀으로 제한된다. 모든 요청 팀에서 팀 관리자이거나 `/team/daily/activity` 권한이 없으면 **내 키로만** 좁힌다(§3.2).
4. 응답 `results[]` 는 날짜 내림차순이고 0 채우기가 없다. models·model_groups·providers·endpoints·mcp_servers 엔트리의 `metadata` 는 항상 `{}` 다. `api_keys[*].metadata` 는 활성 키 → 삭제 키 → 복구 순서로 `key_alias, team_id, user_id, user_email, key_exists` 를 채운다(§3.4). team 의 `entities` 는 `team_id`(없으면 `"Unassigned"`) → `{"team_alias":…}` 다.
5. **렌더 예외 위험**: `day.metrics`·`entry.metrics` 가 없을 때, `metadata.total_*` 다섯 칸이 없거나 null 일 때(Keys 탭이 항상 마운트돼 있다), `/keys` 에 `total_api_keys` 가 없을 때, `/users` 에 `users`·`total_users` 가 없을 때, `/team/spend/by_user` 행에 요청 수 칸이 없을 때다(§3.5, §4.6, §5).
6. **Logs 표는 항상 `group_by_session=true` 와 `sort_by=startTime` 을 보낸다.** 그래서 실제 응답은 세션 묶음 응답이다. 다음 쪽은 `has_more`·`next_session_cursor` 로만 연다(§6.3, §6.5).
7. 로그 행에서 반드시 지킬 것은 넷이다.
   - `data` 는 배열이다.
   - `cache_hit` 은 **문자열**이다(boolean 이면 서랍 예외).
   - `spend` 는 숫자다.
   - 시각에 **`Z`/`+00:00`** 을 붙인다(없으면 현지 시각으로 해석된다).
8. 실패 표시는 표가 `status` 를, 서랍이 `metadata.status` 를 본다. **둘 다** `"failure"` 로 넣는다. 키 열은 최상위 `api_key` 가 아니라 `metadata.user_api_key`·`user_api_key_alias`·`user_api_key_team_alias` 를 읽는다(§6.5).
9. `/spend/logs/ui` 는 날짜 형식을 **`YYYY-MM-DD HH:MM:SS`(UTC) 하나만** 받는다. `total` 은 10,000 에서 자른다. 비관리자는 "내 행 OR 내가 관리하거나 `/spend/logs` 권한이 있는 팀의 행"을 본다(§6.4).
10. 우리 표에 없는 것은 빈 값으로 낸다: endpoint, call_type, session, end_user, tags, cache, 본문, 오류 메시지. 이 값으로 거르는 필터는 빈 결과로 처리한다(§10).

---

## 2. 공통: 일별 집계 표와 역할

### 2.1 데이터 출처
- `/{entity}/daily/activity/aggregated*` 와 `/export` 는 **일별 집계 표**(`LiteLLM_Daily{User,Team,Tag,Organization,EndUser,Agent}Spend`)만 읽는다(DSQL:31-40). 요청 로그 표(`LiteLLM_SpendLogs`)는 읽지 않는다. 예외는 키 메타데이터 복구 한 군데뿐이다(§3.4).
- 일별 행의 키는 `(entity_id, date, api_key, model, custom_llm_provider, mcp_namespaced_tool_name, endpoint)` 다(SP:893). 값은 요청마다 다음처럼 쌓인다(DSW:2866-2931).
  - `date`: `startTime.isoformat().split("T")[0]`. 즉 startTime 의 **UTC 날짜** `YYYY-MM-DD` 문자열이다(DSW:2866-2870). 미확인: startTime 이 항상 UTC 인지.
  - `model`: 실제 호출한 모델 이름. `model_group`: 공개 모델 이름. `custom_llm_provider`: 제공자. `endpoint`: `call_type` 을 `ROUTE_ENDPOINT_MAPPING` 으로 바꾼 값이다. 예: `acompletion`→`/chat/completions`, `aembedding`→`/embeddings`, `aresponses`→`/responses` (RLR:142-160).
  - `api_requests` = 1 (내부 하위 호출·재시도면 0). `successful_requests`/`failed_requests` 는 `metadata.status == "failure"` 이면 실패다(DSW:2919-2921, PU:5297-5316).
  - `total_response_time_ms` = 성공한 요청의 `request_duration_ms`, `timed_requests` = 그런 요청 수다. 실패·내부 호출은 시간에 넣지 않는다(DSW:321-331, DSW:2929-2930).
  - `cache_*`, `compression_*`, `*_savings_spend` 는 usage 객체와 metadata 에서 나온다(DSW:2885-2900).
- **`total_tokens` 는 저장 값이 아니라 `prompt_tokens + completion_tokens` 로 다시 계산한다**(CDA:718-725).

### 2.2 역할과 라우트 단계 허용
- 관리자 보기(`user_api_key_has_admin_view`)는 `proxy_admin`·`proxy_admin_viewer` 두 역할이다(UAKT:328-338). spend logs 쪽 `is_admin_view_safe` 도 같은 두 역할이다(SME:4847-4861).
- 라우트 단계(D 거부 전)에서는 이렇게 통과한다.
  - `/user/daily/activity/aggregated` 와 하위(`/keys`·`/search`·`/model_top_keys`·`/export`·`/cache_leakage_keys`·`/users`), `/team/daily/activity/aggregated` 와 하위, `/team/spend/by_user` 는 `self_managed_routes` 에 들어 있다. 그래서 **모든 역할이 라우트를 통과하고 범위는 핸들러가 정한다**(T:851-857, T:870-877).
  - `/spend/logs/ui`, `/spend/logs/ui/{request_id}`, `/spend/logs/session/ui`, `/management/v1/spend_logs/users`, `/management/v1/spend_logs/end_users` 는 `spend_tracking_routes` 에 들어 있다(T:686-707). 이 목록은 `internal_user_routes`(T:788-816)와 `internal_user_view_only_routes`(T:818-832)에 포함되므로 **네 역할 모두 통과**한다.

### 2.3 날짜 검사 (일별 집계 계열)
- `parse_canonical_date_range`(CDA:86-95)의 규칙은 셋이다.
  - 둘 중 하나라도 없으면 `"Please provide start_date and end_date"` 다.
  - 정확히 `YYYY-MM-DD` 가 아니면 `"start_date and end_date must be valid YYYY-MM-DD dates"` 다. `2026-9-1` 같은 정규화 가능한 철자도 거부한다(CDA:75-83).
  - `end < start` 이면 `"end_date must be on or after start_date"` 다.
- `/user/daily/activity/aggregated` 만 위 검사만 한다(DAR:83-86, DAR:296).
- 나머지(`team` aggregated, 그리고 user·team 의 `/keys`·`/search`·`/model_top_keys`·`/export`·`/users`)는 여기에 **범위 400일 초과 금지**(`"Date range must be at most 400 days"`)를 더한다(DAR:88-89, TE:7097-7108).
- 실패하면 400 **(B)** `{"detail":{"error":"<reason>"}}` 다(CDA:59-64).
- **양 끝 날짜를 포함한다**: `date >= start AND date <= end`. 날짜를 문자열로 비교한다(DSQL:72-73).
- `timezone`(분 오프셋, JS `getTimezoneOffset` 규약)은 `include_current_utc_day=true` 일 때만 효과가 있다. 이때 end_date 가 호출자의 오늘(현지 날짜) 이상이면 end 를 오늘의 UTC 날짜까지 늘린다(DSQL:43-56). team 쿼리는 `include_current_utc_day` 를 항상 false 로 둔다(DAS:271). 즉 **team 에서는 timezone 이 아무 효과가 없다.**

### 2.4 그 밖의 공통 오류
- 핸들러 안의 예외는 500 (B) `{"detail":{"error":"Failed to fetch analytics: <e>"}}` 다(DAR:308-312 등 각 라우트).
- `api_key_limit`·`limit`·`offset` 범위를 벗어나거나 `search`·`model_group` 이 비어 있으면 FastAPI 422 **(C)** 다(DAR:286-288, 362-363, 402, 406, 454-458, 565-566).
- `management_endpoint_wrapper` 는 예외를 그대로 다시 던진다(`proxy/management_helpers/utils.py:813-837`). 모양을 바꾸지 않는다.

---

## 3. `GET /{user|team}/daily/activity/aggregated`

### 3.1 쿼리 파라미터

| 파라미터 | user | team | 기본값 / 처리 | 근거 |
|---|---|---|---|---|
| `start_date`, `end_date` | O | O | 필수 `YYYY-MM-DD` (§2.3) | DAS:220-221, 256-257 |
| `model` | O | O | `model = $n` 정확히 일치. **`model` 칸(실제 모델)이지 model_group 이 아니다** | DSQL:89 |
| `api_key` | O | O | 해시값과 정확히 일치. `sk-` 를 해시로 바꾸지 **않는다** | DAS:79-85, DSQL:90-95 |
| `user_id` | O | — | 단일 id | DAS:224-228, 243 |
| `team_ids` | — | O | 콤마로 나눈다. 빈 문자열이면 None(전체) | DAS:61-62, 255 |
| `exclude_team_ids` | — | O | 콤마로 나눈다. `team_id IS NULL OR NOT (team_id = ANY(…))` | DAS:65-66, DSQL:81-88 |
| `timezone` | O | O | int 분. §2.3 | DAS:229-233, 261 |
| `include_current_utc_day` | O | — | 기본 false | DAS:234-240 |
| `api_key_limit` | O | O | 기본 **100**, 1..1000 | DAR:286-288, CONST:2263-2264 |

대시보드는 모든 엔티티에 `timezone` 을 붙인다. `include_current_utc_day` 는 user 이고 호출자가 요청할 때만 붙인다(NW:1363-1386).

### 3.2 범위(권한)

**user** (IU:2819-2828):
- `proxy_admin`·`proxy_admin_viewer` 는 `user_id` 가 있으면 그 사용자, 없으면 **전체**를 본다.
- 그 밖의 역할(internal_user, internal_user_viewer, 팀 관리자 포함)은 항상 자기 user_id 로 고정된다. 다른 `user_id` 를 주면 403 (B) `{"detail":{"error":"Non-admin users can only view their own spend data."}}` 다.
- 키에 user_id 가 없으면(서비스 계정 키) 403 (B) `"Service-account keys cannot query user analytics. Use a user-bound key, or call as a proxy admin."` 다(CU:130-154).

**team** (`resolve_team_daily_activity_scope`, TE:6928-7026):
- 관리자 보기는 `team_ids` 가 없으면 전체 팀이다.
- 비관리자는 먼저 DB 에서 사용자를 찾는다. 없으면 404 (B) `"User= <id> not found"` 다.
  - `team_ids` 가 없으면 **자기가 속한 팀 전부**로 바꾼다(TE:6961-6962). 사용자가 팀에 하나도 없으면 빈 목록이 되고, SQL 조건이 `FALSE` 가 되어 결과가 비어 있다(DSQL:74-76).
  - 속하지 않은 팀을 주면 404 (B) `"User does not belong to Team= <id>. Call `/user/info` to see user's teams"` 다(TE:6964-6970).
- 비관리자는 요청한 **모든** 팀에서 팀 관리자이거나 팀 멤버 권한 `/team/daily/activity` 를 가져야 "팀 전체 보기"가 된다. 하나라도 아니면 결과 전체를 **자기 키**(`VerificationToken.user_id = 나`)로 좁힌다. `api_key` 를 줬는데 그게 내 키가 아니면 빈 목록 → `FALSE` 다(TE:6989-7019).
- `team_alias_metadata = {team_id: {"team_alias": alias}}` 는 요청한 팀들(없으면 **모든 팀**)의 팀 행에서 만든다(TE:6972-6977).
- team 은 PTU 설정이 켜져 있을 때만 `ptu_hours` 를 붙인다(DAR:305-307, `ptu_consumption.py:137-145`). 우리는 해당 없음이고 0 이다.

### 3.3 응답 모양 `SpendAnalyticsPaginatedResponse`

```jsonc
{
  "results": [                       // 날짜 내림차순(CDA:862). 행이 있는 날만 나온다(0 채우기 없음)
    {
      "date": "2026-11-20",          // pydantic date → "YYYY-MM-DD" (CDAT:81, CDA:856)
      "metrics": SpendMetrics,       // 그날 전체 (GROUPING SETS (date), DSQL:178)
      "breakdown": {
        "models":       { "<model>":      MetricWithMetadata },  // `model` 칸 기준 (DSQL:179)
        "model_groups": { "<group>":      MetricWithMetadata },  // COALESCE(NULLIF(model_group,''), model) (DSQL:15,180)
        "providers":    { "<provider>":   MetricWithMetadata },  // custom_llm_provider, 비면 "unknown" (CDA:830)
        "endpoints":    { "/chat/completions": MetricWithMetadata },
        "mcp_servers":  { "<tool>":       MetricWithMetadata },  // mcp_namespaced_tool_name 이 있을 때만
        "api_keys":     { "<key hash>":   KeyMetricWithMetadata },
        "entities":     { "<team_id>":    MetricWithMetadata }   // team 만. user 는 항상 {}
      }
    }
  ],
  "metadata": DailySpendMetadata
}
```

- **SpendMetrics** (CDAT:21-39, 모두 기본 0): `spend, flat_cost, ptu_hours, prompt_tokens, completion_tokens, cache_read_input_tokens, cache_creation_input_tokens, compression_saved_tokens, compression_savings_spend, prompt_caching_savings_spend, gateway_injected_caching_savings_spend, autorouter_savings_spend, total_tokens, successful_requests, failed_requests, api_requests, total_response_time_ms, timed_requests`.
- **MetricWithMetadata** (CDAT:62-65): `{"metrics": SpendMetrics, "metadata": {}, "api_key_breakdown": {"<hash>": KeyMetricWithMetadata}}`.
  - models·model_groups·providers·endpoints·mcp_servers 의 `metadata` 는 **항상 `{}`** 다(CDA:763-768).
  - `api_key_breakdown` 은 (차원, api_key) 묶음이다. 상위 `api_key_limit` 개 키만 담는다(DSQL:204-211).
  - providers 의 `metrics.flat_cost` 는 0 으로 둔다(CDA:829).
- **KeyMetricWithMetadata** (CDAT:56-59): `{"metrics": SpendMetrics, "metadata": KeyMetadata}`. KeyMetadata 는 `{key_alias, team_id, user_id, user_email, key_exists}` 이고 모두 null 가능이다(CDAT:46-53).
- **entities 의 키와 metadata** (team 만, `_fold_entity_rollups_sync` CDA:982-1013):
  - 키는 `team_id` 다. 일별 행의 team_id 가 비었거나 NULL 이면 `"Unassigned"` 다(CDA:998, DSQL:234,242).
  - `metadata` 는 `{"team_alias": "<alias 또는 null>"}` 다(§3.2 의 team_alias_metadata). 그 맵에 없는 팀(삭제된 팀, `"Unassigned"`)은 `{}` 다(CDA:275-281).
  - `metrics` 는 (date, team) 합계다(`api_key_rolled=1` 행, CDA:1008-1009). `api_key_breakdown` 은 (date, team, key) 묶음이고 상위 키만 담는다(CDA:1010-1013).
- **DailySpendMetadata** (CDAT:86-123): SpendMetrics 의 각 칸에 `total_` 을 붙인 합계다(`total_spend` … `total_timed_requests`, `total_ptu_hours`). 여기에 다음 칸이 붙는다.
  - `page=1`, `total_pages=1`, `has_more=false` 로 고정이다(CDA:1083-1085).
  - `api_key_limit`: 요청 값이다.
  - `total_api_keys`: 범위 안의 **고유 키 수**다. PTU 센티널은 뺀다(DSQL:140-148, DRP:185-187). 키가 없으면 0 이다.
  - `entity_total_api_keys`: team 은 `{team_id|"Unassigned": 고유 키 수}`, user 는 `null` 이다(CDA:1033-1041).
  - 합계(`total_*`)는 **키 수 제한과 무관하게 모든 행**을 더한다(GROUPING SETS `()`, DSQL:184, CDA:789-791).

### 3.4 `breakdown.api_keys[*].metadata` 를 채우는 법
`repository.key_metadata(keys, window)`(DRP:277-304) → `_ProxyDailyActivityReads.recover_key_metadata`(CDA:486-551). 아래 순서로 채운다.
1. 활성 키(`LiteLLM_VerificationToken.token = hash`)에서 `key_alias`, `team_id`, `user_id` 를 가져오고 `key_exists=true` 로 둔다. **`user_email` 은 여기서 null** 이다(DRP:283-284, 306-317).
2. 없으면 삭제된 키(`LiteLLM_DeletedVerificationToken`, 같은 token 이 여럿이면 `deleted_at` 이 가장 늦은 것)에서 같은 칸을 가져오고 `key_exists=false` 로 둔다(DRP:285-303).
3. 그래도 없으면 litellm 고유 복구를 순서대로 시도한다: CLI 세션 키 접두어, 이중 해시 역추적, 그리고 기간 `[min(date)-1일, max(date)+2일]` 안의 **SpendLogs metadata** 에서 alias·team 복구(CDA:499-515, CDA:455-459, KMR:484-500).
4. user_id 도 key_exists 도 없는 키는 일별 표에서 소유자 user_id 를 찾는다(CDA:516-527).
5. 마지막으로 user_id 가 있으면 `LiteLLM_UserTable.user_email` 로 `user_email` 을 채운다(`attach_user_details`, CDA:528, KMR:324-338).
6. 끝까지 못 찾은 키는 `KeyMetadata()` 로 둔다. 즉 alias·team·user·email 은 null 이고 `key_exists=false` 다(CDA:179-187).

같은 함수가 `breakdown.api_keys`, 모든 `api_key_breakdown`, entities 의 `api_key_breakdown`, `/keys`·`/model_top_keys`·`/search` 행의 `metadata` 를 채운다(CDA:781, 803, 1012, DAR:131-174).

### 3.5 대시보드가 읽는 것
대시보드 정규화(DAA:89-104)는 `results`, `breakdown`, 각 breakdown 맵, `api_key_breakdown`, 엔트리 `metadata` 를 `?? {}` 로 채운다. **그러나 `day.metrics`, `entry.metrics`, `api_key_breakdown[k].metrics` 는 채우지 않는다.**

| 필드 | 읽는 곳 | 성격 | 없을 때 |
|---|---|---|---|
| `results[].date` | UPV:335, `…/overview/overviewData.ts:117,200`, `components/activity_metrics.tsx:484,707` | 필수 | 날짜 깨짐(예외 없음) |
| `results[].metrics` (객체) | `components/UsagePage/keyActivityData.ts:27`, overviewData.ts:110,203, EU/EntityUsage.tsx:82 | 필수 | **렌더 예외** |
| `metrics.spend, api_requests, total_tokens, prompt_tokens, completion_tokens, successful_requests, failed_requests, cache_read_input_tokens, cache_creation_input_tokens, total_response_time_ms, timed_requests` | overviewData.ts:77-79,203-205, keyActivityData.ts:27-38 | 필수 | 칸 하나 빠지면 NaN |
| `metrics.flat_cost` | EU/EntityUsage.tsx:82 (team) | 장식 | NaN |
| `breakdown.{models,model_groups,providers,endpoints,mcp_servers}[*].metrics.*` (위와 같은 칸) | activity_metrics.tsx:689-717, overviewData.ts:31-35, EU/entityUsageAggregations.ts:38-45,222-226, `…/EndpointUsage/EndpointUsage.tsx:42-51` | 필수 | `metrics` 가 없으면 **예외** |
| `breakdown.models[*].api_key_breakdown[h].metrics.{spend,api_requests,successful_requests,failed_requests,total_tokens}` | activity_metrics.tsx:732-751 (키 상세의 top_models) | 필수(그 보기) | `metrics` 가 없으면 예외 |
| 위 차원들의 엔트리 `metadata` | 안 읽는다 | — | — |
| `breakdown.api_keys[h].metrics.*` | entityUsageAggregations.ts:116-124,182-190, activity_metrics.tsx:692+ | 필수 | 예외 |
| `breakdown.api_keys[h].metadata.key_alias`, `.user_email` | `components/UsagePage/keyActivityLabel.ts:7`. alias 가 없으면 `key-hash-<hash>` (activity_metrics.tsx:646) | 표시 | 대체 표시 |
| `.metadata.team_id` | activity_metrics.tsx:647-650 (팀 이름은 **클라이언트의 팀 목록**으로 바꾼다) | 표시 | — |
| `.metadata.user_id` | entityUsageAggregations.ts:132,198 ("User" 열) | 표시 | — |
| `.metadata.key_exists` | `components/UsagePage/components/EntityUsage/TopKeyView.tsx:39` (`false` 면 키 정보 클릭을 막는다) | 동작 | — |
| 키 해시(맵의 키) | TopKeyView.tsx:73 → 클릭하면 `keyInfoV1Call(hash)` | 필수 | — |
| `breakdown.entities[id].metrics.{spend,api_requests,successful_requests,failed_requests,total_tokens}` | EU/EntityUsage.tsx:314-318, entityUsageAggregations.ts:72-76, `…/overview/agentCatalog.ts:285-291` | 필수(team 보기) | `metrics` 가 없으면 **예외** |
| `breakdown.entities[id].metadata.team_alias` | EU/EntityUsage.tsx:264-283 (클라이언트 팀 목록 → `team_alias` → `user_email` → `user_alias` → id 순서), activity_metrics.tsx:671 | 표시 | id 표시 |
| `entities[id].api_key_breakdown[k].metrics.spend` | entityUsageAggregations.ts:147-149 (tag 보기) | 그 보기 | 예외 |
| `metadata.total_spend, total_api_requests, total_successful_requests, total_failed_requests, total_tokens` | keyActivityData.ts:12-22 → `components/UsagePage/components/KeyActivityPanel.tsx:140-178` (Keys 탭은 keepMounted, UPV:566), EU/entityUsageSummary.ts:56-68 | **필수** | 빠지거나 null 이면 `formatUsd`/`.toLocaleString` **예외**(overviewData.ts:143-161,209) |
| `metadata.total_prompt_tokens, total_completion_tokens, total_cache_read_input_tokens, total_cache_creation_input_tokens, total_response_time_ms, total_timed_requests` | keyActivityData.ts:12-22, overviewData.ts:244-259 (`{...EMPTY, ...raw}`) | 필수 | **명시적 `null` 이면** `…/overview/UsageStatStrip.tsx:39-58` 에서 예외. 칸이 아예 없으면 안전 |
| `metadata.total_flat_cost`, `total_ptu_hours` | entityUsageSummary.ts:33-82 (`?? 0`) | 장식 | 안전 |
| `metadata.page/total_pages/has_more/api_key_limit/total_api_keys/entity_total_api_keys`, 모든 `*_savings_spend`, `compression_saved_tokens` | 안 읽는다 | 장식 | — |

**요청 쪽 사실** (NW:1363-1395, UPV, `app/(dashboard)/home/_components/useHomeUsage.ts`)
- 관리자 판정은 클라이언트에서 한다. `all_admin_roles = ["Admin","Admin Viewer","proxy_admin","proxy_admin_viewer","org_admin"]` (`utils/roles.ts:11-13`) 이다. 관리자 global 보기는 `user_id` 를 **보내지 않는다**. 비관리자와 "my usage" 보기는 항상 자기 `user_id` 를 보낸다(UPV:125,146, useHomeUsage.ts:33,44).
- `api_key_limit=1` 은 키 상세 호출(`api_key=<hash>` 와 함께)에서만 보낸다(UPV:387, EU/EntityUsage.tsx:249). 그 밖에서는 보내지 않으므로 기본 100 이 적용된다.
- `include_current_utc_day` 는 usage·home 이 보내지 않는다. cost-optimization 만 보낸다(`app/(dashboard)/cost-optimization/_components/useDailyActivityRange.ts:71`).
- team 호출에는 항상 `exclude_team_ids=litellm-dashboard` 가 붙는다(EU/entityFetchFns.ts:47).
- 관리자 global overview 는 `tag` aggregated 도 부르고(UPV:236), agent 를 볼 수 있는 사용자의 팀 페이지는 `agent` aggregated 도 부른다(EU/EntityUsage.tsx:215). 둘 다 M 문서상 STUB 이다.

---

## 4. 키·사용자 하위 엔드포인트와 내보내기

모두 §3.1 의 엔티티 쿼리(같은 범위 검사와 400일 제한)를 그대로 받는다(DAR:358-372, 401-415, 450-467, 500-514, 561-575).

### 4.1 `GET /{entity}/daily/activity/aggregated/keys` (DAR:355-395)
- 추가 쿼리는 둘이다. `offset`(기본 0, ≥0)과 `limit`(기본 **50**, 1..100)이다(CONST:2265-2266).
- 응답 `DailyActivityKeyPageResponse`: `{"api_keys":[KeySpendActivityRow], "total_api_keys":int, "offset":int, "limit":int}` (CDAT:159-163).
- 행 `KeySpendActivityRow` = `{"api_key":hash, "metrics":KeySpendMetrics, "metadata":KeyMetadata}` (CDAT:149-152).
  - KeySpendMetrics 는 **9개 칸뿐**이다: `spend, prompt_tokens, completion_tokens, total_tokens, api_requests, successful_requests, failed_requests, cache_read_input_tokens, cache_creation_input_tokens` (CDAT:137-146).
- 정렬은 기간 합계 spend 내림차순, 같으면 api_key 순이다. `total_api_keys` 는 고유 키 수다(DSQL:277-301).

### 4.2 `GET /{entity}/daily/activity/aggregated/search` (DAR:398-444)
- `search` 는 필수다(min_length 1). 없으면 422 (C) 다. `limit` 는 기본 100, 1..100 이다(CONST:2269-2270).
- 찾는 대상(대소문자 무시, 부분 일치)은 넷이다: `api_key` 해시, 그리고 활성·삭제 키의 `key_alias`, 키의 `user_id`, 소유자 `user_email` (DSQL:347-379).
- 응답 `{"api_keys":[KeyActivityRow]}`. 행의 `metrics` 는 **전체 SpendMetrics** 다. 찾은 키들로 aggregated 를 다시 돌려 기간을 합친 값이다(DAR:423-428, 225-238, CDAT:131-134, 155-156). 맞는 키가 없으면 `{"api_keys":[]}` 다.

### 4.3 `GET /{entity}/daily/activity/aggregated/model_top_keys` (DAR:447-494)
- 쿼리는 셋이다.
  - `model_group` 필수.
  - `by_model_group` 기본 true. true 면 `COALESCE(NULLIF(model_group,''),model) = 값`, false 면 `model = 값` 이다.
  - `limit` 기본 **5**, 1..100 (DSQL:382-401, CONST:2271-2272).
- 응답 `{"model":"<요청 값>", "by_model_group":bool, "api_keys":[KeySpendActivityRow]}` (CDAT:190-193). spend 내림차순이다.

### 4.4 `GET /user/daily/activity/aggregated/users` (user 만, DAR:558-598, DAR:652-654)
- 쿼리는 `offset`(기본 0)과 `limit`(기본 50, 1..100)이다(CONST:2267-2268).
- 응답 `{"users":[UserActivityRow], "total_users":int, "offset", "limit"}` (CDAT:181-187).
  - 행: `user_id`(빈 문자열이면 null), `user_email`, `user_alias`(UserTable 에서), `spend, prompt_tokens, completion_tokens, total_tokens, api_requests, successful_requests, failed_requests` (CDAT:166-178, DAR:177-200, DRP:211-233).
  - 정렬: spend 내림차순, user_id 오름차순, null 은 끝이다(DSQL:315-339).
- 범위는 §3.2 user 와 같다. 비관리자는 **자기 한 줄**만 받는다.
- `/user/daily/activity/aggregated/cache_leakage_keys` 도 user 에만 있다(DAR:601-642). 대시보드는 cost-optimization 화면에서만 부른다(`app/(dashboard)/cost-optimization/_components/useCacheLeakageKeys.ts:43`). 이 문서의 범위가 아니다.

### 4.5 `GET /{entity}/daily/activity/export` (DAR:497-555)
- 쿼리는 둘이다.
  - `export_type` 은 `daily`(기본)·`daily_with_keys`·`daily_with_models`·`daily_with_users` 중 하나다(DTY:103-107).
  - `format` 은 `csv`(기본)·`json` 이다(DAR:504-505).
- 응답은 `StreamingResponse` 다.
  - `Content-Type`: csv 면 `text/csv`, json 이면 `application/json` 이다(DAR:519). 미확인: starlette 가 `text/csv` 에 `; charset=utf-8` 을 붙이는지.
  - `Cache-Control: no-store`
  - `Content-Disposition: attachment; filename="{entity}-usage-{start}-{end}-{export_type}.{csv|json}"` (DAR:203-222, 520-529).
- 열(순서 고정) = `ExportRow` 필드 순서(DTY:111-128, DAR:260): `date, entity_id, entity_alias, api_key, key_alias, user_id, user_email, model, spend, flat_cost, prompt_tokens, completion_tokens, api_requests, successful_requests, failed_requests, cache_read_input_tokens, cache_creation_input_tokens`.
  - CSV 는 헤더 줄 다음에 데이터 줄이 오고 줄 끝은 `\r\n` 이다. `= + - @ \t \r` 로 시작하는 문자열 칸 앞에는 `'` 를 붙인다(수식 주입 방지, DAR:241-250).
  - JSON 은 같은 키를 가진 객체 배열이고 공백이 없다. 행이 없으면 `[]` 다(DAR:267-273).
- 행 내용 (DSQL:421-547):
  - `entity_id`: user 면 user_id, team 이면 team_id 이고 NULL 은 `''` 다.
  - `entity_alias`: team 이면 `TeamTable.team_alias`, user 면 null 이다.
  - `daily` 는 (date, entity)로 묶고 api_key·key_alias·user_id·user_email·model 은 null 이다.
  - `daily_with_keys` 는 key 별로 묶고 alias·user 는 활성 키, 없으면 삭제 키에서 가져온다.
  - `daily_with_models` 는 `model` 칸으로 묶는다.
  - `daily_with_users` 는 키 소유자 user_id 로 묶는다.
  - 정렬은 묶은 키 오름차순이다.

### 4.6 대시보드가 읽는 것
| 엔드포인트 | 필드 | 읽는 곳 | 없을 때 |
|---|---|---|---|
| `/keys` | `api_keys`, `total_api_keys` | KeyActivityPanel.tsx:230-233,282-286 | `api_keys` 가 없으면 catch 로 실패 표시된다. **`api_keys` 는 있고 `total_api_keys` 가 없으면 렌더 예외**(KeyActivityPanel.tsx:64) |
| `/keys` 행 | `api_key`(중복 제거 키), `metadata.{key_alias,team_id,user_id,user_email,key_exists}`(null 안전), `metrics.{api_requests,successful_requests,failed_requests,cache_read_input_tokens,cache_creation_input_tokens,total_tokens,prompt_tokens,completion_tokens,spend}` | `components/UsagePage/components/keySearch.ts:12-31`, keyActivityData.ts:51 | `metrics` 가 없거나 `api_requests`·`spend` 가 없으면 **예외**(KeyActivityPanel.tsx:417-419) |
| `/keys` | 응답의 `offset`, `limit` | 안 읽는다(클라이언트가 offset 을 직접 센다, keyActivityData.ts:53) | — |
| `/search` | `api_keys[]` (행은 `/keys` 와 같은 칸) | KeyActivityPanel.tsx:328,351 (`?? []`) | 배열은 안전. 행 칸은 위와 같다 |
| `/model_top_keys` | 행 `api_key`, `metadata.key_alias`, `metadata.team_id`(그대로 표시), `metadata.user_email ?? user_id`, `metrics.{spend,api_requests,total_tokens}` | activity_metrics.tsx:155-162,214-219 | 매핑 단계 예외는 catch 된다. **alias 가 비고 `api_key` 가 없으면 `.substring` 렌더 예외**(:214) |
| `/users` | `users[]`, `total_users` | `components/UsagePage/components/useUserActivityPage.ts:49-52,84-88`, `TopUsersView.tsx:34` | `users` 가 없으면 **TopUsersView.tsx:69 예외**. `total_users` 가 없으면 **UserActivityTable.tsx:110 예외** |
| `/users` 행 | `user_id`(키·보조 라벨), `user_email`, `user_alias`, `spend`, `api_requests`, `successful_requests`, `failed_requests`, `total_tokens` | `…/userActivityData.ts:7-17`, TopUsersView.tsx:69-97, UserActivityTable.tsx:79-92 | `api_requests`·`successful_requests`·`failed_requests` 가 없으면 **예외** |
| `/export` | Blob 본문만 | `lib/http/client.ts:210-213` | Content-Type·Content-Disposition 은 무시한다. 파일 이름은 클라이언트가 `${entity}_usage_${export_type}_${from}_${to}.${format}` 로 만든다(`components/EntityUsageExport/utils.ts:9-14`). 선택지는 export_type 4종(user 엔티티에서는 `daily_with_users` 를 숨긴다, `ExportTypeSelector.tsx:12-34`)과 format csv·json(`EntityUsageExportModal.tsx:21-22`)이다 |

---

## 5. `GET /team/spend/by_user` (TE:7111-7230)

- **쿼리**: `team_ids`(필수, 콤마), `start_date`·`end_date`(필수, `YYYY-MM-DD`, 400일 제한).
  - 날짜가 없거나 틀리면 400 (B) `{"detail":{"error":"<reason 또는 Please provide start_date and end_date>"}}` 다.
  - `team_ids` 가 없으면 400 (B) `"Please provide team_ids"` 다(TE:7178-7183).
- **범위**: §3.2 와 같은 `resolve_team_daily_activity_scope` 를 쓴다(TE:7185-7193). 속하지 않은 팀이면 404 (B) 다.
  - 팀 전체 보기가 아니면 `"user" = 호출자` 로 **자기 줄만** 받는다(TE:7198-7199, 7113).
  - 범위 안 팀이 없으면 `results: []` 다(TE:7194-7196).
- **데이터 출처는 일별 표가 아니라 `LiteLLM_SpendLogs`** 다(요청 단위). 조건은 `"startTime" >= start AND "startTime" < end + 1 day`, `team_id IN (…)` 이다. 묶음은 `(team_id, user)` 다(TE:7114-7134).
  - 실패는 `status = 'failure'` 이고 성공은 `status IS DISTINCT FROM 'failure'` 다(TE:7125-7126).
  - 정렬은 spend 내림차순, 그다음 team_id, user 순이다.
- **응답** `TeamUserSpendResponse` (TET:248-266):
```json
{"start_date":"2026-11-01","end_date":"2026-11-21",
 "results":[{"team_id":"t1","team_alias":"팀1","user_id":"u1","user_email":"a@b","user_alias":null,
             "spend":1.2,"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,
             "api_requests":3,"successful_requests":3,"failed_requests":0}]}
```
  - `user_id` 가 NULL 이면 `""` 다. `team_alias` 는 team_alias_metadata 에서 가져오고 문자열이 아니면 null 이다(TE:7208-7230).
  - `total_tokens` 는 SpendLogs 의 `total_tokens` 칸 합이다. 일별 표처럼 다시 계산하지 않는다.
- **대시보드가 읽는 것** (EU/TeamUserSpendCard.tsx, EU/teamUserSpend.ts):
  - `results` 는 `data?.results ?? []` 로 읽어서 안전하다(TeamUserSpendCard.tsx:82).
  - 행의 표시:
    - 팀 라벨은 `team_alias || team_id`(teamUserSpend.ts:14)이고, 사용자 라벨은 `user_email || user_alias || user_id`(:10-11)다.
    - 행 id 는 `team_id+user_id`(:16)이고, `spend` 로 정렬·표시한다(:19, Card:46).
  - **`api_requests`·`successful_requests`·`failed_requests`·`total_tokens` 가 없으면 렌더 예외**다(Card:52-70).
  - CSV 내보내기에만 `prompt_tokens`·`completion_tokens`·최상위 `start_date`·`end_date` 를 쓴다(teamUserSpend.ts:21-43).
  - 요청의 `team_ids` 는 선택한 팀이다. 선택이 없으면 클라이언트 팀 목록에서 `litellm-dashboard` 를 뺀 전부다(EU/EntityUsage.tsx:361-366).

---

## 6. `GET /spend/logs/ui` (SME:2501-3077)

같은 핸들러가 `/spend/logs/v2` 도 받는다. 아래는 UI 경로(`is_v2=false`) 기준이다(SME:2641).

### 6.1 쿼리 파라미터 (SME:2513-2614)

| 파라미터 | 기본 | 동작 (UI 경로) |
|---|---|---|
| `start_date`, `end_date` | — | **필수**(request_id 단독 조회 때만 빼도 된다). 형식은 **`YYYY-MM-DD HH:MM:SS` 하나만** 받고, UTC 로 해석한다(SME:2704-2720). 둘 중 하나가 없으면 400 (A) `type=bad_request`, `"Start date and end date are required"` 다(SME:2697-2703). 형식이 틀리면 HTTPException 400 이 (A) 로 바뀐다: `message="Invalid date format: <v>. Expected: 'YYYY-MM-DD HH:MM:SS'"`, `type=internal_server_error` (SME:2714-2717, 3077, PU:9052-9059). 조건: `"startTime" >= start AND "startTime" <= end` (양 끝 포함, SME:2843-2849) |
| `page` | 1 | ≥1 |
| `page_size` | 50 | 1..1000. 넘으면 422 (C) |
| `api_key` | — | `api_key = 값` (해시 그대로) |
| `user_id` | — | `"user" = 값` |
| `team_id` | — | `team_id = 값`. 비관리자는 팀 열람 권한을 검사한다(§6.4) |
| `request_id` | — | `(request_id = v OR litellm_call_id = v)`. 날짜 창을 무시한다(모든 기간). 정확히 일치하는 행이 먼저 온다(SME:2688-2695, 2878-2883) |
| `search` | — | `request_id = v` 는 모든 기간에서, 아니면 창 안에서 `litellm_call_id/api_key/team_id/user/end_user/session_id/model_id = v` 로 찾는다(SME:2473-2490). search 가 있으면 일반 날짜 조건 대신 이 조건이 창을 정한다(SME:2686, 2843) |
| `session_id` | — | `session_id LIKE %v%` (부분 일치, SME:2891-2895) |
| `status_filter` | — | `success` 면 `(status='success' OR status IS NULL)`, 그 밖은 `status = v` 다(SME:2897-2904). group_by_session 이면 세션 대표 행의 상태로 거른다(SME:2948-2963) |
| `cache_hit_filter` | — | `hit`·`miss` 만 받는다. 아니면 400 (A) `bad_request` (SME:2667-2673, 2906-2909) |
| `used_client_oauth_token` | — | `metadata->>'used_client_oauth_token' = 'true'/'false'` (SME:2943-2946) |
| `span_type` | — | `llm`·`agent`·`mcp`·`batch` 만 받는다. call_type 으로 거른다(SME:99-108, 2674-2680) |
| `model`, `model_id`, `model_group`, `end_user` | — | 정확히 일치 (SME:2862-2876) |
| `key_alias` | — | `metadata->>'user_api_key_alias' LIKE %v%` (SME:2931-2934) |
| `error_code` | — | `metadata->'error_information'->>'error_code' = v` (SME:2935-2938) |
| `error_message` | — | `metadata->'error_information'->>'error_message' LIKE %v%` (SME:2939-2942) |
| `min_spend`, `max_spend` | — | `spend >=`, `spend <=` |
| `exclude_internal_health_checks` | false | 내부 헬스체크 서비스 계정 키 두 개를 뺀다(SME:123-126, 2915-2918) |
| `sort_by` | `startTime` | `spend, total_tokens, startTime, endTime, request_duration_ms, model, ttft_ms` 만 받는다. 아니면 400 (A) `type=bad_request`, `param=sort_by` 다(SME:2644-2659). `ttft_ms` 는 `completionStartTime-startTime` 으로 계산하고 NULL 은 끝에 둔다(SME:2992-2998) |
| `sort_order` | `desc` | `asc`·`desc` 만 받는다(SME:2660-2666) |
| `group_by_session` | false | §6.3 |
| `session_cursor` | — | `'<last_activity>\|<api_key>\|<session_key>'` (SME:2600-2606, 3086-3093) |

### 6.2 응답 (평면 목록, SME:3004-3074, 4640-4819)
```jsonc
{"data":[Row…], "total":int, "page":int, "page_size":int, "total_pages":int, "total_is_capped":bool}
```
- `total` 은 **최대 10,000 에서 자른다**. 일치 행이 10,000 을 넘으면 `total=10000`, `total_is_capped=true` 다(SME:86, 3007-3022). `total_pages = ceil(total/page_size)` 다(SME:3061).
- 평면 목록에는 `has_more`·`next_session_cursor` 가 **없다**. 이 둘은 §6.3 세션 묶음 응답에만 있다(SME:3264).
- 정렬은 `ORDER BY <sort_by> <dir>` 이고 `LIMIT page_size OFFSET (page-1)*page_size` 다(SME:3037-3046).
- **행(Row)** 칸은 다음과 같다. 무거운 칸(`messages`, `response`, `proxy_server_request`)은 **빠진다**(SME:110-121, 2833-2835).
  `request_id, call_type, api_key, spend, total_tokens, prompt_tokens, completion_tokens, startTime, endTime, completionStartTime, model, model_id, model_group, custom_llm_provider, api_base, user, metadata, cache_hit, cache_key, request_tags, team_id, organization_id, end_user, requester_ip_address, session_id, status, mcp_namespaced_tool_name, agent_id, litellm_call_id, request_duration_ms`
  - `request_duration_ms` 가 NULL 이면 `(endTime-startTime)` ms 로 채운다(SME:119-120).
  - `metadata` 는 JSON **객체**로 다시 풀어 준다. 풀 수 없으면 `{}` 다(SME:3286-3304).
  - 모든 행에 **`session_total_count`** 가 붙는다. session_id 가 없거나 세션 집계가 없으면 1 이다.
  - 세션 집계가 있으면 다음 칸도 붙는다(SME:4782-4804): `session_total_spend, session_total_duration_ms, session_cache_hit_count, session_llm_count, session_agent_count, session_total_prompt_tokens, session_total_completion_tokens, session_total_tokens, session_models[], session_models_truncated`, MCP 가 있으면 `mcp_tool_call_count, mcp_tool_call_spend`. 세션 집계 키는 `(session_id, api_key)` 다(SME:4705-4775).
- `status` 칸 값은 `'success'` 또는 `'failure'` 다(`spend_tracking_utils.py:2057-2066`). `metadata.status` 도 같은 값이다(T:3738).
- `metadata` 의 주요 키(`SpendLogsMetadata`, T:3703-3759): `user_api_key, user_api_key_alias, user_api_key_team_id, user_api_key_team_alias, user_api_key_user_id, user_api_key_user_email, user_api_key_org_id, requester_ip_address, user_agent, status, error_information{error_code, error_class, llm_provider, traceback, error_message}, usage_object, additional_usage_values, cost_breakdown, guardrail_information, mcp_tool_call_metadata, attempted_retries, max_retries, litellm_overhead_time_ms, internal_call_origin …`.
- 날짜 직렬화: 열은 Prisma `DateTime`(timestamp, SP:728-731)이고 `query_raw` 결과를 그대로 낸다. 미확인: 문자열 형식(`2026-11-20T10:00:00+00:00` 인지, `Z` 인지, 접미어가 없는지). 대시보드의 해석은 §6.5 에 있다.

### 6.3 `group_by_session=true` (SME:2965-2981, 3155-3264)
- 쓰는 조건은 넷이다: UI 경로이고, `request_id`·`search` 가 없고, `sort_by=startTime` 이다.
  - 그 밖의 정렬이면 DISTINCT ON 으로 세션마다 대표 행 하나를 골라 OFFSET 으로 쪽을 나눈다. 이때 `total` 은 세션 수다(SME:3005-3036).
- 세션 키는 `COALESCE(NULLIF(session_id,''), request_id)` 와 `api_key` 의 쌍이다. 세션 id 가 없는 행은 혼자 한 세션이다(SME:89-90).
- 대표 행은 에이전트 행(`asend_message`, 늦게 끝난 것)이 먼저다. 그다음 MCP 가 아닌 행 중 가장 최근 `startTime` 이다(SME:93-97).
- 쪽 나누기는 `(MAX(startTime), session_key, api_key)` 키셋이다. `session_cursor` 가 있으면 그 뒤부터, 없고 `page>1` 이면 OFFSET 으로 대신한다(SME:3192-3221).
- 응답은 §6.2 에 **`"next_session_cursor": "<last_activity>|<api_key>|<session_key>"|null`, `"has_more": bool`** 이 더해진다(SME:3223-3229, 3264).
  - `last_activity` 는 `MAX("startTime")::text` 다. Postgres 의 timestamp 텍스트 형식이라 `2026-11-20 10:00:00.123` 처럼 생긴다(SME:3209).
- `total` 은 세션 수(최대 10,000)다. 이번 쪽에서 목록이 끝나면 count 쿼리 없이 `offset + 행 수` 다(SME:3231-3237).

### 6.4 범위(권한) (SME:2799-2825, 4885-4964, AUZ:26-68, AUZD:20-42)
- `proxy_admin`·`proxy_admin_viewer` 는 전체를 본다.
- `team_id` 를 주면: 비관리자는 그 팀의 **팀 관리자이거나 팀 멤버 권한 `/spend/logs`** 를 가져야 한다(AUZ:40-50). 아니면 403 → (A) `internal_server_error`, message `{'error': 'Not authorized to view team spend for team_id=<id>'}` 다(SME:2814-2825, 3077). 통과하면 그 팀 행 **전부**를 본다. 사용자 범위는 걸지 않는다(SME:2805-2809).
- `team_id` 가 없고 역할이 `internal_user`·`internal_user_viewer`(user_id 있음)이면 `("user" = 나 OR team_id = ANY(열람 가능 팀))` 이다. 열람 가능 팀은 내가 속한 팀 중 위 조건(팀 관리자 또는 `/spend/logs` 권한)을 만족하는 팀이다(SME:2810-2812, 4952-4964). 팀 조회가 실패하면 자기 행만 본다(AUZ:33-37).
- `request_id` 조회(비관리자): 일치하는 행 중 하나라도 내 것(user 가 나이거나 열람 가능 팀)이어야 한다. 아니면, **행이 없을 때도**, 403 이다. 받은 행도 다시 검사한다(SME:2799-2804, 3050-3056, 4922-4942, 5039-5054).
- 주의: 비관리자이고 `team_id` 가 없고 request_id 조회도 아닌데 역할이 internal_user 계열이 아니면(또는 user_id 가 없으면) `user_scope_applies=false` → `AllRows()` 다(SME:2805-2812). 라우트 단계에서 네 역할만 들어오므로 실제로는 **user_id 없는 internal_user 키**만 해당한다. 우리는 이 경우를 "행 없음"으로 막는 것이 안전하다(litellm 과 다르다. 일탈로 기록할 것).

### 6.5 대시보드가 읽는 것
**요청** (NW:1986-2025, LG/request/useLogFilterLogic.ts:159-203)
- `start_date`·`end_date` 는 항상 보낸다. `moment(x).utc().format("YYYY-MM-DD HH:mm:ss")` 로 만든 **UTC 값**이다(useLogFilterLogic.ts:64-74). 기본 창은 최근 24시간이고 시작 초는 항상 `:00` 이다(LG/request/LogsTableToolbar.tsx:16-34).
- `page_size` 기본은 **25** 이고 25/50/100 중에서 고른다(LG/request/RequestLogsPanel.tsx:36).
- **표는 항상 `group_by_session=true`** 를 보낸다(useLogFilterLogic.ts:200). 기본 정렬이 `startTime desc` 이므로 **실제로 쓰이는 길은 §6.3 세션 묶음 응답**이다.
- `session_cursor` 는 "다음"으로 넘어온 쪽에서만 보낸다. 직전 응답의 `next_session_cursor` 를 쪽 번호별로 저장해 둔다(:141-142, RequestLogsPanel.tsx:219-226).
- URL `log_id` 로 한 건을 열 때는 `request_id` 와 `page_size=1` 만 보내고 group_by_session 은 없다(RequestLogsPanel.tsx:143-151).
- `sort_by` 값은 `startTime, spend, total_tokens, request_duration_ms, model, ttft_ms` 다(LG/types.ts:2-9).
- 필터는 값이 있을 때만 보낸다(useLogFilterLogic.ts:181-196): `api_key, team_id, request_id, search, session_id, user_id, end_user, status_filter(success|failure), cache_hit_filter, used_client_oauth_token, span_type, model_id, model, key_alias, error_code, error_message`. `exclude_internal_health_checks` 는 true 일 때만 보낸다(NW:2009-2010). `min_spend`·`max_spend` 는 보내지 않는다.

**응답 봉투** (RequestLogsPanel.tsx:175-179, 219-226)
- `data`: **배열 필수**. 없으면 `rows.length`, `data.flatMap`(LG/request/RequestLogsTable.tsx:92)에서 예외다.
- `total`: 행 수 계산에 쓴다(`Math.max(total, …)`). 없으면 NaN 이 되지만 예외는 없다.
- `has_more`: `false` 면 마지막 쪽이다. 없으면 `rows.length < pageSize` 로 대신 판단한다.
- `next_session_cursor`: 시간 정렬(기본)일 때 "다음" 버튼이 쓴다. null 이면 다음 쪽으로 못 간다.
- `page`, `page_size`, `total_pages`, `total_is_capped`: 목록에서는 안 읽는다.

**행 — 표 열** (LG/request/RequestLogsTableColumns.tsx = Cols)

| 필드 | 근거 | 성격 / 없을 때 |
|---|---|---|
| `request_id` | RequestLogsTable.tsx:106, RequestLogsPanel.tsx:37-39, Cols:186-191 | 필수(행 id, 서랍 열기). batch call_type 이면 `.endsWith` 예외(`batchLogUtils.ts:40`) |
| `startTime` | Cols:85 → `new Date(v)` (`shared/table_cells/date_cell.tsx:30`) | 필수. 잘못되면 "-" |
| `status` → `metadata.status` → "Success" | Cols:146-147 | 필수. 소문자로 `"failure"` 일 때만 실패다 |
| `metadata.user_api_key` | Cols:286 ("Key Hash" 열. 클릭하면 `keyInfoV1Call`, RequestLogsPanel.tsx:126) | 표시. **최상위 `api_key` 가 아니라 metadata 값을 읽는다** |
| `metadata.user_api_key_alias` | Cols:294 | 표시 |
| `metadata.user_api_key_team_alias` | Cols:278 | 표시 |
| `metadata.user_api_key_user_email` | Cols:370 | 표시 |
| `spend` | Cols:206-209 → `money_cell.tsx:16` | **JSON 숫자**여야 한다(문자열이면 "-") |
| `request_duration_ms` | Cols:238-240 | null 이면 "-" |
| `completionStartTime`, `endTime` | Cols:254-264 (TTFT) | 장식 |
| `total_tokens`, `prompt_tokens`, `completion_tokens` | Cols:342-351 (`String(x \|\| "0")`) | 안전 |
| `model`, `custom_llm_provider`(로고) | Cols:304-306 | 안전 |
| `user`, `end_user` | Cols:367-381 | 표시 |
| `session_id`, `session_total_count`(`\|\| 1`) | Cols:95,168,207,237,342, RequestLogsPanel.tsx:167,256 | `>1` 이면 세션 서랍(§8 호출)을 연다 |
| `session_total_spend/duration_ms/tokens/prompt/completion`, `session_llm_count`, `session_agent_count`, `session_cache_hit_count`, `mcp_tool_call_count/spend` | Cols:98-134,206-209,238-239,342-345 | count>1 일 때만 쓴다. 없으면 행 값으로 대신한다 |
| `session_models` | Cols:305-307 (`?? []`) | 배열이 아닌 값이 오면 `.join` 예외 |
| `request_tags` | Cols:391-395 (`Object.entries`) | 안전 |
| `call_type` | Cols:96-102 (MCP/agent/batch 배지, `LG/constants.ts:16-22`) | 배지 |
| `litellm_call_id` | Cols:186, RequestLogsPanel.tsx:37-39 | 장식 |
| 최상위 `api_key`, `team_id`, `organization_id`, `agent_id`, `mcp_namespaced_tool_name` | 로그 화면에서 안 읽는다 | — |

**행 — 상세 서랍** (LG/detail/LogDetailsDrawer.tsx, LG/detail/LogDetailContent.tsx = LDC)
- 상태 표시는 **`metadata.status === "failure"` 만** 본다(LogDetailsDrawer.tsx:269-270, LDC:79). 최상위 `status` 만 failure 로 주면 표는 실패, 서랍은 성공으로 나온다. **둘 다 채워야 한다.**
- 오류 배너는 `metadata.error_information.{error_code,error_message,error_class}` 로 그린다(LDC:79-114, 329-343). 모두 안전하게 읽는다.
- 환경 배지는 `metadata.user_api_key_team_alias || "default"` 다(LogDetailsDrawer.tsx:271).
- **`cache_hit` 은 문자열이어야 한다.** `cacheHit?.toLowerCase()`(LG/detail/sections/CostBreakdownViewer.tsx:54) 때문에 boolean 이면 예외다. litellm 은 `"True"`/`"False"`/`""` 문자열을 저장한다(SP:739).
- **`metadata.litellm_overhead_time_ms` 가 있으면 숫자여야 한다.** null 이 아니면 `.toFixed` 를 부른다(LDC:582-584).
- `metadata.cost_breakdown`, `usage_object`/`additional_usage_values`, `guardrail_information`, `routing_decision`, `attempted_retries`/`max_retries` 등은 있을 때만 그린다(LDC:94-209, 395-602, CostBreakdownViewer.tsx:57-69). `metadata` 전체를 JSON 으로도 보여 준다(LDC:268).
- `model, model_id, model_group, api_base, custom_llm_provider, call_type, user, requester_ip_address, cache_key` 는 장식이다(LDC:150-174, 487, `LG/detail/DrawerHeader.tsx:56-86`).

**시각 해석**
- `new Date(str)`, `Date.parse`, `moment(str)` 를 쓴다(date_cell.tsx:30, LogDetailsDrawer.tsx:69-277, DrawerHeader.tsx:297-300).
- 오프셋이 없는 문자열은 **브라우저 현지 시각**으로 해석된다. 그러므로 **`Z` 또는 `+00:00` 을 반드시 붙여야** 한다. 화면은 현지 시간대로 보여 준다.

---

## 7. `GET /spend/logs/ui/{request_id}` (SME:3390-3482)
- 쿼리 `start_date`, `end_date` 는 선택이고 형식은 `YYYY-MM-DD HH:MM:SS` 다. 커스텀 로거(콜드 스토리지)에만 넘긴다. 형식이 틀리면 `strptime` ValueError → 500 이다(감싸지 않음, SME:3436-3441). 미확인: 정확한 본문 모양.
- 권한: 비관리자는 §6.4 의 request_id 검사를 받고, 아니면 403 (B) `{"detail":{"error":"Not authorized to view spend log for request_id=<id>"}}` 다(SME:3417-3433, 4915-4919).
- 행 조회: `(request_id = $1 OR litellm_call_id = $1)` 이고 비관리자는 범위를 걸고 1행만 가져온다(SME:4976-4993).
- 응답:
  - 행이 없고 커스텀 로거 결과도 없으면 **`null`**(HTTP 200, 본문 `null`)이다(SME:3472-3473).
  - 있으면 `{"messages": …, "response": …, "proxy_server_request": …}` 세 칸뿐이다(`RequestResponsePayload._asdict()`, SME:3267-3270, 3481-3482). DB 에 프롬프트를 저장하지 않으면 각 칸은 `{}` 이다. 콜드 스토리지 키가 있으면 거기서 가져온다(SME:3324-3387).
- 대시보드가 읽는 것: `messages`, `response`, `proxy_server_request` 셋만 읽는다. 각각 값이 truthy 일 때만 쓰고, 아니면 목록 행 값을 쓴다(LogDetailsDrawer.tsx:256-263). `null`·`{}` 본문도 안전하다.
  - 요청의 `start_date` 는 표 창의 시작이다(RequestLogsPanel.tsx:350, NW:4779-4805).
  - 2xx 가 아니면 `response.json()` 후 토스트를 띄운다. 그러므로 **오류 본문도 JSON** 이어야 한다.

## 8. `GET /spend/logs/session/ui` (SME:4536-4637)
- 쿼리: `session_id`(필수, **정확히 일치**), `page`(기본 1), `page_size`(기본 50, **1..100**).
- 범위: 관리자는 전체다. internal_user 계열(user_id 있음)은 §6.4 와 같은 "나 OR 열람 가능 팀"이다. 그 밖은 `OwnedRows(user_id)` 다(SME:4584-4593).
- 응답: `{"data":[Row], "total", "page", "page_size", "total_pages"}` (SME:4623-4629).
  - Row 칸은 §6.2 목록에서 **`litellm_call_id`, `request_duration_ms` 와 session_* 집계가 빠진** 26개다(SME:4603-4610).
  - `metadata` 는 객체로 풀어 준다. 정렬은 `startTime DESC` 다.
  - `total_is_capped` 는 없다.
- 오류: DB 가 없으면 500 (B) `{"detail":"Database not connected"}`, 그 밖은 500 (B) `{"detail":"<str(e)>"}` 다(SME:4630-4637).
- 대시보드가 읽는 것 (NW:5559-5597, LogDetailsDrawer.tsx:63-285):
  - 요청: `page_size=100` 으로 최대 50쪽까지, 5개씩 동시에 받는다(:138-157).
  - 봉투: `data`(`firstPage.data || firstPage`. **`{}` 면 `.map` 예외**), `total_pages`(`?? 1`), `total`(`?? rows.length`).
  - 행: `request_id, startTime, endTime, request_duration_ms`(없으면 end-start), `spend, total_tokens, call_type, model, model_group, cache_hit, metadata.internal_call_origin`.

## 9. `GET /management/v1/spend_logs/users` 와 `/end_users` (MV1:154-240)
- 쿼리(선언되지 않은 쿼리를 주면 400 RFC 9457 problem, LAC:97-107):
  - `filter[startTime][gte]`, `filter[startTime][lte]`: **필수**, ISO datetime 이다. 오프셋이 없으면 UTC 로 본다(MV1:36-37).
  - `q`: 선택. ILIKE 부분 일치다.
  - `page`: 기본 1.
  - `page_size`: 기본 50, 1..100.
- 동작:
  - `users` 는 `"user"` 칸을, `end_users` 는 `end_user` 칸을 본다. 창 안에서 값이 NULL·빈 문자열이 아닌 행을 `startTime DESC` 로 **최대 10,000 행** 훑는다. 그 안에서 DISTINCT 를 구하고 **값 오름차순**으로 쪽을 나눈다(MV1:85-129).
  - `has_more` 는 `page_size+1` 개를 가져와 판단한다(MV1:130).
- 범위: 관리자는 전체다. **그 밖은 역할과 무관하게** "나 OR 열람 가능 팀"이다(MV1:40-58).
- 응답 `FacetListResponse` (MV1T:23-46):
```json
{"data":["u1","u2"],"meta":{"page":1,"page_size":50,"has_more":false},
 "links":{"self":"/management/v1/spend_logs/users?…&page=1","prev":null,"next":null}}
```
  - `links.self` 는 alias 이름이다. `next`·`prev` 는 경로에 쿼리를 붙인 상대 URL 이고, 없으면 null 이다(LAC:110-120).
- 오류는 RFC 9457 `application/problem+json` `{"type":"urn:litellm:error:…","title","status","detail"}` 이다. DB 가 없으면 503, 그 밖은 500 `"Failed to list spend log users."` 다(MV1:75-83, 139-151, LAC:26-39).
- 대시보드가 읽는 것 (`app/(dashboard)/hooks/spendLogs/useSpendLogUsers.ts`, `useSpendLogEndUsers.ts`):
  - 요청: `filter[startTime][gte|lte]` 는 표와 같은 UTC `"YYYY-MM-DD HH:mm:ss"` 값이다. 공백 구분이고 오프셋이 없다. FastAPI datetime 파싱이 받는 형식이다. 미확인: pydantic 버전별로 공백 구분을 받는지. 그 밖에 `page_size=25`, `q`(있을 때만), `page` 를 보낸다.
  - `data` 는 **배열 필수**다(`.flatMap`, LG/request/RequestLogsFilters.tsx:194,239).
  - 다음 쪽은 **`links.next` 안의 `page=` 값**으로 정한다. 없거나 null 이면 멈춘다(useSpendLogEndUsers.ts:14-19). 따라서 `links` 객체는 반드시 있어야 한다.
  - `meta.has_more` 는 안 읽는다.

---

## 10. 우리 request_log 로 만들 수 있는 것 / 없는 것

전제가 둘 있다.
- litellm 은 일별 표를 미리 쌓아 두지만, 우리는 `request_log` 를 그때그때 묶어서 같은 응답을 만든다.
- 날짜 버킷은 `(created_at AT TIME ZONE 'UTC')::date` 로 litellm 과 같게 둔다(§2.1).

표기는 셋이다: **직접**(request_log 칸), **조인**(다른 표와 묶어서 얻음), **없음**(안전한 빈 값을 냄).

### 10.1 일별 집계 (`/aggregated`, `/keys`, `/search`, `/model_top_keys`, `/users`, `/export`)

| 필요한 값 | 출처 | 값 |
|---|---|---|
| `date` | 직접 | `created_at` 의 UTC 날짜 `YYYY-MM-DD` |
| `spend` | 직접 | `SUM(cost)`. JSON **숫자**로 낸다 |
| `prompt_tokens`, `completion_tokens` | 직접 | SUM. `total_tokens` 는 둘의 합(CDA:725 와 같음) |
| `api_requests` | 직접 | `COUNT(*)` |
| `successful_requests` / `failed_requests` | 직접 | `status='success'` / `status IN ('error','cancelled')`. litellm 은 `metadata.status=='failure'` 만 실패로 센다. 우리 cancelled 를 실패에 넣을지는 결정 사항이다(litellm 의 클라이언트 끊김 기록 방식은 미확인) |
| `total_response_time_ms`, `timed_requests` | 직접 | 성공 행만 대상으로 `SUM(latency_ms)`, `COUNT(*)` 다(DSW:321-331 과 같은 규칙) |
| `cache_read_input_tokens`, `cache_creation_input_tokens`, `compression_*`, `*_savings_spend`, `flat_cost`, `ptu_hours` | 없음 | `0` (칸은 반드시 낸다. null 금지, §3.5) |
| `breakdown.model_groups` 키 | 직접 | `model` (우리 모델 그룹 이름) |
| `breakdown.models` 키 | 조인 | `model_deployment.upstream_model` (`deployment_id`). 배포를 지웠거나 deployment_id 가 null 이면 `model` 로 대신한다(litellm 의 `COALESCE(model_group, model)` 과 반대 방향의 대체) |
| `breakdown.providers` 키 | 조인 | `model_deployment.provider`. 없으면 `"unknown"` (CDA:830 과 같음) |
| `breakdown.endpoints` | 없음 | `{}`. call_type 칸이 없다. 채팅만 중계하는 동안 `"/chat/completions"` 상수로 낼 수도 있다(결정 사항) |
| `breakdown.mcp_servers` | 없음 | `{}` |
| `breakdown.api_keys` 키 | 직접 | `key_token`. key_token 이 null 인 행은 litellm 의 `api_key=""` 에 해당한다. 미확인: 그런 행이 키 목록에 어떻게 나오는지 |
| `api_keys[*].metadata.key_alias/team_id/user_id` | 조인 | `api_key` 에 있으면 그 값과 `key_exists=true`. 없으면 `deleted_api_key`(같은 token 중 가장 늦게 지운 것)와 `key_exists=false`. 둘 다 없으면 전부 null 과 `key_exists=false` |
| `api_keys[*].metadata.user_email` | 조인 | `app_user.user_email` (위에서 얻은 user_id 기준) |
| litellm 고유 복구(CLI 세션 키, 이중 해시, SpendLogs metadata, §3.4 의 3·4단계) | 없음 | 하지 않는다. 우리 키 형식에는 해당하지 않는다 |
| `entities` (team) 키 | 직접 | `team_id`. null·빈 값이면 `"Unassigned"` |
| `entities[*].metadata.team_alias` | 조인 | `team.team_alias`. 팀이 없으면 `{}` |
| `api_key_breakdown`, `api_key_limit` | 직접 | (차원, key_token) 묶음. 기간 spend 상위 N 키로 제한한다(DSQL:140-148) |
| `metadata.total_api_keys`, `entity_total_api_keys` | 직접 | `COUNT(DISTINCT key_token)` (전체 / 팀별) |
| `/users` 행 `user_email`, `user_alias` | 조인 | `app_user` |
| `/team/spend/by_user` 전체 | 직접 + 조인 | `(team_id, user_id)` 로 묶고 `app_user`, `team` 과 조인한다. 날짜는 `created_at >= start AND < end+1일` (UTC) |
| `/export` `entity_alias` | 조인 | team 이면 `team.team_alias`, user 면 null |
| `/export` `key_alias`, `user_id`, `user_email` | 조인 | 위 키 메타데이터와 같다 |

### 10.2 요청 로그 (`/spend/logs/ui`, `/session/ui`, `/{request_id}`)

| 행 필드 | 출처 | 값 |
|---|---|---|
| `request_id` | 직접 | `request_id` |
| `litellm_call_id` | 직접 | `request_id` 와 같은 값 |
| `startTime` | 직접 | `created_at`. RFC 3339 **`Z` 포함**(§6.5) |
| `endTime` | 직접 | `created_at + latency_ms` |
| `completionStartTime` | 직접 | `created_at + ttft_ms`. ttft 가 null 이면 null |
| `request_duration_ms` | 직접 | `latency_ms` |
| `spend` | 직접 | `cost` (숫자) |
| `prompt_tokens`, `completion_tokens`, `total_tokens` | 직접 | `total_tokens` 는 합 |
| `model_group` | 직접 | `model` |
| `model_id` | 직접 | `deployment_id` |
| `model` | 조인 | `model_deployment.upstream_model`. 없으면 `model` |
| `custom_llm_provider` | 조인 | `model_deployment.provider`. 없으면 `""` |
| `api_base` | 조인 | `model_deployment.base_url`. 장식이라 `""` 도 된다 |
| `api_key`, `metadata.user_api_key` | 직접 | `key_token` (표의 Key Hash 열은 metadata 쪽을 읽는다) |
| `metadata.user_api_key_alias` | 조인 | `api_key.key_alias`. 없으면 `deleted_api_key`. litellm 은 요청 시점 값을 저장하지만 우리는 현재 값을 낸다. 일탈로 기록할 것 |
| `metadata.user_api_key_team_id`, `user_api_key_user_id` | 직접 | `team_id`, `user_id` |
| `metadata.user_api_key_team_alias` | 조인 | `team.team_alias` |
| `metadata.user_api_key_user_email` | 조인 | `app_user.user_email` |
| `user`, `team_id` | 직접 | `user_id`, `team_id` |
| `status`, `metadata.status` | 직접 | `success` → `"success"`, `error`·`cancelled` → `"failure"`. **두 곳 모두** 넣는다(§6.5) |
| `metadata.error_information.error_code` | 직접 | litellm 은 HTTP 상태 문자열이다(예: `"404"`, SME:2580). 실패 행이면 `http_status` 를 문자열로 낸다. 우리 `error_code` 를 넣을지는 결정 사항이다 |
| `metadata.error_information.error_message` | 없음 | 본문을 저장하지 않으므로 null 또는 `error_code` 다. `error_message` 필터는 아무 행도 맞지 않게 한다 |
| `call_type` | 없음 | `"acompletion"` (채팅만 중계하는 동안). null 이어도 표는 `.includes` 비교만 하므로 예외가 없다(Cols:96-102, `components/logs/batchLogUtils.ts:19`). 다른 곳은 확인하지 않았다 |
| `cache_hit` | 없음 | **문자열 `""`** (boolean 이면 예외, §6.5). `cache_key` 도 `""` |
| `session_id` | 없음 | null. 그러면 `session_total_count=1` 이고, 세션 서랍과 `/spend/logs/session/ui` 는 불리지 않는다(RequestLogsPanel.tsx:167,256) |
| `end_user`, `organization_id`, `requester_ip_address`, `agent_id`, `mcp_namespaced_tool_name` | 없음 | null |
| `request_tags` | 없음 | `[]` |
| `messages`, `response`, `proxy_server_request` (`/{request_id}`) | 없음 | `{"messages":{},"response":{},"proxy_server_request":{}}`. litellm 이 프롬프트를 저장하지 않을 때와 같은 모양이다(SME:3337-3351). 권한이 있고 행이 없으면 `null` |
| `metadata.litellm_overhead_time_ms`, `cost_breakdown`, `usage_object` 등 | 없음 | 넣지 않는다. 넣는다면 `litellm_overhead_time_ms` 는 숫자만 넣는다 |

**필터와 정렬 대응** (§6.1)
- `user_id`, `team_id`, `api_key`, `model_id`, `model`(upstream 조인), `request_id`, `status_filter` 는 그대로 대응한다.
- `key_alias` 는 alias 조인 후 LIKE 로 처리한다.
- `error_code` 는 http_status 로 처리한다.
- `search` 는 `request_id`, `key_token`, `team_id`, `user_id`, `deployment_id` 다섯 칸으로 찾는다. end_user·session 은 없다.
- 맞는 행이 있을 수 없는 필터는 **빈 결과**로 처리한다(무시하면 결과가 너무 많이 나온다). 해당 필터: `end_user`, `session_id`, `cache_hit_filter=hit`, `span_type∈{agent,mcp,batch}`, `used_client_oauth_token`.
- `cache_hit_filter=miss` 와 `span_type=llm` 은 모든 행이 맞는다.
- 정렬 대응: `startTime`→`created_at`, `endTime`→`created_at+latency_ms`, `request_duration_ms`→`latency_ms`, `ttft_ms`→`ttft_ms NULLS LAST`, `spend`→`cost`, `total_tokens`→합, `model`→upstream.

**세션 묶음** (§6.3)
- session_id 가 없으면 모든 행이 혼자 한 세션이다. 그래서 `group_by_session=true` 응답은 평면 목록과 같은 행을 낸다. 키셋 순서는 `(created_at, request_id, key_token)` 이다.
- 대신 `has_more`(page_size+1 개를 읽어 판단)와 `next_session_cursor` 를 꼭 넣어야 한다. 커서는 대시보드가 그대로 돌려보내기만 하므로(§6.5) 모양을 litellm 과 맞출 필요는 없다. 다만 `<last_activity>|<api_key>|<session_key>` 를 그대로 쓰는 편이 단순하다.
- `total` 은 10,000 에서 자른다.

**`/management/v1/spend_logs/users`·`/end_users`**
- `users` 는 `DISTINCT user_id` 다. §9 규칙(창 안 최근 10,000 행, 오름차순, 범위)을 그대로 따른다.
- `end_users` 는 **없음**이다. `{"data":[],"meta":{…,"has_more":false},"links":{"self":"…","prev":null,"next":null}}` 를 낸다.

**범위(권한)**
- §3.2·§5·§6.4 의 판정은 우리 표만으로 모두 할 수 있다. 필요한 표는 `app_user.user_role`, `team_member.role='admin'`, 팀 멤버 권한(`/team/daily/activity`, `/spend/logs`), `api_key.user_id` 다.
- 팀 멤버 권한 목록을 어디에 두는지는 H·I 문서를 따른다. 미확인: 우리 스키마에 팀별 member_permissions 가 있는지.
