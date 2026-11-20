# B. 인증·신원·접근 제어 전수조사

기준: `<workspace>/gateway` (litellm_airgap 브랜치). 경로는 별도 표기 없으면 `litellm/proxy/` 기준. 참고 문서(SSO_ANALYSIS 등)는 보지 않고 코드로 직접 확인했다. premium 표시: **[P]** = `premium_user is not True` 이면 막힘(`LITELLM_LICENSE` 필요). 권장: 필수 / 후순위 / 제외.

## 0. 먼저 읽을 것 (요약)

- 전체 요청 인증은 `user_api_key_auth()` (auth/user_api_key_auth.py:4216) 하나로 들어와 `user_api_key_auth_builder` (:2088) -> `validate_resolved_virtual_key` (:2838) -> `_authorize_authenticated_request` (:4018) -> `run_centralized_common_checks` (:3459) -> `common_checks` (auth/auth_checks.py:1076) 순서로 간다.
- 역할은 `LitellmUserRoles` (litellm/types/proxy/auth/user_roles.py:5) 7개: proxy_admin, proxy_admin_viewer, org_admin, internal_user, internal_user_viewer, team, customer. (auth/roles.py 의 `Role` 은 별도 신형 매핑이며 인증 경로에서의 사용처는 확인 못함.)
- 키는 SHA-256(무염) 해시로 `LiteLLM_VerificationToken.token` 에 저장된다 (utils.py:7837 `hash_token`). 비밀번호는 scrypt (utils.py:7846 `hash_password`), 예전 SHA256 hex 도 읽음(utils.py:7860대 `verify_password`).
- 경로 권한은 **라우트 문자열 집합**(`LiteLLMRoutes`, proxy/_types.py:223~1042)과 `RouteChecks` (auth/route_checks.py:109) 로 판정한다. 새 게이트웨이는 이 표를 그대로 옮기기보다 "admin / viewer / user 3역할 x 엔드포인트 그룹" 으로 단순화하는 것이 맞다.

## 1. 요청 단계 순서 (추론 API 호출 한 건)

| # | 단계 | 위치 | 동작 |
|---|------|------|------|
| 1 | 키 추출·본문 선파싱 | user_api_key_auth.py:4216 `user_api_key_auth`, :1380 `get_api_key`, :4369 | 헤더 우선순위: `general_settings.litellm_key_header_name` > `x-litellm-api-key` > `Authorization: Bearer` > `api-key`(Azure) > `x-api-key`(Anthropic) > `x-goog-api-key` > Azure APIM 헤더 (:218~246) |
| 2 | 사전 검사(DB 읽기 전) | auth/auth_utils.py:801 `pre_db_read_auth_checks` | 요청 크기 한도[P, :1100], 본문 안전성(클라이언트가 api_base 등 못 넣게, :654), `allowed_ips`[P], `allowed_routes`(:845, 아래 6.4 참고) |
| 3 | custom_auth | user_api_key_auth.py:2160~2200 | enterprise 래퍼(`enterprise/litellm_enterprise/proxy/auth/user_api_key_auth.py` mode on/off/auto) 또는 `general_settings.custom_auth` 사용자 함수. 성공하면 즉시 반환. 기본은 common_checks 를 건너뜀(`custom_auth_run_common_checks`, :3499~3510) |
| 4 | 공개 라우트 | :2209 + proxy/_types.py:725 `public_routes` | `/health/liveliness`, `/public/model_hub*`, `/.well-known/litellm-ui-config`, `/moyai/connect/exchange` 등은 키 없이 통과 (INTERNAL_USER_VIEW_ONLY 부여) |
| 5 | OAuth2 인트로스펙션 [P] | :2229~2244, auth/oauth2_check.py:15 | `enable_oauth2_auth` 시 토큰을 IdP 에 질의(RFC 7662). premium 아니면 403 |
| 6 | oauth2-proxy 헤더 인증 | :2246, auth/oauth2_proxy_hook.py:40 | `enable_oauth2_proxy_auth`. 이 분기엔 premium 체크 못 찾음(확인 못함) |
| 7 | JWT 인증 [P] | :2250~2480, auth/handle_jwt.py:1370 `auth_jwt`, :2490 `JWTAuthManager.auth_builder` | `enable_jwt_auth` + 토큰이 JWT 형태면 JWKS 검증(발급자 여러 개 지원) -> 역할·팀·조직·유저·end_user 클레임 매핑 -> (선택) JWT->가상키 매핑(:1734) -> UserAPIKeyAuth 구성. premium 아니면 403 (:2254~2260) |
| 8 | master_key 없음 | :2501 | master_key 미설정이면 어떤 키든 INTERNAL_USER 로 통과(무인증 개발 모드). 폐쇄망 배포에선 반드시 막아야 함 |
| 9 | end_user(고객) 해석 | :2527~2560, auth_checks.py:2230 `resolve_and_validate_end_user_id`, auth_utils.py:1842 | 본문 `user`, 헤더 매핑, JWT 클레임 등에서 end_user_id 추출 -> `LiteLLM_EndUserTable` 조회·기본 예산 적용 |
| 10 | 키 조회 | :2590~2640, auth/resolvers/store.py:47 `IdentityStore` | 캐시 -> (sk- 아니면) UI 세션 암호문 복호(auth_checks.py:4080 `get_key_object_from_ui_hash_key`) -> DB. 키는 해시로 조회 |
| 11 | 마스터 키 | :2717 `secrets.compare_digest` | 일치하면 PROXY_ADMIN, 키 별칭은 상수(해시도 로그에 안 남게). 이후 `master_key_only_routes` (:2752, proxy/_types.py:588) 는 마스터 키만 |
| 12 | 가상키 검증 | :2838 `validate_resolved_virtual_key` | blocked, 만료(expires), 키·유저·팀·팀멤버·모델별 예산, 모델 접근, 프로젝트·글로벌 예산 순. 해시 아닌 비-`sk-` 토큰은 401 (:2780대) |
| 13 | 라우트 권한 | :4018 `_authorize_authenticated_request` -> auth/route_checks.py:111 `should_call_route`, :133 `is_virtual_key_allowed_to_call_route`, :301 `non_proxy_admin_allowed_routes_check` | 키의 `allowed_routes`, 역할별 라우트 집합, 패스스루 허용 판정 |
| 14 | 공통 검사 | auth/auth_checks.py:1076 `common_checks` | 팀 차단 -> 팀/키/멤버 모델 접근 -> 팀·프로젝트·유저·end_user·글로벌 예산 -> 조직 RBAC -> 벡터스토어·툴 allowlist -> 트레이스 id 요구. 예산 예약(`_reserve_budget_after_common_checks`, :3927) |
| 15 | 예외 -> 응답 | auth/auth_exception_handler.py:20대 | 401/403/429 로 변환, 일부 경우 "복구된 인증 객체"로 통과 |

## 2. 기능 인벤토리

### 2.1 인증 방식·마스터 키·세션

| 기능 | 엔드포인트/진입점 | 구현 위치 | 동작 요약 | DB 테이블 | 외부호출·premium | 새 게이트웨이 권장 |
|---|---|---|---|---|---|---|
| 가상키(sk-) 베어러 인증 | `Authorization: Bearer sk-...` 전 라우트 | user_api_key_auth.py:2590~2700, 2838 | 키 SHA-256 해시로 조회, 차단·만료·예산·모델 검사 | LiteLLM_VerificationToken | 없음 | 필수 - OpenAI SDK 호환의 핵심. 해시는 무염 SHA-256 이므로 새 쪽도 동일하게 하면 기존 키 이관 가능 |
| 마스터 키 | 설정 `master_key`/`LITELLM_MASTER_KEY` | user_api_key_auth.py:2717, auth/master_key_boot_check.py:79,203 | 상수시간 비교, PROXY_ADMIN 부여. 비었거나 공개된 알려진 값이면 부팅 거부(override 설정 있음) | 없음(env/설정) | 없음 | 필수 - 부트스트랩 관리자 키. 부팅 거부 검사도 유지 권장 |
| master_key 미설정 무인증 모드 | 설정 | user_api_key_auth.py:2501, :3378 `is_no_auth_dev_mode` | 모든 요청을 INTERNAL_USER 로 허용 | 없음 | 없음 | 제외 - 폐쇄망 운영에서 위험. master_key 필수로 |
| 키 헤더 대체 이름 | `litellm_key_header_name` | user_api_key_auth.py:2142, :4369 | Authorization 대신 지정 헤더 사용 | 없음 | 없음 | 후순위 - 프록시 뒤 배치 시만 |
| Azure/Anthropic/Google 헤더 키 수용 | `api-key`, `x-api-key`, `x-goog-api-key` | user_api_key_auth.py:218~246, 1380 | 각 SDK 가 쓰는 헤더에서 같은 가상키를 읽음 | 동일 | 없음 | 필수(x-api-key)/후순위(나머지) - 코딩 도구(Claude Code 등)가 x-api-key 를 쓰므로 Anthropic 형 헤더는 필요. Azure·Google 은 요구 시 |
| 사용자 정의 인증 `custom_auth` | 설정 `general_settings.custom_auth` | user_api_key_auth.py:2160~2200, enterprise/litellm_enterprise/proxy/auth/user_api_key_auth.py | 파이썬 함수가 UserAPIKeyAuth 반환. 기본은 common_checks(예산·모델) 건너뜀(:3499) | 없음 | premium 아님(코드상 게이트 못 찾음) | 제외 - 코틀린엔 플러그인 개념 불필요. Keycloak 하나라 정책이 고정됨 |
| 사용자 정의 SSO `custom_sso` / `custom_ui_sso_sign_in_handler` | 설정 | proxy_server.py:7094~7100, ui_sso.py:3990대, enterprise/.../auth/custom_sso_handler.py | SSO 결과·로그인 헤더를 사용자 함수로 가공 | 없음 | 확인 못함(헤더 기반 핸들러는 enterprise 패키지 내) | 제외 - Keycloak 단일 |
| 패스스루 엔드포인트 인증 | `pass_through_endpoints[].auth` | user_api_key_auth.py:1440 | 엔드포인트별 인증 on/off, 커스텀 헤더에서 키 추출. `auth` 기본 true | 없음 | 없음 | 후순위 - 패스스루 도입 시 같이 |
| IP 허용 목록 [P] | `general_settings.allowed_ips` | auth/auth_utils.py:109,832, proxy_server.py:7120, auth/ip_address_utils.py, auth/trusted_proxy_utils.py | 요청 IP 가 목록 밖이면 403. 설정하면 premium 아닐 때 기동 오류 | 없음 | **[P]** | 후순위 - 폐쇄망이라 필수는 아님. 넣는다면 단순 CIDR 허용 |
| 라우트 허용 목록 `allowed_routes` / 관리자 전용 `admin_only_routes` | general_settings | auth_utils.py:845, auth/route_checks.py:414 | 프록시 전체 라우트 제한 / 특정 라우트를 관리자에게만 | 없음 | `admin_only_routes` **[P]**(비premium 이면 무시). `allowed_routes` 는 로그만 남기고 그대로 적용됨(게이트가 느슨) | 제외 - 새 게이트웨이는 정책을 코드 라우팅 표로 고정 |
| 요청 크기 한도 [P] | `max_request_size_mb` | auth_utils.py:1083~1110 | 큰 요청 거부 | 없음 | **[P]**(비premium 이면 검사 건너뜀) | 후순위 - WebFlux 기본 코덱 한도로 대체 가능 |
| 공개 라우트 | `public_routes`, `public_allowed_routes` | proxy/_types.py:725, auth_utils.py:861 | 헬스체크·모델 허브 등 무인증 | 없음 | 없음 | 필수 - 헬스체크만. 모델 허브류는 제외 |

### 2.2 JWT·OAuth2·IdP 연동

| 기능 | 엔드포인트/진입점 | 구현 위치 | 동작 요약 | DB 테이블 | 외부호출·premium | 새 게이트웨이 권장 |
|---|---|---|---|---|---|---|
| JWT 베어러 인증 (Keycloak 토큰으로 직접 호출) | `enable_jwt_auth` + `litellm_jwtauth` 설정 | auth/handle_jwt.py:233 `JWTHandler`, :1370 `auth_jwt`, :2490 `auth_builder`, :2534 `resolve_identity`; user_api_key_auth.py:2250~2480; 설정 모델 proxy/_types.py:4868~4941 `LiteLLM_JWTAuth` | JWKS(또는 OIDC discovery)로 서명 검증, 발급자 다중, 클레임으로 팀·유저·조직·end_user·역할 매핑, 스코프/`role_mappings` 로 RBAC, 헤더 `x-litellm-team-id` 로 팀 선택 | LiteLLM_UserTable, LiteLLM_TeamTable, LiteLLM_OrganizationTable (upsert 옵션) | IdP 의 JWKS URL 호출(내부 Keycloak 이면 OK). **[P]** (user_api_key_auth.py:2254) | 필수 - Keycloak 한 개를 쓰므로 "Keycloak 액세스 토큰 -> 사용자·팀·역할" 이 사실상 주 인증. 단 litellm 에선 유료이니 새 쪽이 직접 구현해야 함 |
| JWT -> 가상키 매핑 | `/jwt/key/mapping/{new,update,delete,list,info}` | management_endpoints/jwt_key_mapping_endpoints.py:146,202,269,305,343; user_api_key_auth.py:1539 `_auto_register_jwt_mapping`, :1697, :1734 `_resolve_jwt_to_virtual_key` | JWT 클레임(sub·email) 값 -> 특정 가상키로 치환해 기존 키 예산·모델 제한 재사용. 자동 등록 옵션 | LiteLLM_JWTKeyMapping (token FK, cascade 삭제) | **[P]** (JWT 경로에 속함) | 후순위 - JWT 에서 바로 사용자 단위 예산을 주는 설계면 불필요 |
| OAuth2 토큰 인트로스펙션 [P] | `enable_oauth2_auth` + `oauth_token_info_endpoint` | auth/oauth2_check.py:15, user_api_key_auth.py:2229~2244 | 불투명 토큰을 IdP 인트로스펙션 엔드포인트에 질의 | 없음 | IdP 호출. **[P]** (oauth2_check.py:131) | 후순위 - Keycloak 은 JWT 로 충분 |
| oauth2-proxy 헤더 인증 | `enable_oauth2_proxy_auth` | auth/oauth2_proxy_hook.py:40 | 앞단 oauth2-proxy 가 넣어준 헤더 신뢰 | 없음 | 없음(게이트 못 찾음) | 제외 |
| JWT 역할 맵(`jwt_litellm_role_map`) -> SSO 사용자 역할 동기화 | 설정 | ui_sso.py:2100 `_sync_user_role_from_jwt_role_map`, handle_jwt.py:325,627 | IdP 역할 클레임을 litellm 역할로 변환 | LiteLLM_UserTable.user_role | 없음 | 필수 - Keycloak realm/client role -> admin/viewer/user 매핑 |
| 관리형 에이전트 신원 (Entra/Okta 형 서비스 주체) | JWT 직접 인증 + `/v1/agents/{id}/identity` | auth/user_api_key_auth.py:2284 (`resolve_managed_agent`), :4040~4090, agent_endpoints/identity_store.py, agent_endpoints/auth/managed_authorization.py; sso/agent_subject_enrollment.py | 에이전트 신원 바인딩·주체 등록. Microsoft 중심 | LiteLLM_AgentIdentity, LiteLLM_RetiredAgentIdentity, LiteLLM_RetiredAgent, LiteLLM_VerifiedSubject | Entra 등 외부 IdP(폐쇄망 밖) | 제외 - 범위 밖(에이전트 영역은 다른 조사 담당). 인증 경계만 기록 |

### 2.3 UI 로그인·SSO·세션

| 기능 | 엔드포인트/진입점 | 구현 위치 | 동작 요약 | DB 테이블 | 외부호출·premium | 새 게이트웨이 권장 |
|---|---|---|---|---|---|---|
| 아이디/비밀번호 로그인 | `POST /login`(폼), `POST /v2/login`(JSON+쿠키), `POST /v3/login`(+`/v3/login/exchange`, control_plane_url 필요), `GET /fallback/login` | proxy_server.py:18363,18457,18521,18596,18337; auth/login_utils.py:213 `authenticate_user`, :283 `_sign_in`, :488 `encode_ui_session_jwt`, :501 `create_ui_token_object` | env 관리자(`UI_USERNAME`/`UI_PASSWORD`, 없으면 master_key 가 비밀번호) 또는 DB 유저(이메일+scrypt). 성공 시 DB 에 `litellm-dashboard` 팀 세션 키를 만들고 HS256 JWT(마스터 키로 서명, `LITELLM_UI_SESSION_DURATION`)를 `token` 쿠키로 발급 | LiteLLM_UserTable, LiteLLM_VerificationToken(세션 키) | HIBP 호출 있음(아래). premium 아님 | 필수 - 관리 UI/운영자 로그인. 단 Keycloak 단일이면 로컬 비밀번호는 "비상 관리자" 하나만 두는 것을 권장. `/v3` 계열은 제외 |
| 로그인 시도 제한 | 로그인 내부 | auth/login_throttle.py:270 `LoginThrottle` | 계정·원천 IP 별 실패 횟수 제한, 429, redis 선택(단일 워커면 워커별 카운트) | 없음(캐시/redis) | 없음 | 필수 - 비밀번호 로그인을 남기는 한. 간단한 메모리 제한으로 충분 |
| 비밀번호 정책 + 유출 검사 | 로그인·`/user/password/change`·사용자 생성 | auth/password_policy.py:96,110~160,185,193; constants.py:2322 `HIBP_RANGE_API_BASE` | 길이·문자종류 정책 + haveibeenpwned(`https://api.pwnedpasswords.com/range`) k-익명 조회(기본 켜짐, 실패 시 통과) | LiteLLM_UserTable.last_breach_check_at, password_reset_required | **외부 호출**(폐쇄망 밖). `password_policy_check_breached_passwords: false` 로 끔 (docker-dev/config.yaml 에서 이미 끔) | 제외(유출 검사)/후순위(길이 정책) - phone-home 이므로 새 쪽엔 넣지 않음 |
| 비밀번호 변경 | `POST /user/password/change` | management_endpoints/password_endpoints.py:71,76 | 현재 비밀번호 확인 후 변경, 다른 UI 세션 폐기 | LiteLLM_UserTable, LiteLLM_VerificationToken | HIBP(위와 동일) | 후순위 - 로컬 비밀번호를 비상 관리자만 둔다면 불필요 |
| 로그아웃 / 세션 폐기 | `POST /session/logout` | management_endpoints/session_endpoints.py:54,117 | UI 세션 키 삭제(`litellm-dashboard` 팀 키) + 캐시 무효화 | LiteLLM_VerificationToken, LiteLLM_DeletedVerificationToken | 없음 | 필수 - UI 세션 수명 관리 |
| 세션 쿠키 | `token` 쿠키 | management_endpoints/ui_sso.py:3199 `set_session_token_cookie` | HttpOnly 아님(UI 가 document.cookie 로 읽음), Secure 는 https 일 때, SameSite=Lax | 없음 | 없음 | 필수(방식 변경) - 새 UI 는 HttpOnly 쿠키+서버 세션으로 가는 것이 안전 |
| UI 세션 토큰(암호화 베어러) | `litellm-session` 접두 토큰 | auth/auth_checks.py:3972 `ExperimentalUIJWTToken`, :4080; common_utils/encrypt_decrypt_utils.py:32 | 사용자 정보를 `LITELLM_SALT_KEY`(없으면 master_key)로 암호화해 베어러로 사용(10분, CLI 는 24시간 기본) | 없음(상태 없음) | 없음 | 후순위 - 세션을 DB 또는 서명 쿠키 하나로 단순화 |
| 초대 링크 / 온보딩 | `POST /invitation/new`, `GET /invitation/info`, `POST /invitation/update`, `POST /invitation/delete`, `GET /onboarding/get_token`, `POST /onboarding/claim_token` | proxy_server.py:19161,19235,19280,19337,18661,18863; management_helpers/user_invitation.py | 관리자가 만든 링크로 신규 사용자가 비밀번호 설정. 만료일 검사 | LiteLLM_InvitationLink, LiteLLM_UserTable | 이메일 발송 시 SMTP/Resend/SendGrid(enterprise 콜백) | 제외 - Keycloak 이 사용자 등록을 맡음 |
| SSO 로그인(Google/Microsoft/Generic OIDC) | `GET /sso/key/generate`(시작), `GET /sso/callback` | management_endpoints/ui_sso.py:1128 `google_login`, :2245 `auth_callback`, :3294 `SSOAuthenticationHandler`, :1621 `get_generic_sso_response`, :1399 `_setup_generic_sso_env_vars`, :4698 Microsoft, :5084 Google | `GENERIC_*` env(클라이언트 ID/시크릿, authorization/token/userinfo 엔드포인트)로 Keycloak 등 OIDC 지원, PKCE 지원(:866~1284), 사용자 upsert·세션 키 발급. `MICROSOFT_*`, `GOOGLE_*` 는 외부 IdP | LiteLLM_UserTable(upsert), LiteLLM_VerificationToken, LiteLLM_SSOConfig(UI 에서 저장 시), LiteLLM_SSOIdentityAssertion | Keycloak = 내부 호출. Google·Microsoft 는 폐쇄망 밖. **[P] 5명 초과** (아래) | 필수(Generic OIDC = Keycloak 만) - Google·Microsoft 는 제외 |
| SSO 무료 한도 5명 | 로그인·콜백 시 | ui_sso.py:1106 `_raise_if_sso_exceeds_free_user_limit` (호출 :1168, :2402), litellm/repositories/user_repository.py:132 | premium 아니면 전체 사용자 수 > 5 이면 403. 환경변수 `SSO_MAX_BILLABLE_USERS`(0=무제한, 기본 5)로 조정 가능(코드에 이미 있음). docker-dev/Dockerfile.patch 가 같은 방식으로 우회 | LiteLLM_UserTable(개수) | **[P]** (우회 쉬움) | 필수 이슈 - 새 게이트웨이엔 이런 한도 자체를 만들지 않음 |
| Keycloak 그룹 -> 팀/역할 매핑 | SSO 콜백 내부, `sso_settings`의 team_mappings·role_mappings | ui_sso.py:876 `determine_role_from_groups`, :1458 `_setup_team_mappings`, :1494 `_setup_role_mappings`, :444 `verify_user_in_restricted_sso_group`, :480 `create_litellm_team_from_sso_group`, :427 `add_user_to_teams_from_sso_response`; management_endpoints/types.py:75 (Keycloak 는 역할을 리스트로 반환한다는 주석) | 그룹 클레임으로 역할(최고 권한 우선)·팀 자동 가입·팀 자동 생성, 특정 그룹만 허용 | LiteLLM_UserTable, LiteLLM_TeamTable | 없음. (Keycloak 전용 코드는 없음, 일반 OIDC 처리) | 필수 - 운영 자동화의 핵심(그룹 = 팀) |
| SSO 사용자 기본 팀·기본 모델 설정 | `litellm.default_internal_user_params`, `default_team_params` | ui_sso.py:2898 `insert_sso_user`, :2976 `_join_default_teams_as_admin`, :540 `_cast_and_deepcopy_litellm_default_team_params` | 첫 SSO 로그인 유저에 기본 역할·모델·예산·팀 부여 | LiteLLM_UserTable, LiteLLM_TeamTable | 팀 admin 지정은 **[P]** 가능성(team_endpoints.py:2909) | 필수 - 신규 사용자 기본 예산 |
| SSO 설정을 UI 로 저장 | `GET /get/sso_settings`, `POST /update/sso_settings`, `GET /sso/get/ui_settings`, `GET /sso/readiness` | ui_crud_endpoints/proxy_setting_endpoints.py:1172,1227; ui_sso.py:3017,3050 | SSO 환경변수를 DB 에 암호화 저장하고 반영 | LiteLLM_SSOConfig, LiteLLM_UISettings | 없음 | 후순위 - 우선 설정 파일/env 로 시작 |
| SAML 2.0 | `GET /sso/saml/login`, `GET /sso/saml/metadata`, `POST /sso/saml/callback` | ui_sso.py:2351,2362,2371; management_endpoints/sso/saml_sso.py:117 `SAMLAuthHandler` (python3-saml 선택 의존) | IdP 메타데이터 URL/XML 로 SP 구성, 리플레이 방지·InResponseTo 검사 | LiteLLM_UserTable | IdP 메타데이터 URL 호출. 5명 한도 대상 | 제외 - Keycloak 은 OIDC 로 충분 |
| SSO 디버그 | `GET /sso/debug/login`, `/sso/debug/callback` | ui_sso.py:5135,5184 | 클레임을 화면에 출력 | 없음 | 외부 IdP 설정 시 **[P]** (ui_sso.py:5152) | 제외 |
| CLI 로그인(device-code 형) | `POST /sso/cli/start`, `POST /sso/cli/complete/{login_id}`, `POST /sso/cli/password/{login_id}`, `GET /sso/cli/poll/{key_id}` | ui_sso.py:771,817,2697,2765, :2618 `cli_sso_callback`, auth_checks.py:4012 `get_cli_jwt_auth_token` | `lite login` 용. 로그인 흐름을 서버에 두고 폴링으로 24시간 세션 토큰 전달, 팀 선택, 사용자 코드 확인 | LiteLLM_UserTable, (세션은 암호화 토큰/캐시) | Keycloak 사용 시 내부. SSO 5명 한도 대상 | 후순위 - 코딩 도구 로그인을 키 발급 UI 로 대체 가능. 필요하면 Keycloak device flow 를 직접 사용 |
| Moyai 빠른 연결 | `POST /moyai/connect/start`, `/moyai/connect/exchange`(공개) | moyai_endpoints.py:1~40 | 서명 코드로 관리자 키 발급(외부 배포와 연결) | LiteLLM_VerificationToken, LiteLLM_UISettings | 외부 연결 성격 | 제외 |
| 관리자 UI 전용(`ui_access_mode`) | 설정 | ui_sso.py:2278~2290, 3948대 | `admin_only` 이면 관리자 역할만 UI 로그인 허용 | 없음 | 없음 | 후순위 |
| 네이티브 관리자 연결(liteadmin) | enterprise 라우트 | enterprise/litellm_enterprise/proxy/liteadmin.py:205,219,251 | 별도 관리 워커와 연결(`premium_user is True` 필요) | LiteLLM_UserTable | **[P]** | 제외 |

### 2.4 API 키 수명주기

| 기능 | 엔드포인트/진입점 | 구현 위치 | 동작 요약 | DB 테이블 | 외부호출·premium | 새 게이트웨이 권장 |
|---|---|---|---|---|---|---|
| 키 생성 | `POST /key/generate` | management_endpoints/key_management_endpoints.py:2268(데코레이터)/2274, 헬퍼 :5028 `generate_key_helper_fn`, :1414 `_common_key_generation_helper` | `sk-` 랜덤 키 생성(직접 지정도 가능, `sk-` 접두 필수 :1726), 기간(duration)->expires, 모델 제한, 예산·한도, 메타데이터, 팀·유저·프로젝트 귀속, 기본값·상한(`default_key_generate_params`,`upperbound_key_generate_params`) 적용, 응답에만 평문 키(DB 엔 해시) | LiteLLM_VerificationToken, LiteLLM_ObjectPermissionTable, LiteLLM_BudgetTable | 키 `tags` **[P]**(:1652), 키 `model_max_budget` **[P]**(:8387), 와일드카드 모델 접근그룹 **[P]**(:4983), `get_spend_routes` 권한 **[P]**(:5245) | 필수 - 예산·모델 제한·만료·팀/유저 귀속까지. 위 premium 항목들도 "운영자가 직접 다룸" 범위라 새 쪽에서 기본 제공 권장(키별 모델 예산은 후순위) |
| 서비스 계정 키 | `POST /key/service-account/generate` | key_management_endpoints.py:2482 | 팀에 귀속되는 유저 없는 키(팀 서비스 계정) | LiteLLM_VerificationToken | 없음 | 필수 - 팀 공용 키/CI 용 |
| 키 수정 | `POST /key/update` | key_management_endpoints.py:3805/3807, :3505 `_validate_update_key_data` | 모델·예산·한도·만료·차단·메타데이터·allowed_routes 변경. 권한 상승 방지 검사(예: allowed_routes 설정 권한 :1129, 예산 위임 상한 :1224) | LiteLLM_VerificationToken | 위 premium 필드 동일 | 필수 |
| 키 삭제 | `POST /key/delete` | key_management_endpoints.py:4538/4540, :5380 `can_modify_verification_token` | 키 목록 또는 키 별칭으로 삭제. 삭제본을 이력 테이블에 보관. 권한: proxy_admin / 팀 admin / 키 소유자 | LiteLLM_VerificationToken, LiteLLM_DeletedVerificationToken | 없음 | 필수 |
| 키 차단/해제 | `POST /key/block`, `POST /key/unblock` | key_management_endpoints.py:7848,7962 | `blocked=true/false`. 인증 단계에서 즉시 거부 | LiteLLM_VerificationToken | 없음 | 필수 |
| 키 정보·목록·별칭·상태 | `GET /key/info`, `GET /v2/key/info`, `GET /key/list`, `GET /key/aliases`, `GET /key/health` | key_management_endpoints.py:4815,4718,7044,7279,8077 | 본인 키/팀 키 조회(역할별 범위), 필터·페이지네이션 | LiteLLM_VerificationToken | 없음 | 필수(info·list) / 후순위(aliases·health) |
| 키 재생성(회전) [P] | `POST /key/regenerate`, `POST /key/{key}/regenerate` | key_management_endpoints.py:6251,6256,6261; 게이트 :6370~6375 | 새 키 값 발급, 이전 키 유예(`grace_period`) 후 폐기. 마스터 키 재생성만 비premium 허용 | LiteLLM_VerificationToken, LiteLLM_DeprecatedVerificationToken | **[P]** | 필수 - 키 유출 대응은 운영 필수. litellm 에선 유료이니 새 쪽이 직접 구현 |
| 자동 회전 | env `LITELLM_KEY_ROTATION_ENABLED`, 키의 `auto_rotate`·`rotation_interval` | proxy_server.py:11711~11740, common_utils/key_rotation_manager.py:40,166~205 | 스케줄러가 주기적으로 만기 키를 `regenerate_key_fn` 으로 회전 | LiteLLM_VerificationToken(`key_rotation_at`,`rotation_count` 등) | 내부적으로 위 재생성 게이트를 거쳐 사실상 **[P]**. 비premium 에서 실제 동작은 확인 못함 | 후순위 - 수동 회전 먼저 |
| 만료·예산 리셋 | 인증 단계 + 스케줄러 | user_api_key_auth.py:3000대(만료 검사), proxy_server.py `reset_budget` 계열 | expires 지나면 401, `budget_duration` 마다 spend 리셋 | LiteLLM_VerificationToken(`expires`,`budget_reset_at`) | 없음 | 필수 |
| 키 예산 리셋/지출 초기화 | `POST /key/{key}/reset_spend` | key_management_endpoints.py:6733,6738 | 키 spend 를 0 으로 | LiteLLM_VerificationToken | 없음 | 후순위 |
| 키 일괄 수정 | `POST /key/bulk_update`, `POST /team/key/bulk_update` | key_management_endpoints.py:4090,4096,4285,4291 | 여러 키를 한 번에 변경(팀 키 일괄 포함) | LiteLLM_VerificationToken | 없음 | 후순위 |
| 키 정책: 생성 허용 역할·필수 파라미터 | 설정 `key_generation_settings` | key_management_endpoints.py:676~866 `key_generation_check`, :720 | 팀/개인 키를 누가 만들 수 있고 어떤 파라미터가 필수인지 | LiteLLM_TeamTable, LiteLLM_UserTable | 없음 | 필수(단순형) - "일반 사용자가 개인 키 생성 가능?" 스위치 하나 |
| 키 허용 라우트·권한 | 키 필드 `allowed_routes`, `permissions` | auth/route_checks.py:133, key_management_endpoints.py:1129 | 키별로 `llm_api_routes` 같은 그룹 또는 경로만 허용 | LiteLLM_VerificationToken(`allowed_routes`,`permissions`) | 키 필드 자체는 무료. 서버 전역 `allowed_routes` 는 6.4 참고 | 후순위 - 추론 전용 키 / 관리 키 구분 정도만 |
| 키 사용자 정의 훅 | 설정 `custom_key_generate`, `user_custom_key_update` | key_management_endpoints.py:344~390 | 키 생성·수정을 파이썬 훅으로 검증 | 없음 | 없음 | 제외 |
| 키 마지막 사용 시각 | 인증 후 비동기 갱신 | LiteLLM_VerificationToken.last_active (schema.prisma:502대), proxy/utils.py 스펜드 큐 | 마지막 사용 시각 기록 | LiteLLM_VerificationToken | 없음 | 후순위 |
| 자격증명 암호화 마이그레이션 | `POST /credentials/migrate-encryption(/check)` | key_management_endpoints.py:5885,5925 | 저장된 비밀(모델 키 등) 재암호화 | LiteLLM_CredentialsTable 등 | 없음 | 제외 - 새 저장소는 별도 설계 |

### 2.5 사용자·팀·조직·고객·액세스 그룹

| 기능 | 엔드포인트/진입점 | 구현 위치 | 동작 요약 | DB 테이블 | 외부호출·premium | 새 게이트웨이 권장 |
|---|---|---|---|---|---|---|
| 사용자 생성·수정·삭제 | `POST /user/new`, `/user/update`, `/user/delete`, `/user/bulk_update`; `POST /management/v1/users/bulk`, `/management/v1/users/bulk_delete` | management_endpoints/internal_user_endpoints.py:462/469, 1659/1665, 2383/2389, 1827/1834; management_endpoints/management_v1/users.py:28,111; management_helpers/bulk_user_creation.py, bulk_user_deletion.py | 역할·모델·예산·한도·이메일 설정, 생성 시 키 동시 발급 옵션, 초대 메일 옵션 | LiteLLM_UserTable, LiteLLM_VerificationToken | 라이선스 좌석 한도(`is_over_limit`, :556, bulk :795)는 license 에 `max_users` 가 있을 때만 | 필수(new/update/delete) / 후순위(bulk) |
| 사용자 정보·목록 | `GET /user/info`, `GET /v2/user/info`, `GET /user/list`, `GET /user/filter/ui`, `GET /user/available_roles`, `GET /management/v1/users/me` | internal_user_endpoints.py:922/929, 1062/1069, 2210/2216, 2710/2719, 670/676 | 본인 또는 관리자가 전체 조회, 키·팀 포함 | LiteLLM_UserTable, LiteLLM_VerificationToken, LiteLLM_TeamTable | 없음 | 필수(info·list) / 후순위(나머지) |
| 사용자 일별 활동 | `GET /user/daily/activity` | internal_user_endpoints.py:2845 | 사용량 집계 | LiteLLM_DailyUserSpend | 없음 | 사용량 영역으로 넘김(다른 조사) |
| 팀 생성·수정·삭제·차단 | `POST /team/new`, `/team/update`, `PATCH /team/{team_id}`, `/team/delete`, `/team/block`, `/team/unblock` | management_endpoints/team_endpoints.py:1487/1494, 2280/2282, 2782/2788, 4545/4547, 5571, 5603 | 팀 단위 모델·예산·한도·정책, 삭제 시 이력 테이블 보관, 팀 차단 | LiteLLM_TeamTable, LiteLLM_DeletedTeamTable, LiteLLM_ModelTable, LiteLLM_ObjectPermissionTable | 팀 `model_max_budget` **[P]**(common_utils.py:88), `custom_team_metadata_validate` **[P]**(management_helpers/team_metadata_validation.py:112), 팀 개수 라이선스 한도(team_endpoints.py:1663) | 필수 - 팀은 예산 단위. `model_max_budget` 은 후순위 |
| 팀 멤버 관리 | `POST /team/member_add`, `/team/member_delete`, `/team/member_update`, `/team/bulk_member_add`, `/management/v1/teams/{id}/members/bulk_*` | team_endpoints.py:3578/3585, 3763/3769, 3988/3995, 4399/4406; management_v1/teams.py:28,100; management_helpers/team_roster_sync.py | 멤버 추가·역할·멤버별 예산·모델 | LiteLLM_TeamTable(`members_with_roles`), LiteLLM_TeamMembership, LiteLLM_UserTable | **팀 admin 지정 [P]**(team_endpoints.py:2909~2916 add, :4018 update). 일반 member 는 무료 | 필수(member 추가/삭제) / 후순위(팀 admin 역할) - 팀 admin 은 litellm 에선 유료. 새 쪽에선 "운영자만 관리" 로 시작하고 필요 시 추가 |
| 팀 멤버 권한 | `GET /team/permissions_list`, `POST /team/permissions_update`, `/team/permissions_bulk_update` | team_endpoints.py:6653,6719,6780; management_helpers/team_member_permission_checks.py:23,245 | 일반 멤버가 호출 가능한 키 관리 라우트를 팀별로 지정(`team_member_permissions`), 기본값 존재 | LiteLLM_TeamTable.team_member_permissions | 없음 | 후순위 - 멤버가 키를 스스로 만들지 말지 스위치 하나로 대체 가능 |
| 팀 정보·목록·멤버 본인 | `GET /team/info`, `/team/list`, `/v2/team/list`, `/team/available`, `/team/filter/ui`, `/team/{team_id}/members/me` | team_endpoints.py:5240/5242, 6260/6262, 5991/5998, 5647, 6371/6380, 5411/5418 | 역할별 가시 범위 | LiteLLM_TeamTable, LiteLLM_TeamMembership | 없음 | 필수(info·list) |
| 팀 멤버 개인 예산 | `POST /team/{id}/member/{uid}/reset_spend`, `reset_budget` | team_endpoints.py:4198,4296; management_helpers/bulk_team_member_budgets.py | 멤버별 팀 내 지출·예산 초기화 | LiteLLM_TeamMembership, LiteLLM_BudgetTable | 없음 | 후순위 |
| 팀 모델 추가/삭제, 팀 모델 별칭 | `POST /team/model/add`, `/team/model/delete` | team_endpoints.py:6454,6570; auth/team_grants.py | 팀이 쓸 수 있는 모델 목록·별칭 관리 | LiteLLM_TeamTable.models, LiteLLM_ModelTable | 없음 | 필수(팀 모델 제한) |
| 팀 스코프 모델 배포 [P] | `POST /model/new` 에 `team_id` | management_endpoints/model_management_endpoints.py:2183,2257 | 팀 전용 모델 배포 등록 | LiteLLM_ProxyModelTable | **[P]** | 제외 |
| 조직(organization) | `POST /organization/new`, `/update`, `/delete`, `GET /list`, `/info`, `POST /member_add`, `/member_update`, `/member_delete`, `GET /v2/organization/{id}`, `/organization/daily/activity` | management_endpoints/organization_endpoints.py:367,700,1013,1100,1206,1292,1441,1593,851,567; 라우터 전체 게이트 :99~114 `_enterprise_license_required` | 팀 상위 계층. 조직 admin 역할(`org_admin`), 조직 예산 | LiteLLM_OrganizationTable, LiteLLM_OrganizationMembership, LiteLLM_BudgetTable | **[P] 라우터 전체** (조직 기능 전부 유료) | 제외 - 팀 하나 계층으로 충분. 필요해지면 후순위 |
| 조직 RBAC 검사 | common_checks 내부 | auth/auth_checks_organization.py:1~219, auth_checks.py:1470대 | 유저가 조직 소속이면 조직 모델·예산 검사 | LiteLLM_OrganizationTable | 조직을 못 만들면(위 [P]) 사실상 미사용 | 제외 |
| 고객(end user) | `POST /customer/new`, `/customer/update`, `/customer/delete`, `/customer/block`, `/customer/unblock`, `GET /customer/info`, `/customer/list`, `/customer/daily/activity` (+`/end_user/*` 별칭) | management_endpoints/customer_endpoints.py:328/334, 602/608, 782/788, 157/163, 213/219, 544/550, 860/866, 917/923 | 요청 본문 `user` 필드(또는 헤더)로 식별되는 외부 최종 사용자. 개별 예산·모델·차단·리전 제한 | LiteLLM_EndUserTable, LiteLLM_BudgetTable | 없음(무료) | 후순위 - 사내 사용자가 키·팀으로 식별되면 불필요. 쓴다면 단순 차단+예산만 |
| 고객 기본 예산·존재 검증 | 설정 `max_end_user_budget_id`, `validate_end_user_id_in_db`, `enforce_user_param` | auth_checks.py:1669~1930, 2230; user_api_key_auth.py:2527~2600; auth_checks.py:865 | 신규 end_user 에 기본 예산, `user` 필수 강제 | LiteLLM_EndUserTable, LiteLLM_BudgetTable | 없음 | 후순위 |
| 모델 액세스 그룹(모델 묶음) | `POST /access_group/new`, `GET /access_group/list`, `GET /access_group/{name}/info`, `PUT .../update`, `DELETE .../delete`, 예산 `GET/POST/DELETE /access_group/{name}/budget` | management_endpoints/model_access_group_management_endpoints.py:570,716,766,825,979,1089,1124,1202; auth/auth_checks.py:2748,5197~5470 | 모델 배포의 `access_groups` 문자열로 만든 묶음을 키·팀 `models` 에 이름으로 부여. 그룹 단위 예산 | LiteLLM_ProxyModelTable(model_info), LiteLLM_ModelAccessGroupBudgetTable | 와일드카드 모델에 그룹 지정 **[P]** | 후순위 - 팀·키 `models` 목록으로 충분하나 "모델 묶음" 편의는 유용 |
| 액세스 그룹(신형, 자원 묶음) | `POST/GET /v1/access_group`, `GET/PUT/DELETE /v1/access_group/{id}` | management_endpoints/access_group_endpoints.py:528,602,617,638,735; auth_checks.py:4605~4800 | 모델·MCP·에이전트·스킬을 한 그룹으로 묶어 팀·키에 할당 | LiteLLM_AccessGroupTable | 없음(게이트 못 찾음) | 후순위 - MCP·에이전트·스킬 범위가 정해진 뒤 |
| 예산 객체 | `POST /budget/new`, `/budget/update`, `/budget/info`, `/budget/settings`, `/budget/list`, `/budget/delete` | management_endpoints/budget_management_endpoints.py:39,123,214,243,312,340 | 재사용 가능한 예산 템플릿(한도·기간)을 고객·팀멤버·조직에 연결 | LiteLLM_BudgetTable | 없음 | 후순위 - 예산 영역(다른 조사)과 함께 판단 |
| SCIM v2 [P] | `/scim/v2/Users`, `/Groups`, `/ServiceProviderConfig` 등 | management_endpoints/scim/scim_v2.py:289(라우터 `premium_user_check`), 1738,1830,2736,2818,2953; scim_transformations.py | IdP 가 사용자·그룹을 푸시 | LiteLLM_UserTable, LiteLLM_TeamTable | **[P] 라우터 전체** | 제외 - Keycloak 로그인 시 자동 생성(JIT)+그룹 매핑으로 대체 |
| 기본 제공 역할별 라우트 집합 | 내부 상수 | proxy/_types.py:559(info), 594(key mgmt), 626(management), 788(internal_user), 818(view_only), 834(self_managed), 1042(org_admin) | 역할이 호출 가능한 관리 API 경로를 문자열 목록으로 정의 | 없음 | 없음 | 필수(개념) - 새 쪽에선 admin / viewer / user 별 허용 API 표로 재정의 |
| 관리자 뷰어 쓰기 차단 목록 | 내부 상수 | auth/route_checks.py:30~75 `_PROXY_ADMIN_VIEW_ONLY_BLOCKED_ROUTES` | viewer 가 쓰기 API 못 쓰게 경로를 나열(새 API 를 추가하면 여기에도 추가해야 함, 누락 시 허용) | 없음 | 없음 | 필수(방식 변경) - 허용 목록(기본 거부)으로 뒤집을 것. 현재 방식은 누락 위험 |

### 2.6 감사·기타 접근 제어

| 기능 | 엔드포인트/진입점 | 구현 위치 | 동작 요약 | DB 테이블 | 외부호출·premium | 새 게이트웨이 권장 |
|---|---|---|---|---|---|---|
| 감사 로그 [P] | 키·팀·유저 변경 시 자동 기록, enterprise `audit_logging_endpoints` | management_helpers/audit_logs.py:286,317; enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py | 누가 무엇을 바꿨는지 before/after 저장 | LiteLLM_AuditLog | **[P]** (비premium 이면 기록 안 함) | 필수 - 키·팀·예산을 운영자가 직접 다루므로 변경 이력 필요. litellm 에선 유료이므로 새로 구현 |
| 라이선스 검사 | 기동 시·주기 | auth/litellm_license.py:100 `is_premium`, :60 온라인 검증, public_key.pem | RSA-PSS 오프라인 서명 검증, 실패 시 `https://license.litellm.ai` 온라인 검증(license 가 설정된 경우만) | 없음 | `LITELLM_LICENSE` 가 있을 때만 외부 호출 | 제외 - 새 게이트웨이엔 라이선스 개념 없음 |
| 초대·키 생성 메일 | `send_invite_email` 옵션 | enterprise/litellm_enterprise/enterprise_callbacks/send_emails/ (smtp/resend/sendgrid) | 사용자 생성·키 생성 알림 메일 | 없음 | 외부 메일 서비스(SMTP 는 내부 가능). 커스텀 값 **[P]**(base_email.py:749) | 제외(후순위: 내부 SMTP 필요 시) |
| 요청별 자격 정보 주입 차단 | 모든 요청 | auth/auth_utils.py:654 `is_request_body_safe`, :367 `_build_banned_observability_params`, :259 | 클라이언트가 본문으로 api_base·api_key·로깅 설정을 넣어 우회하는 것을 거부(관리자 허용 예외) | 없음 | 없음 | 필수 - SSRF·자격증명 유출 방지. 새 쪽은 처음부터 본문의 이런 필드를 무시 |

## 3. DB 테이블 정리 (이 영역에서 쓰는 것)

필수: LiteLLM_VerificationToken(키), LiteLLM_UserTable, LiteLLM_TeamTable, LiteLLM_TeamMembership, LiteLLM_BudgetTable, LiteLLM_ObjectPermissionTable(자원 접근 부여), LiteLLM_DeletedVerificationToken, LiteLLM_DeletedTeamTable, LiteLLM_AuditLog.
후순위: LiteLLM_EndUserTable, LiteLLM_AccessGroupTable, LiteLLM_ModelAccessGroupBudgetTable, LiteLLM_DeprecatedVerificationToken(재생성 유예), LiteLLM_JWTKeyMapping, LiteLLM_UISettings, LiteLLM_SSOConfig.
제외: LiteLLM_OrganizationTable/Membership, LiteLLM_InvitationLink, LiteLLM_SSOIdentityAssertion, LiteLLM_AgentIdentity 계열, LiteLLM_VerifiedSubject.
(정의: schema.prisma 147~860, 1456~1610 근처.)

## 4. 새 게이트웨이 최소 인증 설계안 (제안)

1. 인증 3가지만: (a) 마스터 키(부트스트랩), (b) 가상키 `sk-` (SHA-256 해시 저장, 기존 키 이관 가능), (c) Keycloak OIDC 액세스 토큰(JWKS 검증, 그룹->팀, 역할->admin/viewer/user).
2. 역할 3개(admin / viewer / user) + 팀 member. org_admin·customer·team 역할은 만들지 않음.
3. 허용 목록(기본 거부) 방식의 경로 권한. litellm 의 "차단 목록 + 문자열 집합" 은 복제하지 않음.
4. 검사 순서는 litellm 과 동일하게 유지: 키 유효 -> blocked -> 만료 -> 모델 접근 -> 예산(키/유저/팀) -> 라우트.
5. 로그인은 Keycloak(UI 는 Authorization Code + PKCE) + 비상 관리자 비밀번호 1개, HttpOnly 세션 쿠키.

## 5. premium 로 막힌 기능 (이 영역, 코드로 확인한 것)

| 기능 | 위치 |
|---|---|
| JWT 인증 전체 | user_api_key_auth.py:2254 |
| OAuth2 인트로스펙션 | user_api_key_auth.py:2236, auth/oauth2_check.py:131 |
| SSO 사용자 5명 초과(환경변수로 해제 가능) | ui_sso.py:1106 |
| 조직(Organization) API 전체 | organization_endpoints.py:99~114 |
| SCIM v2 전체 | scim_v2.py:289 |
| 키 재생성(+ 자동 회전은 이를 호출) | key_management_endpoints.py:6370~6375 |
| 키 tags / 키 model_max_budget / 와일드카드 모델 접근그룹 / `get_spend_routes` 권한 | key_management_endpoints.py:1652, 8387, 4983, 5245 |
| 팀 admin 지정 / 팀 model_max_budget / 팀 메타 검증 | team_endpoints.py:2909~2916, 4018; common_utils.py:88; team_metadata_validation.py:112 |
| 팀 스코프 모델 배포 | model_management_endpoints.py:2183, 2257 |
| IP 허용 목록 `allowed_ips` | proxy_server.py:7120 |
| `admin_only_routes`, 요청 크기·응답 크기 한도 | route_checks.py:418, auth_utils.py:1100, 1165 |
| 감사 로그 기록 | management_helpers/audit_logs.py:286, 317 |
| 에이전트·LLM API 엔드포인트 비활성화(`disable_llm_api_endpoints`, `disable_admin_endpoints`) | enterprise/litellm_enterprise/proxy/auth/route_checks.py:13~39 |
| 이메일 커스텀 값, liteadmin | send_emails/base_email.py:749, liteadmin.py:205 |

premium 아님으로 확인: 가상키 CRUD(재생성 제외), 사용자·팀 CRUD, 팀 member 추가·권한, end_user(고객), 모델 액세스 그룹(와일드카드 제외), `/v1/access_group`, 예산 객체, 비밀번호 로그인, 초대 링크, JWT-가상키 매핑 CRUD 엔드포인트 자체(단 JWT 인증이 막혀 있어 쓸 일 없음).

## 6. 놀라운 점·주의

1. 폐쇄망 위반 호출: 비밀번호 로그인·변경 시 `api.pwnedpasswords.com` 호출이 기본 켜짐(constants.py:2322, password_policy.py:145). 실패 시 통과(fail-open)라 막혀도 동작하지만 외부 시도는 함. 라이선스 설정 시 `license.litellm.ai` 도 호출 가능(litellm_license.py:28).
2. SSO 5명 제한은 `SSO_MAX_BILLABLE_USERS=0` 으로 무제한. 우회가 코드에 공식 옵션으로 있고 docker-dev 도 이미 사용.
3. master_key 를 안 주면 누구나 INTERNAL_USER 로 통과(user_api_key_auth.py:2501). 부팅 검사(master_key_boot_check.py)가 평소엔 막아주나 override 설정으로 해제 가능.
4. 세션 쿠키가 HttpOnly 가 아님(ui_sso.py:3199), UI 세션 토큰은 `LITELLM_SALT_KEY`(없으면 master_key)로 암호화 -> master_key 를 바꾸면 세션·저장 비밀 복호가 깨짐.
5. 전역 `allowed_routes` 는 premium 가 아니어도 실제로 적용됨(로그만 "Enterprise 기능"이라 찍음, auth_utils.py:845~858). 반대로 `admin_only_routes`, 요청 크기 한도는 비premium 이면 조용히 무시됨.
6. viewer 쓰기 차단이 "차단 목록"이라 새 API 가 누락되면 viewer 가 쓸 수 있음(route_checks.py:30 주석이 명시). 새 게이트웨이는 허용 목록으로.
7. custom_auth 는 기본적으로 예산·모델 검사를 건너뜀(user_api_key_auth.py:3499).
8. 조직 API 전체와 팀 admin 이 유료라, 라이선스 없는 현재 구조는 사실상 "관리자 + 일반 사용자 + 팀 member" 3단계. 새 게이트웨이를 이 수준으로 두면 기능 손실이 거의 없음.
9. 이 fork 는 upstream 보다 구조가 크게 바뀐 상태(IdentityStore, Principal, `auth/roles.py` 신형 Role 등). 단 `auth/roles.py`·`auth_method.py` 가 실제 인증 경로에 쓰이는지는 확인 못함.
10. 확인 못함: oauth2-proxy 헤더 인증의 premium 여부, 비premium 에서 자동 키 회전의 실제 실패 양상, `/v1/access_group` 의 모든 라우트 권한 세부, 팀 `team_member_permissions` 기본값의 구체적 목록.
