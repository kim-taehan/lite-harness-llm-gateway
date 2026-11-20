# C. 관리(control plane) API 전수조사

조사 대상: `litellm/proxy/management_endpoints/` 전체, `management_v1`, `credential_endpoints`, `health_endpoints`, `ui_crud_endpoints`, `proxy_server.py` 의 관리·설정·로그인·모델정보 라우트, `enterprise/litellm_enterprise` 의 관리 라우트, `config.yaml` 로딩과 DB 설정 동기화, UI 가 부르는 API. 읽기 전용 조사이며 코드는 전부 직접 확인했다(참고 문서는 대조용).

## 0. 요약

- **관리 계열 엔드포인트 514행**(메서드·경로 한 행씩). 이 가운데 라우터에 실제로 연결된 것은 490행이고, 24행(ROI 계산기 23 + 빈 스텁 1)은 어디에도 `include_router` 되지 않은 죽은 코드다.
- 신규 게이트웨이 권장: **필수 41 / 후순위 150 / 제외 323**. 필수는 키·팀·사용자 CRUD, 모델 등록·조회, 프로브, Keycloak 로그인·로그아웃, 사용자·팀 사용량 조회뿐이다.
- 이 저장소는 이미 `backend/`(관리면)와 `gateway/`(추론면)로 쪼개고 `backend/routes/allowlist.py` 로 노출 경로를 줄여 두었다. 514행 중 73행은 그 allowlist 밖이라 지금 분리 배포에서도 서빙되지 않는다.
- 공식 대시보드(`ui/litellm-dashboard`)는 이 가운데 약 269행(추정)을 부른다. 새 게이트웨이가 `/api/v1/*` 자체 spec(`GATEWAY_DESIGN.md` 7장)으로 가면 공식 대시보드를 그대로 쓸 수 없다.

표시 규칙
- 권장: 필수 / 후순위 / 제외. 근거는 폐쇄망 우선, 운영자가 키·팀·예산·사용량을 직접 다루는 범위 우선(`product.md`), 그리고 `GATEWAY_DESIGN.md` 의 7개 테이블 설계.
- DB 테이블: 파일 안의 prisma 호출을 직접 확인한 것과, 헬퍼·스키마 명칭으로 추정한 것이 섞여 있다. 군 머리말의 "주요 테이블"은 추정 포함이다. 정확한 컬럼 수준은 확인 못함.
- 유료: `premium_user`·라이선스 검사 지점을 직접 grep 으로 확인한 것만 적었다. "일부 유료"는 엔드포인트는 열려 있고 특정 필드·값에서만 막힌다는 뜻.
- UI: `ui/litellm-dashboard/src` 의 호출 문자열을 정규식으로 뽑아 대조한 추정값이다(`${base}/path` 형태와 `fetchClient`·`apiClient` 호출). 동적으로 조립되는 경로는 놓쳤을 수 있다.
- 분리배포: `backend/routes/allowlist.py` 기준 노출 여부.

### 군별 합계

| 기능군 | 엔드포인트 수 | 필수 | 후순위 | 제외 | UI 호출 추정 |
|---|---:|---:|---:|---:|---:|
| 키(Virtual Key) | 16 | 8 | 7 | 1 | 11 |
| 팀 관리 | 30 | 10 | 15 | 5 | 16 |
| 사용자 관리 | 13 | 6 | 7 | 0 | 10 |
| 조직(organization) | 11 | 0 | 11 | 0 | 9 |
| 고객(end_user) | 14 | 0 | 9 | 5 | 0 |
| 예산(budget) | 7 | 0 | 6 | 1 | 5 |
| 태그·프로젝트 | 10 | 0 | 0 | 10 | 9 |
| 모델 관리·조회 | 26 | 7 | 7 | 12 | 9 |
| 접근 그룹(access group) | 18 | 0 | 5 | 13 | 6 |
| 자격증명(credentials) | 13 | 0 | 6 | 7 | 5 |
| 오토라우터 | 15 | 0 | 0 | 15 | 9 |
| 폴백·라우터·캐시 설정 | 15 | 0 | 5 | 10 | 9 |
| 설정·DB 동기화·UI 설정 | 62 | 0 | 15 | 47 | 46 |
| 헬스·시스템·디버그 | 30 | 3 | 7 | 20 | 5 |
| 인증·SSO·온보딩 | 31 | 3 | 6 | 22 | 8 |
| SCIM 프로비저닝 | 21 | 0 | 0 | 21 | 2 |
| MCP 서버 관리 | 42 | 0 | 0 | 42 | 35 |
| 정책·툴·컴플라이언스·워크플로 | 28 | 0 | 0 | 28 | 19 |
| 사용량·분석 조회 | 58 | 4 | 42 | 12 | 41 |
| 엔터프라이즈·공개·기타 | 30 | 0 | 2 | 28 | 15 |
| ROI 계산기(미마운트) | 23 | 0 | 0 | 23 | 0 |
| 죽은 코드(미마운트) | 1 | 0 | 0 | 1 | 0 |
| **합계** | **514** | **41** | **150** | **323** | **269** |

## 1. 군별 엔드포인트 표

- `GET /` 는 `root_redirect`(proxy_server.py:2922)와 `home`(proxy_server.py:21375) 두 핸들러가 같은 경로에 있어 두 행으로 셌다(먼저 등록된 쪽이 응답, 어느 쪽이 이기는지는 확인 못함).
- 경로 접두: MCP 는 `/v1/mcp`, management_v1 은 `/management/v1`, SCIM 은 `/scim/v2`, ROI 관측은 `/roi-calculator/observed`, 캐시 라우트는 `/cache` 를 붙여 실제 경로로 적었다.
- 사용량 일별 집계 `/{user|team|tag|organization|customer|end_user|agent}/daily/activity/*` 37행은 `daily_activity_routes.py` 가 반복문으로 동적 등록한다(데코레이터가 없어 위치는 등록 함수 줄 번호).
- 통합 접근 그룹 별칭 `/v1/unified_access_group*` 5행은 `access_group_endpoints.py:838-867` 의 `add_api_route`.

### 키(Virtual Key) (16개)

주요 테이블: VerificationToken(+DeletedVerificationToken, BudgetTable, TeamTable, UserTable, ObjectPermissionTable, AuditLog)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /key/generate` | management_endpoints/key_management_endpoints.py:2267 | 가상 키 발급(모델·예산·만료·팀·사용자 지정) | VerificationToken(+BudgetTable, TeamTable, UserTable) | 일부 유료: tags·model_max_budget·guardrails/policies/logging 메타 필드 | **필수** 키 발급은 운영자 1순위 기능 | UI○ / 분리배포 노출 |
| `POST /key/service-account/generate` | management_endpoints/key_management_endpoints.py:2481 | 서비스 계정 키 발급(사용자 소유 아님) | VerificationToken(+BudgetTable, TeamTable, UserTable) | 일부 유료: tags·model_max_budget·guardrails/policies/logging 메타 필드 | **후순위** 서비스 계정 키는 사용자 소유 키 이후 | UI○ / 분리배포 노출 |
| `POST /key/update` | management_endpoints/key_management_endpoints.py:3805 | 키 속성 수정 | VerificationToken(+BudgetTable, TeamTable, UserTable) | 일부 유료: tags·model_max_budget·guardrails/policies/logging 메타 필드 | **필수** 만료·예산·모델 변경에 필요 | UI○ / 분리배포 노출 |
| `POST /key/bulk_update` | management_endpoints/key_management_endpoints.py:4089 | 키 일괄 수정 | VerificationToken(+BudgetTable, TeamTable, UserTable) | 일부 유료: tags·model_max_budget·guardrails/policies/logging 메타 필드 | **후순위** 여러 키 한 번에 다룰 때만 필요 | UI× / 분리배포 노출 |
| `POST /team/key/bulk_update` | management_endpoints/key_management_endpoints.py:4284 | 팀 소속 키 전체에 같은 수정 적용 | VerificationToken, TeamTable | 일부 유료: tags·model_max_budget·guardrails/policies/logging 메타 필드 | **후순위** 여러 키 한 번에 다룰 때만 필요 | UI× / 분리배포 노출 |
| `POST /key/delete` | management_endpoints/key_management_endpoints.py:4538 | 키 삭제(삭제 이력 보관) | VerificationToken(+BudgetTable, TeamTable, UserTable) | - | **필수** 키 폐기 | UI○ / 분리배포 노출 |
| `POST /v2/key/info` | management_endpoints/key_management_endpoints.py:4717 | 키 여러 개 정보 조회(POST) | VerificationToken(+BudgetTable, TeamTable, UserTable) | - | **후순위** 여러 키 한 번에 다룰 때만 필요 | UI× / 분리배포 노출 |
| `GET /key/info` | management_endpoints/key_management_endpoints.py:4815 | 키 정보 조회 | VerificationToken(+BudgetTable, TeamTable, UserTable) | - | **필수** 키 조회(UI·운영) | UI○ / 분리배포 노출 |
| `POST /key/{key:path}/regenerate` | management_endpoints/key_management_endpoints.py:6250 | 키 재발급(옵션 속성 변경) | VerificationToken(+BudgetTable, TeamTable, UserTable) | 유료: 마스터 키 외 재발급은 Enterprise(key_management_endpoints.py:6371) | **후순위** upstream 은 유료. 키 교체 운영 시 필요 | UI○ / 분리배포 노출 |
| `POST /key/regenerate` | management_endpoints/key_management_endpoints.py:6255 | 키 재발급(옵션 속성 변경) | VerificationToken(+BudgetTable, TeamTable, UserTable) | 유료: 마스터 키 외 재발급은 Enterprise(key_management_endpoints.py:6371) | **후순위** upstream 은 유료. 키 교체 운영 시 필요 | UI× / 분리배포 노출 |
| `POST /key/{key:path}/reset_spend` | management_endpoints/key_management_endpoints.py:6732 | 키 누적 사용액 0 으로 초기화 | VerificationToken(+BudgetTable, TeamTable, UserTable) | - | **필수** 예산 주기 운영에 필요 | UI○ / 분리배포 노출 |
| `GET /key/list` | management_endpoints/key_management_endpoints.py:7043 | 키 목록(사용자·팀·조직 필터) | VerificationToken(+BudgetTable, TeamTable, UserTable) | - | **필수** 키 조회(UI·운영) | UI○ / 분리배포 노출 |
| `GET /key/aliases` | management_endpoints/key_management_endpoints.py:7278 | 키 별칭 목록(검색·페이지) | VerificationToken(+BudgetTable, TeamTable, UserTable) | - | **후순위** UI 목록 보조 | UI○ / 분리배포 노출 |
| `POST /key/block` | management_endpoints/key_management_endpoints.py:7848 | 키 차단 | VerificationToken(+BudgetTable, TeamTable, UserTable) | - | **필수** 즉시 차단·해제가 운영 수단 | UI○ / 분리배포 노출 |
| `POST /key/unblock` | management_endpoints/key_management_endpoints.py:7962 | 키 차단 해제 | VerificationToken(+BudgetTable, TeamTable, UserTable) | - | **필수** 즉시 차단·해제가 운영 수단 | UI○ / 분리배포 노출 |
| `POST /key/health` | management_endpoints/key_management_endpoints.py:8076 | 키 상태 점검 | VerificationToken(+BudgetTable, TeamTable, UserTable) | - | **제외** 모델 호출로 상태를 보는 보조 기능 | UI× / 분리배포 노출 |

### 팀 관리 (30개)

주요 테이블: TeamTable(+TeamMembership, UserTable, VerificationToken, DeletedTeamTable, BudgetTable, ObjectPermissionTable)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /management/v1/teams/{team_id}/members/bulk_delete` | management_endpoints/management_v1/teams.py:28 | 팀 멤버 최대 500명 일괄 제거 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 멤버 한도·초기화는 팀 예산 다음 | UI× / 분리배포 노출 |
| `POST /management/v1/teams/{team_id}/members/bulk_update` | management_endpoints/management_v1/teams.py:100 | 팀 멤버 최대 500명 한도 일괄 설정 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 멤버 한도·초기화는 팀 예산 다음 | UI○ / 분리배포 노출 |
| `POST /team/{team_id:path}/callback` | management_endpoints/team_callback_endpoints.py:268 | 팀에 성공·실패 콜백 추가 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **제외** 팀별 로깅 콜백은 외부 로깅 연동 전제 | UI× / 분리배포 노출 |
| `DELETE /team/{team_id:path}/callback/{callback_name}` | management_endpoints/team_callback_endpoints.py:471 | 팀 콜백 1건 제거 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **제외** 팀별 로깅 콜백은 외부 로깅 연동 전제 | UI× / 분리배포 노출 |
| `POST /team/{team_id}/disable_logging` | management_endpoints/team_callback_endpoints.py:616 | 팀 로깅 전체 끄기 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **제외** 팀별 로깅 콜백은 외부 로깅 연동 전제 | UI× / 분리배포 노출 |
| `GET /team/{team_id:path}/callback` | management_endpoints/team_callback_endpoints.py:755 | 팀 콜백·변수 조회 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **제외** 팀별 로깅 콜백은 외부 로깅 연동 전제 | UI× / 분리배포 노출 |
| `POST /team/new` | management_endpoints/team_endpoints.py:1487 | 팀 생성(예산·모델·멤버 한도) | TeamTable(+TeamMembership, UserTable, VerificationToken) | 일부 유료: 팀 model_max_budget, 팀 admin 지정, custom_team_metadata_validate, 라이선스 max_teams | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI○ / 분리배포 노출 |
| `POST /team/update` | management_endpoints/team_endpoints.py:2280 | 팀 수정 | TeamTable(+TeamMembership, UserTable, VerificationToken) | 일부 유료: 팀 model_max_budget, 팀 admin 지정, custom_team_metadata_validate, 라이선스 max_teams | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI○ / 분리배포 노출 |
| `PATCH /team/{team_id}` | management_endpoints/team_endpoints.py:2782 | 팀 부분 수정(RFC 7386) | TeamTable(+TeamMembership, UserTable, VerificationToken) | 일부 유료: 팀 model_max_budget, 팀 admin 지정, custom_team_metadata_validate, 라이선스 max_teams | **후순위** 팀 부분 수정은 update 로 대체 가능 | UI× / 분리배포 노출 |
| `POST /team/member_add` | management_endpoints/team_endpoints.py:3578 | 팀 멤버 추가(이메일·id) | TeamTable(+TeamMembership, UserTable, VerificationToken) | 일부 유료: role=admin 지정 | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI○ / 분리배포 노출 |
| `POST /team/member_delete` | management_endpoints/team_endpoints.py:3763 | 팀 멤버 제거 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI○ / 분리배포 노출 |
| `POST /team/member_update` | management_endpoints/team_endpoints.py:3988 | 팀 멤버 역할·한도 수정 | TeamTable(+TeamMembership, UserTable, VerificationToken) | 일부 유료: role=admin 지정 | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI○ / 분리배포 노출 |
| `POST /team/{team_id}/member/{user_id}/reset_spend` | management_endpoints/team_endpoints.py:4198 | 팀 멤버 사용액 초기화 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 멤버 한도·초기화는 팀 예산 다음 | UI○ / 분리배포 노출 |
| `POST /team/{team_id}/member/{user_id}/reset_budget` | management_endpoints/team_endpoints.py:4296 | 팀 멤버를 팀 기본 한도로 되돌림 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 멤버 한도·초기화는 팀 예산 다음 | UI○ / 분리배포 노출 |
| `POST /team/bulk_member_add` | management_endpoints/team_endpoints.py:4399 | 팀 멤버 일괄 추가 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 멤버 한도·초기화는 팀 예산 다음 | UI○ / 분리배포 노출 |
| `POST /team/delete` | management_endpoints/team_endpoints.py:4545 | 팀과 팀 키 삭제 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI○ / 분리배포 노출 |
| `GET /team/info` | management_endpoints/team_endpoints.py:5240 | 팀 정보(+소속 키) | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI○ / 분리배포 노출 |
| `GET /team/{team_id}/members/me` | management_endpoints/team_endpoints.py:5411 | 내 팀 멤버십 조회 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** UI 보조 조회 | UI× / 분리배포 노출 |
| `POST /team/block` | management_endpoints/team_endpoints.py:5571 | 팀 차단(소속 키 호출 거부) | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI× / 분리배포 노출 |
| `POST /team/unblock` | management_endpoints/team_endpoints.py:5603 | 팀 차단 해제 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI× / 분리배포 노출 |
| `GET /team/metadata_schema` | management_endpoints/team_endpoints.py:5629 | 팀 메타데이터 필드 스키마 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **제외** 팀 메타데이터 검증은 유료 기능 | UI× / 분리배포 노출 |
| `GET /team/available` | management_endpoints/team_endpoints.py:5647 | 가입 가능한 팀 목록 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** UI 보조 조회 | UI○ / 분리배포 노출 |
| `GET /v2/team/list` | management_endpoints/team_endpoints.py:5991 | 팀 목록(페이지·필터·정렬) | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** UI 보조 조회 | UI○ / 분리배포 노출 |
| `GET /team/list` | management_endpoints/team_endpoints.py:6260 | 팀 목록 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **필수** 팀 단위 예산·모델 제한이 운영 핵심 | UI○ / 분리배포 노출 |
| `GET /team/filter/ui` | management_endpoints/team_endpoints.py:6371 | UI 팀 검색 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** UI 보조 조회 | UI× / 분리배포 노출 |
| `POST /team/model/add` | management_endpoints/team_endpoints.py:6454 | 팀 허용 모델 추가 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 팀 허용 모델 제한에 필요하나 update 로 대체 가능 | UI× / 분리배포 노출 |
| `POST /team/model/delete` | management_endpoints/team_endpoints.py:6570 | 팀 허용 모델 제거 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 팀 허용 모델 제한에 필요하나 update 로 대체 가능 | UI× / 분리배포 노출 |
| `GET /team/permissions_list` | management_endpoints/team_endpoints.py:6653 | 팀 멤버 권한 목록 조회 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 세부 권한은 admin/user 로 단순화 | UI○ / 분리배포 노출 |
| `POST /team/permissions_update` | management_endpoints/team_endpoints.py:6719 | 팀 멤버 권한 수정 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 세부 권한은 admin/user 로 단순화 | UI○ / 분리배포 노출 |
| `POST /team/permissions_bulk_update` | management_endpoints/team_endpoints.py:6780 | 여러 팀에 권한 추가 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 세부 권한은 admin/user 로 단순화 | UI× / 분리배포 노출 |

### 사용자 관리 (13개)

주요 테이블: UserTable(+VerificationToken, TeamMembership, BudgetTable, OrganizationMembership)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `GET /management/v1/users/me` | management/users/endpoints.py:14 | 현재 로그인 사용자 정보 | UserTable | - | **필수** UI 가 현재 사용자·역할을 알아야 함 | UI○ / 분리배포 노출 |
| `POST /user/new` | management_endpoints/internal_user_endpoints.py:462 | 내부 사용자 생성(예산 포함) | UserTable(+VerificationToken, TeamMembership) | 일부 유료: 라이선스 max_users 초과 시 403(키 있을 때만) | **필수** 사용자·SSO 계정 관리의 기본 | UI○ / 분리배포 노출 |
| `GET /user/available_roles` | management_endpoints/internal_user_endpoints.py:670 | UI 용 부여 가능 역할 목록 | UserTable(+VerificationToken, TeamMembership) | - | **후순위** UI 보조 조회 | UI○ / 분리배포 노출 |
| `GET /user/info` | management_endpoints/internal_user_endpoints.py:922 | 사용자 정보(+키·팀) | UserTable(+VerificationToken, TeamMembership) | - | **필수** 사용자·SSO 계정 관리의 기본 | UI× / 분리배포 노출 |
| `GET /v2/user/info` | management_endpoints/internal_user_endpoints.py:1062 | 경량 사용자 정보 | UserTable(+VerificationToken, TeamMembership) | - | **후순위** UI 보조 조회 | UI○ / 분리배포 노출 |
| `POST /user/update` | management_endpoints/internal_user_endpoints.py:1659 | 사용자 수정 | UserTable(+VerificationToken, TeamMembership) | - | **필수** 사용자·SSO 계정 관리의 기본 | UI○ / 분리배포 노출 |
| `POST /user/bulk_update` | management_endpoints/internal_user_endpoints.py:1827 | 사용자 일괄 수정 | UserTable(+VerificationToken, TeamMembership) | - | **후순위** 대량 작업은 운영 규모가 커질 때 | UI○ / 분리배포 노출 |
| `GET /user/list` | management_endpoints/internal_user_endpoints.py:2210 | 사용자 목록(페이지·필터·정렬) | UserTable(+VerificationToken, TeamMembership) | - | **필수** 사용자·SSO 계정 관리의 기본 | UI○ / 분리배포 노출 |
| `POST /user/delete` | management_endpoints/internal_user_endpoints.py:2383 | 사용자와 그 키 삭제 | UserTable(+VerificationToken, TeamMembership) | - | **필수** 사용자·SSO 계정 관리의 기본 | UI○ / 분리배포 노출 |
| `GET /user/filter/ui` | management_endpoints/internal_user_endpoints.py:2710 | UI 사용자 검색(부분 일치) | UserTable(+VerificationToken, TeamMembership) | - | **후순위** UI 보조 조회 | UI○ / 분리배포 노출 |
| `POST /management/v1/users/bulk` | management_endpoints/management_v1/users.py:28 | 내부 사용자 최대 500명 일괄 생성 | UserTable | 일부 유료: 라이선스 max_users 초과 시 403(키 있을 때만) | **후순위** 대량 작업은 운영 규모가 커질 때 | UI× / 분리배포 노출 |
| `POST /management/v1/users/bulk_delete` | management_endpoints/management_v1/users.py:111 | 사용자 최대 500명 일괄 삭제 | UserTable | - | **후순위** 대량 작업은 운영 규모가 커질 때 | UI× / 분리배포 노출 |
| `GET /user/available_users` | enterprise/litellm_enterprise/proxy/management_endpoints/internal_user_endpoints.py:14 | max_users 키에 배정 가능한 사용자 목록 | UserTable(+VerificationToken, TeamMembership) | enterprise 모듈(litellm_enterprise) | **후순위** UI 보조 조회 | UI○ / 분리배포 노출 |

### 조직(organization) (11개)

주요 테이블: OrganizationTable(+OrganizationMembership, BudgetTable, ObjectPermissionTable, TeamTable)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /organization/new` | management_endpoints/organization_endpoints.py:366 | 조직 생성 | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI○ / 분리배포 노출 |
| `GET /organization/daily/activity` | management_endpoints/organization_endpoints.py:566 | 조직별 일별 사용량(구 방식) | Daily*Spend, SpendLogs | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI× / 분리배포 노출 |
| `PATCH /organization/update` | management_endpoints/organization_endpoints.py:699 | 조직 수정 | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI× / 분리배포 노출 |
| `PATCH /v2/organization/{organization_id}` | management_endpoints/organization_endpoints.py:850 | 조직 부분 수정(RFC 7396) | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI○ / 분리배포 노출 |
| `DELETE /organization/delete` | management_endpoints/organization_endpoints.py:1012 | 조직 삭제 | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI○ / 분리배포 노출 |
| `GET /organization/list` | management_endpoints/organization_endpoints.py:1099 | 조직 목록 | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI○ / 분리배포 노출 |
| `GET /organization/info` | management_endpoints/organization_endpoints.py:1205 | 조직 정보 조회 | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI○ / 분리배포 노출 |
| `POST /organization/info` | management_endpoints/organization_endpoints.py:1252 | [폐기] POST 방식 조직 조회 | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI○ / 분리배포 노출 |
| `POST /organization/member_add` | management_endpoints/organization_endpoints.py:1291 | 조직 멤버 추가 | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI○ / 분리배포 노출 |
| `PATCH /organization/member_update` | management_endpoints/organization_endpoints.py:1440 | 조직 멤버 역할 수정 | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI○ / 분리배포 노출 |
| `DELETE /organization/member_delete` | management_endpoints/organization_endpoints.py:1592 | 조직 멤버 삭제 | OrganizationTable(+Membership, BudgetTable) | 유료: 라우터 전체가 라이선스 의존성(organization_endpoints.py:99,114), 없으면 403 | **후순위** 팀 위 계층이고 upstream 은 유료. 팀까지 먼저 | UI○ / 분리배포 노출 |

### 고객(end_user) (14개)

주요 테이블: EndUserTable(+BudgetTable, DailyEndUserSpend)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /end_user/block` | management_endpoints/customer_endpoints.py:151 | 고객(end_user) 차단 | EndUserTable | - | **후순위** block 은 고객 차단 운영 시 | UI× / 분리배포 노출 |
| `POST /customer/block` | management_endpoints/customer_endpoints.py:157 | 고객(end_user) 차단 | EndUserTable | - | **후순위** block 은 고객 차단 운영 시 | UI× / 분리배포 노출 |
| `POST /end_user/unblock` | management_endpoints/customer_endpoints.py:207 | 고객 차단 해제 | EndUserTable | - | **후순위** block 은 고객 차단 운영 시 | UI× / 분리배포 노출 |
| `POST /customer/unblock` | management_endpoints/customer_endpoints.py:213 | 고객 차단 해제 | EndUserTable | - | **후순위** block 은 고객 차단 운영 시 | UI× / 분리배포 노출 |
| `POST /end_user/new` | management_endpoints/customer_endpoints.py:322 | 고객(end_user) 생성 | EndUserTable | - | **제외** end_user 별칭 경로 중복(customer 로 통일) | UI× / 분리배포 노출 |
| `POST /customer/new` | management_endpoints/customer_endpoints.py:328 | 고객(end_user) 생성 | EndUserTable | - | **후순위** 요청 user 필드별 추적은 키·팀 다음 순서 | UI× / 분리배포 노출 |
| `GET /customer/info` | management_endpoints/customer_endpoints.py:538 | 고객 정보 조회 | EndUserTable | - | **후순위** 요청 user 필드별 추적은 키·팀 다음 순서 | UI× / 분리배포 노출 |
| `GET /end_user/info` | management_endpoints/customer_endpoints.py:544 | 고객 정보 조회 | EndUserTable | - | **제외** end_user 별칭 경로 중복(customer 로 통일) | UI× / 분리배포 노출 |
| `POST /customer/update` | management_endpoints/customer_endpoints.py:596 | 고객 수정 | EndUserTable | - | **후순위** 요청 user 필드별 추적은 키·팀 다음 순서 | UI× / 분리배포 노출 |
| `POST /end_user/update` | management_endpoints/customer_endpoints.py:602 | 고객 수정 | EndUserTable | - | **제외** end_user 별칭 경로 중복(customer 로 통일) | UI× / 분리배포 노출 |
| `POST /customer/delete` | management_endpoints/customer_endpoints.py:776 | 고객 일괄 삭제 | EndUserTable | - | **후순위** 요청 user 필드별 추적은 키·팀 다음 순서 | UI× / 분리배포 노출 |
| `POST /end_user/delete` | management_endpoints/customer_endpoints.py:782 | 고객 일괄 삭제 | EndUserTable | - | **제외** end_user 별칭 경로 중복(customer 로 통일) | UI× / 분리배포 노출 |
| `GET /customer/list` | management_endpoints/customer_endpoints.py:854 | 고객 목록(관리자) | EndUserTable | - | **후순위** 요청 user 필드별 추적은 키·팀 다음 순서 | UI× / 분리배포 노출 |
| `GET /end_user/list` | management_endpoints/customer_endpoints.py:860 | 고객 목록(관리자) | EndUserTable | - | **제외** end_user 별칭 경로 중복(customer 로 통일) | UI× / 분리배포 노출 |

### 예산(budget) (7개)

주요 테이블: BudgetTable

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /budget/new` | management_endpoints/budget_management_endpoints.py:38 | 예산 객체 생성 | BudgetTable | - | **후순위** 키·팀에 max_budget 을 직접 두면 독립 예산 객체는 나중 | UI○ / 분리배포 노출 |
| `POST /budget/update` | management_endpoints/budget_management_endpoints.py:122 | 예산 수정 | BudgetTable | - | **후순위** 키·팀에 max_budget 을 직접 두면 독립 예산 객체는 나중 | UI○ / 분리배포 노출 |
| `POST /budget/info` | management_endpoints/budget_management_endpoints.py:213 | 예산 id 로 조회 | BudgetTable | - | **후순위** 키·팀에 max_budget 을 직접 두면 독립 예산 객체는 나중 | UI○ / 분리배포 노출 |
| `GET /budget/settings` | management_endpoints/budget_management_endpoints.py:242 | 예산 설정 가능 항목·현재값 | BudgetTable | - | **제외** 설정 항목 메타 조회 | UI× / 분리배포 노출 |
| `GET /budget/list` | management_endpoints/budget_management_endpoints.py:311 | 예산 목록(UI) | BudgetTable | - | **후순위** 키·팀에 max_budget 을 직접 두면 독립 예산 객체는 나중 | UI× / 분리배포 노출 |
| `POST /budget/delete` | management_endpoints/budget_management_endpoints.py:339 | 예산 삭제 | BudgetTable | - | **후순위** 키·팀에 max_budget 을 직접 두면 독립 예산 객체는 나중 | UI○ / 분리배포 노출 |
| `GET /management/v1/budgets` | management_endpoints/management_v1/budgets.py:138 | 예산 목록(페이지·정렬·필터) | BudgetTable | - | **후순위** 목록 보조 | UI○ / 분리배포 노출 |

### 태그·프로젝트 (10개)

주요 테이블: TagTable / ProjectTable (+DailyTagSpend)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /tag/new` | management_endpoints/tag_management_endpoints.py:249 | 태그 생성 | TagTable | - | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI○ / 분리배포 노출 |
| `POST /tag/update` | management_endpoints/tag_management_endpoints.py:399 | 태그 수정 | TagTable | - | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI○ / 분리배포 노출 |
| `POST /tag/info` | management_endpoints/tag_management_endpoints.py:491 | 태그 정보 조회 | TagTable | - | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI○ / 분리배포 노출 |
| `GET /tag/list` | management_endpoints/tag_management_endpoints.py:580 | 태그 목록(예산 포함) | TagTable | - | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI○ / 분리배포 노출 |
| `POST /tag/delete` | management_endpoints/tag_management_endpoints.py:691 | 태그 삭제 | TagTable | - | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI○ / 분리배포 노출 |
| `POST /project/new` | enterprise/litellm_enterprise/proxy/management_endpoints/project_endpoints.py:436 | 프로젝트(팀과 키 사이) 생성 | ProjectTable | 유료: enterprise 모듈, project 생성은 premium(project_endpoints.py:537) | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI○ / 분리배포 노출 |
| `POST /project/update` | enterprise/litellm_enterprise/proxy/management_endpoints/project_endpoints.py:659 | 프로젝트 수정 | ProjectTable | 유료: enterprise 모듈, project 생성은 premium(project_endpoints.py:537) | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI○ / 분리배포 노출 |
| `DELETE /project/delete` | enterprise/litellm_enterprise/proxy/management_endpoints/project_endpoints.py:927 | 프로젝트 삭제 | ProjectTable | 유료: enterprise 모듈, project 생성은 premium(project_endpoints.py:537) | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI× / 분리배포 노출 |
| `GET /project/info` | enterprise/litellm_enterprise/proxy/management_endpoints/project_endpoints.py:1036 | 프로젝트 조회 | ProjectTable | enterprise 모듈(litellm_enterprise) | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI○ / 분리배포 노출 |
| `GET /project/list` | enterprise/litellm_enterprise/proxy/management_endpoints/project_endpoints.py:1096 | 프로젝트 목록 | ProjectTable | enterprise 모듈(litellm_enterprise) | **제외** 태그 예산·프로젝트는 유료이거나 폐쇄망 1차 범위 밖 | UI○ / 분리배포 노출 |

### 모델 관리·조회 (26개)

주요 테이블: ProxyModelTable(+TeamTable, CredentialsTable, HealthCheckTable)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `GET /model-insights` | management_endpoints/model_insights_endpoints.py:183 | 모델 인사이트(성능 비교) 조회 | ProxyModelTable(+TeamTable) | - | **제외** 개발·진단용 유틸 | UI× / 분리배포 allowlist 밖 |
| `GET /model-insights/tasks` | management_endpoints/model_insights_endpoints.py:233 | 모델 인사이트 과제 목록 | ProxyModelTable(+TeamTable) | - | **제외** 개발·진단용 유틸 | UI× / 분리배포 allowlist 밖 |
| `GET /model/{model_id}/kubernetes_pods` | management_endpoints/model_management_endpoints.py:1180 | 모델 배포 쿠버네티스 파드 조회 | ProxyModelTable(+TeamTable) | - | **제외** 쿠버네티스 연동 전용 | UI× / 분리배포 노출 |
| `PATCH /model/{model_id}/update` | management_endpoints/model_management_endpoints.py:1251 | 모델 부분 수정(PATCH) | ProxyModelTable(+TeamTable) | 일부 유료: team_id 가 붙은 팀 소유 모델(model_management_endpoints.py:2257) | **필수** 모델 등록·수정·삭제·조회는 라우팅의 전제 | UI○ / 분리배포 노출 |
| `POST /model/block` | management_endpoints/model_management_endpoints.py:1595 | DB 저장 모델 배포 차단 | ProxyModelTable(+TeamTable) | - | **후순위** 배포 임시 차단은 운영 중 필요 | UI× / 분리배포 노출 |
| `POST /model/unblock` | management_endpoints/model_management_endpoints.py:1624 | 모델 배포 차단 해제 | ProxyModelTable(+TeamTable) | - | **후순위** 배포 임시 차단은 운영 중 필요 | UI× / 분리배포 노출 |
| `POST /model/delete` | management_endpoints/model_management_endpoints.py:2441 | 모델 삭제 | ProxyModelTable(+TeamTable) | 일부 유료: team_id 가 붙은 팀 소유 모델(model_management_endpoints.py:2257) | **필수** 모델 등록·수정·삭제·조회는 라우팅의 전제 | UI○ / 분리배포 노출 |
| `POST /model/new` | management_endpoints/model_management_endpoints.py:2616 | 모델 추가(DB 저장·라우터 즉시 반영) | ProxyModelTable(+TeamTable) | 일부 유료: team_id 가 붙은 팀 소유 모델(model_management_endpoints.py:2257) | **필수** 모델 등록·수정·삭제·조회는 라우팅의 전제 | UI○ / 분리배포 노출 |
| `POST /model/update` | management_endpoints/model_management_endpoints.py:2843 | 모델 수정(구 방식) | ProxyModelTable(+TeamTable) | 일부 유료: team_id 가 붙은 팀 소유 모델(model_management_endpoints.py:2257) | **필수** 모델 등록·수정·삭제·조회는 라우팅의 전제 | UI× / 분리배포 노출 |
| `POST /model_group/make_public` | management_endpoints/model_management_endpoints.py:3066 | 공개 모델 그룹 지정 | ProxyModelTable(+TeamTable) | - | **제외** 공개 허브·링크는 폐쇄망에서 의미 없음 | UI○ / 분리배포 노출 |
| `POST /model_hub/update_useful_links` | management_endpoints/model_management_endpoints.py:3153 | 모델 허브 유용한 링크 갱신 | (없음) | - | **제외** 공개 허브·링크는 폐쇄망에서 의미 없음 | UI○ / 분리배포 노출 |
| `POST /utils/token_counter` | proxy_server.py:15180 | 토큰 수 계산 | (없음) | - | **후순위** 모델 지표는 spend 로그로 대체 가능 | UI× / 분리배포 노출 |
| `GET /utils/supported_openai_params` | proxy_server.py:15405 | 모델별 지원 OpenAI 파라미터 | (없음) | - | **제외** 개발·진단용 유틸 | UI× / 분리배포 노출 |
| `GET /utils/model_info` | proxy_server.py:15449 | 모델 비용·한도 사전 조회 | (없음) | - | **제외** 개발·진단용 유틸 | UI× / 분리배포 노출 |
| `POST /utils/transform_request` | proxy_server.py:15483 | 요청을 제공자 형식으로 변환 미리보기 | (없음) | - | **제외** 개발·진단용 유틸 | UI○ / 분리배포 노출 |
| `GET /v2/model/info` | proxy_server.py:16686 | 모델 배포 목록(페이지) | ProxyModelTable(+TeamTable) | - | **필수** 모델 등록·수정·삭제·조회는 라우팅의 전제 | UI○ / 분리배포 노출 |
| `GET /model/streaming_metrics` | proxy_server.py:16934 | 모델 스트리밍 지표 | ProxyModelTable(+TeamTable) | - | **제외** 개발·진단용 유틸 | UI× / 분리배포 노출 |
| `GET /model/metrics` | proxy_server.py:17066 | 모델별 지연·요청 지표 | ProxyModelTable(+TeamTable) | - | **후순위** 모델 지표는 spend 로그로 대체 가능 | UI× / 분리배포 노출 |
| `GET /model/metrics/slow_responses` | proxy_server.py:17181 | 느린 응답 지표 | ProxyModelTable(+TeamTable) | - | **후순위** 모델 지표는 spend 로그로 대체 가능 | UI× / 분리배포 노출 |
| `GET /model/metrics/exceptions` | proxy_server.py:17270 | 모델별 예외 지표 | ProxyModelTable(+TeamTable) | - | **후순위** 모델 지표는 spend 로그로 대체 가능 | UI× / 분리배포 노출 |
| `GET /model/info` | proxy_server.py:17476 | 모델 배포 상세(config 설명 포함) | ProxyModelTable(+TeamTable) | - | **필수** 모델 등록·수정·삭제·조회는 라우팅의 전제 | UI× / 분리배포 노출 |
| `GET /v1/model/info` | proxy_server.py:17481 | 모델 배포 상세(config 설명 포함) | ProxyModelTable(+TeamTable) | - | **후순위** v1 별칭이라 하나만 두면 됨 | UI○ / 분리배포 노출 |
| `GET /model/deprecations` | proxy_server.py:17706 | 모델 폐기 예정일 목록 | ProxyModelTable(+TeamTable) | - | **제외** 폐기 예정일은 외부 단가표 기반 | UI× / 분리배포 노출 |
| `GET /v1/model/deprecations` | proxy_server.py:17712 | 모델 폐기 예정일 목록 | ProxyModelTable(+TeamTable) | - | **제외** 폐기 예정일은 외부 단가표 기반 | UI× / 분리배포 노출 |
| `GET /model_group/info` | proxy_server.py:17794 | 모델 그룹별 배포 정보 | ProxyModelTable(+TeamTable) | - | **필수** 모델 그룹별 배포 상태 표시 | UI○ / 분리배포 노출 |
| `GET /model/settings` | proxy_server.py:18027 | UI 모델 추가 화면용 설정 | ProxyModelTable(+TeamTable) | - | **제외** UI 모델 추가 화면용 제공자 메타데이터 | UI× / 분리배포 노출 |

### 접근 그룹(access group) (18개)

주요 테이블: AccessGroupTable(+ProxyModelTable, TeamTable, VerificationToken, AgentsTable)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /v1/access_group` | management_endpoints/access_group_endpoints.py:528 | 통합 접근 그룹 생성 | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI○ / 분리배포 노출 |
| `GET /v1/access_group` | management_endpoints/access_group_endpoints.py:602 | 통합 접근 그룹 목록 | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI○ / 분리배포 노출 |
| `GET /v1/access_group/{access_group_id}` | management_endpoints/access_group_endpoints.py:617 | 통합 접근 그룹 조회 | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI× / 분리배포 노출 |
| `PUT /v1/access_group/{access_group_id}` | management_endpoints/access_group_endpoints.py:638 | 통합 접근 그룹 수정 | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI× / 분리배포 노출 |
| `DELETE /v1/access_group/{access_group_id}` | management_endpoints/access_group_endpoints.py:734 | 통합 접근 그룹 삭제 | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI× / 분리배포 노출 |
| `POST /access_group/new` | management_endpoints/model_access_group_management_endpoints.py:569 | 모델 접근 그룹 생성 | AccessGroupTable(+ProxyModelTable) | - | **후순위** 모델 접근 그룹은 allow list 이후 | UI× / 분리배포 노출 |
| `GET /access_group/list` | management_endpoints/model_access_group_management_endpoints.py:715 | 모델 접근 그룹 목록 | AccessGroupTable(+ProxyModelTable) | - | **후순위** 모델 접근 그룹은 allow list 이후 | UI○ / 분리배포 노출 |
| `GET /access_group/{access_group}/info` | management_endpoints/model_access_group_management_endpoints.py:765 | 모델 접근 그룹 조회 | AccessGroupTable(+ProxyModelTable) | - | **후순위** 모델 접근 그룹은 allow list 이후 | UI× / 분리배포 노출 |
| `PUT /access_group/{access_group}/update` | management_endpoints/model_access_group_management_endpoints.py:824 | 접근 그룹 모델 수정 | AccessGroupTable(+ProxyModelTable) | - | **후순위** 모델 접근 그룹은 allow list 이후 | UI× / 분리배포 노출 |
| `DELETE /access_group/{access_group}/delete` | management_endpoints/model_access_group_management_endpoints.py:978 | 접근 그룹 삭제 | AccessGroupTable(+ProxyModelTable) | - | **후순위** 모델 접근 그룹은 allow list 이후 | UI× / 분리배포 노출 |
| `GET /access_group/{access_group}/budget` | management_endpoints/model_access_group_management_endpoints.py:1088 | 접근 그룹 공유 예산·사용액 조회 | AccessGroupTable(+ProxyModelTable) | - | **제외** 그룹 공유 예산은 부가 기능 | UI○ / 분리배포 노출 |
| `PUT /access_group/{access_group}/budget` | management_endpoints/model_access_group_management_endpoints.py:1123 | 접근 그룹 공유 예산 설정·교체 | AccessGroupTable(+ProxyModelTable) | - | **제외** 그룹 공유 예산은 부가 기능 | UI○ / 분리배포 노출 |
| `DELETE /access_group/{access_group}/budget` | management_endpoints/model_access_group_management_endpoints.py:1201 | 접근 그룹 공유 예산 해제 | AccessGroupTable(+ProxyModelTable) | - | **제외** 그룹 공유 예산은 부가 기능 | UI○ / 분리배포 노출 |
| `POST /v1/unified_access_group` | management_endpoints/access_group_endpoints.py:838 | 통합 접근 그룹 생성(별칭) | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI× / 분리배포 allowlist 밖 |
| `GET /v1/unified_access_group` | management_endpoints/access_group_endpoints.py:838 | 통합 접근 그룹 목록(별칭) | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI× / 분리배포 allowlist 밖 |
| `GET /v1/unified_access_group/{access_group_id}` | management_endpoints/access_group_endpoints.py:838 | 통합 접근 그룹 조회(별칭) | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI× / 분리배포 allowlist 밖 |
| `PUT /v1/unified_access_group/{access_group_id}` | management_endpoints/access_group_endpoints.py:838 | 통합 접근 그룹 수정(별칭) | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI× / 분리배포 allowlist 밖 |
| `DELETE /v1/unified_access_group/{access_group_id}` | management_endpoints/access_group_endpoints.py:838 | 통합 접근 그룹 삭제(별칭) | AccessGroupTable(+ProxyModelTable) | - | **제외** 통합 접근 그룹과 별칭이 중복 | UI× / 분리배포 allowlist 밖 |

### 자격증명(credentials) (13개)

주요 테이블: CredentialsTable(+UserProviderCredentials)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /credentials` | credential_endpoints/endpoints.py:303 | 모델용 자격증명 등록(값은 암호화 저장) | CredentialsTable | - | **후순위** Ollama 는 키가 필요 없어 모델 인증 재사용은 나중 | UI○ / 분리배포 노출 |
| `GET /credentials` | credential_endpoints/endpoints.py:383 | 자격증명 목록(값 가림) | CredentialsTable | - | **후순위** Ollama 는 키가 필요 없어 모델 인증 재사용은 나중 | UI○ / 분리배포 노출 |
| `GET /credentials/by_name/{credential_name:path}` | credential_endpoints/endpoints.py:406 | 이름으로 자격증명 조회 | CredentialsTable | - | **후순위** Ollama 는 키가 필요 없어 모델 인증 재사용은 나중 | UI× / 분리배포 노출 |
| `GET /credentials/{credential_name:path}/jwks` | credential_endpoints/endpoints.py:437 | anthropic 내부 발급자 공개 JWKS 내보내기 | CredentialsTable | - | **제외** anthropic 발급자 JWKS는 범위 밖 | UI× / 분리배포 노출 |
| `GET /credentials/by_model/{model_id}` | credential_endpoints/endpoints.py:496 | 모델에 연결된 자격증명 조회 | CredentialsTable | - | **후순위** Ollama 는 키가 필요 없어 모델 인증 재사용은 나중 | UI× / 분리배포 노출 |
| `GET /credentials/user_connections` | credential_endpoints/endpoints.py:568 | 내 사용자별 제공자 연결(GitHub Copilot) 목록 | CredentialsTable | - | **제외** GitHub 외부 디바이스 플로우 | UI○ / 분리배포 노출 |
| `POST /credentials/{credential_name:path}/user_connection/start` | credential_endpoints/endpoints.py:627 | GitHub 디바이스 플로우 시작 | CredentialsTable | 외부: GitHub 디바이스 플로우 | **제외** GitHub 외부 디바이스 플로우 | UI× / 분리배포 노출 |
| `POST /credentials/{credential_name:path}/user_connection/poll` | credential_endpoints/endpoints.py:675 | 디바이스 플로우 폴링 후 토큰 저장 | CredentialsTable | 외부: GitHub 디바이스 플로우 | **제외** GitHub 외부 디바이스 플로우 | UI× / 분리배포 노출 |
| `DELETE /credentials/{credential_name:path}/user_connection` | credential_endpoints/endpoints.py:761 | 내 GitHub 연결 해제 | CredentialsTable | - | **제외** GitHub 외부 디바이스 플로우 | UI× / 분리배포 노출 |
| `DELETE /credentials/{credential_name:path}` | credential_endpoints/endpoints.py:835 | 자격증명 삭제 | CredentialsTable | - | **후순위** Ollama 는 키가 필요 없어 모델 인증 재사용은 나중 | UI○ / 분리배포 노출 |
| `PATCH /credentials/{credential_name:path}` | credential_endpoints/endpoints.py:911 | 자격증명 수정 | CredentialsTable | - | **후순위** Ollama 는 키가 필요 없어 모델 인증 재사용은 나중 | UI○ / 분리배포 노출 |
| `POST /credentials/migrate-encryption` | management_endpoints/key_management_endpoints.py:5884 | 저장된 자격값을 AES-256-GCM 으로 재암호화 | CredentialsTable | - | **제외** upstream 의 저장값 암호화 마이그레이션 도구 | UI× / 분리배포 노출 |
| `GET /credentials/migrate-encryption/check` | management_endpoints/key_management_endpoints.py:5924 | 재암호화 잔여분 읽기 전용 점검 | CredentialsTable | - | **제외** upstream 의 저장값 암호화 마이그레이션 도구 | UI× / 분리배포 노출 |

### 오토라우터 (15개)

주요 테이블: AutoRouter*(Session, DailySpend, Baseline*), ShadowEval*

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /auto_router/validate_complexity_router_config` | management_endpoints/auto_router_endpoints.py:367 | 복잡도 라우터 설정 검증 | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI○ / 분리배포 노출 |
| `POST /auto_router/availability` | management_endpoints/auto_router_endpoints.py:403 | 오토라우터 기능 사용 가능 여부(라이선스 한도) | AutoRouter*, ShadowEval* | 일부 유료: 오토라우터 분류기·커스터마이즈 1개 초과(litellm_license.py:165) | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI○ / 분리배포 노출 |
| `POST /auto_router/test_routing` | management_endpoints/auto_router_endpoints.py:485 | 오토라우터로 단건 라우팅 미리보기 | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI○ / 분리배포 노출 |
| `GET /auto_router/benchmarks` | management_endpoints/auto_router_endpoints.py:916 | 오토라우터 벤치마크 대시보드 | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI× / 분리배포 노출 |
| `GET /auto_router/session` | management_endpoints/auto_router_endpoints.py:998 | 오토라우팅 세션 1건 조회 | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI× / 분리배포 노출 |
| `POST /auto_router/shadow_eval/start` | management_endpoints/auto_router_endpoints.py:1668 | 섀도 평가 시작(요청 일부 복제) | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI○ / 분리배포 노출 |
| `GET /auto_router/shadow_eval` | management_endpoints/auto_router_endpoints.py:1890 | 섀도 평가 작업 목록 | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI× / 분리배포 노출 |
| `GET /auto_router/shadow_eval/{job_id}` | management_endpoints/auto_router_endpoints.py:1937 | 섀도 평가 작업 상세 | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI× / 분리배포 노출 |
| `POST /auto_router/shadow_eval/{job_id}/stop` | management_endpoints/auto_router_endpoints.py:1985 | 섀도 평가 중지 | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI○ / 분리배포 노출 |
| `POST /auto_router/classifier/default_prompt` | management_endpoints/model_management_endpoints.py:3266 | 분류기 프롬프트 미리보기(POST) | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI○ / 분리배포 노출 |
| `GET /auto_router/classifier/default_prompt` | management_endpoints/model_management_endpoints.py:3301 | 분류기 기본 프롬프트 조회 | AutoRouter*, ShadowEval* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI○ / 분리배포 노출 |
| `GET /adaptive_router/state` | proxy_server.py:21380 | 적응형 라우터 상태(밴딧 사후분포) | AutoRouter* | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI× / 분리배포 노출 |
| `GET /public/complexity_router/scorer_defaults` | public_endpoints/public_endpoints.py:464 | 복잡도 점수 기본값 | (없음) | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI○ / 분리배포 노출 |
| `GET /public/complexity_router/fuse_presets` | public_endpoints/public_endpoints.py:486 | 복잡도 라우터 프리셋 | (없음) | - | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI× / 분리배포 노출 |
| `GET /public/autorouter_presets` | public_endpoints/public_endpoints.py:603 | 오토라우터 프리셋 카탈로그 | (없음) | 외부: 프리셋 GitHub raw(litellm/__init__.py:424) | **제외** 자동 라우팅 실험 기능. 사용자가 요청한 범위가 아님 | UI○ / 분리배포 노출 |

### 폴백·라우터·캐시 설정 (15개)

주요 테이블: Config(+CacheConfig)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `GET /cache/settings` | management_endpoints/cache_settings_endpoints.py:443 | 캐시 설정·가능 항목 조회 | CacheConfig | - | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI○ / 분리배포 노출 |
| `POST /cache/settings/test` | management_endpoints/cache_settings_endpoints.py:509 | 캐시(Redis) 연결 테스트 | CacheConfig | 내부 Redis(설정 시) | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI○ / 분리배포 노출 |
| `POST /cache/settings` | management_endpoints/cache_settings_endpoints.py:572 | 캐시 설정을 DB 에 저장하고 캐시 초기화 | CacheConfig | - | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI○ / 분리배포 노출 |
| `GET /coordination_redis/settings` | management_endpoints/coordination_redis_endpoints.py:293 | 조정용 Redis 설정 조회 | Config | - | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI○ / 분리배포 노출 |
| `POST /coordination_redis/settings` | management_endpoints/coordination_redis_endpoints.py:325 | 조정용 Redis 설정 저장 | Config | - | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI○ / 분리배포 노출 |
| `POST /coordination_redis/settings/test` | management_endpoints/coordination_redis_endpoints.py:401 | 조정용 Redis 연결 테스트 | Config | 내부 Redis(설정 시) | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI○ / 분리배포 노출 |
| `POST /fallback` | management_endpoints/fallback_management_endpoints.py:81 | 모델별 폴백 생성·갱신 | Config(router_settings) | - | **후순위** 폴백은 모델이 여러 개일 때 | UI× / 분리배포 노출 |
| `GET /fallback/{model}` | management_endpoints/fallback_management_endpoints.py:221 | 모델 폴백 조회 | Config(router_settings) | - | **후순위** 폴백은 모델이 여러 개일 때 | UI× / 분리배포 노출 |
| `DELETE /fallback/{model}` | management_endpoints/fallback_management_endpoints.py:278 | 모델 폴백 삭제 | Config(router_settings) | - | **후순위** 폴백은 모델이 여러 개일 때 | UI× / 분리배포 노출 |
| `GET /router/settings` | management_endpoints/router_settings_endpoints.py:84 | 라우터 설정·가능 항목 | Config(router_settings) | - | **후순위** 라우터 전략 설정 | UI○ / 분리배포 노출 |
| `GET /router/fields` | management_endpoints/router_settings_endpoints.py:167 | 라우터 설정 필드 정의 | Config(router_settings) | - | **후순위** 라우터 전략 설정 | UI○ / 분리배포 노출 |
| `GET /cache/ping` | caching_routes.py:52 | 캐시 ping | (없음) | - | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI○ / 분리배포 노출 |
| `POST /cache/delete` | caching_routes.py:124 | 캐시 키 삭제 | (없음) | - | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI× / 분리배포 노출 |
| `GET /cache/redis/info` | caching_routes.py:182 | Redis info | (없음) | - | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI× / 분리배포 노출 |
| `POST /cache/flushall` | caching_routes.py:218 | 캐시 전체 비우기 | (없음) | - | **제외** 폴백·Redis 캐시는 모델 1~2개 폐쇄망에서 후순위 이하 | UI× / 분리배포 노출 |

### 설정·DB 동기화·UI 설정 (62개)

주요 테이블: Config / UISettings / SSOConfig / ConfigOverrides

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `GET /callbacks/list` | management_endpoints/callback_management_endpoints.py:20 | 활성 로깅 콜백 목록 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI× / 분리배포 노출 |
| `GET /callbacks/configs` | management_endpoints/callback_management_endpoints.py:38 | 사용 가능한 콜백 설정 목록 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `POST /config_overrides/hashicorp_vault` | management_endpoints/config_override_endpoints.py:343 | HashiCorp Vault 설정 저장 | ConfigOverrides | 유료: 외부 비밀관리 연동(Vault·CyberArk, secret_managers 의 premium 확인) | **제외** 외부 비밀관리 연동은 1차 범위 밖(유료) | UI○ / 분리배포 allowlist 밖 |
| `GET /config_overrides/hashicorp_vault` | management_endpoints/config_override_endpoints.py:483 | Vault 설정 조회 | ConfigOverrides | 유료: 외부 비밀관리 연동(Vault·CyberArk, secret_managers 의 premium 확인) | **제외** 외부 비밀관리 연동은 1차 범위 밖(유료) | UI○ / 분리배포 allowlist 밖 |
| `DELETE /config_overrides/hashicorp_vault` | management_endpoints/config_override_endpoints.py:543 | Vault 설정 삭제 | ConfigOverrides | 유료: 외부 비밀관리 연동(Vault·CyberArk, secret_managers 의 premium 확인) | **제외** 외부 비밀관리 연동은 1차 범위 밖(유료) | UI○ / 분리배포 allowlist 밖 |
| `POST /config_overrides/hashicorp_vault/test_connection` | management_endpoints/config_override_endpoints.py:612 | Vault 연결 테스트 | ConfigOverrides | 유료: 외부 비밀관리 연동(Vault·CyberArk, secret_managers 의 premium 확인); 외부: 비밀관리 서버(설정 시) | **제외** 외부 비밀관리 연동은 1차 범위 밖(유료) | UI× / 분리배포 allowlist 밖 |
| `POST /config_overrides/cyberark` | management_endpoints/config_override_endpoints.py:672 | CyberArk Conjur 설정 저장 | ConfigOverrides | 유료: 외부 비밀관리 연동(Vault·CyberArk, secret_managers 의 premium 확인) | **제외** 외부 비밀관리 연동은 1차 범위 밖(유료) | UI○ / 분리배포 allowlist 밖 |
| `GET /config_overrides/cyberark` | management_endpoints/config_override_endpoints.py:785 | CyberArk 설정 조회 | ConfigOverrides | 유료: 외부 비밀관리 연동(Vault·CyberArk, secret_managers 의 premium 확인) | **제외** 외부 비밀관리 연동은 1차 범위 밖(유료) | UI○ / 분리배포 allowlist 밖 |
| `DELETE /config_overrides/cyberark` | management_endpoints/config_override_endpoints.py:841 | CyberArk 설정 삭제 | ConfigOverrides | 유료: 외부 비밀관리 연동(Vault·CyberArk, secret_managers 의 premium 확인) | **제외** 외부 비밀관리 연동은 1차 범위 밖(유료) | UI○ / 분리배포 allowlist 밖 |
| `POST /config_overrides/cyberark/test_connection` | management_endpoints/config_override_endpoints.py:901 | CyberArk 연결 테스트 | ConfigOverrides | 유료: 외부 비밀관리 연동(Vault·CyberArk, secret_managers 의 premium 확인); 외부: 비밀관리 서버(설정 시) | **제외** 외부 비밀관리 연동은 1차 범위 밖(유료) | UI○ / 분리배포 allowlist 밖 |
| `GET /config/cost_discount_config` | management_endpoints/cost_tracking_settings.py:193 | 비용 할인 설정 조회 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `PATCH /config/cost_discount_config` | management_endpoints/cost_tracking_settings.py:228 | 비용 할인 설정 수정 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /config/cost_margin_config` | management_endpoints/cost_tracking_settings.py:335 | 비용 마진 설정 조회 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `PATCH /config/cost_margin_config` | management_endpoints/cost_tracking_settings.py:370 | 비용 마진 설정 수정 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /config/block_requests_for_models_without_pricing` | management_endpoints/cost_tracking_settings.py:517 | 단가 없는 모델 요청 차단 설정 조회 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI× / 분리배포 노출 |
| `PATCH /config/block_requests_for_models_without_pricing` | management_endpoints/cost_tracking_settings.py:527 | 단가 없는 모델 요청 차단 설정 수정 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI× / 분리배포 노출 |
| `GET /get/latest_release_info` | ui_crud_endpoints/latest_release_endpoints.py:181 | 최신 LiteLLM 릴리스 정보(업그레이드 배너) | Config | 외부: GitHub API(api.github.com), 실패 시 null | **제외** GitHub 호출(폐쇄망 불가) | UI× / 분리배포 노출 |
| `GET /get/allowed_ips` | ui_crud_endpoints/proxy_setting_endpoints.py:626 | 허용 IP 목록 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `POST /add/allowed_ip` | ui_crud_endpoints/proxy_setting_endpoints.py:654 | 허용 IP 추가 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `POST /delete/allowed_ip` | ui_crud_endpoints/proxy_setting_endpoints.py:716 | 허용 IP 삭제 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /get/internal_user_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:893 | SSO 신규 사용자 기본값 조회 | Config | - | **후순위** SSO 신규 사용자 기본 팀·예산에 필요 | UI○ / 분리배포 노출 |
| `GET /get/default_team_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:916 | 기본 팀 설정 조회 | Config | - | **후순위** SSO 신규 사용자 기본 팀·예산에 필요 | UI○ / 분리배포 노출 |
| `PATCH /update/internal_user_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1116 | SSO 신규 사용자 기본값 수정 | Config | - | **후순위** SSO 신규 사용자 기본 팀·예산에 필요 | UI○ / 분리배포 노출 |
| `PATCH /update/default_team_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1146 | 기본 팀 설정 수정 | Config | - | **후순위** SSO 신규 사용자 기본 팀·예산에 필요 | UI○ / 분리배포 노출 |
| `GET /get/sso_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1171 | SSO 설정 조회(DB 테이블) | SSOConfig | - | **제외** Keycloak 고정이라 DB SSO 설정은 불필요 | UI○ / 분리배포 노출 |
| `PATCH /update/sso_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1226 | SSO 설정 수정(DB 저장) | SSOConfig | - | **제외** Keycloak 고정이라 DB SSO 설정은 불필요 | UI○ / 분리배포 노출 |
| `GET /get/ui_theme_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1368 | UI 테마 조회 | UISettings | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `PATCH /update/ui_theme_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1423 | UI 테마 수정 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /get/mcp_semantic_filter_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1509 | MCP 시맨틱 필터 설정 조회 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `PATCH /update/mcp_semantic_filter_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1539 | MCP 시맨틱 필터 설정 수정 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /get/websearch_interception_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1575 | 웹검색 가로채기 설정 조회 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `PATCH /update/websearch_interception_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1617 | 웹검색 가로채기 설정 수정 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /get/mcp_tool_search_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1657 | MCP 도구 검색 설정 조회 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI× / 분리배포 노출 |
| `PATCH /update/mcp_tool_search_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1683 | MCP 도구 검색 설정 수정 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI× / 분리배포 노출 |
| `GET /get/ui_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1803 | UI 플래그 조회 | UISettings | - | **후순위** UI 부트스트랩이 호출 | UI○ / 분리배포 노출 |
| `PATCH /update/ui_settings` | ui_crud_endpoints/proxy_setting_endpoints.py:1882 | UI 플래그 수정 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `POST /upload/logo` | ui_crud_endpoints/proxy_setting_endpoints.py:2033 | 관리자 UI 로고 업로드 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI× / 분리배포 노출 |
| `GET /get/user_banner` | ui_crud_endpoints/user_banner_endpoints.py:68 | 대시보드 배너 조회 | UISettings | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `PATCH /update/user_banner` | ui_crud_endpoints/user_banner_endpoints.py:88 | 배너 게시·수정·내림 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /.well-known/litellm-ui-config` | discovery_endpoints/ui_discovery_endpoints.py:16 | UI 부트스트랩 설정(.well-known) | (없음) | - | **후순위** UI 부트스트랩이 호출 | UI× / 분리배포 노출 |
| `GET /litellm/.well-known/litellm-ui-config` | discovery_endpoints/ui_discovery_endpoints.py:17 | UI 부트스트랩 설정(.well-known) | (없음) | - | **후순위** UI 부트스트랩이 호출 | UI○ / 분리배포 노출 |
| `GET /alerting/settings` | proxy_server.py:18076 | 알림(Slack 등) 설정 조회 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /get_logo_url` | proxy_server.py:19008 | 로고 URL 조회 | (없음) | - | **후순위** UI 부트스트랩이 호출 | UI× / 분리배포 노출 |
| `GET /get_image` | proxy_server.py:19049 | 관리자 UI 로고 이미지 | (없음) | 외부 URL 로고 로드(LITELLM_LOGO_URL 설정 시) | **후순위** UI 부트스트랩이 호출 | UI○ / 분리배포 노출 |
| `GET /get_favicon` | proxy_server.py:19124 | 파비콘 | (없음) | - | **후순위** UI 부트스트랩이 호출 | UI× / 분리배포 노출 |
| `POST /config/update` | proxy_server.py:19410 | config 를 UI 에서 갱신(DB 저장) | Config | - | **후순위** 운영 설정 저장 방식은 신규 설계에서 정함 | UI○ / 분리배포 노출 |
| `POST /config/field/update` | proxy_server.py:19723 | general_settings 필드 1개 갱신 | Config | - | **후순위** 운영 설정 저장 방식은 신규 설계에서 정함 | UI○ / 분리배포 노출 |
| `GET /config/field/info` | proxy_server.py:19991 | general_settings 필드 조회 | Config | - | **후순위** 운영 설정 저장 방식은 신규 설계에서 정함 | UI○ / 분리배포 노출 |
| `GET /config/list` | proxy_server.py:20206 | 설정 가능 필드와 현재값 목록 | Config | - | **후순위** 운영 설정 저장 방식은 신규 설계에서 정함 | UI○ / 분리배포 노출 |
| `POST /config/field/delete` | proxy_server.py:20382 | DB 값 삭제(초기값 복귀) | Config | - | **후순위** 운영 설정 저장 방식은 신규 설계에서 정함 | UI○ / 분리배포 노출 |
| `POST /config/callback/delete` | proxy_server.py:20489 | 로깅 콜백 삭제 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /get/config/callbacks` | proxy_server.py:20656 | UI 용 콜백 설정 조회 | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI○ / 분리배포 노출 |
| `GET /config/yaml` | proxy_server.py:20824 | config.yaml 예시(Swagger 용 모의) | Config | - | **제외** DB 로 바꾸는 런타임 설정은 신규 게이트웨이 자체 설정으로 대체 | UI× / 분리배포 노출 |
| `POST /reload/model_cost_map` | proxy_server.py:20850 | 모델 단가표 즉시 갱신 | (없음) | 외부: 단가표 GitHub raw 기본 URL(litellm/__init__.py:410), LITELLM_LOCAL_MODEL_COST_MAP=True 로 로컬 고정 | **제외** GitHub 호출(폐쇄망 불가) | UI○ / 분리배포 노출 |
| `POST /schedule/model_cost_map_reload` | proxy_server.py:20918 | 단가표 주기 갱신 예약 | (없음) | 외부: 단가표 GitHub raw 기본 URL(litellm/__init__.py:410), LITELLM_LOCAL_MODEL_COST_MAP=True 로 로컬 고정 | **제외** GitHub 호출(폐쇄망 불가) | UI○ / 분리배포 노출 |
| `DELETE /schedule/model_cost_map_reload` | proxy_server.py:20967 | 단가표 주기 갱신 취소 | (없음) | - | **제외** GitHub 호출(폐쇄망 불가) | UI○ / 분리배포 노출 |
| `GET /schedule/model_cost_map_reload/status` | proxy_server.py:21007 | 단가표 갱신 상태 | (없음) | 외부: 단가표 GitHub raw 기본 URL(litellm/__init__.py:410), LITELLM_LOCAL_MODEL_COST_MAP=True 로 로컬 고정 | **제외** GitHub 호출(폐쇄망 불가) | UI○ / 분리배포 노출 |
| `GET /model/cost_map/source` | proxy_server.py:21049 | 단가표 출처(로컬·원격) 정보 | (없음) | 외부: 단가표 GitHub raw 기본 URL(litellm/__init__.py:410), LITELLM_LOCAL_MODEL_COST_MAP=True 로 로컬 고정 | **제외** GitHub 호출(폐쇄망 불가) | UI○ / 분리배포 노출 |
| `POST /reload/anthropic_beta_headers` | proxy_server.py:21103 | Anthropic 베타 헤더 목록 즉시 갱신 | (없음) | 외부: 베타 헤더 GitHub raw(litellm/__init__.py:420) | **제외** GitHub 호출(폐쇄망 불가) | UI× / 분리배포 노출 |
| `POST /schedule/anthropic_beta_headers_reload` | proxy_server.py:21176 | 베타 헤더 갱신 예약 | (없음) | 외부: 베타 헤더 GitHub raw(litellm/__init__.py:420) | **제외** GitHub 호출(폐쇄망 불가) | UI× / 분리배포 노출 |
| `DELETE /schedule/anthropic_beta_headers_reload` | proxy_server.py:21236 | 베타 헤더 갱신 취소 | (없음) | - | **제외** GitHub 호출(폐쇄망 불가) | UI× / 분리배포 노출 |
| `GET /schedule/anthropic_beta_headers_reload/status` | proxy_server.py:21281 | 베타 헤더 갱신 상태 | (없음) | - | **제외** GitHub 호출(폐쇄망 불가) | UI× / 분리배포 노출 |

### 헬스·시스템·디버그 (30개)

주요 테이블: (없음) 일부 HealthCheckTable

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `GET /test` | health_endpoints/_health_endpoints.py:346 | [폐기] 생존 확인(liveliness 로 대체) | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 allowlist 밖 |
| `GET /health/services` | health_endpoints/_health_endpoints.py:370 | 외부 연동 서비스(Slack·Langfuse·Datadog 등) 상태 확인(관리자) | (없음) | 외부: Slack·Langfuse·Datadog 등 설정된 연동 서비스 | **제외** 외부 서비스·라이선스·디버그는 불필요 | UI○ / 분리배포 노출 |
| `GET /health` | health_endpoints/_health_endpoints.py:1229 | 등록된 모델 배포 전체를 실제 호출해 상태 점검 | (없음) | 내부 모델 서버 호출(Ollama 등) | **후순위** 모델 서버 점검과 진단 | UI○ / 분리배포 노출 |
| `GET /health/history` | health_endpoints/_health_endpoints.py:1403 | 모델 헬스체크 이력 | HealthCheckTable(일부) | - | **후순위** 모델 서버 점검과 진단 | UI× / 분리배포 노출 |
| `GET /health/latest` | health_endpoints/_health_endpoints.py:1443 | 모델별 최신 헬스체크 결과 | HealthCheckTable(일부) | - | **후순위** 모델 서버 점검과 진단 | UI× / 분리배포 노출 |
| `GET /health/shared-status` | health_endpoints/_health_endpoints.py:1475 | 파드 간 헬스체크 조율 상태 | HealthCheckTable(일부) | - | **제외** 외부 서비스·라이선스·디버그는 불필요 | UI× / 분리배포 노출 |
| `GET /health/license` | health_endpoints/_health_endpoints.py:1552 | 라이선스 메타데이터(enterprise/community) | (없음) | 유료 기능 정보 조회(라이선스 상태) | **제외** 외부 서비스·라이선스·디버그는 불필요 | UI○ / 분리배포 노출 |
| `GET /settings` | health_endpoints/_health_endpoints.py:1662 | 설정 목록(litellm 수준) | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /active/callbacks` | health_endpoints/_health_endpoints.py:1667 | 활성 콜백 목록 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /health/readiness` | health_endpoints/_health_endpoints.py:1970 | 공개 readiness 프로브(저정보) | (없음) | - | **필수** 프로브는 컨테이너 운영의 필수 | UI× / 분리배포 노출 |
| `GET /health/readiness/details` | health_endpoints/_health_endpoints.py:1993 | 인증 필요 readiness 진단(DB·캐시·콜백) | HealthCheckTable(일부) | - | **후순위** 모델 서버 점검과 진단 | UI○ / 분리배포 노출 |
| `GET /health/backlog` | health_endpoints/_health_endpoints.py:2005 | 처리 중 HTTP 요청 수 | (없음) | - | **후순위** 모델 서버 점검과 진단 | UI× / 분리배포 노출 |
| `GET /health/drain` | health_endpoints/_health_endpoints.py:2029 | K8s preStop 용 그레이스풀 드레인 프로브 | (없음) | - | **후순위** 모델 서버 점검과 진단 | UI× / 분리배포 노출 |
| `GET /health/liveliness` | health_endpoints/_health_endpoints.py:2077 | liveness 프로브(무인증) | (없음) | - | **필수** 프로브는 컨테이너 운영의 필수 | UI× / 분리배포 노출 |
| `GET /health/liveness` | health_endpoints/_health_endpoints.py:2081 | liveness 프로브(무인증) | (없음) | - | **필수** 프로브는 컨테이너 운영의 필수 | UI× / 분리배포 노출 |
| `POST /health/test_connection` | health_endpoints/_health_endpoints.py:2134 | 모델 직접 연결 테스트(UI 모델 추가용) | (없음) | 내부 모델 서버 호출(Ollama 등) | **후순위** 모델 서버 점검과 진단 | UI× / 분리배포 노출 |
| `GET /health/deployment_warnings` | health_endpoints/deployment_warning_endpoints.py:276 | 배포 경고 목록 | (없음) | - | **제외** 외부 서비스·라이선스·디버그는 불필요 | UI× / 분리배포 노출 |
| `PATCH /health/deployment_warnings/{warning_id}` | health_endpoints/deployment_warning_endpoints.py:303 | 배포 경고 무시·스누즈·복원 | (없음) | - | **제외** 외부 서비스·라이선스·디버그는 불필요 | UI○ / 분리배포 노출 |
| `GET /debug/asyncio-tasks` | common_utils/debug_utils.py:63 | asyncio 태스크 통계 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /memory-usage` | common_utils/debug_utils.py:113 | 프로세스 메모리 사용량 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 allowlist 밖 |
| `GET /memory-usage-in-mem-cache` | common_utils/debug_utils.py:140 | 인메모리 캐시 메모리 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /memory-usage-in-mem-cache-items` | common_utils/debug_utils.py:186 | 인메모리 캐시 항목별 메모리 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /debug/memory/summary` | common_utils/debug_utils.py:329 | 메모리 요약 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /debug/memory/details` | common_utils/debug_utils.py:698 | 메모리 상세 진단 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `POST /debug/memory/gc/configure` | common_utils/debug_utils.py:757 | GC 임계값 설정 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /debug/report` | common_utils/debug_utils.py:815 | 디버그 환경 보고서 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /otel-spans` | common_utils/debug_utils.py:832 | OTel 스팬 조회 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /` | proxy_server.py:2922 | 루트 핸들러 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /` | proxy_server.py:21375 | 루트 핸들러 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |
| `GET /routes` | proxy_server.py:21415 | 앱 라우트 전체 목록 | (없음) | - | **제외** 파이썬 프로세스 진단·메모리 디버그는 신규 구현에 해당 없음 | UI× / 분리배포 노출 |

### 인증·SSO·온보딩 (31개)

주요 테이블: UserTable(+InvitationLink, SSOConfig, SSOIdentityAssertion, VerificationToken, JWTKeyMapping)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /jwt/key/mapping/new` | management_endpoints/jwt_key_mapping_endpoints.py:145 | JWT 클레임→키 매핑 생성 | JWTKeyMapping | - | **제외** JWT 클레임→키 매핑은 SSO 사용 시 불필요 | UI× / 분리배포 노출 |
| `POST /jwt/key/mapping/update` | management_endpoints/jwt_key_mapping_endpoints.py:201 | JWT 키 매핑 수정 | JWTKeyMapping | - | **제외** JWT 클레임→키 매핑은 SSO 사용 시 불필요 | UI× / 분리배포 노출 |
| `POST /jwt/key/mapping/delete` | management_endpoints/jwt_key_mapping_endpoints.py:269 | JWT 키 매핑 삭제 | JWTKeyMapping | - | **제외** JWT 클레임→키 매핑은 SSO 사용 시 불필요 | UI× / 분리배포 노출 |
| `GET /jwt/key/mapping/list` | management_endpoints/jwt_key_mapping_endpoints.py:304 | JWT 키 매핑 목록 | JWTKeyMapping | - | **제외** JWT 클레임→키 매핑은 SSO 사용 시 불필요 | UI× / 분리배포 노출 |
| `GET /jwt/key/mapping/info` | management_endpoints/jwt_key_mapping_endpoints.py:342 | JWT 키 매핑 조회 | JWTKeyMapping | - | **제외** JWT 클레임→키 매핑은 SSO 사용 시 불필요 | UI× / 분리배포 노출 |
| `POST /user/password/change` | management_endpoints/password_endpoints.py:71 | 내 비밀번호 변경 | UserTable | - | **제외** 비밀번호는 Keycloak 이 관리 | UI○ / 분리배포 노출 |
| `POST /session/logout` | management_endpoints/session_endpoints.py:117 | UI 세션 키 폐기(로그아웃) | VerificationToken | - | **필수** UI 로그아웃 | UI○ / 분리배포 노출 |
| `POST /sso/cli/start` | management_endpoints/ui_sso.py:771 | litellm CLI SSO 시작 | UserTable, VerificationToken, SSOConfig | 외부: 외부 IdP(Google·Microsoft·일반 OIDC). 폐쇄망은 Keycloak 으로 지정 | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `POST /sso/cli/complete/{login_id}` | management_endpoints/ui_sso.py:817 | CLI SSO 완료 | UserTable, VerificationToken, SSOConfig | - | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `GET /sso/key/generate` | management_endpoints/ui_sso.py:1128 | SSO 로그인 시작(IdP 로 리다이렉트) | UserTable, VerificationToken, SSOConfig | 일부 유료: SSO 5명 초과 시 403(ui_sso.py:1106, env SSO_MAX_BILLABLE_USERS); 외부: 외부 IdP(Google·Microsoft·일반 OIDC). 폐쇄망은 Keycloak 으로 지정 | **필수** Keycloak OIDC 로그인 흐름 | UI○ / 분리배포 노출 |
| `GET /sso/callback` | management_endpoints/ui_sso.py:2245 | SSO 콜백: 사용자 upsert 후 UI 세션 발급 | UserTable, VerificationToken, SSOConfig | 일부 유료: SSO 5명 초과 시 403(ui_sso.py:1106, env SSO_MAX_BILLABLE_USERS); 외부: 외부 IdP(Google·Microsoft·일반 OIDC). 폐쇄망은 Keycloak 으로 지정 | **필수** Keycloak OIDC 로그인 흐름 | UI× / 분리배포 노출 |
| `GET /sso/saml/login` | management_endpoints/ui_sso.py:2351 | SAML 로그인 시작 | UserTable, VerificationToken, SSOConfig | 외부: SAML IdP | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `GET /sso/saml/metadata` | management_endpoints/ui_sso.py:2362 | SAML SP 메타데이터 | UserTable, VerificationToken, SSOConfig | - | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `POST /sso/saml/callback` | management_endpoints/ui_sso.py:2371 | SAML 응답 검증·로그인 | UserTable, VerificationToken, SSOConfig | 일부 유료: SSO 5명 초과 시 403(ui_sso.py:1106, env SSO_MAX_BILLABLE_USERS); 외부: SAML IdP | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `POST /sso/cli/password/{login_id}` | management_endpoints/ui_sso.py:2697 | CLI 비밀번호 로그인 | UserTable, VerificationToken, SSOConfig | 외부: 외부 IdP(Google·Microsoft·일반 OIDC). 폐쇄망은 Keycloak 으로 지정 | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `GET /sso/cli/poll/{key_id}` | management_endpoints/ui_sso.py:2765 | CLI 키 발급 폴링 | UserTable, VerificationToken, SSOConfig | - | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `GET /sso/get/ui_settings` | management_endpoints/ui_sso.py:3017 | SSO 로그인 화면 설정(공개) | UserTable, VerificationToken, SSOConfig | - | **후순위** 관리자 비밀번호 로그인은 부트스트랩 용도 | UI○ / 분리배포 노출 |
| `GET /sso/readiness` | management_endpoints/ui_sso.py:3049 | SSO 준비 상태 점검 | UserTable, VerificationToken, SSOConfig | - | **후순위** 관리자 비밀번호 로그인은 부트스트랩 용도 | UI× / 분리배포 노출 |
| `GET /sso/debug/login` | management_endpoints/ui_sso.py:5135 | SSO 디버그 로그인 | UserTable, VerificationToken, SSOConfig | 유료: 디버그 로그인은 premium 만(ui_sso.py:5152); 외부: 외부 IdP(Google·Microsoft·일반 OIDC). 폐쇄망은 Keycloak 으로 지정 | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `GET /sso/debug/callback` | management_endpoints/ui_sso.py:5184 | SSO IdP 응답 객체 그대로 반환 | UserTable, VerificationToken, SSOConfig | 외부: 외부 IdP(Google·Microsoft·일반 OIDC). 폐쇄망은 Keycloak 으로 지정 | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `GET /fallback/login` | proxy_server.py:18337 | 구 방식 Google SSO 로그인 | Config(router_settings) | 외부: 외부 IdP(Google·Microsoft·일반 OIDC). 폐쇄망은 Keycloak 으로 지정 | **제외** SAML·CLI·디버그 로그인은 범위 밖 | UI× / 분리배포 노출 |
| `POST /login` | proxy_server.py:18363 | 관리자 비밀번호 로그인(UI 세션 발급) | UserTable, VerificationToken | - | **후순위** 관리자 비밀번호 로그인은 부트스트랩 용도 | UI× / 분리배포 노출 |
| `POST /v2/login` | proxy_server.py:18457 | 로그인 v2 | UserTable, VerificationToken | - | **후순위** 관리자 비밀번호 로그인은 부트스트랩 용도 | UI× / 분리배포 노출 |
| `POST /v3/login` | proxy_server.py:18520 | 로그인 v3 | UserTable, VerificationToken | - | **후순위** 관리자 비밀번호 로그인은 부트스트랩 용도 | UI× / 분리배포 노출 |
| `POST /v3/login/exchange` | proxy_server.py:18596 | 로그인 v3 코드 교환 | UserTable, VerificationToken | - | **후순위** 관리자 비밀번호 로그인은 부트스트랩 용도 | UI○ / 분리배포 노출 |
| `GET /onboarding/get_token` | proxy_server.py:18661 | 초대 링크 토큰 조회 | InvitationLink, UserTable | - | **제외** SSO 가 사용자를 만들어 초대 링크 불필요 | UI○ / 분리배포 노출 |
| `POST /onboarding/claim_token` | proxy_server.py:18863 | 초대 링크로 비밀번호 설정 | InvitationLink, UserTable | - | **제외** SSO 가 사용자를 만들어 초대 링크 불필요 | UI○ / 분리배포 노출 |
| `POST /invitation/new` | proxy_server.py:19160 | 초대 링크 생성 | InvitationLink, UserTable | - | **제외** SSO 가 사용자를 만들어 초대 링크 불필요 | UI○ / 분리배포 노출 |
| `GET /invitation/info` | proxy_server.py:19234 | 초대 링크 조회 | InvitationLink, UserTable | - | **제외** SSO 가 사용자를 만들어 초대 링크 불필요 | UI× / 분리배포 노출 |
| `POST /invitation/update` | proxy_server.py:19279 | 초대 수락 처리 | InvitationLink, UserTable | - | **제외** SSO 가 사용자를 만들어 초대 링크 불필요 | UI× / 분리배포 노출 |
| `POST /invitation/delete` | proxy_server.py:19336 | 초대 링크 삭제 | InvitationLink, UserTable | - | **제외** SSO 가 사용자를 만들어 초대 링크 불필요 | UI× / 분리배포 노출 |

### SCIM 프로비저닝 (21개)

주요 테이블: UserTable, TeamTable(+TeamMembership)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `GET /scim/v2` | management_endpoints/scim/scim_v2.py:1576 | SCIM 베이스(리소스 발견) | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI○ / 분리배포 allowlist 밖 |
| `GET /scim/v2/` | management_endpoints/scim/scim_v2.py:1581 | SCIM 베이스(리소스 발견) | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI○ / 분리배포 allowlist 밖 |
| `GET /scim/v2/ResourceTypes` | management_endpoints/scim/scim_v2.py:1608 | SCIM ResourceTypes | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `GET /scim/v2/ResourceTypes/{resource_type_id}` | management_endpoints/scim/scim_v2.py:1633 | SCIM ResourceType 1건 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `GET /scim/v2/Schemas` | management_endpoints/scim/scim_v2.py:1657 | SCIM Schemas | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `GET /scim/v2/Schemas/{schema_id:path}` | management_endpoints/scim/scim_v2.py:1681 | SCIM Schema 1건 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `GET /scim/v2/ServiceProviderConfig` | management_endpoints/scim/scim_v2.py:1704 | SCIM ServiceProviderConfig | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `GET /scim/v2/Users` | management_endpoints/scim/scim_v2.py:1738 | SCIM 사용자 목록 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `GET /scim/v2/Users/{user_id}` | management_endpoints/scim/scim_v2.py:1806 | SCIM 사용자 1건 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `POST /scim/v2/Users` | management_endpoints/scim/scim_v2.py:1830 | SCIM 사용자 생성 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `PUT /scim/v2/Users/{user_id}` | management_endpoints/scim/scim_v2.py:1905 | SCIM 사용자 전체 교체 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `DELETE /scim/v2/Users/{user_id}` | management_endpoints/scim/scim_v2.py:1994 | SCIM 사용자 삭제 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `GET /scim/v2/placeholders` | management_endpoints/scim/scim_v2.py:2055 | SSO 식별자와 겹치는 placeholder 사용자 목록 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `POST /scim/v2/placeholders/{user_id}/merge` | management_endpoints/scim/scim_v2.py:2093 | placeholder 사용자를 실제 계정에 병합 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `PATCH /scim/v2/Users/{user_id}` | management_endpoints/scim/scim_v2.py:2736 | SCIM 사용자 부분 수정 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `GET /scim/v2/Groups` | management_endpoints/scim/scim_v2.py:2818 | SCIM 그룹 목록 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `GET /scim/v2/Groups/{group_id}` | management_endpoints/scim/scim_v2.py:2898 | SCIM 그룹 1건 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `POST /scim/v2/Groups` | management_endpoints/scim/scim_v2.py:2953 | SCIM 그룹 생성 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `PUT /scim/v2/Groups/{group_id}` | management_endpoints/scim/scim_v2.py:3007 | SCIM 그룹 교체 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `DELETE /scim/v2/Groups/{group_id}` | management_endpoints/scim/scim_v2.py:3074 | SCIM 그룹 삭제 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |
| `PATCH /scim/v2/Groups/{group_id}` | management_endpoints/scim/scim_v2.py:3421 | SCIM 그룹 부분 수정 | UserTable, TeamTable | 유료: 라우터 의존성 premium_user_check(scim_v2.py:289-293) | **제외** 전부 유료 게이트이고 Keycloak 이 사용자·그룹을 관리 | UI× / 분리배포 allowlist 밖 |

### MCP 서버 관리 (42개)

주요 테이블: MCPServerTable(+MCPToolsetTable, MCPUserCredentials, MCPUserEnvVars, MCPServerOAuthClient, ObjectPermissionTable)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `GET /v1/mcp/tools` | management_endpoints/mcp_management_endpoints.py:1081 | MCP 도구 목록(현재 키 기준) | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI× / 분리배포 노출 |
| `GET /v1/mcp/access_groups` | management_endpoints/mcp_management_endpoints.py:1106 | MCP 접근 그룹 목록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/network/client-ip` | management_endpoints/mcp_management_endpoints.py:1145 | 호출자 IP 조회(네트워크 판정) | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/registry.json` | management_endpoints/mcp_management_endpoints.py:1157 | MCP 레지스트리 JSON | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI× / 분리배포 노출 |
| `GET /v1/mcp/server` | management_endpoints/mcp_management_endpoints.py:1379 | MCP 서버 목록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/server/health` | management_endpoints/mcp_management_endpoints.py:1513 | MCP 서버 상태 점검 | MCPServerTable(+MCPToolsetTable, MCPUser*) | 외부: 등록된 MCP 서버·OAuth 서버(설정 시) | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/server/register` | management_endpoints/mcp_management_endpoints.py:1579 | 팀원이 MCP 서버 등록 신청 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/sessions` | management_endpoints/mcp_management_endpoints.py:1655 | MCP 게이트웨이 세션 목록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `DELETE /v1/mcp/sessions` | management_endpoints/mcp_management_endpoints.py:1679 | MCP 게이트웨이 세션 삭제 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/server/submissions` | management_endpoints/mcp_management_endpoints.py:1714 | 사용자가 제출한 MCP 서버 심사 목록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI× / 분리배포 노출 |
| `POST /v1/mcp/server/{server_id}/pin` | management_endpoints/mcp_management_endpoints.py:1744 | MCP 서버 도구 고정 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI× / 분리배포 노출 |
| `DELETE /v1/mcp/server/{server_id}/pin` | management_endpoints/mcp_management_endpoints.py:1785 | MCP 도구 고정 해제 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI× / 분리배포 노출 |
| `PUT /v1/mcp/server/{server_id}/approve` | management_endpoints/mcp_management_endpoints.py:1828 | 제출된 MCP 서버 승인 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI× / 분리배포 노출 |
| `PUT /v1/mcp/server/{server_id}/reject` | management_endpoints/mcp_management_endpoints.py:1872 | 제출된 MCP 서버 반려 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI× / 분리배포 노출 |
| `GET /v1/mcp/server/{server_id}` | management_endpoints/mcp_management_endpoints.py:1919 | MCP 서버 1건 조회 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/server` | management_endpoints/mcp_management_endpoints.py:2009 | MCP 서버 추가 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/server/import` | management_endpoints/mcp_management_endpoints.py:2107 | MCP 커넥터 일괄 가져오기(Claude Desktop 형식) | MCPServerTable(+MCPToolsetTable, MCPUser*) | 외부: 등록된 MCP 서버·OAuth 서버(설정 시) | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/server/oauth/session` | management_endpoints/mcp_management_endpoints.py:2217 | MCP 서버 정보 임시(약 5분) 캐시 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/server/oauth/{server_id}/authorize` | management_endpoints/mcp_management_endpoints.py:2427 | MCP OAuth 인가 시작 | MCPServerTable(+MCPToolsetTable, MCPUser*) | 외부: 등록된 MCP 서버·OAuth 서버(설정 시) | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/server/oauth/{server_id}/token` | management_endpoints/mcp_management_endpoints.py:2486 | MCP OAuth 토큰 교환 | MCPServerTable(+MCPToolsetTable, MCPUser*) | 외부: 등록된 MCP 서버·OAuth 서버(설정 시) | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/server/oauth/{server_id}/register` | management_endpoints/mcp_management_endpoints.py:2549 | MCP OAuth 클라이언트 등록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `DELETE /v1/mcp/server/{server_id}` | management_endpoints/mcp_management_endpoints.py:2578 | MCP 서버 삭제 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/server/{server_id}/user-credential` | management_endpoints/mcp_management_endpoints.py:2644 | 호출자 BYOK 자격 저장 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `DELETE /v1/mcp/server/{server_id}/user-credential` | management_endpoints/mcp_management_endpoints.py:2681 | BYOK 자격 삭제 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/server/{server_id}/oauth-user-credential` | management_endpoints/mcp_management_endpoints.py:2712 | 사용자 OAuth2 토큰 저장 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `DELETE /v1/mcp/server/{server_id}/oauth-user-credential` | management_endpoints/mcp_management_endpoints.py:2781 | 사용자 OAuth2 자격 해제 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/server/{server_id}/oauth-user-credential/status` | management_endpoints/mcp_management_endpoints.py:2819 | OAuth 자격 유무·만료 조회(값 비노출) | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/user-credentials` | management_endpoints/mcp_management_endpoints.py:2857 | 내가 연결한 MCP 서버 목록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/server/{server_id}/user-credentials` | management_endpoints/mcp_management_endpoints.py:2926 | MCP 서버별 사용자 자격 목록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/server/{server_id}/user-env-vars` | management_endpoints/mcp_management_endpoints.py:3045 | MCP 사용자 환경변수 조회 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/server/{server_id}/user-env-vars` | management_endpoints/mcp_management_endpoints.py:3067 | MCP 사용자 환경변수 저장 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `DELETE /v1/mcp/server/{server_id}/user-env-vars` | management_endpoints/mcp_management_endpoints.py:3109 | MCP 사용자 환경변수 삭제 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/user-env-vars/status` | management_endpoints/mcp_management_endpoints.py:3136 | MCP 환경변수 설정 여부 목록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `PUT /v1/mcp/server` | management_endpoints/mcp_management_endpoints.py:3190 | MCP 서버 수정 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/make_public` | management_endpoints/mcp_management_endpoints.py:3348 | MCP 서버를 AI Hub 에 공개 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/discover` | management_endpoints/mcp_management_endpoints.py:3436 | 알려진 MCP 서버 큐레이션 목록(정적) | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/openapi-registry` | management_endpoints/mcp_management_endpoints.py:3502 | OpenAPI 레지스트리 목록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `POST /v1/mcp/toolset` | management_endpoints/mcp_management_endpoints.py:3539 | MCP 툴셋 생성 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/toolset` | management_endpoints/mcp_management_endpoints.py:3579 | 키가 쓸 수 있는 툴셋 목록 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `GET /v1/mcp/toolset/{toolset_id}` | management_endpoints/mcp_management_endpoints.py:3599 | 툴셋 1건 조회 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `PUT /v1/mcp/toolset` | management_endpoints/mcp_management_endpoints.py:3624 | 툴셋 부분 수정 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |
| `DELETE /v1/mcp/toolset/{toolset_id}` | management_endpoints/mcp_management_endpoints.py:3675 | 툴셋 삭제 | MCPServerTable(+MCPToolsetTable, MCPUser*) | - | **제외** MCP 게이트웨이는 OpenAI 호환 추론 범위 밖. 필요하면 별도 결정 | UI○ / 분리배포 노출 |

### 정책·툴·컴플라이언스·워크플로 (28개)

주요 테이블: PolicyTable, PolicyAttachmentTable, ToolTable, WorkflowRun/Event/Message

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /compliance/eu-ai-act` | management_endpoints/compliance_endpoints.py:26 | spend 로그 1건의 EU AI Act 준수 검사 | SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 allowlist 밖 |
| `POST /compliance/gdpr` | management_endpoints/compliance_endpoints.py:55 | spend 로그 1건의 GDPR 준수 검사 | SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 allowlist 밖 |
| `POST /utils/test_policies_and_guardrails` | management_endpoints/policy_endpoints/endpoints.py:281 | 정책·가드레일을 입력에 적용해 시험 | (없음) | 외부: LLM 호출(프록시 모델) | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI× / 분리배포 노출 |
| `POST /policy/validate` | management_endpoints/policy_endpoints/endpoints.py:384 | 정책 설정 검증 | PolicyTable, PolicyAttachmentTable | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI× / 분리배포 allowlist 밖 |
| `GET /policy/list` | management_endpoints/policy_endpoints/endpoints.py:470 | 로드된 정책 목록 | PolicyTable, PolicyAttachmentTable | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI× / 분리배포 allowlist 밖 |
| `GET /policy/info/{policy_name}` | management_endpoints/policy_endpoints/endpoints.py:509 | 정책 상세 | PolicyTable, PolicyAttachmentTable | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 allowlist 밖 |
| `POST /policy/test` | management_endpoints/policy_endpoints/endpoints.py:566 | 요청에 맞는 정책 시험 | PolicyTable, PolicyAttachmentTable | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI× / 분리배포 allowlist 밖 |
| `GET /policy/templates` | management_endpoints/policy_endpoints/endpoints.py:644 | UI 용 정책 템플릿 | PolicyTable, PolicyAttachmentTable | 외부: GitHub raw(policy_templates.json), LITELLM_LOCAL_POLICY_TEMPLATES=true 로 차단 가능 | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 allowlist 밖 |
| `POST /policy/templates/enrich` | management_endpoints/policy_endpoints/endpoints.py:732 | 정책 템플릿을 LLM 으로 보강 | PolicyTable, PolicyAttachmentTable | 외부: LLM 호출(프록시 모델) | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 allowlist 밖 |
| `POST /policy/templates/enrich/stream` | management_endpoints/policy_endpoints/endpoints.py:891 | 정책 템플릿 보강(SSE 스트림) | PolicyTable, PolicyAttachmentTable | 외부: LLM 호출(프록시 모델) | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 allowlist 밖 |
| `POST /policy/templates/suggest` | management_endpoints/policy_endpoints/endpoints.py:1137 | LLM 으로 정책 템플릿 추천 | PolicyTable, PolicyAttachmentTable | 외부: LLM 호출(프록시 모델) | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 allowlist 밖 |
| `POST /policy/templates/test` | management_endpoints/policy_endpoints/endpoints.py:1184 | 정책 템플릿 가드레일 시험 | PolicyTable, PolicyAttachmentTable | 외부: LLM 호출(프록시 모델) | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 allowlist 밖 |
| `GET /v1/tool/policy/options` | management_endpoints/tool_management_endpoints.py:163 | 툴 정책 선택지 | ToolTable, SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `GET /v1/tool/list` | management_endpoints/tool_management_endpoints.py:179 | 자동 발견된 툴과 정책 목록 | ToolTable, SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `GET /v1/tool/spend` | management_endpoints/tool_management_endpoints.py:235 | 툴별 사용액 | ToolTable, SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `GET /v1/tool/{tool_name:path}/detail` | management_endpoints/tool_management_endpoints.py:316 | 툴 상세(정책 override 포함) | ToolTable, SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `GET /v1/tool/{tool_name:path}/logs` | management_endpoints/tool_management_endpoints.py:406 | 툴 호출 spend 로그 | ToolTable, SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `GET /v1/tool/{tool_name:path}` | management_endpoints/tool_management_endpoints.py:496 | 툴 1건 조회 | ToolTable, SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI× / 분리배포 노출 |
| `POST /v1/tool/policy` | management_endpoints/tool_management_endpoints.py:587 | 툴 입력·출력 정책 설정 | ToolTable, SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `DELETE /v1/tool/{tool_name:path}/overrides` | management_endpoints/tool_management_endpoints.py:700 | 툴 정책 override 제거 | ToolTable, SpendLogs | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `POST /v1/workflows/runs` | management_endpoints/workflow_management_endpoints.py:221 | 워크플로 실행 생성 | WorkflowRun/Event/Message | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI× / 분리배포 노출 |
| `GET /v1/workflows/runs` | management_endpoints/workflow_management_endpoints.py:256 | 워크플로 실행 목록 | WorkflowRun/Event/Message | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI× / 분리배포 노출 |
| `GET /v1/workflows/runs/{run_id}` | management_endpoints/workflow_management_endpoints.py:301 | 워크플로 실행 조회 | WorkflowRun/Event/Message | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI× / 분리배포 노출 |
| `PATCH /v1/workflows/runs/{run_id}` | management_endpoints/workflow_management_endpoints.py:335 | 워크플로 실행 상태 수정 | WorkflowRun/Event/Message | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI× / 분리배포 노출 |
| `POST /v1/workflows/runs/{run_id}/events` | management_endpoints/workflow_management_endpoints.py:380 | 실행 이벤트 추가 | WorkflowRun/Event/Message | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `GET /v1/workflows/runs/{run_id}/events` | management_endpoints/workflow_management_endpoints.py:441 | 실행 이벤트 조회 | WorkflowRun/Event/Message | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `POST /v1/workflows/runs/{run_id}/messages` | management_endpoints/workflow_management_endpoints.py:471 | 대화 메시지 추가 | WorkflowRun/Event/Message | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |
| `GET /v1/workflows/runs/{run_id}/messages` | management_endpoints/workflow_management_endpoints.py:525 | 대화 메시지 조회 | WorkflowRun/Event/Message | - | **제외** 가드레일·정책·툴·워크플로는 1차 범위 밖 | UI○ / 분리배포 노출 |

### 사용량·분석 조회 (58개)

주요 테이블: DailyUserSpend/TeamSpend/TagSpend/OrganizationSpend/EndUserSpend/AgentSpend, DailyModelUsage, DailyGateway*, SpendLogs

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /cost/estimate` | management_endpoints/cost_tracking_settings.py:573 | 모델·토큰 수로 비용 추정 | DailyUserSpend/TeamSpend/TagSpend/OrganizationSpend/EndUserSpend/AgentSpend, DailyModelUsage, DailyGateway*, SpendLogs | - | **제외** Anthropic 가격 기반 추정 | UI○ / 분리배포 노출 |
| `GET /customer/daily/activity` | management_endpoints/customer_endpoints.py:911 | 고객별 일별 사용량(구 방식) | Daily*Spend, SpendLogs | - | **후순위** 사용량 핵심은 spend 로그와 키·팀 합계(타 영역). 변종 집계는 나중 | UI× / 분리배포 노출 |
| `GET /end_user/daily/activity` | management_endpoints/customer_endpoints.py:917 | 고객별 일별 사용량(구 방식) | Daily*Spend, SpendLogs | - | **제외** end_user 별칭 경로 중복(customer 로 통일) | UI× / 분리배포 노출 |
| `GET /gateway/daily/activity` | management_endpoints/gateway_request_endpoints.py:124 | 게이트웨이 일별 성공·실패 요청 수(ASGI 계측) | DailyGatewayRequests/FailedRequests | - | **후순위** 사용량 화면 세부 집계 | UI○ / 분리배포 노출 |
| `GET /user/daily/activity` | management_endpoints/internal_user_endpoints.py:2845 | 사용자별 일별 사용량(구 방식, 페이지) | Daily*Spend, SpendLogs | - | **필수** 사용자·팀 사용량 조회는 운영 핵심 | UI× / 분리배포 노출 |
| `GET /management/v1/spend_logs/end_users` | management_endpoints/management_v1/spend_logs.py:154 | spend 로그에 나온 고유 end_user 목록 | SpendLogs | - | **후순위** 사용량 화면 세부 집계 | UI× / 분리배포 노출 |
| `GET /management/v1/spend_logs/users` | management_endpoints/management_v1/spend_logs.py:207 | spend 로그에 나온 고유 내부 사용자 목록 | SpendLogs | - | **후순위** 사용량 화면 세부 집계 | UI× / 분리배포 노출 |
| `POST /cost/predict-cache` | management_endpoints/prompt_cache_prediction.py:67 | Anthropic 프롬프트 캐시 비용 비교 예측 | DailyUserSpend/TeamSpend/TagSpend/OrganizationSpend/EndUserSpend/AgentSpend, DailyModelUsage, DailyGateway*, SpendLogs | - | **제외** 외부 모델 호출·일부 기능은 범위 밖 | UI× / 분리배포 노출 |
| `GET /cost_optimization/prompt_caching/requests` | management_endpoints/prompt_caching_requests.py:129 | 프롬프트 캐싱 요청 목록 | DailyUserSpend/TeamSpend/TagSpend/OrganizationSpend/EndUserSpend/AgentSpend, DailyModelUsage, DailyGateway*, SpendLogs | - | **제외** 외부 모델 호출·일부 기능은 범위 밖 | UI× / 분리배포 노출 |
| `GET /gateway/errors/activity` | management_endpoints/request_error_endpoints.py:221 | 실패 요청의 상태코드별 시계열·키·팀 | DailyRequestErrors | - | **후순위** 사용량 화면 세부 집계 | UI○ / 분리배포 노출 |
| `GET /tag/daily/activity` | management_endpoints/tag_management_endpoints.py:727 | 태그별 일별 사용량(구 방식) | Daily*Spend, SpendLogs | - | **후순위** 사용량 핵심은 spend 로그와 키·팀 합계(타 영역). 변종 집계는 나중 | UI× / 분리배포 노출 |
| `GET /team/daily/activity` | management_endpoints/team_endpoints.py:7029 | 팀별 일별 사용량(구 방식, 페이지) | Daily*Spend, SpendLogs | - | **필수** 사용자·팀 사용량 조회는 운영 핵심 | UI× / 분리배포 노출 |
| `GET /team/spend/by_user` | management_endpoints/team_endpoints.py:7151 | 팀 내 사용자별 사용액 | TeamTable(+TeamMembership, UserTable, VerificationToken) | - | **후순위** 사용량 화면 세부 집계 | UI○ / 분리배포 노출 |
| `POST /usage/ai/chat` | management_endpoints/usage_endpoints/endpoints.py:35 | 사용량 데이터 AI 채팅(SSE, 프록시 모델 호출) | DailyUserSpend/TeamSpend/TagSpend/OrganizationSpend/EndUserSpend/AgentSpend, DailyModelUsage, DailyGateway*, SpendLogs | 외부: LLM 호출(프록시 모델) | **제외** 외부 모델 호출·일부 기능은 범위 밖 | UI○ / 분리배포 노출 |
| `GET /tag/distinct` | management_endpoints/user_agent_analytics_endpoints.py:164 | user-agent 태그 목록 | TagTable | - | **제외** user-agent 분석은 부가 기능 | UI○ / 분리배포 노출 |
| `GET /tag/dau` | management_endpoints/user_agent_analytics_endpoints.py:215 | 태그별 일간 활성 사용자 | TagTable | - | **제외** user-agent 분석은 부가 기능 | UI○ / 분리배포 노출 |
| `GET /tag/wau` | management_endpoints/user_agent_analytics_endpoints.py:307 | 태그별 주간 활성 사용자 | TagTable | - | **제외** user-agent 분석은 부가 기능 | UI○ / 분리배포 노출 |
| `GET /tag/mau` | management_endpoints/user_agent_analytics_endpoints.py:428 | 태그별 월간 활성 사용자 | TagTable | - | **제외** user-agent 분석은 부가 기능 | UI○ / 분리배포 노출 |
| `GET /tag/summary` | management_endpoints/user_agent_analytics_endpoints.py:549 | 태그별 요약(사용자·요청·토큰) | TagTable | - | **제외** user-agent 분석은 부가 기능 | UI○ / 분리배포 노출 |
| `GET /tag/user-agent/per-user-analytics` | management_endpoints/user_agent_analytics_endpoints.py:654 | 사용자별 요청·토큰 분석 | TagTable | - | **제외** user-agent 분석은 부가 기능 | UI○ / 분리배포 노출 |
| `GET /global/activity/cache_hits` | analytics_endpoints/analytics_endpoints.py:27 | 캐시 히트 활동(캐시 대시보드) | DailyUserSpend/TeamSpend/TagSpend/OrganizationSpend/EndUserSpend/AgentSpend, DailyModelUsage, DailyGateway*, SpendLogs | - | **제외** 외부 모델 호출·일부 기능은 범위 밖 | UI× / 분리배포 노출 |
| `GET /user/daily/activity/aggregated` | management_endpoints/daily_activity_routes.py:315 | 사용자 일별 사용량 합계 집계(동적 등록) | Daily*Spend, SpendLogs | - | **필수** 사용자·팀 사용량 조회는 운영 핵심 | UI○ / 분리배포 노출 |
| `GET /user/daily/activity/aggregated/keys` | management_endpoints/daily_activity_routes.py:387 | 사용자 일별 사용량 키별 집계(페이지)(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /user/daily/activity/aggregated/search` | management_endpoints/daily_activity_routes.py:436 | 사용자 일별 사용량 키 검색(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /user/daily/activity/aggregated/model_top_keys` | management_endpoints/daily_activity_routes.py:486 | 사용자 일별 사용량 모델별 상위 키(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /user/daily/activity/export` | management_endpoints/daily_activity_routes.py:538 | 사용자 일별 사용량 CSV·JSON 내보내기(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI× / 분리배포 노출 |
| `GET /user/daily/activity/aggregated/users` | management_endpoints/daily_activity_routes.py:590 | 사용자 일별 사용량 사용자별 집계(페이지)(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /user/daily/activity/aggregated/cache_leakage_keys` | management_endpoints/daily_activity_routes.py:634 | 사용자 일별 사용량 캐시 누수 키(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /team/daily/activity/aggregated` | management_endpoints/daily_activity_routes.py:315 | 팀 일별 사용량 합계 집계(동적 등록) | Daily*Spend, SpendLogs | - | **필수** 사용자·팀 사용량 조회는 운영 핵심 | UI○ / 분리배포 노출 |
| `GET /team/daily/activity/aggregated/keys` | management_endpoints/daily_activity_routes.py:387 | 팀 일별 사용량 키별 집계(페이지)(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /team/daily/activity/aggregated/search` | management_endpoints/daily_activity_routes.py:436 | 팀 일별 사용량 키 검색(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /team/daily/activity/aggregated/model_top_keys` | management_endpoints/daily_activity_routes.py:486 | 팀 일별 사용량 모델별 상위 키(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /team/daily/activity/export` | management_endpoints/daily_activity_routes.py:538 | 팀 일별 사용량 CSV·JSON 내보내기(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI× / 분리배포 노출 |
| `GET /tag/daily/activity/aggregated` | management_endpoints/daily_activity_routes.py:315 | 태그 일별 사용량 합계 집계(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /tag/daily/activity/aggregated/keys` | management_endpoints/daily_activity_routes.py:387 | 태그 일별 사용량 키별 집계(페이지)(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /tag/daily/activity/aggregated/search` | management_endpoints/daily_activity_routes.py:436 | 태그 일별 사용량 키 검색(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /tag/daily/activity/aggregated/model_top_keys` | management_endpoints/daily_activity_routes.py:486 | 태그 일별 사용량 모델별 상위 키(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /tag/daily/activity/export` | management_endpoints/daily_activity_routes.py:538 | 태그 일별 사용량 CSV·JSON 내보내기(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI× / 분리배포 노출 |
| `GET /organization/daily/activity/aggregated` | management_endpoints/daily_activity_routes.py:315 | 조직 일별 사용량 합계 집계(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /organization/daily/activity/aggregated/keys` | management_endpoints/daily_activity_routes.py:387 | 조직 일별 사용량 키별 집계(페이지)(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /organization/daily/activity/aggregated/search` | management_endpoints/daily_activity_routes.py:436 | 조직 일별 사용량 키 검색(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /organization/daily/activity/aggregated/model_top_keys` | management_endpoints/daily_activity_routes.py:486 | 조직 일별 사용량 모델별 상위 키(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /organization/daily/activity/export` | management_endpoints/daily_activity_routes.py:538 | 조직 일별 사용량 CSV·JSON 내보내기(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI× / 분리배포 노출 |
| `GET /customer/daily/activity/aggregated` | management_endpoints/daily_activity_routes.py:315 | 고객 일별 사용량 합계 집계(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /customer/daily/activity/aggregated/keys` | management_endpoints/daily_activity_routes.py:387 | 고객 일별 사용량 키별 집계(페이지)(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /customer/daily/activity/aggregated/search` | management_endpoints/daily_activity_routes.py:436 | 고객 일별 사용량 키 검색(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /customer/daily/activity/aggregated/model_top_keys` | management_endpoints/daily_activity_routes.py:486 | 고객 일별 사용량 모델별 상위 키(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /customer/daily/activity/export` | management_endpoints/daily_activity_routes.py:538 | 고객 일별 사용량 CSV·JSON 내보내기(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI× / 분리배포 노출 |
| `GET /end_user/daily/activity/aggregated` | management_endpoints/daily_activity_routes.py:315 | 고객(end_user 별칭) 일별 사용량 합계 집계(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /end_user/daily/activity/aggregated/keys` | management_endpoints/daily_activity_routes.py:387 | 고객(end_user 별칭) 일별 사용량 키별 집계(페이지)(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /end_user/daily/activity/aggregated/search` | management_endpoints/daily_activity_routes.py:436 | 고객(end_user 별칭) 일별 사용량 키 검색(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /end_user/daily/activity/aggregated/model_top_keys` | management_endpoints/daily_activity_routes.py:486 | 고객(end_user 별칭) 일별 사용량 모델별 상위 키(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /end_user/daily/activity/export` | management_endpoints/daily_activity_routes.py:538 | 고객(end_user 별칭) 일별 사용량 CSV·JSON 내보내기(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI× / 분리배포 노출 |
| `GET /agent/daily/activity/aggregated` | management_endpoints/daily_activity_routes.py:315 | 에이전트 일별 사용량 합계 집계(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /agent/daily/activity/aggregated/keys` | management_endpoints/daily_activity_routes.py:387 | 에이전트 일별 사용량 키별 집계(페이지)(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /agent/daily/activity/aggregated/search` | management_endpoints/daily_activity_routes.py:436 | 에이전트 일별 사용량 키 검색(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /agent/daily/activity/aggregated/model_top_keys` | management_endpoints/daily_activity_routes.py:486 | 에이전트 일별 사용량 모델별 상위 키(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI○ / 분리배포 노출 |
| `GET /agent/daily/activity/export` | management_endpoints/daily_activity_routes.py:538 | 에이전트 일별 사용량 CSV·JSON 내보내기(동적 등록) | Daily*Spend, SpendLogs | - | **후순위** 사용량 세부 집계 | UI× / 분리배포 노출 |

### 엔터프라이즈·공개·기타 (30개)

주요 테이블: (없음)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `POST /moyai/connect/start` | moyai_endpoints.py:144 | Moyai(Slack) 연결 시작 |  | 외부: Slack 연동 워커 | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /api/plugins` | plugin_routes.py:196 | 등록된 플러그인 목록 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |
| `GET /api/plugins/auth-token` | plugin_routes.py:217 | 플러그인 세션 클레임 발급 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |
| `API_ROUTE /plugin-proxy/{plugin_name}/{path:path}` | plugin_routes.py:245 | 플러그인 백엔드 역방향 프록시 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |
| `GET /public/model_hub` | public_endpoints/public_endpoints.py:267 | 공개 모델 허브 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /public/agent_hub` | public_endpoints/public_endpoints.py:314 | 공개 에이전트 허브 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /public/mcp_hub` | public_endpoints/public_endpoints.py:338 | 공개 MCP 허브 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /public/skill_hub` | public_endpoints/public_endpoints.py:364 | 공개 스킬 허브 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /public/model_hub/info` | public_endpoints/public_endpoints.py:411 | 모델 허브 안내 정보 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /public/providers` | public_endpoints/public_endpoints.py:435 | 지원 제공자 목록 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |
| `GET /public/providers/fields` | public_endpoints/public_endpoints.py:448 | 제공자 입력 필드 메타데이터 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /public/litellm_model_cost_map` | public_endpoints/public_endpoints.py:494 | 모델 단가표 공개 조회 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |
| `GET /public/litellm_blog_posts` | public_endpoints/public_endpoints.py:516 | LiteLLM 블로그 RSS 최신 글 | (없음) | 외부: 블로그 RSS(LITELLM_LOCAL_BLOG_POSTS 로 차단) | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /public/endpoints` | public_endpoints/public_endpoints.py:620 | 프록시 엔드포인트 지원 목록 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |
| `GET /public/agents/fields` | public_endpoints/public_endpoints.py:638 | 에이전트 유형 메타데이터 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /model_hub` | public_endpoints/public_v1/model_hub.py:227 | 공개 모델 그룹 목록(페이지) | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /model_hub/{facet}` | public_endpoints/public_v1/model_hub.py:277 | 공개 허브 제공자·모드·기능 목록 | (없음) | - | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |
| `POST /telemetry/ui_events` | telemetry/endpoints.py:62 | UI 페이지 이동 이벤트 기록(동의 시) | TelemetryReport | - | **제외** telemetry 0 원칙과 충돌 | UI× / 분리배포 노출 |
| `GET /telemetry/reports` | telemetry/endpoints.py:78 | 저장된 텔레메트리 보고서 내보내기 | TelemetryReport | 외부: 설정 시 HTTP 목적지로 전송(기본 없음) | **제외** telemetry 0 원칙과 충돌 | UI× / 분리배포 노출 |
| `GET /telemetry/settings` | telemetry/endpoints.py:208 | 텔레메트리 그룹·동의 설정 조회 | TelemetryReport | - | **제외** telemetry 0 원칙과 충돌 | UI○ / 분리배포 노출 |
| `PUT /telemetry/settings` | telemetry/endpoints.py:221 | 텔레메트리 설정 저장 | TelemetryReport | - | **제외** telemetry 0 원칙과 충돌 | UI○ / 분리배포 노출 |
| `GET /telemetry/ui_events/enabled` | telemetry/endpoints.py:270 | UI 이벤트 수집 켜짐 여부 | TelemetryReport | - | **제외** telemetry 0 원칙과 충돌 | UI× / 분리배포 노출 |
| `GET /robots.txt` | enterprise/litellm_enterprise/proxy/enterprise_routes.py:22 | robots.txt(크롤러 차단) | (없음) | enterprise 모듈(litellm_enterprise) | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |
| `GET /audit` | enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:63 | 감사 로그 목록(필터·페이지) | AuditLog | 일부 유료: 기록은 premium_user 일 때만(audit_logs.py:286,317) | **후순위** 감사 로그 조회(기록은 upstream 에서 유료) | UI○ / 분리배포 노출 |
| `GET /audit/{id}` | enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:186 | 감사 로그 1건 | AuditLog | 일부 유료: 기록은 premium_user 일 때만(audit_logs.py:286,317) | **후순위** 감사 로그 조회(기록은 upstream 에서 유료) | UI× / 분리배포 노출 |
| `GET /email/event_settings` | enterprise/litellm_enterprise/enterprise_callbacks/send_emails/endpoints.py:117 | 이메일 이벤트 설정 조회 | Config(general_settings) | enterprise 모듈(litellm_enterprise); 외부: SMTP·메일 서비스(설정 시) | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `PATCH /email/event_settings` | enterprise/litellm_enterprise/enterprise_callbacks/send_emails/endpoints.py:150 | 이메일 이벤트 설정 수정 | Config(general_settings) | enterprise 모듈(litellm_enterprise) | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `POST /email/event_settings/reset` | enterprise/litellm_enterprise/enterprise_callbacks/send_emails/endpoints.py:186 | 이메일 이벤트 설정 초기화 | Config(general_settings) | enterprise 모듈(litellm_enterprise) | **제외** upstream 마케팅·허브·부가 연동 | UI○ / 분리배포 노출 |
| `GET /liteadmin/slack/connect/{token}` | enterprise/litellm_enterprise/proxy/liteadmin.py:219 | LiteAdmin Slack 연결 페이지(HTML) |  | enterprise 모듈(litellm_enterprise) | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |
| `POST /liteadmin/slack/connect/{token}` | enterprise/litellm_enterprise/proxy/liteadmin.py:251 | LiteAdmin Slack 계정 연결 처리 |  | enterprise 모듈(litellm_enterprise); 외부: Slack 연동 워커 | **제외** upstream 마케팅·허브·부가 연동 | UI× / 분리배포 노출 |

### ROI 계산기(미마운트) (23개)

주요 테이블: (없음) 설정은 UISettings/Config 로 추정

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `GET /roi-calculator/settings` | management_endpoints/roi_calculator_endpoints.py:328 | ROI 계산기 설정 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `PUT /roi-calculator/settings` | management_endpoints/roi_calculator_endpoints.py:340 | ROI 계산기 설정 (PUT) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/repositories` | management_endpoints/roi_calculator_endpoints.py:441 | ROI 대상 저장소 목록 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/sync` | management_endpoints/roi_calculator_endpoints.py:469 | ROI 데이터 동기화 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `POST /roi-calculator/sync` | management_endpoints/roi_calculator_endpoints.py:486 | ROI 데이터 동기화 (POST) |  | 외부: GitHub OAuth·API | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `DELETE /roi-calculator/sync` | management_endpoints/roi_calculator_endpoints.py:517 | ROI 데이터 동기화 (DELETE) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/report` | management_endpoints/roi_calculator_endpoints.py:533 | ROI 보고서 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `PUT /roi-calculator/identity-map` | management_endpoints/roi_calculator_endpoints.py:556 | ROI 사용자 매핑 수정 (PUT) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `POST /roi-calculator/connections/test` | management_endpoints/roi_calculator_endpoints.py:677 | ROI 연동 테스트 (POST) |  | 외부: GitHub OAuth·API | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `POST /roi-calculator/setup/reset` | management_endpoints/roi_calculator_endpoints.py:698 | ROI 설정 초기화 (POST) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/observed/settings` | management_endpoints/roi_observed_endpoints.py:133 | (관측) ROI 계산기 설정 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `PUT /roi-calculator/observed/settings` | management_endpoints/roi_observed_endpoints.py:141 | (관측) ROI 계산기 설정 (PUT) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/observed/report` | management_endpoints/roi_observed_endpoints.py:213 | (관측) ROI 보고서 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/observed/identities` | management_endpoints/roi_observed_endpoints.py:223 | (관측) ROI 관측 사용자 매핑 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `PUT /roi-calculator/observed/identities` | management_endpoints/roi_observed_endpoints.py:253 | (관측) ROI 관측 사용자 매핑 (PUT) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/observed/sync` | management_endpoints/roi_observed_endpoints.py:312 | (관측) ROI 데이터 동기화 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `POST /roi-calculator/observed/sync` | management_endpoints/roi_observed_endpoints.py:373 | (관측) ROI 데이터 동기화 (POST) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `DELETE /roi-calculator/observed/sync` | management_endpoints/roi_observed_endpoints.py:386 | (관측) ROI 데이터 동기화 (DELETE) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/observed/repositories` | management_endpoints/roi_observed_endpoints.py:421 | (관측) ROI 대상 저장소 목록 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/observed/apps` | management_endpoints/roi_observed_endpoints.py:447 | (관측) ROI 관측 앱 목록 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `POST /roi-calculator/observed/oauth/{provider}/start` | management_endpoints/roi_observed_endpoints.py:461 | (관측) ROI 관측 OAuth 시작 (POST) |  | 외부: GitHub OAuth·API | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/observed/oauth/{provider}/callback` | management_endpoints/roi_observed_endpoints.py:504 | (관측) ROI 관측 OAuth 콜백 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |
| `GET /roi-calculator/observed/oauth/github/installed` | management_endpoints/roi_observed_endpoints.py:557 | (관측) GitHub 앱 설치 콜백 (GET) |  | - | **제외** 라우터에 연결되지 않은 죽은 코드이고 GitHub 연동 | UI× / 분리배포 allowlist 밖 |

### 죽은 코드(미마운트) (1개)

주요 테이블: (없음)

| 엔드포인트 | 구현 위치 | 동작 요약 | DB 테이블 | 유료·외부 | 권장 / 이유 | UI·분리배포 |
|---|---|---|---|---|---|---|
| `GET /config/pass_through_endpoints/settings` | config_management_endpoints/pass_through_endpoints.py:17 | 빈 스텁(본문 pass) | Config | - | **제외** 본문이 pass 인 스텁이고 라우터도 연결 안 됨 | UI× / 분리배포 노출 |

## 2. config.yaml 로딩과 DB 설정 동기화

| 항목 | 동작 | 위치 |
|---|---|---|
| 설정 원천 | 파일 경로(YAML) 또는 S3·GCS 버킷(`LITELLM_CONFIG_BUCKET_NAME/OBJECT_KEY/TYPE`). 파일이 없으면 빈 기본 구조로 시작 | proxy_server.py:6261(get_config), 5720(_get_config_from_file) |
| 최상위 키 | model_list, litellm_settings, general_settings, router_settings, environment_variables, credential_list, search_tools, guardrails, prompts, callback_settings, finetune_settings, files_settings, assistant_settings, default_vertex_config, sandbox_tools, include(다른 파일 합치기) | proxy_server.py:6496(load_config) |
| 값 치환 | `os.environ/NAME` 형태 참조를 환경변수·시크릿 매니저 값으로 풀고 `environment_variables` 를 환경에 반영 | proxy_server.py:5993, 6034, 6431 |
| 설정 저장소 4종 | general_settings, router_settings, litellm_settings, environment_variables 가 YAML 값과 DB 값을 합치는 대상 | proxy_server.py:5671 |
| DB 덮어쓰기 | `general_settings.store_model_in_db=true` 이고 DB 가 있을 때만 `LiteLLM_Config`(param_name/param_value) 행을 읽어 위 4종에 덮어씀. 환경변수 행은 버전 비교로 오래된 값을 무시 | proxy_server.py:8351, 6300-6310 |
| DB 모델 동기화 | `LiteLLM_ProxyModelTable` 의 모델을 라우터에 반영. 시작 시 1회와 주기 작업(`add_deployment_job`, 기본 30초, `proxy_config_reload_interval_seconds`) | proxy_server.py:8529, 11355 |
| 주기 설정 재적용 | `periodic_reload_job`(같은 간격)이 설정·콜백 등 변경분을 다시 읽음. 다중 파드는 Redis pub/sub(`ConfigSyncSubscriber`)로 변경 전파 | proxy_server.py:11340, 8622 |
| DB 객체 로드 | 시작 시 가드레일·정책·벡터스토어·MCP·에이전트·프롬프트·검색툴·툴정책·SSO 설정·캐시 설정·Vault/CyberArk override 를 DB 에서 읽어 메모리 초기화 | proxy_server.py:8676 |
| 예산 초기화 작업 | `reset_budget_job` 이 `budget_duration` 만료 키·팀·사용자·예산의 spend 를 주기 초기화(`disable_reset_budget` 로 끔) | proxy_server.py:11217 부근 |
| 키 자동 회전 | `LITELLM_KEY_ROTATION_ENABLED=true` 일 때만 `key_rotation_job` | proxy_server.py:11729 |
| 관리 API 로 쓰는 경로 | `/config/update`, `/config/field/*`, `/config/callback/delete`, `/update/*`, `/cache/settings`, `/router/*`, `/fallback`, `/coordination_redis/settings`, `/config_overrides/*` 가 위 DB 행들을 갱신 | 1장 표 13·12군 |
| 죽은 코드 | `config_management_endpoints/pass_through_endpoints.py` 는 본문이 `pass` 인 스텁이고 어디서도 import 되지 않음. 실제 pass-through 설정 API(`/config/pass_through_endpoint*`)는 `pass_through_endpoints/` 에 있어 타 영역 | 해당 파일 |

신규 게이트웨이 권장: YAML 한 파일(모델·Keycloak·예산 기본값)과 7개 테이블로 충분하다. DB 로 런타임 설정을 덮어쓰는 층(위 표의 4~7행)은 제외하고, 모델 등록은 `POST /api/v1/models` 같은 관리 API 가 맡는 것이 설계서와 맞다.

## 3. UI 가 부르는 API 와 ko-dashboard

- 공식 대시보드 `ui/litellm-dashboard`(Next.js, 파일 2595개): 호출은 `components/networking.tsx`(8000줄)의 직접 호출과 `lib/http/schema.d.ts`(OpenAPI 타입)를 쓰는 `fetchClient`, 그리고 `apiClient` 로 나뉜다. 추출한 고유 경로 약 369개 가운데 이 문서 범위에서 매칭된 것이 269행이다. 나머지는 가드레일·spend·벡터스토어·에이전트·프롬프트·정책 엔진 등 타 영역이다.
- UI 가 의존하지만 현 분리 배포 allowlist 밖인 것: `/compliance/*`, `/config_overrides/*`, `/policy/templates*`, `/scim/v2`(해당 표의 "allowlist 밖" 표시). 즉 지금도 그 화면은 `backend` 에서 404 가 날 가능성이 있다(실제 동작은 확인 못함).
- UI 부트스트랩 호출: `/.well-known/litellm-ui-config`, `/get/ui_settings`, `/get_image`, `/health/readiness/details`, `/v2/team/list`, `/v2/user/info`, `/key/list` 등. 이 응답 모양을 맞추지 않으면 공식 UI 는 빈 화면이 된다.
- `ui/ko-dashboard` 는 **이 저장소에 없다**(`find` 로 확인). `GATEWAY_DESIGN.md` 1.2·7.1 이 "4 뷰(개요·로그·비용·키)를 가진 별도 React SPA, `/user/info`·`/key/list`·`/key/generate`·`/key/info`·`/spend/*`·`/global/spend*`·`/v1/models` 만 호출"한다고 적었고, 이를 `/api/v1/*` 로 옮기는 매핑이 있다. 코드는 확인 못함(다른 저장소일 가능성).
- 판단: 공식 UI 를 유지하려면 위 필수·후순위 행의 요청·응답 모양을 LiteLLM 과 같게 맞춰야 한다(약 269행 중 상당수). `/api/v1` 자체 spec 을 택하면 UI 를 새로 만들거나 ko-dashboard 를 쓰는 쪽이 싸다. 이 선택이 관리 API 범위를 가장 크게 좌우한다.

## 4. 필수로 본 최소 세트 (41행)

키: `POST /key/generate|update|delete|block|unblock`, `GET /key/info|list`, `POST /key/{key}/reset_spend`
팀: `POST /team/new|update|delete|block|unblock|member_add|member_delete|member_update`, `GET /team/info|list`
사용자: `POST /user/new|update|delete`, `GET /user/info|list`, `GET /management/v1/users/me`
모델: `POST /model/new|update|delete`, `PATCH /model/{id}/update`, `GET /model/info`, `GET /v2/model/info`, `GET /model_group/info`
프로브·세션: `GET /health/liveliness|liveness|readiness`, `GET /sso/key/generate`, `GET /sso/callback`, `POST /session/logout`
사용량: `GET /user|team/daily/activity`, `GET /user|team/daily/activity/aggregated`

예산은 별도 `/budget/*` 객체 없이 키·팀의 `max_budget`·`budget_duration` 필드와 초기화 작업으로 다룬다(설계서 4.2·4.3). 사용량 원본(spend 로그·`/spend/*`·`/global/spend*`)은 타 영역 조사 몫이다.

## 5. 놀라운 발견

1. 조직 API 11행(구 일별 사용량 포함) 전체가 유료다. `organization_endpoints.py:114` 에서 라우터 의존성으로 `_enterprise_license_required` 를 걸어 라이선스가 없으면 전부 403. SCIM 21행도 `premium_user_check` 라우터 의존성이다(scim_v2.py:289-293).
2. 사용자 수 제한의 실체는 두 가지다. SSO 는 라이선스가 없으면 과금 대상 사용자 5명 초과 시 403(`ui_sso.py:1106`, `SSO_MAX_BILLABLE_USERS` 로 조정, 0=무제한). `/user/new` 의 `is_over_limit` 는 라이선스에 `max_users` 가 있을 때만 걸려서 라이선스가 없으면 제한이 없다. 새 게이트웨이는 이 벽을 그냥 만들지 않으면 된다.
3. 감사 로그는 조회(`/audit`)는 열려 있으나 기록이 `premium_user is True` 일 때만 쌓인다(`audit_logs.py:41,286,317`). 라이선스 없이는 비어 있다.
4. `premium_user` 는 로그인 때 UI 토큰에 실리는 값이기도 하다(`login_utils.py:504`). UI 가 이 값으로 유료 메뉴를 가리거나 보이게 하는지는 확인 못함.
5. ROI 계산기 23행(`roi_calculator_endpoints.py`, `roi_observed_endpoints.py`)은 라우터를 만들었지만 `proxy_server.py` 에 `include_router` 가 없고 `_lazy_features.py` 항목도 없다. 호출해도 404. 게다가 GitHub OAuth 연동이다. 같은 이유로 `config_management_endpoints` 도 죽은 코드.
6. 폐쇄망에서 바깥으로 나가려는 관리 엔드포인트: `GET /get/latest_release_info`(GitHub API), `POST /reload/model_cost_map`·`/schedule/model_cost_map_reload`(단가표 GitHub raw 기본 URL, `litellm/__init__.py:410`), `/reload/anthropic_beta_headers`(`:420`), `GET /public/litellm_blog_posts`(RSS), `GET /public/autorouter_presets`(`:424`), `GET /policy/templates`(GitHub raw, `LITELLM_LOCAL_POLICY_TEMPLATES=true` 로 차단), 자격증명 GitHub 디바이스 플로우. 단가표는 기동 시에도 환경변수 `LITELLM_LOCAL_MODEL_COST_MAP=True` 가 없으면 원격을 가져오려 한다(`get_model_cost_map.py:459`). `product.md` 1번(기본으로 켜지는 바깥 요청은 꺼 둔다)과 직접 충돌하므로 신규에서는 아예 만들지 않는다.
7. 텔레메트리 엔드포인트(`/telemetry/*`)가 있다. 기본 목적지는 없고 동의한 그룹만 저장·전송하는 구조로 보이나(`telemetry/settings.py:42`) 전송부는 확인 못함. `product.md` 의 telemetry 0 원칙상 제외.
8. 같은 로직의 중복 경로가 많다: `/customer/*` 와 `/end_user/*`, `/model/info` 와 `/v1/model/info`, `/access_group/*`·`/v1/access_group*`·`/v1/unified_access_group*` 세 벌(합 18행), `/health/liveliness` 와 `/health/liveness`. 신규에서는 하나만 둔다.
9. 데코레이터가 아니라 반복문으로 등록되는 라우트가 있다(사용량 일별 집계 37행, 접근 그룹 별칭 5행). 데코레이터 grep 만으로 세면 42행이 빠진다.
10. 이 저장소는 이미 관리면과 추론면을 allowlist 로 나눠 두었다(`backend/routes/allowlist.py`, `gateway/routes/allowlist.py`). 그 allowlist 가 사실상 "지금 운영에서 쓰는 관리면"의 정의이므로 범위 결정의 출발점으로 삼을 만하다.

## 6. 타 영역으로 넘긴 것(이 문서 표에 미포함)

추론 라우트(`/v1/chat/completions`, `/v1/models`, 임베딩·오디오·파일·배치·어시스턴트 등), spend 조회(`spend_tracking/` 44행: `/spend/*`, `/global/spend*`, `/key/spend/report` 등), 가드레일(`guardrails/` 25행, `policy_engine/` 18행), pass-through(`pass_through_endpoints/` 35행, `/config/pass_through_endpoint*`), 에이전트 17행, 프롬프트 10행, 벡터스토어, MCP 데이터면(`/mcp`, `/mcp-rest`, `/{server}/mcp`), 트레이싱(`/v1/traces*` 9행), memory(`/v1/memory*`), lens, 검색툴, 영역 외 enterprise 벡터스토어. 개수는 데코레이터 추출 기준이다.

## 7. 확인 못함

- 표의 DB 테이블 중 "추정" 부분(헬퍼를 거치는 키·팀·사용자 쓰기의 정확한 컬럼과 트랜잭션 경계).
- UI 호출 추정에서 동적 조립 경로 누락 가능성, `ko-dashboard` 의 실제 코드.
- `GET /` 두 핸들러 중 어느 쪽이 응답하는지, `premium_user` 가 UI 메뉴 노출에 쓰이는 방식.
- `/health/services` 가 실제로 건드리는 외부 서비스 목록과 `/telemetry` 전송부.
- 각 엔드포인트의 인증·역할 검사(관리자 전용인지 팀 관리자 허용인지)는 이 표에 넣지 않았다. 신규 설계 시 `route_checks.py` 와 함께 별도 확인이 필요하다.
