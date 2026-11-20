# I. 팀·사용자 화면이 부르는 엔드포인트 전체와, H 에 없는 엔드포인트의 HTTP 계약

H(`H_team_user_contract.md`)는 v1 팀·사용자 엔드포인트 15개를 다뤘다. 이 문서는 두 가지를 적는다.
1. 대시보드 팀 화면·사용자 화면이 실제로 부르는 엔드포인트 전부(목록·상세·생성/수정 모달·멤버 탭·드롭다운)
2. 그중 H 에 없는 엔드포인트의 계약. 형식은 H 와 같다.

근거는 모두 `파일:줄`로 달았다. 코드로 확인하지 못한 것은 "미확인"으로 적었다. 오류 본문 모양 (A)(B)(C)(D)와 권한 판정 두 단계는 H 2.3·2.4 를 그대로 쓴다. 여기서는 다시 설명하지 않는다.

정해 둔 차이는 이렇게 표시한다.
- **[차이:토큰]** 팀 응답에 해시 키 `token` 을 넣지 않는다
- **[차이:조직]** 조직(organization)이 없다
- **[차이:예산표]** budgets 테이블(`LiteLLM_BudgetTable`)이 없다
- **[차이:SCIM]** SCIM 이 없다
- **[차이:가드레일]** 가드레일이 없다
- **premium, 적용 안 함**: 유료 검사. 기록만 하고 강제하지 않는다

## 0. 약어 (경로는 `<workspace>/` 기준)

H 0절의 약어(TE, IU, T, MT, TT, UL, UR, ROLES, AZ, RC, AC, PS, PU, SP, NW, CL, UI/…)를 그대로 쓴다. 이 문서에서 새로 쓰는 약어는 아래와 같다.

| 약어 | 파일 |
|---|---|
| PSE | `gateway/litellm/proxy/ui_crud_endpoints/proxy_setting_endpoints.py` |
| SSO | `gateway/litellm/proxy/management_endpoints/ui_sso.py` |
| MV1T | `gateway/litellm/proxy/management_endpoints/management_v1/teams.py` |
| MUE | `gateway/litellm/proxy/management/users/endpoints.py` |
| MUX | `gateway/litellm/proxy/management/users/exceptions.py` |
| LAC | `gateway/litellm/proxy/list_api/common.py` |
| MV1 | `gateway/litellm/types/proxy/management_endpoints/management_v1.py` |
| BTMB | `gateway/litellm/proxy/management_helpers/bulk_team_member_budgets.py` |
| BUD | `gateway/litellm/proxy/management_helpers/bulk_user_deletion.py` |
| TMP | `gateway/litellm/proxy/management_helpers/team_member_permission_checks.py` |
| UINV | `gateway/litellm/proxy/management_helpers/user_invitation.py` |
| CU | `gateway/litellm/proxy/management_endpoints/common_utils.py` |
| SA | `gateway/litellm/types/integrations/slack_alerting.py` |
| MTM | `gateway/litellm/models/team_membership.py` |
| UAK | `gateway/litellm/types/proxy/auth/user_api_key_auth.py` |
| UIS | `gateway/litellm/types/proxy/management_endpoints/ui_sso.py` |
| D/… | `gateway/ui/litellm-dashboard/src/app/(dashboard)/…` |
| C/… | `gateway/ui/litellm-dashboard/src/components/…` |

**조사 방법**: `D/teams/page.tsx`, `D/users/page.tsx` 에서 시작해 import 를 따라가며 닿는 `.ts/.tsx` 파일을 모두 모았다(테스트 제외). 팀 쪽 351개, 사용자 쪽 229개다. 그 파일들에서 `networking.tsx` 함수 호출, `apiClient.*`, `fetchClient.*`, 직접 `fetch` 를 찾았다. import 로 닿는다고 해서 화면에 실제로 그려진다는 보장은 없다. 그래서 아래 표에는 렌더 경로를 직접 확인한 호출만 넣었다.

---

## 1. 요약 표 — 팀·사용자 화면이 부르는 엔드포인트

"H" 열: ○ = H 에 계약이 있음, ● = 이 문서 2~4절에 계약이 있음, — = 범위 밖(다른 기능 영역. 목록만 적음).

### 1.1 팀·사용자 도메인

| # | 메서드·경로 | 대시보드 호출 위치 | 화면 | H |
|---|---|---|---|---|
| 1 | POST `/team/new` | C/Teams.tsx:558 | 팀 생성 모달 | ○ |
| 2 | POST `/team/update` | C/team/TeamInfo.tsx:944 (`persistTeamUpdate`) | 팀 상세 설정 편집 | ○ |
| 3 | POST `/team/delete` | C/Teams.tsx:386 | 팀 목록 삭제 | ○ |
| 4 | POST `/team/member_add` | C/team/TeamInfo.tsx:831, C/team/AvailableTeamsPanel.tsx:52(셀프 가입), D/users/_components/view_users/user_info_view.tsx:205 | 멤버 탭, 가입 가능 팀 탭, 사용자 상세 "팀에 추가" | ○ |
| 5 | POST `/team/member_delete` | C/team/TeamInfo.tsx:917, user_info_view.tsx:245 | 멤버 탭, 사용자 상세 | ○ |
| 6 | POST `/team/member_update` | C/team/TeamInfo.tsx:879 | 멤버 편집 모달 | ○ |
| 7 | GET `/team/info` | C/team/TeamInfo.tsx:728,741,839,886,923; D/hooks/teams/useTeams.ts:174(`useTeam`, ModelSelect 가 씀); D/hooks/teams/useTeamMemberBudgets.ts:35(`keyLimit:1`); user_info_view.tsx:138,214,255(팀 별칭 표시용, 팀마다 1회) | 팀 상세, 모델 선택, 사용자 상세 | ○ |
| 8 | GET `/team/list` (v1) | D/networking.ts:11,13 ← D/hooks/teams/useTeams.ts:125(`useTeams`) ← D/users/page.tsx:9; user_info_view.tsx:176 | 사용자 화면(필터·일괄 편집 팀 목록), 사용자 상세 "팀에 추가" 모달 | ○ |
| 9 | GET `/v2/team/list` | D/hooks/teams/useTeams.ts:77(`teamListCall`) ← :113 `useTeamsTable`(C/TeamsPage/TeamsTable.tsx:90), :134·139 `useAllTeams`, :209 `useInfiniteTeams`(C/common_components/team_dropdown.tsx:31 ← C/CreateUserButton.tsx:260), C/TeamsPage/teamsCsvExport.ts:88 | 팀 목록 표, CSV 내보내기, 사용자 생성 모달 팀 드롭다운 | ● 2.1 |
| 10 | GET `/team/available` | C/team/AvailableTeamsPanel.tsx:28 | "Available Teams" 탭(C/Teams.tsx:654-657) | ● 2.2 |
| 11 | GET `/team/metadata_schema` | D/hooks/teams/useTeamMetadataSchema.ts:20 ← C/Teams.tsx:221, C/team/TeamInfo.tsx:651 | 팀 생성·편집 메타데이터 폼 | ● 2.3 |
| 12 | GET `/team/permissions_list` | C/team/member_permissions.tsx:28 (NW:5509-5514) | "Member Permissions" 탭(TeamInfo.tsx:1547) | ● 2.4 |
| 13 | POST `/team/permissions_update` | C/team/member_permissions.tsx:58 (NW:5536-5543) | 같은 탭 저장 | ● 2.5 |
| 14 | GET `/team/{team_id}/members/me` | C/team/useMyTeamMember.ts:30-35 ← C/team/MyUserTab.tsx:34 | "My User" 탭(TeamInfo.tsx:1521) | ● 2.6 |
| 15 | POST `/team/{team_id}/member/{user_id}/reset_spend` | D/hooks/teams/useResetTeamMemberSpend.ts:10-13 ← C/team/TeamMemberTab.tsx:276 | 멤버 탭 "사용액 초기화" | ● 2.7 |
| 16 | POST `/team/{team_id}/member/{user_id}/reset_budget` | D/hooks/teams/useResetTeamMemberBudget.ts:9-11 ← C/team/TeamMemberTab.tsx:291 | 멤버 탭 "팀 기본 예산으로" | ● 2.8 |
| 17 | POST `/management/v1/teams/{team_id}/members/bulk_update` | C/team/TeamInfo.tsx:961-964 | 팀 기본 멤버 예산을 바꾼 뒤 개별 예산 일괄 해제 | ● 2.9 |
| 18 | POST `/team/bulk_member_add` | D/users/_components/BulkEditUsers.tsx:163 (NW:2938-2969) | 사용자 일괄 편집 "팀에 추가" | ● 2.10 |
| 19 | POST `/user/new` | C/CreateUserButton.tsx:209, C/bulk_create_users_button.tsx:367 | 사용자 생성·CSV 일괄 생성 | ○ |
| 20 | POST `/user/update` | user_info_view.tsx:331 | 사용자 상세 편집 | ○ |
| 21 | POST `/user/delete` | D/users/_components/view_users.tsx:179, user_info_view.tsx:301 | 사용자 목록·상세 삭제 | ○ |
| 22 | GET `/user/list` | view_users.tsx:245(목록 표); D/hooks/users/useUsers.ts:22(`useInfiniteUsers`),50,64(`useUserLookup` ← C/common_components/UserDropdown.tsx:24,39) | 사용자 목록, 사용자 드롭다운 | ○ |
| 23 | GET `/v2/user/info` | user_info_view.tsx:130,209,250; D/hooks/users/useCurrentUser.ts:13 ← C/ModelSelect/ModelSelect.tsx:154 | 사용자 상세, 모델 선택(본인 모델) | ● 3.1 |
| 24 | GET `/user/filter/ui` | C/common_components/user_search_modal.tsx:98 (NW:1929-1939) ← TeamInfo 멤버 추가 모달 | 멤버 추가 시 사용자 검색 | ● 3.2 |
| 25 | GET `/user/available_roles` | view_users.tsx:269 | 역할 라벨·역할 드롭다운 | ● 3.3 |
| 26 | POST `/user/bulk_update` | BulkEditUsers.tsx:137,140 (NW:3191-3222) | 사용자 일괄 편집 | ● 3.4 |
| 27 | POST `/invitation/new` | view_users.tsx:165, user_info_view.tsx:289(비밀번호 재설정 링크), C/CreateUserButton.tsx:221(생성 직후 자동), C/bulk_create_users_button.tsx:378 | 초대·재설정 링크 | ● 3.5 |

### 1.2 설정·공통·드롭다운 (팀·사용자 화면이 쓰는 것)

| # | 메서드·경로 | 대시보드 호출 위치 | 화면 | H |
|---|---|---|---|---|
| 28 | GET `/get/default_team_settings` | C/Teams.tsx:290(팀 생성 모달 열릴 때만, :291 `enabled: isTeamModalVisible`), C/TeamSSOSettings.tsx:131 | 생성 모달 예산 기간 placeholder, "Default Team Settings" 탭(proxy admin 만, C/Teams.tsx:659-667) | ● 4.1 |
| 29 | PATCH `/update/default_team_settings` | C/TeamSSOSettings.tsx:152 | 같은 탭 저장 | ● 4.1 |
| 30 | GET `/get/internal_user_settings` | D/users/_components/default-user-settings/DefaultUserSettingsForm.tsx:54 | 사용자 화면 "Default User Settings"(proxy admin 만, view_users.tsx:366,396) | ● 4.2 |
| 31 | PATCH `/update/internal_user_settings` | DefaultUserSettingsForm.tsx:62 | 같은 폼 저장 | ● 4.2 |
| 32 | GET `/models` | C/Teams.tsx:409 → C/key_team_helpers/fetch_available_models_team_key.tsx:14; D/hooks/models/useModels.ts:257(`useAllProxyModels`, `scope=expand`) ← ModelSelect.tsx:151; useModels.ts:279(`useModelAccessGroupNames`) ← TeamInfo.tsx:643; view_users.tsx:97; user_info_view.tsx:156; C/CreateUserButton.tsx:178 | 모든 모델 선택 드롭다운 | ● 4.3 |
| 33 | GET `/sso/get/ui_settings` | C/CreateUserButton.tsx:185, C/bulk_create_users_button.tsx:76 | 사용자 생성 후 초대 링크 분기 | ● 4.4 |
| 34 | GET `/get/ui_settings` | D/hooks/uiSettings/useUISettings.ts:19 ← C/team/TeamMemberTab.tsx:138 | 멤버 삭제 버튼 노출 여부 | ● 4.5 |
| 35 | GET `/management/v1/users/me` | D/hooks/currentUser/useCurrentUser.ts:12 ← D/layout.tsx | 레이아웃 공통(화면 전용 아님) | ● 4.6 |
| 36 | GET `/organization/list` | D/hooks/organizations/useOrganizations.ts:24 ← C/Teams.tsx:219, C/TeamsPage/TeamsTable.tsx:48, TeamInfo.tsx:650, C/CreateUserButton.tsx:168, C/TeamSSOSettings.tsx:121 | 조직 드롭다운·조직 열 | ● 4.7 [차이:조직] |
| 37 | GET `/organization/info` | C/team/TeamInfo.tsx:762(팀에 `organization_id` 가 있을 때만); useOrganizations.ts:41 ← ModelSelect.tsx:153 | 팀 상세 조직 표시 | ● 4.7 [차이:조직] |

### 1.3 범위 밖 (목록만, 계약은 쓰지 않음)

| # | 메서드·경로 | 대시보드 호출 위치 | 비고 |
|---|---|---|---|
| 38 | POST `/budget/info` | C/TeamsPage/teamsCsvExport.ts:91 | CSV 내보내기가 팀 `metadata.team_member_budget_id` 로 멤버 예산을 조회. [차이:예산표] |
| 39 | GET `/key/list` | D/hooks/keys/useKeys.ts:84 ← C/team/TeamVirtualKeysTable.tsx:91 (`team_id`, `expand=user`) | 팀 상세 키 탭. 키 계약 문서 범위 |
| 40 | 키 상세 묶음: POST `/key/update`, `/key/delete`, `/key/{id}/regenerate`, `/key/{id}/reset_spend`, `/key/block`·`/key/unblock`, GET `/tag/list`, `/prompts/list` | C/templates/key_info_view.tsx:398,419; C/organisms/RegenerateKeyModal.tsx:98; D/hooks/keys/useResetKeySpend.ts:20; D/hooks/keys/useSetKeyBlockedState.ts:23; C/templates/key_edit_view.tsx:184,214 | 팀 키 탭에서 키를 누르면 C/team/TeamVirtualKeysTable.tsx:36 의 `KeyInfoView` 가 열린다. 키 문서 범위 |
| 41 | GET `/v2/guardrails/list` (실패하면 `/guardrails/list`) | C/Teams.tsx:308, D/hooks/guardrails/useGuardrails.ts:38 ← TeamInfo.tsx:627 (NW:3823-3843) | [차이:가드레일]. try/catch 로 감싸 실패해도 화면은 뜬다 |
| 42 | GET `/policies/list`, GET `/policy/info/{name}` | C/Teams.tsx:322, TeamInfo.tsx:777,802 | 정책. `canViewPolicies` 일 때만 |
| 43 | GET `/v1/mcp/server`, `/v1/mcp/toolset`, `/v1/mcp/access_groups`, `/mcp-rest/tools/list` | D/hooks/mcpServers/useMCPServers.ts:14, useMCPToolsets.ts:13, useMCPAccessGroups.ts:11, C/mcp_server_management/MCPToolPermissions.tsx:120 | 팀·사용자 MCP 권한 편집 |
| 44 | GET `/v1/access_group` | D/hooks/accessGroups/useAccessGroups.ts:20 ← TeamInfo.tsx:646 | 관리자 역할일 때만(useAccessGroups.ts:48) |
| 45 | GET `/v1/agents`, `/vector_store/list`, `/search_tools/list`, `/claude-code/plugins`, `/config/pass_through_endpoint[/team/{id}]` | C/agent_management/AgentSelector.tsx:41, C/vector_store_management/VectorStoreSelector.tsx:31, C/search_tools/SearchToolSelector.tsx:44, C/skills/SkillSelector.tsx:60, C/common_components/PassThroughRoutesSelector.tsx:43 (NW:3407-3412) | 팀 생성·편집 폼의 부가 선택기 |
| 46 | GET `/router/settings` 와 자동 라우터 설정 하위 호출(`/auto_router/classifier/default_prompt`, `/public/complexity_router/scorer_defaults` 등) | C/common_components/RouterSettingsAccordion.tsx:151; C/add_model/ClassifierPromptEditor.tsx:41; D/hooks/autoRouter/useComplexityScorerDefaults.ts:11 | 팀 라우터 설정 아코디언. 실제로 렌더되는 범위는 미확인 |
| 47 | GET `/user/daily/activity/aggregated`, GET `/auto_router/benchmarks`, 배포 목록(`useAutoRouters` → `useDeployments`) | NW:1388-1395 ← D/cost-optimization/_components/useDailyActivityRange.ts:78 ← C/shared/ScopedSavingsTab.tsx:39; D/cost-optimization/_components/useAutoRouterBenchmarks.ts:34; D/hooks/models/useModels.ts:234-235 | 사용자 상세의 Savings·Auto Router Usage 탭(user_info_view.tsx:47-49) |

- 별도 페이지인 "Deleted Teams"(C/DeletedTeamsPage)는 `/v2/team/list?status=deleted` 를 부른다(useTeams.ts:250,256). 팀 화면의 import 경로에는 들어 있지 않다.
- 어떤 API 오류든 메시지에 `"Authentication Error - Expired Key"` 가 들어 있으면 대시보드는 로그아웃한다(NW:434-448 `handleError`, NW:530-540 에서 모든 apiClient 에 연결). 다른 오류는 이 경로에서 화면에 아무것도 띄우지 않는다. 각 호출부가 직접 토스트를 띄운다.

**엔드포인트 수**: 1.1~1.2 에 37개다. H 에 있는 것이 11개(#1~8, #19~22. `/team/list` v1 포함), 이 문서에서 계약을 쓴 것이 26개다. 1.3 범위 밖은 10줄이고 묶음 행을 풀면 약 30개다.

---

## 2. 팀 엔드포인트 (H 에 없는 것)

### 2.1 GET `/v2/team/list` (TE:5991-6184)

팀 목록 표의 본체다. **v1 `/team/list` 와 권한 판정, 멤버십 기준, 응답 모양이 모두 다르다.**

**쿼리**(TE:5998-6027)

| 파라미터 | 타입 | 기본값 | 의미 | 대시보드 전송 |
|---|---|---|---|---|
| user_id | str | null | 이 사용자가 속한 팀만. 기준은 **사용자 행의 `teams` 배열**(TE:5795-5833) | 비관리자는 본인 id, Admin/Admin Viewer 는 보내지 않음(C/TeamsPage/TeamsTable.tsx:68,77; useTeams.ts:197,212) |
| organization_id | str | null | 일치(TE:5784-5785) | 필터 `org_id`(TeamsTable.tsx:72) [차이:조직] |
| team_id | str | null | 정확히 일치(TE:5769-5770) | 필터 `team_id`(TeamsTable.tsx:74) |
| team_alias | str | null | 부분 일치, 대소문자 무시(TE:5772-5776) | 필터 `alias`(TeamsTable.tsx:73), 드롭다운 검색어(useTeams.ts:210) |
| search | str | null | `team_id` 일치 **또는** `team_alias` 부분 일치(대소문자 무시)(TE:5778-5782) | 검색창(TeamsTable.tsx:75) |
| search_team_id_match | `"exact"`\|`"prefix"` | `"exact"` | `prefix` 면 `team_id` 가 **대소문자 구분** `startsWith`(TE:5780) | 표는 항상 `"prefix"`(TeamsTable.tsx:76) |
| page | int ≥1 | 1 | | ○ |
| page_size | int 1..100 | **10** | 범위 밖이면 422 (C) | 표 50(TeamsTable.tsx:52), `useAllTeams` 100(useTeams.ts:130), 드롭다운 50(useTeams.ts:195) |
| sort_by | str | null | `team_id`·`team_alias`·`created_at` 만 반영. **다른 값은 오류 없이 무시**되고 기본 정렬을 쓴다(TE:6113-6118) | 기본 `created_at`(TeamsTable.tsx:33). 정렬 가능한 열은 `team_alias`·`created_at` 뿐(C/TeamsPage/teamTableColumns.tsx:160,232) |
| sort_order | str | `"asc"` | `asc`/`desc` 가 아니면 **오류 없이 `asc`**(TE:6115-6116) | `asc`/`desc`(TeamsTable.tsx:35-39) |
| status | str | null | `"deleted"` 만 허용. 다른 값은 400(TE:6075-6079) | 팀 화면은 보내지 않음 |

- 기본 정렬은 `created_at desc` 다(TE:6127,6136).
- 대시보드는 null·undefined 값을 쿼리에서 뺀다(useTeams.ts:73-74).

**성공 응답**: 200 `TeamListResponse`(TT:98-105, TE:6178-6184)
```json
{
  "teams": [
    { ...LiteLLM_TeamTable 필드 (MT:63-101)...,
      "litellm_model_table": {...}|null,          // include 됨 (TE:6137, TE:475)
      "members_count": 3,                          // len(members_with_roles) (TE:5878)
      "keys_count": 2,                             // 그 팀의 VerificationToken 행 수 (TE:5891-5913)
      "access_group_models": [..]|null,
      "access_group_mcp_server_ids": [..]|null,
      "access_group_agent_ids": [..]|null,
      "access_group_skill_names": [..]|null }      // TT:86-95, 계산은 TE:6155-6176
  ],
  "total": 12, "page": 1, "page_size": 50, "total_pages": 1
}
```
- `total_pages = ceil(total / page_size)` 다(TE:6143). 결과가 없으면 0 이다.
- **`keys` 와 `team_memberships` 는 없다.** v1 `/team/list`(H 3.10)와 다르다. 그래서 [차이:토큰]과 부딪치지 않는다.
- `keys_count` 는 만료 키와 대시보드 세션 키를 걸러내지 않는다. `team_id in (그 페이지 팀들)` 로 group by 만 한다(TE:5905-5911).
- `object_permission` 은 include 하지 않아서 null 이다(TE:6133-6138 은 `litellm_model_table` 만 include).
- `members_with_roles` 가 리스트가 아니면 `[]` 로 바꾼다(TE:5874-5877).
- 다음 두 경우에는 DB 를 조회하지 않고 `{"teams":[],"total":0,"page":p,"page_size":s,"total_pages":0}` 를 돌려준다(TE:6103-6110). `user_id` 사용자의 `teams` 가 비어 있을 때(TE:5823-5824).
- `status=deleted` 이면 `LiteLLM_DeletedTeamTable`(MT:140-147: `deleted_at`, `deleted_by`, `deleted_by_api_key`, `litellm_changed_by` 추가)을 돌려준다. `members_count`·`keys_count`·`litellm_model_table` 은 없다(TE:6121-6130, 5871-5872). user_id 필터는 `members has user_id`(레거시 배열)로 건다(TE:5816-5817).

**대시보드가 읽는 필드**
- 표: `team_alias, team_id, organization_id, spend, max_budget, created_at, updated_at, tpm_limit, rpm_limit, models, members_count(없으면 members_with_roles.length), keys_count(없으면 keys.length)` (teamTableColumns.tsx:39-41,147-280). 최상위 `teams`, `total`(TeamsTable.tsx:92-93).
- CSV: 위 필드에 더해 `budget_duration, budget_reset_at, blocked, metadata`, 그리고 `total_pages`(teamsCsvExport.ts:22-67).
- 드롭다운: `page`, `total_pages`(useTeams.ts:217-221).
- `useAllTeams`: `total_pages`, `teams`(useTeams.ts:134-141).

**오류** ((B) 형태. `management_endpoint_wrapper` 는 예외를 다시 던진다, MH:813-836)
- 비관리자이고 org admin 도 아니면서 `user_id` 가 없거나 남의 id: **401** `{"detail":{"error":"Only admin users can query all teams/other teams. Your user role=<role>"}}` (TE:5980-5986). `allowed_route_check_inside_route` 는 `requested_user_id=None` 이면 비관리자에게 False 를 돌려준다(AC:1634-1647). 그래서 TE:5988 의 "본인 id 자동 주입"은 실제로는 실행되지 않는다. **비관리자는 반드시 `user_id=본인`을 보내야 한다.**
- `user_id` 사용자가 없음: 404 `{"detail":{"error":"User not found, passed user_id=<id>"}}` (TE:5804-5813).
- `user_id` 와 `team_id` 를 같이 보냈는데 그 팀 멤버가 아님: 404 `{"detail":{"error":"User is not a member of team_id=<id>"}}` (TE:5825-5831).
- `status` 값 오류: 400 `{"detail":{"error":"Invalid status value. Currently only 'deleted' is supported."}}` (TE:6075-6079).
- org admin 이 남의 조직을 요청: 403 `"You can only view teams within your organizations."` (TE:5955-5959). [차이:조직]
- DB 없음: 500 (TE:6059-6063).

**권한**
- 라우트 단계: info_route 라 누구나 통과한다(T:564, RC:328).
- 핸들러(TE:5916-5988):
  - proxy_admin·proxy_admin_viewer(UAK:328-338): 전체. `user_id` 를 주면 그 사용자 팀만.
  - org admin: 자기 조직 팀과 본인이 속한 팀의 합집합(TE:5786-5791). [차이:조직]
  - 나머지: `user_id == 본인` 일 때 본인 `teams` 배열에 있는 팀만.
- **v1 과 멤버십 기준이 다르다.** v1 `/team/list` 는 `members_with_roles[].user_id` 로 판정한다(TE:6250-6257). v2 는 사용자 행의 `teams` 배열로 판정한다(TE:5818-5833). 두 값이 어긋나면 두 목록의 결과가 달라진다.

**부수 효과**: 없음(읽기). `management_endpoint_wrapper` 가 성공한 뒤 캐시 삭제 훅을 부른다. 하지만 이 요청에는 삭제할 키·팀 id 가 없다(MH:802-806).

### 2.2 GET `/team/available` (TE:5647-5687)

**쿼리**: 없음. 시그니처에 `response_model=list[LiteLLM_TeamTable]` 이 **함수 매개변수**로 잘못 들어가 있다(TE:5651). FastAPI 가 이것을 쿼리 매개변수로 해석하는지는 미확인이다. 대시보드는 아무것도 보내지 않는다(NW:1223-1229).

**성공 응답**: 200, **맨 배열** `[LiteLLM_TeamTable…]` (TE:5685-5687)
- `litellm_settings.default_internal_user_params.available_teams` 가 없으면 `[]` 다(TE:5661-5670).
- 호출자 `teams` 배열에 이미 있는 팀은 뺀다(TE:5681).
- include 가 없어서 `litellm_model_table`·`object_permission` 은 null 이다(TE:5683).
- 대시보드가 읽는 필드: `team_id, team_alias, description, members_with_roles, models` (C/team/AvailableTeamsTableColumns.tsx). **`description` 은 팀 모델에 없는 필드라서 항상 비어 있다**(MT:63-101).

**오류** ((B) 형태, wrapper 없음)
- 호출자 사용자 행 없음: 404 `{"detail":{"error":"User not found"}}` (TE:5673-5678).
- DB 없음: 400 (TE:5655-5659).
- 키에 user_id 가 없을 때의 동작은 미확인이다(`find_unique(where={"user_id": None})`).

**권한**: info_route(T:566)다. 핸들러 검사는 없고 누구나 부를 수 있다.

**짝 동작**: 가입 버튼은 `POST /team/member_add {team_id, member:{user_id: 본인, role:"user"}}`를 보낸다(AvailableTeamsPanel.tsx:52-55). 셀프 가입 허용은 H 3.6 에 있다(TE:3011).

**설정 소스**: `available_teams` 는 `DefaultInternalUserParams` 의 필드가 아니다(T:5133-5160). 따라서 4.2 의 PATCH 로는 넣을 수 없다. config 파일에서만 넣을 수 있다. 그 PATCH 가 기존 `available_teams` 값을 지우는지(`model_dump(exclude_none=True)`, PSE:1075)는 미확인이다. 확장 필드를 허용하는지 확인하지 못했다.

### 2.3 GET `/team/metadata_schema` (TE:5629-5644)

- 쿼리·본문 없음.
- 성공: 200 `{"fields": [{"key": "cost_center", "label": "Cost center"|null}, …]}` (TT:229-245). 원본은 `general_settings.team_metadata_schema` 다. 설정이 없으면 `{"fields": []}` (TE:5644).
- 대시보드는 `fields[].key`, `fields[].label` 을 읽는다. `fields` 가 배열이 아니면 `[]` 로 본다(D/hooks/teams/useTeamMetadataSchema.ts:19-22). 별도 클라이언트를 써서 오류를 조용히 넘기고, 재시도는 1회다(useTeamMetadataSchema.ts:14-17,35).
- 권한: info_route(T:567)다. 핸들러 검사는 없다.
- 오류: 핸들러 자체에는 오류 경로가 없다.

### 2.4 GET `/team/permissions_list` (TE:6653-6716)

**쿼리**: `team_id: str` (기본 None, TE:6660). 대시보드는 `?team_id=<id>` 를 문자열로 붙인다(NW:5509-5511). 인코딩은 하지 않는다.

**성공**: 200 `GetTeamMemberPermissionsResponse`(TT:39-55)
```json
{"team_id": "<id>",
 "team_member_permissions": ["/key/info", "/key/health"],
 "all_available_permissions": ["/key/generate", "/key/update", … ]}
```
- 팀 값이 null 이면 기본값 `["/key/info","/key/health"]`(TMP:18-23, :245-246)를 넣는다. DB 에는 쓰지 않는다(TE:6709-6710).
- `all_available_permissions` = `LiteLLMRoutes.key_management_routes` 전체(TMP:235-242, T:594-619). 값은 `/key/generate, /key/update, /key/delete, /key/info, /key/regenerate, /key/service-account/generate, /key/{key_id}/regenerate, /key/list, /key/block, /key/unblock, /key/bulk_update, /team/key/bulk_update, /team/daily/activity, /team/daily/activity/aggregated, /team/daily/activity/aggregated/keys, …/search, …/model_top_keys, /team/daily/activity/export, /spend/logs, /spend/logs/v2, /key/{key_id}/reset_spend, /key/aliases, /key/access_group_assignment, /auto_router/manage` 이다.
- 대시보드는 두 배열만 읽고 체크박스로 그린다(C/team/member_permissions.tsx:28-32).

**오류** ((B) 형태)
- 권한 없음: 403 `{"detail":{"error":"Call not allowed. User not proxy admin OR team admin. route=/team/member_add, team_id=<id>"}}`. 문구 안의 route 가 **`/team/member_add` 로 잘못 박혀 있다**(TE:6698-6707).
- 팀 없음: 404 `{"detail":{"error":"Team doesn't exist in db. Team=<id>. Create team via `/team/new` call."}}` (AC:3656-3660).

**권한**: 라우트 단계는 self_managed(T:849)다. 핸들러는 admin_view, `TEAM_OR_ORG_ADMIN`(AZ:14), **또는 `available_teams` 에 든 팀**(TE:948-953)을 허용한다(TE:6691-6697). 즉 가입 가능 팀이면 아무 사용자나 볼 수 있다.

### 2.5 POST `/team/permissions_update` (TE:6719-6777)

**본문**: `{"team_id": str, "team_member_permissions": [str]}` (TT:58-62). 대시보드는 체크된 목록 전체를 보낸다(NW:5536-5543). **값 검증이 없다.** 아무 문자열이나 그대로 저장된다(TT:62, TE:6767-6770).

**성공**: 200, 갱신된 **팀 행 전체**. 반환 타입 표기 `-> LiteLLM_TeamTable` 이 response_model 역할을 한다(TE:6728,6777). 대시보드는 응답을 읽지 않는다(member_permissions.tsx:58-60).

**오류** ((B) 형태)
- 권한 없음: 403 `"Call not allowed. User not proxy admin OR team admin. route=/team/permissions_update, team_id=<id>"` (TE:6756-6764). 가입 가능 팀 예외는 **쓰기에는 적용되지 않는다**(TE:6754-6755).
- 팀 없음: 404 (AC:3656-3660, 또는 TE:6771-6775).
- proxy_admin_viewer: 라우트 단계 차단(RC:44).

**권한**: 라우트 단계는 self_managed(T:850)다. 핸들러는 TEAM_OR_ORG_ADMIN 이다.

**부수 효과**: `team_member_permissions` 컬럼을 덮어쓴다. **`management_endpoint_wrapper` 가 없어서 팀 캐시를 지우지 않는다**(TE:6719-6723 데코레이터 비교, MH:806 `_delete_team_id_from_cache`). 바뀐 권한이 캐시 TTL 동안 반영되지 않을 수 있다. 다른 곳에서 캐시를 지우는지는 미확인이다.

### 2.6 GET `/team/{team_id}/members/me` (TE:5411-5522)

**경로**: `team_id`. 대시보드는 `encodeURIComponent` 로 인코딩한다(useMyTeamMember.ts:30-32).

**성공**: 200 `TeamMemberInfoResponse`(TT:221-226 = `LiteLLM_TeamMembership`(MTM:12-18) + `role`, `user_email`, `team_alias`)
```json
{"user_id": "<본인>", "team_id": "<id>", "team_alias": "...",
 "role": "admin"|"user", "user_email": "...",
 "budget_id": "..."|null, "spend": 0.0, "total_spend": 0.0,
 "litellm_budget_table": {budget_id, soft_budget, max_budget, max_parallel_requests, tpm_limit, rpm_limit,
                          model_max_budget, budget_duration, budget_reset_at, allowed_models}|null}
```
- 판정 기준은 `members_with_roles` 다. user_id 로 맞추고, 없으면 이메일로 맞춘다(TE:5464-5471).
- 멤버십 행이 없으면 `spend=0.0, total_spend=0.0, budget_id=null, litellm_budget_table=null` 을 돌려준다(TE:5497-5508).
- `user_email` 은 사용자 테이블에서 다시 읽는다(TE:5489-5495).
- 대시보드가 쓰는 필드는 useMyTeamMember.ts:5-26 의 인터페이스 전체다. `litellm_budget_table` 은 [차이:예산표]다. 우리 저장소에서 같은 모양을 만들어 내야 한다.

**오류** ((B) 형태)
- 키에 user_id 없음: 400 `{"detail":{"error":"API key has no associated user_id; cannot resolve 'me' for team membership."}}` (TE:5449-5454).
- 멤버가 아님: **404** `{"detail":{"error":"User user_id=<uid> is not a member of team_id=<id>."}}`. proxy admin 이어도 404 다(TE:5473-5480). **대시보드는 404 를 "멤버 아님"으로 받아 `null` 로 처리한다**(useMyTeamMember.ts:43-49). 다른 상태 코드를 쓰면 탭이 오류를 띄운다.
- 팀 없음: 404 (AC:3656-3660).

**권한**: 라우트 단계는 self_managed(T:858)다. 핸들러는 "본인이 roster 에 있는가" 하나만 본다.

### 2.7 POST `/team/{team_id}/member/{user_id}/reset_spend` (TE:4198-4268)

**본문**: `{"reset_to": float}` 필수(T:1255-1265). bool 을 보내면 422 다. 대시보드는 항상 `{"reset_to": 0}` 을 보낸다(useResetTeamMemberSpend.ts:10-13).

**성공**: 200 (response_model 없음)
```json
{"team_id": "...", "user_id": "...", "spend": 0.0, "previous_spend": 12.3, "max_budget": 50.0|null}
```
(TE:4262-4268.) 대시보드는 응답을 읽지 않는다.

**검증·오류** ((B) 형태 `{"detail":{"error":…}}`, TE:4165-4167)
- 팀 없음: 404 (AC:3656-3660).
- 권한 없음: **403 `{"detail":"You do not have access to this team"}` — detail 이 문자열**이다(AZ:60-61, TE:4233-4234).
- 자기 사용액 초기화(proxy_admin 이 아닐 때): 403 `"Cannot reset your own spend. Ask a proxy admin."` (TE:4152-4162).
- 멤버십 행 없음: 404 `"User <uid> is not a member of team <tid>."` (TE:4241-4243).
- `reset_to` 가 음수·무한대이거나 현재 사용액보다 크거나 멤버 예산보다 크면: 400 (TE:4170-4190).

**권한**: 라우트 단계는 self_managed(T:847)다. 핸들러는 TEAM_OR_ORG_ADMIN 이다. viewer 는 차단된다(RC:70 `_PROXY_ADMIN_VIEW_ONLY_BLOCKED_KEY_SUFFIXES` 에 `/reset_spend` 가 있다. 팀 경로에도 적용되는지는 미확인).

**부수 효과**: `LiteLLM_TeamMembership.spend = reset_to` 로 쓰고(TE:4251-4254), 여러 파드의 사용액 카운터와 멤버십 캐시를 무효화한다(TE:4256-4261).

### 2.8 POST `/team/{team_id}/member/{user_id}/reset_budget` (TE:4296-4357)

**본문**: 없음. 대시보드도 본문을 보내지 않는다(useResetTeamMemberBudget.ts:9-11).

**성공**: 200 `TeamMemberResetBudgetResponse`(T:4399-4404)
```json
{"team_id": "...", "user_id": "...", "budget_id": "<팀 기본 예산 id>"|null,
 "previous_budget_id": "..."|null, "budget_source": "team_default"|"none"}
```
- 팀 `metadata.team_member_budget_id` 의 예산 행이 있으면 거기에 연결하고, 없으면 연결을 끊는다(TE:4335-4344, :4276-4288).
- 사용액은 건드리지 않는다.
- `budget_source` 는 이 경로에서 `"custom"` 이 나올 수 없다(TE:4291-4294, :4356).

**오류**: 2.7 과 같다(403 문자열 detail, 404 멤버 아님. TE:4327-4333). 자기 자신 검사는 **없다**.

**권한**: self_managed(T:848) + TEAM_OR_ORG_ADMIN 이다.

[차이:예산표] litellm 은 멤버별 한도를 `LiteLLM_BudgetTable` 행 연결로 표현한다. 우리 쪽 저장 방식이 달라도 응답 필드(`budget_id`, `previous_budget_id`, `budget_source`)는 맞춰야 한다. 대시보드가 이 응답 필드를 쓰는 곳은 미확인이다. mutate 결과를 읽는 코드를 보지 못했다.

### 2.9 POST `/management/v1/teams/{team_id}/members/bulk_update` (MV1T:100-176)

새 `management/v1` 계열이다. **오류가 RFC 9457 problem 문서**라서 H 의 (A)(B)와 다르다.

**본문**: `{"members": [TeamMemberBudgetPatch…]}`. 개수는 1..500 이다(TT:192-196, TT:17). 알 수 없는 필드는 422(`extra="forbid"`, TT:194).
- `TeamMemberBudgetPatch`(TT:172-189): `user_id` **또는** `user_email` 중 정확히 하나(TT:139-148). 그리고 `max_budget_in_team, tpm_limit, rpm_limit, budget_duration, allowed_models`.
- **merge-patch 의미**다. 빠진 필드는 그대로 둔다. `null` 은 지운다. 마지막 한도까지 지우면 팀 기본값으로 돌아간다(TT:173-176).
- 알 수 없는 **쿼리** 매개변수도 거부한다. 400 problem, `type=urn:litellm:error:unknown-query-parameter`(LAC:87-107).
- 대시보드는 `{"members":[{"user_id":…, "max_budget_in_team": null}, …]}` 만 보낸다(TeamInfo.tsx:961-964). 팀 기본 멤버 예산을 바꾼 뒤 "개별 예산이 있는 멤버를 기본값으로 되돌릴까" 프롬프트에서 부른다(C/team/memberBudgetReset.ts, useMemberBudgetReset.ts).

**성공**: 200 `{"data": [TeamMemberBudgetUpdateResult…]}` (MV1:70-73, TT:200-218). 순서는 요청 순서와 같다.
```json
{"data": [{"user_id": "...", "user_email": null, "success": true, "error": null,
           "budget_id": "...", "max_budget": 10.0, "max_budget_source": "member"|"team_default"|null,
           "tpm_limit": null, "rpm_limit": null, "budget_duration": null, "allowed_models": null}]}
```
- roster 에 없는 멤버나 중복 행은 `success:false` 와 `error` 를 붙인 행이 된다(BTMB:190-200, MV1T:126-128).
- 대시보드는 `data[].success` 만 센다(useMemberBudgetReset.ts:72-80).

**오류** (problem 문서. `Content-Type: application/problem+json`, 본문 `{type,title,status,detail[,allowed]}`, LAC:19,34-39, MV1:13-20)
- 팀 없음: 404 `type=urn:litellm:error:team-not-found`, `detail="Team id=<id> does not exist in db"` (BUD:136-144, BTMB:180-181).
- 권한 없음: 403 `type=urn:litellm:error:forbidden`, `detail="Call not allowed. User not proxy admin OR team admin OR org admin for this team. route='/management/v1/teams/<id>/members/bulk_update'"` (BTMB:184-188, BUD:147-150).
- 그 밖의 예외: 500 `internal-server-error`, `"Failed to update team member budgets."`. DB 없음은 503 이다(MV1T:142-176).
- 대시보드 클라이언트는 `[/+]json` 을 JSON 으로 받아 들인다(lib/http/api.ts:16). 메시지는 `detail` 문자열에서 꺼낸다(CL:71-80).
- 본문 검증 실패(422) 본문이 problem 인지 FastAPI 기본 `{"detail":[…]}` 인지는 미확인이다.

**권한**: 라우트 단계는 self_managed(T:845)다. viewer 차단(RC:39). 핸들러는 TEAM_OR_ORG_ADMIN 이다.

**부수 효과**: 한 트랜잭션 안에서 멤버별 예산을 갱신한다(BTMB:178). 여러 멤버십이 같이 쓰는 예산 행은 복사해서 쓴다(MV1T:121-124). [차이:예산표]

### 2.10 POST `/team/bulk_member_add` (TE:4399-4542)

**본문**: `BulkTeamMemberAddRequest`(TT:108-114)
```json
{"team_id": "...", "members": [Member…]|null, "all_users": false, "max_budget_in_team": 10.0|null}
```
- 대시보드(NW:2948-2960)는 두 모드로 보낸다. "모든 사용자" 모드는 `{team_id, all_users:true[, max_budget_in_team]}` 이다. 선택 모드는 `{team_id, members:[{user_id, role:"user", user_email|null}], [max_budget_in_team]}` 이다(BulkEditUsers.tsx:151-169).

**성공**: **항상 200** `BulkTeamMemberAddResponse`(TT:128-135)
```json
{"team_id": "...",
 "results": [{"user_id","user_email","success":true,"error":null,"updated_user":{…}|null,"updated_team_membership":{…}|null}],
 "total_requested": 2, "successful_additions": 2, "failed_additions": 0,
 "updated_team": { TeamAddMemberResponse.model_dump() }|null}
```
- 내부에서 `team_member_add` 를 **한 번에** 부른다(TE:4498-4505). 성공하면 모두 성공으로 표시한다(TE:4508-4519).
- **`team_member_add` 가 어떤 예외를 던져도(권한 403, 중복, 팀 없음 포함) HTTP 200 으로 돌려준다.** 이때 모든 멤버가 `success:false, error:str(e)` 가 되고 `updated_team: null` 이다(TE:4521-4542). 일부만 추가된 상태가 남는지는 미확인이다. `team_member_add` 가 원자적인지 확인하지 못했다.
- `updated_team` 은 `TeamAddMemberResponse`(T:4170) 전체다. 팀 행과 `updated_users`·`updated_team_memberships` 가 들어 있다. 팀 행에 키가 들어 있지는 않다. 사용자 행 덤프에 민감 필드가 섞이는지는 미확인이다.
- 대시보드는 `successful_additions`, `failed_additions` 만 읽는다(BulkEditUsers.tsx:171-176).

**오류** ((B) 형태. 핸들러 진입 전 검사만)
- `all_users=true` 인데 proxy_admin 이 아님: 403 `"`all_users=true` is restricted to PROXY_ADMIN. Org/team admins must specify explicit member lists."` (TE:4458-4472).
- 멤버 없음: 400 `"At least one member is required"` (TE:4484-4488).
- 500명 초과: 400 `"Maximum 500 members can be added at once"` (TE:4491-4496).

**권한**: **이 라우트는 어느 라우트 목록에도 없다**(`_types.py`·`route_checks.py` 를 grep 해 확인). 그래서 라우트 단계에서 proxy_admin 만 통과한다. 그 밖에는 401 (D) 다(RC:405-411). viewer 의 목록 밖 POST 는 기본 거부다(H 2.4). 핸들러 주석은 "org/team admin" 을 말하지만(TE:4462-4464) 실제로는 닿을 수 없다.

---

## 3. 사용자 엔드포인트 (H 에 없는 것)

### 3.1 GET `/v2/user/info` (IU:1062-1167)

사용자 상세 화면의 본체다. `/user/info`(H 4.4)와 달리 **키·팀 객체를 싣지 않는다.**

**쿼리**: `user_id: str|null` (IU:1071). 대시보드는 상세에서 `user_id` 를 보내고(user_info_view.tsx:130), 본인 조회(`useCurrentUser`)에서는 보내지 않는다(D/hooks/users/useCurrentUser.ts:13; NW:1175-1178 `user_id || undefined`).
- 값에 공백이 있으면 원본 쿼리에서 `+` 를 살려 다시 읽는다(IU:1110-1111).
- 없으면 호출자 본인 id 를 쓴다(IU:1114-1115).

**성공**: 200 `UserInfoV2Response`(T:3172-3198) — **평면 객체**
```json
{"user_id": "...", "user_email": null, "user_alias": null, "user_role": "internal_user",
 "spend": 0.0, "max_budget": null, "tpm_limit": null, "rpm_limit": null,
 "models": [], "budget_duration": null, "budget_reset_at": null,
 "metadata": {...}|null, "created_at": "...", "updated_at": "...", "sso_user_id": null,
 "teams": ["team-id-1", ...],                 // id 문자열 배열
 "object_permission": {...}|null,             // include 됨 (IU:1028-1030)
 "model_max_budget": {...}|null,
 "model_max_budget_usage": {...}|null}
```
(IU:1139-1165.)
- `models`·`teams` 가 null 이면 `[]` 로 바꾼다(IU:1147,1155).
- `metadata` 에서 SCIM 디렉터리 키를 뺀다(IU:879-888, :1151). [차이:SCIM] 우리는 metadata 를 그대로 내면 된다.
- `model_max_budget_usage` 는 캐시에서 계산한다(IU:1158-1163). 유료 여부는 미확인이다.
- `spend` 는 예산 리셋 때 0 으로 돌아가는 카운터다. 누적 사용액이 아니다(IU:1084-1090).

**대시보드가 읽는 필드**
- 상세: `user_id, user_email, user_alias, user_role, spend, max_budget, tpm_limit, rpm_limit, models, budget_duration, metadata, created_at, updated_at, object_permission, model_max_budget, model_max_budget_usage, teams` (user_info_view.tsx 전체 grep. `teams` 는 :135-152 에서 팀마다 `/team/info` 를 불러 별칭을 얻는다).
- ModelSelect: `models`(ModelSelect.tsx:180).

**오류** ((A) 형태, `handle_exception_on_proxy`, IU:1166-1167. `type="internal_server_error"`, `code` 는 문자열 — H 2.3)
- user_id 를 정할 수 없음(키에 user_id 없음): 400 `"user_id is required. Either pass it as a query parameter or authenticate with a user-bound key."` (IU:1117-1121).
- **사용자 없음과 권한 없음을 구분하지 않는다.** 둘 다 404 `"User not found: <id>"` 다(IU:1127-1135, 접근 검사가 None 을 돌려줌 IU:1058). `/user/info` 의 403 과 다르다.

**권한**
- 라우트 단계: info_route 이고, 핸들러에 맡긴다(RC:348-350).
- 핸들러 `_check_user_info_v2_access`(IU:1003-1058):
  1. proxy_admin·proxy_admin_viewer: 누구든(IU:1032-1034).
  2. 본인(IU:1036-1038).
  3. 팀 admin: 호출자 `teams` 배열의 팀 가운데 호출자가 admin(`is_team_admin`, AZ:53-56)인 팀에 대상이 `teams` 배열로 속해 있으면 허용(IU:1040-1056).
  - 그 밖에는 None → 404.
- **팀 admin 허용**이 `/user/info` 와의 차이다. 거기서는 본인·admin 계열만 허용한다(H 4.4).

### 3.2 GET `/user/filter/ui` (IU:2710-2813)

**쿼리**(IU:2719-2733)

| 파라미터 | 기본 | 의미 | 대시보드 |
|---|---|---|---|
| user_id | null | 부분 일치, 대소문자 무시(IU:2771-2775) | 검색 필드가 user_id 일 때(user_search_modal.tsx:89-90) |
| user_email | null | 부분 일치, 대소문자 무시(IU:2777-2781) | 검색 필드가 user_email 일 때 |
| search | null | user_id 또는 user_email 부분 일치(IU:2197-2208, :2788-2792) | 함수는 지원하지만(NW:1937) 모달은 보내지 않음 |
| team_id | null | `scope_user_search_to_org` 가 켜졌을 때만 쓰임 | 멤버 추가 모달이 팀 id 를 붙임(user_search_modal.tsx:91-93) |
| page | 1 | ≥1 | 보내지 않음 |
| page_size | **50** | 1..100 | 보내지 않음 |

**성공**: 200, **맨 배열** `[{"user_id": "...", "user_email": "..."|null}]` (T:3389-3391, IU:2801-2804).
- `created_at desc` 로 정렬한다(IU:2798).
- 다른 필드는 없다. 민감 정보 노출을 막으려고 모델을 줄였다(T:3389 주석).
- 대시보드는 `user_id`, `user_email` 을 옵션 라벨로 쓴다(user_search_modal.tsx:101-105).
- 검색어가 비어 있으면 대시보드는 아예 호출하지 않는다(user_search_modal.tsx:82-86).

**오류**
- `scope_user_search_to_org`(UI 설정) 이 켜져 있고 호출자가 admin_view 도, 조직 멤버도, 그 팀의 admin 도 아니면 403 `{"detail":{"error":"scope_user_search_to_org is enabled. …"}}` (IU:2606-2700). 설정이 꺼져 있으면(기본) 검사하지 않는다(IU:2624-2625). [차이:조직] 조직이 없으니 이 설정은 사실상 "켜면 admin 계열만 검색 가능, 또는 팀 admin 이 팀 id 를 줄 때 팀 조직이 없어 403(IU:2700-2707)" 이 된다. 이 설정을 아예 지원하지 않아도 된다.
- 예상 밖 예외: 500 `{"detail":"Error searching users: <e>"}` — **detail 이 문자열**이다(IU:2813). DB 장애는 503 ProxyException 이다(IU:2809-2811).

**권한**: info_route(T:579)다. 설정이 꺼져 있으면 **인증된 누구나 전체 사용자의 id·이메일을 검색할 수 있다**(IU:2741-2742 주석).

### 3.3 GET `/user/available_roles` (IU:670-702)

- 쿼리·본문 없음.
- 성공: 200, 역할 값을 키로 하는 객체(IU:689-702)
```json
{"proxy_admin":          {"description": "admin over litellm proxy, has all permissions", "ui_label": "Admin (All Permissions)"},
 "proxy_admin_viewer":   {"description": "view all keys, view all spend",                 "ui_label": "Admin (View Only)"},
 "internal_user":        {"description": "view/create/delete their own keys, view their own spend", "ui_label": "Internal User (Create/Delete/View)"},
 "internal_user_viewer": {"description": "view their own keys, view their own spend",     "ui_label": "Internal User (View Only)"}}
```
(문구는 ROLES:50-77.) 키 순서는 enum 선언 순서를 따른다(IU:690). 대시보드는 `possibleUIRoles[user_role].ui_label` 로 표의 역할 열을 그린다(D/users/_components/view_users/UsersTableColumns.tsx:160). 실패하면 `{}` 로 둔다(view_users.tsx:266 `initialData`).
- 권한: self_managed(T:886)와 admin_viewer(T:966)다. 인증된 누구나 부를 수 있다.
- 오류: 핸들러에는 오류 경로가 없다.

### 3.4 POST `/user/bulk_update` (IU:1827-2056)

**본문**: `BulkUpdateUserRequest`(UL:44-74)
```json
{"users": [UpdateUserRequest…]|null, "all_users": false, "user_updates": UpdateUserRequestNoUserIDorEmail|null}
```
- `UpdateUserRequest` 는 UR:67, `…NoUserIDorEmail` 은 UR:49 다. 필드는 `/user/update` 와 같다(H 4.2).
- 대시보드(NW:3191-3222)는 두 모드로 보낸다. 선택 모드는 `{users:[{user_id, ...공통 변경값}]}` 이다. 전체 모드는 `{all_users:true, user_updates:{...}}` 이다. 변경값은 바뀐 것만 담는다: `user_role, max_budget, models, budget_duration, metadata` (BulkEditUsers.tsx:100-120).

**성공**: 200 `BulkUpdateUserResponse`(UL:76-92)
```json
{"results": [{"user_id","user_email","success":true,"error":null,"updated_user":{…}|null}],
 "total_requested": 3, "successful_updates": 3, "failed_updates": 0}
```
- **선택 모드**는 사용자마다 따로 처리한다. 개별 실패는 `success:false, error:str(e)` 로 남기고 전체는 200 이다(IU:1775-1820).
- **전체 모드**는 `update_many(where={})` 한 번으로 모든 사용자를 갱신한다(IU:1965-1971). `updated_user` 에는 `{"user_id", ...변경값}` 만 담는다(IU:1983-1991). 감사 로그는 1건만 남긴다(IU:1994-2007). 일괄 갱신이 실패하면 사용자별 갱신으로 넘어간다(IU:2009-2015).
- 대시보드는 `total_requested` 를 메시지에 쓴다(BulkEditUsers.tsx:137-138).

**오류**
- `user_role` 을 바꾸는데 proxy_admin 이 아님: 403 `{"detail":"Only proxy admins can modify user roles."}` — **문자열 detail**(IU:1902-1910).
- 전체 모드인데 proxy_admin 이 아님: 403 문자열 `"Only proxy admins can update all users at once."` (IU:1916-1921).
- 전체 모드에서 password 지정: 400 `{"detail":{"error":"Setting one password for all users is not supported. …"}}` (IU:1922-1929).
- 사용자 0명: 400 `"No users found to update"` (IU:1933-1937, 2036-2040).
- 500명 초과: 400 (IU:1941-1947, 2042-2050).
- 두 모드 모두 아님: 400 `"Must specify either 'users' … or 'all_users=True' with 'user_updates' …"` (IU:2028-2034).
- 내부 루프 밖 예외: 500 `{"detail":{"error":…}}` (IU:1822-1824).

**권한**: 라우트 단계는 management_routes(T:632)다. proxy_admin 과 org admin 만 통과한다(T:1042). internal_user 는 401 (D) 다. viewer 는 차단된다(RC:35). 사용자별 권한은 `_update_single_user_helper` 가 판정한다(IU:1780-1785, 내부는 H 4.2 와 같은 경로로 추정. 미확인).

### 3.5 POST `/invitation/new` (PS:19158-19231)

**본문**: `{"user_id": str}` (SA:364-365). 대시보드는 `{user_id}` 만 보낸다(NW:834-842).

**성공**: 200 `InvitationModel`(SA:368-377)
```json
{"id": "<uuid>", "user_id": "...", "is_accepted": false, "accepted_at": null,
 "expires_at": "<now+7일>", "created_at": "...", "created_by": "<호출자 id 또는 proxy admin 이름>",
 "updated_at": "...", "updated_by": "..."}
```
- 만료는 7일이다(UINV:26-27). 행은 `LiteLLM_InvitationLink`(SP:833-847)에 저장한다.
- 대시보드 타입에는 `has_user_setup_sso` 가 더 있지만(C/onboarding_link.tsx:9-20) 서버 response_model 에는 없다. 대시보드는 없으면 false 로 본다(onboarding_link.tsx:70). 생성 직후 경로에서는 직접 false 로 넣는다(CreateUserButton.tsx:222).
- 대시보드는 `id`(링크 토큰)와 `user_id` 를 쓴다(onboarding_link.tsx:69,92).

**오류** ((A) 형태, `handle_exception_on_proxy`, PS:19230-19231. HTTPException 상태 코드를 그대로 쓴다)
- 권한 없음: **400**(403 아님) `"{'error': 'Call not allowed…, your role=<role>'}"` (PS:19202-19208).
- 범위 밖 사용자 초대: 400 `"You can only create invitations for users in your organization or team."` (PS:19211-19222).
- 사용자 없음(FK 실패): 400 `"User id does not exist in 'LiteLLM_UserTable'. …"` (UINV:41-48).

**권한**: 라우트 단계는 self_managed(T:909)다. 핸들러는 proxy_admin 이거나, 어느 팀이든 팀 admin 이거나, org admin 이어야 한다(CU:318-360). 그리고 대상이 그 범위 안에 있어야 한다(CU:456-).

**화면 연동**: 사용자를 만든 직후 `/sso/get/ui_settings` 의 `SSO_ENABLED` 가 false 이면 대시보드가 **자동으로** 이 엔드포인트를 부른다(CreateUserButton.tsx:220-227). true 이면 부르지 않고 SSO 안내를 띄운다(:228-230). B_auth.md·C_management.md 는 초대 링크를 "제외"로 분류했다. 그러면 4.4 에서 `SSO_ENABLED: true` 를 돌려줘야 생성 흐름에서 이 호출이 생기지 않는다. 사용자 목록·상세의 "비밀번호 재설정" 버튼(view_users.tsx:165, user_info_view.tsx:289)은 무조건 이 엔드포인트를 부른다.

---

## 4. 설정·공통 엔드포인트

### 4.1 GET `/get/default_team_settings` · PATCH `/update/default_team_settings` (PSE:916-935, 1146-1166)

**GET 성공**: 200 `DefaultTeamSettingsResponse`(PSE:203-210, 229-230)
```json
{"values": {"models": [], "max_budget": null, "budget_duration": null, "tpm_limit": null,
            "rpm_limit": null, "team_member_permissions": null, "organization_id": null},
 "field_schema": {"description": "...", "properties": {<필드>: {...}}, <중첩 def>: {...}}}
```
- 값은 `litellm_settings.default_team_params` 를 `DefaultTeamSSOParams`(UIS:203-)로 채운 것이다(PSE:860-865).
- `field_schema` 는 pydantic 스키마에서 만든다(PSE:867-890).
- 대시보드는 `values` 만 읽는다(C/TeamSSOSettings.tsx:132; C/Teams.tsx:296 `values.budget_duration`).

**PATCH 본문**: `DefaultTeamSSOParams` 전체(UIS:203-). 대시보드는 편집한 값 전체를 보낸다(TeamSSOSettings.tsx:152).
- `organization_id` 가 있으면 존재를 확인한다(PSE:1160-1161). [차이:조직]

**PATCH 성공**: 200 `{"message": "Default team settings updated successfully", "status": "success", "settings": <exclude_none 값>}` (PSE:1109-1113). 대시보드는 `settings` 를 다시 읽는다(TeamSSOSettings.tsx:153).

**PATCH 오류**: `STORE_MODEL_IN_DB` 가 꺼져 있으면 500 `{"detail":{"error":"Set `'STORE_MODEL_IN_DB='True'` in your env to enable this feature."}}` (PSE:1069-1073). 검증 실패는 422 다.

**부수 효과**: 메모리의 `litellm.default_team_params` 와 config(DB)를 모두 덮어쓴다(PSE:1079-1093). 감사 로그는 비동기로 남긴다(PSE:1099-1107).

**권한**: 두 라우트 모두 어떤 라우트 목록에도 없다(grep 으로 확인). 그래서 proxy_admin 만 통과하고, viewer 는 GET 만 된다(H 2.4). 대시보드도 이 탭을 proxy admin 에게만 보인다(C/Teams.tsx:659). 다만 팀 생성 모달의 GET(C/Teams.tsx:288-294)은 모든 역할이 부른다. 비관리자는 401 을 받는다. `retry:false` 라 placeholder 가 `"n/a"` 로 남는다(C/Teams.tsx:296-299).

### 4.2 GET `/get/internal_user_settings` · PATCH `/update/internal_user_settings` (PSE:893-913, 1116-1144)

- GET 응답 모양은 4.1 과 같다(`values`, `field_schema`). 값의 원본은 `litellm_settings.default_internal_user_params` 이고 모델은 `DefaultInternalUserParams` 다(T:5133-5160: `user_role, max_budget, budget_duration, models, teams`).
- `teams` 는 문자열 배열이거나 `NewUserRequestTeam` 배열이다. 대시보드는 `{team_id, max_budget_in_team, user_role:"user"|"admin"}` 객체로 다룬다(D/users/_components/default-user-settings/mapper.ts:18-30, 68-79).
- 대시보드는 `values` 만 읽는다(DefaultUserSettingsForm.tsx:395).
- PATCH 본문은 `DefaultInternalUserParams` 전체다(mapper.ts:74-80).
  - `teams` 가 있으면 팀 존재를 확인한다(PSE:1129-1130).
  - 객체 배열이면 `update_default_team_member_budget` 로 팀 기본 멤버 예산까지 만든다(PSE:1132-1136). [차이:예산표]
- PATCH 성공·오류·부수 효과·권한은 4.1 과 같다(PSE:1138-1143). 메시지는 `"Internal user settings updated successfully"` 다.
- 사용자 화면은 proxy admin 에게만 이 폼을 보인다(view_users.tsx:366,396).

### 4.3 GET `/models` (PS:12210-12466) — 드롭다운 공급원

추론 쪽 계약은 A_inference.md:12 에 있다. 여기서는 팀·사용자 화면이 보내는 쿼리와 읽는 필드만 적는다.
- **대시보드는 `include_model_access_groups=True` 를 항상 보낸다**(NW:1855). 함수 인자 `include_model_access_groups` 는 무시된다. 나머지:
  - `return_wildcard_routes=True`: 팀 생성 모달(fetch_available_models_team_key.tsx:14), ModelSelect(useModels.ts:257).
  - `scope=expand`: ModelSelect 만(useModels.ts:257).
  - `only_model_access_groups=True`: 접근 그룹 이름 수집(useModels.ts:279-287).
  - `team_id`: `useSelectedTeamModels`(useModels.ts:303).
- 요청은 `gatewayApiClient` 로 보낸다(NW:1853, 536-540). 관리 API 와 기본 URL 이 다를 수 있다(`getGatewayBaseUrl`).
- `scope` 가 `expand` 가 아니면 400 `"Invalid scope parameter. Only 'expand' is currently supported. Received: <v>"` 다(PS:12291-12295). `expand` 는 admin_view 또는 팀/org admin 일 때만 전체 모델로 넓힌다(PS:12298-12304). 그 밖에는 조용히 일반 동작을 한다.
- 성공 응답은 `{"data": [{"id": "...", "object": "model", "created": …, "owned_by": "openai"}], "object": "list"}` 이다(PS:12397-12400, 12463-12466). **대시보드는 `data[].id` 만 읽는다**(fetch_available_models_team_key.tsx:16, view_users.tsx:98, user_info_view.tsx:157, CreateUserButton.tsx:179-183).
- id 의 뜻(대시보드가 해석함):
  - `xxx/*` 와일드카드는 앞쪽으로 모은다(fetch_available_models_team_key.tsx:22-32).
  - 특수값 `all-proxy-models` 는 걸러낸다(fetch_available_models_team_key.tsx:40-41).
  - 접근 그룹 이름도 같은 목록에 섞여 나온다.
- 모델 필터(키·팀·사용자 권한)의 상세(`get_available_models_for_user`)는 이 조사에서 읽지 않았다. 미확인이다.

### 4.4 GET `/sso/get/ui_settings` (SSO:3015-3044)

- 성공: 200 (모델 없음)
```json
{"PROXY_BASE_URL": null, "PROXY_LOGOUT_URL": null, "LITELLM_UI_API_DOC_BASE_URL": null,
 "DEFAULT_TEAM_DISABLED": false, "SSO_ENABLED": true|false,
 "NUM_SPEND_LOGS_ROWS": 0, "DISABLE_EXPENSIVE_DB_QUERIES": false}
```
- 사용자 화면은 `SSO_ENABLED` 만 본다(CreateUserButton.tsx:220). 이 값이 초대 링크 자동 생성 여부를 정한다(3.5 참고).
- 권한: info_route(T:582)다. 핸들러 검사는 없다.

### 4.5 GET `/get/ui_settings` (PSE:1803-)

- 라우트에 `Depends(user_api_key_auth)` 가 **없다**(PSE:1803-1807). 라우터 공통 의존성도 없다(PSE:63 `APIRouter()`). 미들웨어 단계에서 인증하는지는 미확인이다.
- 응답: `UISettingsResponse` = `{"values": {...}, "field_schema": {...}, "source": {<필드>: "db"|"env"|…}}` (PSE:381-384). `values` 키는 허용 목록 `ALLOWED_UI_SETTINGS_FIELDS`(PSE:388-) 안에서만 나온다.
- 팀 화면은 `values.disable_team_admin_delete_team_user` 만 읽는다(C/team/TeamMemberTab.tsx:138-140). 이 값이 참이면 팀 admin 에게 멤버 삭제 버튼을 숨긴다.
- 부수 효과: UI 설정 캐시를 새로 쓴다(PSE:1834-1837). 이 캐시는 3.2 의 `scope_user_search_to_org` 판정이 읽는다.

### 4.6 GET `/management/v1/users/me` (MUE:14-20)

- 레이아웃 공통 호출이다(D/layout.tsx). 팀·사용자 화면 전용은 아니다.
- 성공: 200 `{"user_id": "...", "user_role": "proxy_admin"|…|null, "user_email": "..."|null}` (`CurrentUserResponse`, management/users/schemas.py). 값은 DB 가 아니라 **인증 결과(UserAPIKeyAuth)에서** 바로 꺼낸다(MUE:18-20).
- 키에 user_id 가 없으면 404 problem 문서를 돌려준다(MUX). 모양은 `{"type":"urn:litellm:error:user-not-found","title":"User not found","status":404,"detail":"The key used for this request is not bound to a user."}` 이다.
- 권한: info_route(T:570)다. 비밀번호 재설정 대기 상태에서도 허용된다(`gateway/litellm/proxy/auth/login_utils.py:60`).

### 4.7 GET `/organization/list`, GET `/organization/info` — [차이:조직]

- **`useOrganizations`·`useOrganization` 은 JWT 의 `premium_user` 가 true 일 때만 실행된다**(useOrganizations.ts:26, :35; `premiumUser` 는 D/hooks/useAuthorized.ts:49 `decoded.premium_user`). 우리 UI 토큰에 `premium_user:false` 를 넣으면 팀·사용자 화면은 `/organization/list` 를 부르지 않는다. 조직 드롭다운은 비고 조직 열은 `organization_id` 를 그대로 보여 준다(teamTableColumns.tsx:175-190).
- 예외: C/team/TeamInfo.tsx:762 의 `organizationInfoCall` 은 premium 검사 없이 **팀에 `organization_id` 가 있을 때만** 부른다. 우리 팀은 항상 `organization_id: null` 이므로 호출되지 않는다.
- 응답 모양(참고용): `/organization/list` 는 맨 배열 `list[LiteLLM_OrganizationTableWithMembers]` 다(`gateway/litellm/proxy/management_endpoints/organization_endpoints.py:1099-1104`). premium 을 끌 것이면 구현하지 않아도 된다.

---

## 5. 정리: 어긋나기 쉬운 지점

1. **오류 모양이 엔드포인트마다 다르다.**
   - (A) ProxyException: `/v2/user/info`, `/invitation/new`
   - (B) `{"detail":{"error":…}}`: `/v2/team/list`, `/team/available`, `/team/permissions_*`, `/team/{id}/members/me`, `/team/bulk_member_add`, 설정 PATCH
   - **문자열 detail**: reset_* 의 403(`"You do not have access to this team"`), `/user/bulk_update` 의 403 두 가지, `/user/filter/ui` 의 500
   - **RFC 9457 problem**: `/management/v1/*`
2. **멤버십 기준이 세 가지다.**
   - `members_with_roles[].user_id`: v1 `/team/list`, `/team/{id}/members/me`, 팀 admin 판정
   - 사용자 행 `teams` 배열: `/v2/team/list`, `/v2/user/info` 팀 admin 규칙, `/team/available`
   - 레거시 `members` 배열: `status=deleted`
   - 우리 쪽이 하나의 저장소에서 이 값들을 내면 어긋날 일이 없다. 다만 응답 필드(`members_with_roles`, `teams`)는 모두 채워야 한다.
3. **200 인데 실패인 경우**: `/team/bulk_member_add`(전부 실패해도 200), `/user/bulk_update` 선택 모드, `/management/v1/.../bulk_update` 의 행별 `success:false`.
4. **404 의 뜻**: `/v2/user/info` 는 권한 없음도 404 로 숨긴다. `/team/{id}/members/me` 의 404 는 대시보드가 "멤버 아님"으로 해석한다.
5. **조용히 무시되는 입력**: `/v2/team/list` 의 잘못된 `sort_by`·`sort_order`. 반대로 `/management/v1/*` 는 모르는 쿼리를 400 으로 거부한다.
6. **비관리자의 `/v2/team/list` 는 `user_id=본인` 필수**다. 없으면 401 이다(자동 주입 코드는 실행되지 않는다).

## 6. 미확인 항목

- `/team/available` 의 `response_model` 매개변수가 쿼리로 노출되는지, 키에 user_id 가 없을 때 동작(TE:5648-5674).
- `/update/internal_user_settings` 가 config 의 `available_teams` 를 지우는지(PSE:1075, T:5133-5160).
- `/team/bulk_member_add` 가 실패했을 때 일부 멤버가 이미 추가된 채 남는지(`team_member_add` 의 원자성).
- `/team/permissions_update` 뒤 팀 캐시가 어떻게 갱신되는지.
- `/management/v1/*` 본문 검증 실패(422)의 본문 모양.
- `/get/ui_settings` 에 인증이 걸리는지(미들웨어).
- `/models` 의 사용자·팀별 모델 필터 세부(`get_available_models_for_user`).
- `_PROXY_ADMIN_VIEW_ONLY_BLOCKED_KEY_SUFFIXES`(`/reset_spend`)가 팀 멤버 reset_spend 경로에도 적용되는지(RC:70).
- reset_budget 응답 필드를 대시보드가 쓰는지.
- `/user/bulk_update` 사용자별 권한 판정이 `/user/update` 와 똑같은지(`_update_single_user_helper` 내부).
- 1.3 의 라우터 설정 하위 컴포넌트(자동 라우터 설정)가 팀 화면에서 실제로 렌더되는 조건.
