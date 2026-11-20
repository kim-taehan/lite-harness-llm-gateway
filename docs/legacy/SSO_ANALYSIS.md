# LiteLLM SSO 심층 분석

- 대상 커밋: `b6f91a2` (`refactor(inference): rename RouteError to Error (#47771)`)
- 분석 범위: Admin UI 로그인(웹 SSO), `lite login`(CLI SSO), `lite login --pkce`(OAuth 2.1 native client flow), 그리고 각 플로우가 발행하는 토큰/세션 모델
- 모든 인용은 `파일경로:라인` 기준이며, 해당 커밋의 실제 코드에서 읽은 내용이다

---

## 1. 고수준 개요

LiteLLM proxy에는 서로 다른 대상(인)을 위해 독립적으로 설계된 네 가지 인증 표면이 있다.

1. **Admin UI username/password 로그인**: `POST /login` (`litellm/proxy/proxy_server.py:18363`). `login_utils.py`의 공통 login logic을 거쳐 UI 세션 JWT 쿠키를 발급
2. **웹 SSO**: `GET /sso/key/generate` -> 외부 IdP(Google/Microsoft/Generic OIDC/SAML) -> `GET /sso/callback` -> UI 세션 JWT 쿠키. `litellm/proxy/management_endpoints/ui_sso.py`가 핵심
3. **CLI SSO** (`lite login`): 웹 SSO를 브라우저에서 수행하되, 최종 크레덴셜을 브라우저가 아니라 **CLI 프로세스가 poll해서** 받아 간다. device flow 변형으로, user_code 재입력을 브라우저-CLI 바인딩 수단으로 사용
4. **PKCE 브라우저 로그인** (`lite login --pkce`): LiteLLM이 **authorization server** 역할을 하는 표준 RFC 6749 + 7636(PKCE) + 7591(DCR) 흐름. `litellm/proxy/_experimental/mcp_server/discoverable_endpoints.py`와 `gateway_dcr_flow.py`

```
+----------------------------------------------------------+
|                      LiteLLM Proxy                       |
|                                                          |
|  +----------------+   +---------------------------+      |
|  | /login         |   | ui_sso.py (router)        |      |
|  | user/pass      |   | /sso/key/generate  (1098) |      |
|  +-------+--------+   | /sso/callback      (2198) |      |
|          |             | /sso/cli/start     (742)  |      |
|          v             | /sso/cli/complete  (788)  |      |
|  encode_ui_session_jwt | /sso/cli/poll      (2626) |      |
|  (login_utils.py:488)  | /sso/saml/*        (2304+) |      |
|          |             +----+----------------+-----+      |
|          v                  |                |            |
|   "token" cookie            v                v            |
|   (UI session JWT)    웹 SSO 완료        CLI SSO 완료     |
|                            |                |             |
|                            v                v             |
|              ExperimentalUIJWTToken (auth_checks.py:3972) |
|              - UI login token (고정 10분, :3974)          |
|              - CLI JWT (24h 기본, :4007)                  |
+-----------------+----------------------------------------+
                  |
                  v
+------------------------------------------------------------------+
|  MCP _experimental/mcp_server (PKCE surface)                     |
|  discoverable_endpoints.py                                       |
|    /authorize (2368)  /token (2446)  /register (3481)            |
|    /authorize/complete (2569)  /revoke (2599)  /introspect (2612)|
|    /.well-known/litellm-cli-auth (2632)                          |
|  gateway_dcr_flow.py (authorization server 로직)                 |
+------------------------------------------------------------------+
        |                 |                  |
        v                 v                  v
  Browser(UI)        CLI(lite)          IdP(Google/Msft/
                                 generic OIDC/SAML)
```

세 개의 SSO 플로우(웹, CLI, PKCE)는 각각 다른 토큰을 발행하지만, 크레덴셜 생성은 두 가지 choke point로 수렴한다.

- **UI 세션**: `encode_ui_session_jwt` (`litellm/proxy/auth/login_utils.py:488`)
- **session token(가상키 파생)**: `ExperimentalUIJWTToken` (`litellm/proxy/auth/auth_checks.py:3972`)

---

## 2. 웹 SSO 플로우 상세

### 2.1 진입: `GET /sso/key/generate`

`ui_sso.py:1098-1187`. 함수명은 레거시 이유로 `google_login`이지만 모든 provider의 진입점이다.

1. `source == "litellm-cli"`이면 CLI SSO 요청으로 판별 (`LITELLM_CLI_SOURCE_IDENTIFIER`, `constants.py:1823`). UI가 비활성화되어 있어도 CLI 로그인만 허용 (`is_cli_sso_login` 분기, `ui_sso.py:1128`)
2. premium/free user 한도 체크: `MICROSOFT_CLIENT_ID`, `GOOGLE_CLIENT_ID`, `GENERIC_CLIENT_ID` 또는 SAML 중 하나라도 설정되어 있으면 `_raise_if_sso_exceeds_free_user_limit` (`ui_sso.py:1138`)
3. DB와 MASTER KEY 존재 확인 (`show_missing_vars_in_env`, `ui_sso.py:1142`)
4. Enterprise custom sign-in handler 분기: `user_custom_ui_sso_sign_in_handler`이 있으면 `EnterpriseCustomSSOHandler.handle_custom_ui_sso_sign_in` (`ui_sso.py:1162-1170`)
5. 네 provider client id도 없고 SAML만 설정되어 있으면 SAML 로그인 리다이렉트 (`ui_sso.py:1176-1187`)
6. OIDC의 경우 fastapi_sso 기반 `SSOAuthenticationHandler`가 provider authorization URL로 redirect. CLI 요청이면 state에 CLI 정보 포함:

```
state = "litellm-session-token:<login_id>[:<user_code>]"
    -> SSOAuthenticationHandler.get_cli_state (ui_sso.py:1155)
user_code를 URL에 포함할지는 general_settings.allow_cli_sso_verification_uri_complete
    -> _cli_sso_verification_uri_complete_enabled (ui_sso.py:322)
```

provider별 client 생성은 `litellm/proxy/management_endpoints/sso/` 패키지:

- `LiteLLMGoogleSSO` (`sso/litellm_http_client_sso.py:191`)
- `CustomMicrosoftSSO` (`sso/custom_microsoft_sso.py:27`) - `MICROSOFT_AUTHORIZATION_ENDPOINT`, `MICROSOFT_TOKEN_ENDPOINT`, `MICROSOFT_USERINFO_ENDPOINT` override 지원
- `create_generic_sso_provider` (`sso/litellm_http_client_sso.py:206`)
- 이 패키지 전체가 fastapi_sso의 기본 httpx 대신 LiteLLM의 httpx client를 쓰는 것이 목적(파일 docstring)

### 2.2 콜백: `GET /sso/callback`

`ui_sso.py:2198-2301`.

1. `?error=` 파라미터가 있으면 401 (`ui_sso.py:2203-2213`)
2. `general_settings.ui_access_mode`가 dict이면 별도 `JWTHandler`를 만들어 SSO 토큰의 team/role claim 추출용 `sso_group_jwt_field` 매핑 (`ui_sso.py:2231-2241`). 이 JWTHandler는 `handle_jwt.py:233`의 클래스이며, `auth_jwt`(`:1370`), `get_team_ids_from_jwt`(`:409`), `get_all_jwt_team_ids`(`:432`)를 제공한다
3. `master_key` 없으면 500 (`ui_sso.py:2247-2253`)
4. `_sso_provider_callback`(`ui_sso.py:2154`)에서 authorization code -> access token -> userinfo 교환. 반환값이 `result(OpenID)`, `received_response(raw token payload)`, `access_token_payload`, `sso_assertion`
5. **CLI SSO인가 분기**: `state`가 `litellm-session-token:` prefix로 시작하면 `cli_sso_callback`으로 갈아탄다 (`ui_sso.py:2273-2287`). state 포맷은 `{PREFIX}:{login_id}[:{user_code}]`
6. 일반 웹 SSO: `SSOAuthenticationHandler.get_redirect_response_from_openid`로 이어진다 (`ui_sso.py:2291-2301`). return_to는 `CP_RETURN_TO_COOKIE_PREFIX`("return_to.v1.", `constants.py:1825`) 쿠키에서 읽는다 (`ui_sso.py:2289`)

### 2.3 콜백 이후: 유저/role/team 해석

`get_redirect_response_from_openid` 내부는 이 파일 안으로 깊게 들어가지 않아도, 호출부에 등장하는 핵심 헬퍼들:

- `normalize_email` (`ui_sso.py:827`): email 소문자 정규화, DB 매칭용
- `determine_role_from_groups` (`ui_sso.py:847`): SSO group list -> `LitellmUserRoles`. role 계층은 `proxy_admin > proxy_admin_viewer > internal_user > internal_user_viewer` (docstring, `:854-858`)
- `apply_user_info_values_to_sso_user_defined_values` (`ui_sso.py:2100`): provider claim -> `SSOUserDefinedValues`(user_id/email/name override)
- `insert_sso_user` (`ui_sso.py:2759`)와 `_insert_sso_user_or_find_concurrent_insert` (`ui_sso.py:3560` 부근): 신규 유저 upsert, 동시 insert 경쟁 처리
- `add_user_to_teams_from_sso_response` (`ui_sso.py:3580`): SSO 응답의 team claim -> team membership
- role/team mapping 설정의 타입: `RoleMappings` (`litellm/types/proxy/management_endpoints/ui_sso.py:65`), `TeamMappings` (`:87`)

제한 그룹 검증은 CLI 콜백에서 명시적으로 보인다: `SSOAuthenticationHandler.verify_user_in_restricted_sso_group` (`ui_sso.py:2597`).

### 2.4 SAML

`python3-saml` optional dependency를 쓰는 별도 경로.

- `GET /sso/saml/login` (`ui_sso.py:2304`): SP-initiated, `SAMLAuthHandler.build_login_redirect`
- `GET /sso/saml/metadata` (`ui_sso.py:2315`): SP metadata XML(IdP 등록용)
- `POST /sso/saml/callback` (`ui_sso.py:2324`): ACS, assertion 검증 후 UI 세션 발급
- `sso/saml_sso.py`의 설정은 env 기반: `SAML_IDP_METADATA_URL`, `SAML_IDP_METADATA_XML`(둘 중 하나면 SAML "설정됨", `:126`), `SAML_SP_ENTITY_ID`(`:154`), `SAML_STRICT`(default true, `:195`), `SAML_WANT_ASSERTIONS_SIGNED`(default true, `:209`), `SAML_WANT_MESSAGES_SIGNED`(`:210`), `SAML_AUTHN_REQUESTS_SIGNED`(`:211`), `SAML_ALLOW_UNSOLICITED`(default false, `:440`)

### 2.5 시퀀스 다이어그램

```
Browser            Litellm Proxy                   IdP (Google/Msft/...)
   |  GET /sso/key/generate  |                             |
   |------------------------>|  client config 읽기,         |
   |                         |  state 생성(CLI면 CLI 정보) |
   |<------ 302 /authorize?state=...&client_id=... --------|
   |  (login form)           |                             |
   |  code+state             |                             |
   |  GET /sso/callback?code&state                         |
   |------------------------>|  code -> token (HTTP)       |
   |                         |------------------->userinfo |
   |                         |  user upsert, role/team    |
   |                         |  encode_ui_session_jwt      |
   |<------ 302 /ui/?token=<JWT> --------------------------|
   |  (token cookie 저장, UI 진입)                          |
```

### 2.6 UI 세션 쿠키

`/ui/?token=`으로 받은 JWT는 브라우저가 `token` 쿠키에 저장한다. 이 JWT는:

- claims: `ReturnedUITokenObject` 전체(`litellm/types/proxy/ui_sso.py:6`: user_id, key, user_email, user_role, login_method, premium_user, auth_header_name, ...) + `exp`
- 서명: master key, HS256 (`login_utils.py:498`)
- 수명: `LITELLM_UI_SESSION_DURATION`(기본 "24h", `constants.py:1841`)

`encode_ui_session_jwt`(`login_utils.py:488-498`)의 docstring이 설계 의도를 정확히 설명한다. UI 로그인 **모든** 경로(SSO, /login, /v2, /v3)가 이 한 함수로 세션 쿠키를 발행하므로 수명은 한 곳에서만 결정되며, `exp`가 없으면 master key가 회전될 때까지 쿠키가 유효했다.

쿠키 검증은 `auth_checks.py`의 `get_user_object`(`:2929`)가 `token` 쿠키의 prefix로 session token을 인식해 `get_key_object_from_ui_hash_key`(`:4080`)로 복호화한다.

---

## 3. CLI SSO 플로우 상세

`lite login`(기본 모드)이 쓰는 플로우다. 핵심 아이디어: SSO는 브라우저에서 완결하지만, **토큰을 받아 가는 쪽은 CLI**다. 브라우저와 CLI를 연결하는 결합은 (1) `login_id`를 아는 것(CLI만), (2) 8자 user_code(`XXXX-XXXX` 형식, `_generate_cli_sso_user_code` `ui_sso.py:305`)를 **브라우저에서 사용자가 다시 직접 입력**하는 것(두 기기 바인딩).

### 3.1 서버 side flow 객체

`POST /sso/cli/start` (`ui_sso.py:742-785`).

1. **rate limit**: 클라이언트 IP hash 단위로 `_check_cli_sso_start_rate_limit`(`:358`, `:746`), 초과면 429
2. 세 가지를 생성 (`ui_sso.py:752-754`):
   - `login_id = "cli-" + secrets.token_urlsafe(24)`
   - `poll_secret = secrets.token_urlsafe(32)` (CLI만 가져가는 폴링 비밀)
   - `user_code` (`_generate_cli_sso_user_code`, `:305`)
3. 캐시에 담기는 flow dict는 **평문이 아니라 해시만** (`ui_sso.py:756-762`):

```python
flow = {
    "poll_secret_hash": _hash_cli_sso_secret(poll_secret),
    "user_code_hash": _hash_cli_sso_secret(_normalize_cli_sso_user_code(user_code)),
    "sso_complete": False,
    "user_code_verified": False,
    "session_data": None,
}
```

키 prefix `cli_sso_session`(`CLI_SSO_SESSION_CACHE_KEY_PREFIX`, `constants.py:1826`), TTL 600초(`CLI_SSO_SESSION_TTL_SECONDS`, `constants.py:1827`).

4. 응답 body (`_cli_sso_start_response_body`, `:328`): `{login_id, poll_secret, user_code, expires_in}`. `general_settings.allow_cli_sso_verification_uri_complete`가 true면 `verification_uri_complete`도 추가. 이 URL은 `/sso/key/generate?source=litellm-cli&key=<login_id>&user_code=<code>`이다.

### 3.2 전체 시퀀스

```
CLI               Proxy                  Browser             IdP
 | POST /sso/cli/start                        |                |
 |------------------------------------------>|   flow 생성    |
 |<-- {login_id, poll_secret, user_code}-----|                |
 | (user_code 출력, 브라우저 OPEN)            |                |
 |                                  GET /sso/key/generate     |
 |                                  ?source=litellm-cli       |
 |                                  &key=&user_code=  ----------> 302 IdP
 |                                  |<------------- IdP login/code
 |                                  GET /sso/callback         |
 |                                  |  state = litellm-session-token:<login_id>[:code]
 |                                  CLI SSO 분기 (ui_sso.py:2273)
 |                                  cli_sso_callback (2553)
 |                                  유저 upsert + teams fetch
 |                                  flow.session_data 설정, sso_complete=True
 |                                  browser_complete_token 생성 (2529)
 |                                  <--- 검증 페이지 HTML (user_code 폼)
 |                                  (user_code 입력 -> POST /sso/cli/complete/<login_id>)
 |                                  |  hash 2중 검증 (788-824)
 |                                  |  user_code_verified=True
 |                                  |<--- success 페이지
 |  GET /sso/cli/poll/<login_id>    |                              |
 |  (Header: x-litellm-cli-poll-secret, ui_sso.py:2631)           |
 |-------- pending ------->|  (browser 완료 대기)                   |
 |-------- ready: {status, user_id, teams[], team_details[]}------|  (1차, team 없으면)
 |  (대기/선택)             |                                       |
 |-------- ready 2차 (team_id 지정) -> CLI JWT 생성, 캐시 삭제      |
 |<-- {key: <CLI JWT>, team_id, ...}                                         |
 | keyring 저장 (auth.py:235)                                                 |
```

핵심 라우트 3개의 동작:

**`POST /sso/cli/complete/{login_id}`** (`ui_sso.py:788-824`)
- form: `user_code`, `browser_complete_token`
- 검증: `flow["sso_complete"]` 및 `session_data` 존재(800) -> user_code hash compare_digest(809-812) -> browser_complete_token hash compare_digest(815-818)
- browser_complete_token은 SSO 콜백 직후 `_complete_cli_sso_callback_session`이 생성해 검증 폼에 넣는 것(`:2529-2530`). 이것을 요구하는 이유는 user_code가 URL에 노출된(`verification_uri_complete`) 경우에도 **브라우저 세션만 가진 공격자**가 complete를 대신 못 하도록 하기 위한 second factor
- 성공 시 `render_cli_sso_success_page` HTML

**`GET /sso/cli/poll/{key_id}`** (`ui_sso.py:2626-2756`)
- 인증: `x-litellm-cli-poll-secret` 헤더 vs `flow["poll_secret_hash"]`(`_verify_cli_sso_poll_secret`, `:435`), 불일치 403
- 두 단계 poll:
  1. `team_id` 없이 1팀 초과 유저면 `{"status": "ready", "teams": [...], "team_details": [...], "requires_team_selection": true}`(`:2674-2693`)
  2. `team_id` 지정 시 유저 팀에 속하는지 검증(`:2696-2701`), `ExperimentalUIJWTToken.get_cli_jwt_auth_token`으로 JWT 생성(`:2722-2729`), **즉시 캐시 삭제**(`:2732`, single-use)
- pending이 계속되면 `{"status": "pending"}`(`:2653`)

**`cli_sso_callback`** (`ui_sso.py:2553-2623`) + `_complete_cli_sso_callback_session` (`:2453-2550`)
- 유저 조회: `get_user_info_from_db`(`:2476`), `verify_user_in_restricted_sso_group`(`:2597`)
- team details: `fetch_cli_sso_team_details`(`:2499`) - 팀별 model grants(team_models, team_model_aliases)까지 캐시에 담아 poll 단계에서 JWT에 그대로 반영
- Microsoft agent subject enrollment(`:2505-2509`), attribution metadata 저장(`:2511-2516`)
- 검증 페이지 렌더: `_render_cli_sso_verification_page`(`:2544`), `verification_uri_complete`가 사용된 로그인이면 `prefill_user_code`로 입력값 미리 채움

### 3.3 CLI side

`litellm/proxy/client/cli/commands/auth.py`:

- `login`(`:897`) -> `_start_cli_sso_flow`(`:597`)으로 `/sso/cli/start` 호출, `CliSsoStartData`(`:128`) 검증
- 브라우저 열기 -> `_poll_for_authentication`(`:644`)에서 `x-litellm-cli-poll-secret` 헤더(`_get_cli_sso_poll_headers`, `:640`)로 poll
- 팀 선택: `_handle_team_selection_during_polling`(`:709`), `display_interactive_team_selection`(`:387`)
- 성공 시 `_finish_login`(`:833`) -> `save_token`(`:235`)이 keyring(`SecretVault`, default `SYSTEM_KEYRING`)에 `CliTokenData`(`:67`) 저장, `--config-claude`면 `_configure_claude_code`(`:808`)

### 3.4 보안 설계

- 캐시에 평문 secrets 저장 안 함(해시만, `ui_sso.py:756-758`)
- 모든 시크레트 비교는 `secrets.compare_digest`(timing attack 방어, `:809`, `:815`, `:435` 부근)
- start 단계 rate limit(IP hash 기준, `:346-377`), flow TTL 600초
- user_code 재입력: user_code가 URL에 노출되어도(browser_complete_token 없이는) complete 불가
- JWT poll 직후 single-use 캐시 삭제(`:2732`)

---

## 4. PKCE 브라우저 로그인 상세

`lite login --pkce`(`auth.py:867`의 `_pkce_login`)와 `litellm/proxy/client/cli/commands/pkce_login.py`가 클라이언트이며, 서버는 `litellm/proxy/_experimental/mcp_server/`의 MCP discoverable OAuth 엔드포인트다. 이 표면은 MCP 클라이언트용 OAuth와 같은 authorization server를 공유한다(LiteLLM이 OAuth provider 역할을 한다는 점이 MCP gateway 문서화에서 반복되는 구조).

### 4.1 Discovery

- `GET /.well-known/litellm-cli-auth` (`discoverable_endpoints.py:2632`) -> `native_client_auth_contract` (`gateway_dcr_flow.py:847-861`)
- 계약은 **versioned**다: `NATIVE_CLIENT_AUTH_CONTRACT_VERSION = 1` (`gateway_dcr_flow.py:827`). docstring대로 "기존 필드의 의미가 바뀌거나 사라질 때만" bump하고, 필드 추가는 free
- `NativeClientAuthContract` field (`gateway_dcr_flow.py:832-844`): issuer, authorization/token/registration/revocation endpoint, `resource`(RFC 8707), `response_types_supported`, `grant_types_supported`, `code_challenge_methods_supported`, `token_endpoint_auth_methods_supported`, `revocation_endpoint_auth_methods_supported`
- grant types는 `supported_grant_types()`(`:209`)가 `token_exchange_available`, `jwt_bearer_available` 플래그에 따라 결정

클라이언트는 discovery path 상수가 `CLI_AUTH_DISCOVERY_PATH`(`pkce_login.py:32`)로 이를 fetch하고 `contract_version`을 확인한다.

### 4.2 Dynamic Client Registration

- `POST /register` (`discoverable_endpoints.py:3481`) -> `register_aggregate_client` (`gateway_dcr_flow.py:515`)
- native client라 **client_secret 없음**. client_id는 `is_gateway_dcr_client_id`(`:433`)가 판별하는 리터럴 prefix를 갖는 sealed handle
- registration bounds: `MAX_REDIRECT_URIS`(`:140`), `MAX_STATE_LENGTH`(`:148`), `MIN_CODE_VERIFIER_LENGTH`(`:153`), `MAX_CODE_VERIFIER_LENGTH`(`:154`)
- client 정보 자체는 sealed payload로 캐시에 저장, `open_gateway_dcr_client`(`:511`)가 복원

### 4.3 Authorization

`GET /authorize` (`discoverable_endpoints.py:2368`)는 분기점이다:

```
/authorize?client_id={gateway_dcr_...}&resource=...
   |
   +-- resource == proxy API (is_proxy_api_resource, :872)
   |        -> native_client_authorize (gateway_dcr_flow.py:766)
   |             - loopback redirect만 허용 (:789, is_loopback_redirect_host)
   |             - 미로그인 -> /login 리다이렉트 (_login_redirect, :907)
   |             - consent 팀 조회 (lookup_consent_teams, :794)
   |             - consent 페이지 렌더 (render_native_client_consent_page, :807)
   |               - CSP: frame-ancestors 'none', X-Frame-Options: DENY (:819-825)
   |               - flow handle은 폼에만, URL에 안 나감
   |             - flow를 per-flow 쿠키로 seal (_set_flow_cookie, :939)
   |
   +-- 아니면 (MCP aggregate)
            -> aggregate_authorize (:693)
                 - S256 PKCE 강제, RFC 8707 resource / gateway scope 해석
                 - /ui/connect?connect_flow=<handle> 로 리다이렉트 (:760)
                 - unknown scope는 RFC 6749 4.1.2.1 invalid_scope (:727-736)
```

공통 검증 `_rejected_authorize_request`(`:881`): client 검증, redirect_uri matching, state 길이, `response_type=code`, `code_challenge_method=S256` 강제.

### 4.4 Code 발급과 Token

1. 사용자가 consent/connect 페이지에서 승인 -> `POST /authorize/complete` (`discoverable_endpoints.py:2569`)
2. sealed authorization code 생성, **single-use**로 등록, loopback `redirect_uri`로 `code`+`state` 리다이렉트
3. 클라이언트 local server(`pkce_login.py`의 loopback HTTPServer, `CALLBACK_PATH="/callback"`, `:33`)가 code를 받아
4. `POST /token` (`discoverable_endpoints.py:2446`) -> `aggregate_token` (`gateway_dcr_flow.py:1569`)
   - grant `authorization_code`: `_authorization_code_grant` (`:1821`), PKCE 검증 `_pkce_verifier_matches` (`:1308`, verifier 길이 bound check 포함)
   - grant `refresh_token`: `_refresh_token_grant` (`:1873`), refresh family 기반 재사용 탐지(`_family_refusal`, `:1415`)로 rotation replay를 거부
    - grant `urn:ietf:params:oauth:grant-type:token-exchange` / `urn:ietf:params:oauth:grant-type:jwt-bearer`: `_token_exchange_grant`(`:1945`), `_jwt_bearer_grant`(`:1932`). 그 외 4종(authorization_code, refresh_token)만 지원, `else unsupported_grant_type`
5. token 응답:
   - proxy API resource grant: `proxy_credential_response`(`:1462`) -> `mint_proxy_credential`(`proxy_api_credentials.py:50`)로 **유저의 personal proxy API credential**을 발행(consent에서 고른 팀 scope로). 이 credential이 곧 `lite login --pkce`의 최종 산출물
   - aggregate MCP grant: `_session_token_pair`(`:1432`) - short-lived access + refresh(issuer `SESSION_ISSUER`, `session_token.py:64`; refresh TTL 14일, `SESSION_REFRESH_TTL_SECONDS = 1209600`, `session_token.py:74`)
6. `POST /revoke` (`discoverable_endpoints.py:2599`) -> `revoke_refresh_token`(`:1971`) / `revoke_session_refresh_token`(`:1986`)
7. `POST /introspect` (`discoverable_endpoints.py:2612`, `user_api_key_auth` 의존) -> `introspect_gateway_token`(`:2056`)

상태는 `_seal`/`_open_sealed`(`:484`/`:497`)로 암호화된 sealed model로 운반되며, `_ServerScopedSealedModel`(`:383`) 계층이 멀티-서버(aggregate) 시 server scope를 payload에 묶는다.

### 4.5 시퀀스 다이어그램

```
CLI(terminal)        Browser           LiteLLM(AS)            user
  |  discovery GET                    |
  |-------------------> /.well-known/litellm-cli-auth
  |  POST /register (public client)   |
  |  code_verifier, S256 challenge    |
  |  open browser: /authorize?...&resource=<proxy_api>&code_challenge=...
  |                        |----------->|  미로그인 -> /login (user 로그인)
  |                        |             |  consent 페이지 (loopback check, 팀 선택)
  |                        |<----------- |  user가 팀 선택 + 승인
  |                        | POST /authorize/complete
  |                        |-------------|  code seal, single-use
  |   <- loopback /callback?code&state --|
  |  POST /token (code + code_verifier)
  |------------------------------------->|  PKCE 검증, credential mint
  |   <- access/refresh 또는 proxy credential
  |  keyring에 저장
```

### 4.6 관련 well-known

MCP discovery 문서화도 같은 파일: `/.well-known/oauth-protected-resource`(`:3155`), `/.well-known/oauth-authorization-server`(`:3393`), `/.well-known/openid-configuration`(`:3408`), `/.well-known/jwks.json`(`:3435`).

---

## 5. 토큰/세션 모델

SSO 3플로우 + UI 로그인이 발행하는 토큰은 4종이다.

### 5.1 UI 세션 쿠키 JWT

| 항목 | 값 | 근거 |
|---|---|---|
| 형식 | JWT, HS256, **master key** 서명 | `login_utils.py:498` |
| claims | `ReturnedUITokenObject` 전체 + `exp` | `login_utils.py:497`, `types/proxy/ui_sso.py:6` |
| 수명 | `LITELLM_UI_SESSION_DURATION`, 기본 **24h** | `constants.py:1841`, `login_utils.py:478-485` |
| 발행 choke point | `encode_ui_session_jwt` (SSO, /login, /v2, /v3 공통) | `login_utils.py:488` |

`ReturnedUITokenObject` field: user_id, key(proxy가 발급하는 virtual key), user_email, user_role, login_method, premium_user, auth_header_name, server_root_path, password_reset_required (`login_utils.py:519-530`의 `create_ui_token_object`).

### 5.2 session token (가상키 파생 encrypted blob)

"JWT"라는 이름(`get_cli_jwt_auth_token`)과는 달리, **실제 JWT가 아니다**. `UserAPIKeyAuth` pydantic 객체의 JSON을 master key로 암호화한 bearer 토큰 + prefix `litellm_login_`(`LITELLM_SESSION_TOKEN_PREFIX`, `auth_checks.py:3969`)이다.

```
encrypt_bearer_token(UserAPIKeyAuth.model_dump_json(), prefix="litellm_login_")
    -> auth_checks.py:4004 (UI), :4077 (CLI)
```

| 항목 | UI login token | CLI JWT |
|---|---|---|
| 생성 | `get_experimental_ui_login_jwt_auth_token` (`auth_checks.py:3974`) | `get_cli_jwt_auth_token` (`auth_checks.py:4007`) |
| 수명 | **고정 10분** (`:3985`, docstring: security 위해 LITELLM_UI_SESSION_DURATION 미사용) | `CLI_JWT_EXPIRATION_HOURS`, 기본 **24h** (`:4045`, `constants.py:1829-1831`) |
| 팀 | "litellm-dashboard" 하드코딩 (`:3998`) | poll 시 선택한 팀, team_alias/models/aliases 포함 (`:2722-2729`) |
| limit | rpm 100, max_budget (UI test key pane용) | `allowed_routes` 옵션 (`:4074`), `is_session_token=True` (`:4073`) |
| 토큰 id | "ui-token" | `cli-session-<token_urlsafe(16)>` alias `cli-session-<user_id>` (`:4056-4057`, `CLI_SESSION_KEY_PREFIX` = "cli-session", `constants.py:1828`) |

검증: req에 이 prefix token이 오면 `get_user_object`(`auth_checks.py:2929`)가 `get_key_object_from_ui_hash_key`(`:4080-4096`)로 복호화 -> `UserAPIKeyAuth`로 검증.

### 5.3 PKCE/MCP session token

- access + refresh pair: `_session_token_pair` (`gateway_dcr_flow.py:1432`)
- refresh TTL **14일** (`SESSION_REFRESH_TTL_SECONDS = 1209600`, `session_token.py:74`), issuer `SESSION_ISSUER` (`:64`), mint `mint_session_refresh_token` (`:356`)
- refresh는 rotation, family 단위 replay 거부 (`_family_refusal`, `gateway_dcr_flow.py:1415`)
- proxy-API grant의 token은 별도: `mint_proxy_credential` (`proxy_api_credentials.py:50`)

### 5.4 외부 JWT / OAuth2 (비교용)

proxy 요청 인증 자체도 SSO와 별개로 3가지:

- 자체 JWT: `JWTHandler` (`handle_jwt.py:233`), `auth_jwt` (`:1370`), issuer/JWKS 체크, team claim mapping (`get_team_ids_from_jwt` `:409`, `get_all_jwt_team_ids` `:432`). 웹 SSO의 `ui_access_mode`도 이 클래스를 재사용 (`ui_sso.py:2233`)
- external OAuth2 introspection: `Oauth2Handler` (`oauth2_check.py:15`, RFC 7662)
- trusted oauth2-proxy 헤더: `ALLOWED_OAUTH2_PROXY_FIELDS` = user_id, user_email, team_id, team_alias, org_id, models (`oauth2_proxy_hook.py:28`), `handle_oauth2_proxy_request` (`:40`)

### 5.5 CLI SSO flow 캐시 모델

평문 secrets 없이 해시 2종 + 상태 플래그 (`ui_sso.py:756-762`, TTL 600초, `constants.py:1827`). 완료 시 `session_data`에 user_id, user_role, models, user_email, teams, team_details(model grants), attribution_metadata 저장 (`ui_sso.py:2519-2527`).

---

## 6. 보안 설계 리뷰

### 6.1 잘 된 점(근거)

**PKCE surface**
- S256 강제: `_rejected_authorize_request` (`gateway_dcr_flow.py:881`)가 `code_challenge_method=S256` 아니면 거부. verifier 길이 bound `:153-154`
- loopback만: proxy-API grant의 redirect는 `is_loopback_redirect_host` 검증 (`:789`). "personal key는 본인 기계에만"이라는 결정이 docstring에 명시 (`:777-783`)
- client_secret 없는 public client + sealed registration, redirect URI 수량 cap (`:140`)
- single-use: code single-use 등록, flow cookie per-flow, refresh rotation + family replay 거부 (`:1415`)
- consent 페이지 clickjacking 차단: `frame-ancestors 'none'` + `X-Frame-Options: DENY` (`:819-825`)
- versioned discovery 계약 (`:827-844`) -> 클라이언트가 계약 변화를 신뢰할 수 있다
- DI(Protocol: `LookupConsentTeams` `:295`, `MintProxyCredential` `:195`, `ExchangeSubjectToken` `:269`): 테스트ability와 결합도 절감

**CLI SSO**
- 해시-at-rest + compare_digest (`ui_sso.py:756-758`, `:809`, `:815`)
- user_code 재입력(device flow 2-Factor) + browser_complete_token second factor (`:2529-2530`, `:814-818`)
- IP rate limit (`:358`), TTL 600s, poll single-use 삭제 (`:2732`)

**공통**
- 세션 쿠키 수명 single choke point + bounded exp (`login_utils.py:488` docstring)
- email 정규화 단일 지점 (`ui_sso.py:827`)
- restricted SSO group 강제 (`ui_sso.py:2597`)
- debug route gating: `/sso/debug/*`는 `_raise_if_sso_debug_disabled`(`:4989`)로 default 차단

### 6.2 주의점 / trade-off

1. **user_code가 URL query에 노출될 수 있음**: `allow_cli_sso_verification_uri_complete`를 켜면 user_code가 URL에 들어간다(`ui_sso.py:765-779`). browser history, referrer, access log에 남을 수 있다. default off이므로 opt-in trade-off이나, 켠 배포에서는 로깅 정책까지 함께 고려해야 한다
2. **UI 세션 JWT는 master key 대칭 서명**: master key가 새면 24h까지 UI 세션 위조 가능. exp bound는 일부 완화
3. **CLI flow 캐시는 DualCache**: default in-memory면 멀티 인스턴스/리로드 시 flow 소실. 공유 캐시(Redis 등)가 있어야 수평 스텁업에서 동작
4. **`/sso/key/generate`가 비인증 공개 엔드포인트**: SSO 시작 자체가 비인증이어야 하지만, UI 비활성화 체크와 rate limit은 CLI start만. 웹 SSO redirect 시작에는 rate limit을 찾지 못했다(browser 기반이라 CSRF risk 보다 usability 우선, fastapi_sso의 state cookie가 CSRF 방어)
5. **`ui_access_mode` JWTHandler는 cb마다 재생성**(`ui_sso.py:2233-2241`): JWKS 폴링 비용이 매 요청에 쌓일 수 있음(implementation detail, profile 필요)
6. **SAML optional dependency**: `python3-saml` 미설치 환경에서는 SAML 경로 import-time 실패 가능성(설치 가이드와 에러 메시지 품질이 중요)
7. **`get_ui_settings`와 같은 management 엔드포인트는 `user_api_key_auth` 의존**(`ui_sso.py:2878-2883`): 공개 엔드포인트면 정보 노출에 주의. `readiness`(2910)는 auth 미부여로 설계된 health probe

---

## 7. 설정 (env vars, general_settings)

### 7.1 OIDC provider

| 변수 | 역할 | 근거 |
|---|---|---|
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | Google SSO enable + 인증 | `ui_sso.py:1122`, readiness check `:2945-2947` |
| `MICROSOFT_CLIENT_ID` / `MICROSOFT_CLIENT_SECRET` / `MICROSOFT_TENANT` | Microsoft SSO enable + 인증 | `ui_sso.py:1121`, readiness `:2950-2955` |
| `MICROSOFT_SERVICE_PRINCIPAL_ID` | MS 엔터프라이즈 디렉터리 조회용 | `ui_sso.py` (getenv list) |
| `MICROSOFT_AUTHORIZATION_ENDPOINT` / `MICROSOFT_TOKEN_ENDPOINT` / `MICROSOFT_USERINFO_ENDPOINT` | MS 엔드포인트 override | `sso/custom_microsoft_sso.py` (getenv list) |
| `MICROSOFT_USER_EMAIL_ATTRIBUTE` (default `userPrincipalName`) | MS claim attribute | `constants.py:2139` |
| `MICROSOFT_USER_DISPLAY_NAME_ATTRIBUTE` (default `displayName`) | | `constants.py:2140` |
| `MICROSOFT_USER_ID_ATTRIBUTE` (default `id`) | | `constants.py:2141` |
| `MICROSOFT_USER_FIRST_NAME_ATTRIBUTE` (default `givenName`) | | `constants.py:2142` |
| `MICROSOFT_USER_LAST_NAME_ATTRIBUTE` (default `surname`) | | `constants.py:2143` |
| `GENERIC_CLIENT_ID` / `GENERIC_CLIENT_SECRET` | Generic OIDC enable | `ui_sso.py:1123` |
| `GENERIC_AUTHORIZATION_ENDPOINT` / `GENERIC_TOKEN_ENDPOINT` / `GENERIC_USERINFO_ENDPOINT` | Generic OIDC 엔드포인트 3종 | readiness `:2964-2969` |
| `GENERIC_SCOPE`, `GENERIC_INCLUDE_CLIENT_ID`, `GENERIC_INCLUDE_TOKEN_CLAIMS`, `GENERIC_CLIENT_STATE`, `GENERIC_CLIENT_USE_PKCE`, `GENERIC_SSO_HEADERS` | Generic OIDC 미세 제어 | `ui_sso.py` (getenv list) |
| `GENERIC_USER_ID_ATTRIBUTE`, `GENERIC_USER_EMAIL_ATTRIBUTE`, `GENERIC_USER_FIRST_NAME_ATTRIBUTE`, `GENERIC_USER_LAST_NAME_ATTRIBUTE`, `GENERIC_USER_DISPLAY_NAME_ATTRIBUTE`, `GENERIC_USER_ROLE_ATTRIBUTE`, `GENERIC_USER_PROVIDER_ATTRIBUTE`, `GENERIC_USER_EXTRA_ATTRIBUTES` | Generic claim 매핑 | `ui_sso.py` (getenv list) |
| `GENERIC_ROLE_MAPPINGS_ROLES` / `GENERIC_ROLE_MAPPINGS_GROUP_CLAIM` / `GENERIC_ROLE_MAPPINGS_DEFAULT_ROLE` | Generic group -> role 매핑 | `ui_sso.py` (getenv list) |
| `ALLOWED_EMAIL_DOMAINS` | domain allowlist | `ui_sso.py`, `saml_sso.py:479` |

### 7.2 SAML

`SAML_IDP_METADATA_URL` 또는 `SAML_IDP_METADATA_XML`(둘 중 하나면 설정됨, `saml_sso.py:126`), `SAML_SP_ENTITY_ID`(`:154`), `SAML_IDP_METADATA_VALIDATE_CERT`(default true, `:177`), `SAML_STRICT`(default true, `:195`), `SAML_WANT_ASSERTIONS_SIGNED`(default true, `:209`), `SAML_WANT_MESSAGES_SIGNED`(default false, `:210`), `SAML_AUTHN_REQUESTS_SIGNED`(default false, `:211`), `SAML_ALLOW_UNSOLICITED`(default false, `:440`), NameIDFormat override(`:203`).

### 7.3 세션/수명

| 변수 | default | 근거 |
|---|---|---|
| `LITELLM_UI_SESSION_DURATION` | `"24h"` (format: 30s/30m/24h/7d) | `constants.py:1841` |
| `LITELLM_CLI_JWT_EXPIRATION_HOURS` (혹은 `CLI_JWT_EXPIRATION_HOURS`) | `24` | `constants.py:1829-1831` |
| `LITELLM_MASTER_KEY` | - | 세션 JWT/encrypted token 서명용, 없으면 SSO 전체 불가 (`ui_sso.py:2247-2253`) |
| `PROXY_BASE_URL` | - | SSO redirect URL 산출 필요 |
| `PROXY_LOGOUT_URL` | - | `/sso/get/ui_settings` 반환 |
| `LITELLM_UI_API_DOC_BASE_URL` | - | `/sso/get/ui_settings` 반환 |
| `PROXY_DEFAULT_TEAM_DISABLED` | - | `ui_sso.py:2895-2897` |
| `PKCE_STRICT_CACHE_MISS` | - | PKCE strict mode |
| `CLI_SSO_CLAIM_MAP` (혹은 `LITELLM_CLI_SSO_CLAIM_MAP`) | `""` | CLI poll 응답에 노출할 OIDC claim map (comma separated, `constants.py:1835`) |

### 7.4 general_settings

- `allow_cli_sso_verification_uri_complete` (default false): user_code 포함 URL 발급, `_cli_sso_verification_uri_complete_enabled` (`ui_sso.py:322-325`)
- `ui_access_mode` (dict): 웹 SSO의 JWT claim -> team/role 매핑(`sso_group_jwt_field`), `ui_sso.py:2231-2241`
- `use_x_forwarded_for`: CLI start rate limit의 IP 추출, `ui_sso.py:749`
- `role_mappings` / `team_mappings`: DB `sso_config` row의 `sso_settings.role_mappings`(`ui_sso.py:1465`) 또는 `GENERIC_ROLE_MAPPINGS_*` env(`:1493`)에서 로드, 타입은 `RoleMappings`(`types/proxy/management_endpoints/ui_sso.py:65`), `TeamMappings`(`:87`)

---

## 8. 기능 추가 시 확장 포인트

**새 OIDC provider 추가**
- 코딩 없이: `GENERIC_*` env만 (기존 generic OIDC가 그대로 동작)
- 커스텀 클라이언트: `LitellmHttpClientSSO`(`sso/litellm_http_client_sso.py`) 서브클래스 + `ui_sso.py`의 provider 분기 추가(현재 google/microsoft/generic/SAML 4분기는 if-chain, `ui_sso.py:1176-1187`)
- Enterprise: `user_custom_ui_sso_sign_in_handler`(`ui_sso.py:1162`) -> `litellm_enterprise.proxy.auth.custom_sso_handler`
- SSO provider 클라이언트 생성 choke point: `create_generic_sso_provider`(`sso/litellm_http_client_sso.py:206`)

**새 claim -> 유저 필드 매핑**
- `SSOUserDefinedValues` + `apply_user_info_values_to_sso_user_defined_values`(`ui_sso.py:2100`)
- CLI poll에 claim 노출: `CLI_SSO_CLAIM_MAP` 파서 `_parse_cli_sso_claim_map`(`ui_sso.py:443`), value 길이 bound `CLI_SSO_CLAIM_MAX_SCALAR_LENGTH`(`constants.py:1836`, default 1024), key 안전성 검사 `_is_safe_cli_sso_metadata_dest_key`(`:470`)

**role/team 정책**
- `determine_role_from_groups`(`ui_sso.py:847`), `add_user_to_teams_from_sso_response`(`ui_sso.py:3580`), `RoleMappings`/`TeamMappings`(`types/proxy/management_endpoints/ui_sso.py:65`/`:87`)
- 그룹 제한: `SSOAuthenticationHandler.verify_user_in_restricted_sso_group`(`ui_sso.py:2597`)

**CLI SSO flow 확장**
- server: flow dict 필드 추가(`ui_sso.py:756-762`), poll 응답 필드(`:2683-2693`, `:2735-2744`), 검증 페이지 `_render_cli_sso_verification_page`(`:2544`)
- client: `auth.py`의 `_start_cli_sso_flow`(`:597`)/`_poll_for_authentication`(`:644`), `CliTokenData`(`:67`)

**PKCE/OAuth server 확장**
- discovery 필드 추가: `NativeClientAuthContract`(`gateway_dcr_flow.py:832`) + `native_client_auth_contract`(`:847`)에 추가. **contract_version bump 불필요**(의미 변화/삭제 시만, `:827` docstring)
- 새 grant type: `supported_grant_types`(`:209`) + `aggregate_token`(`:1569`) 분기 + 새 `_xxx_grant` 함수
- 새 credential 종류: `MintProxyCredential` Protocol(`:195`) 구현체 추가, `proxy_credential_response`(`:1462`) 분기
- 새 잘린 상태(state): `_ConnectFlow`/`_GatewayAuthCode`(`:398`/`:416`) sealed model 확장

**세션 토큰 종류 추가**
- `ExperimentalUIJWTToken` static method 추가(`auth_checks.py:3972`) + `get_user_object`(`:2929`)의 dispatch 분기

---

## 9. 테스트 커버리지

### 9.1 단위 테스트 (tests/unit/)

| 파일 | test_ 함수 수 | 대상 |
|---|---|---|
| `proxy/management_endpoints/test_ui_sso.py` | **321** | `/sso/*` 전체 route, role/group 매핑, claim 추출, CLI SSO flow, SSO user insert |
| `proxy/management_endpoints/test_saml_sso.py` | 41 | SP metadata build, assertion 검증, unsolicited 거절 |
| `proxy/management_endpoints/test_sso_helper_utils.py` | - | admin access helper |
| `proxy/client/cli/test_auth_commands.py` | 107 | `lite login`/`logout`/`whoami`/`print-token`, keyring 저장, 팀 선택 |
| `proxy/client/cli/test_pkce_login.py` | 35 | PKCE client(discovery fetch, loopback server, code_verifier, timeout) |
| `proxy/_experimental/mcp_server/test_gateway_dcr_flow.py` | **125** | DCR, authorize, PKCE 검증, token/refresh/revoke, replay 거부 |
| `proxy/_experimental/mcp_server/test_discoverable_endpoints.py` | - | `/.well-known/*`, route wiring |
| `proxy/_experimental/mcp_server/test_proxy_api_credentials.py` | - | `mint_proxy_credential`, consent teams |
| `proxy/auth/test_cli_auth.py` | - | CLI session token 생성/검증 |

(함수 수 기준: 이번 커밋에서 각 파일의 `def test_` 개수)

### 9.2 통합 테스트 (tests/integration/, tests/e2e/)

- `authorization/test_cli_sso_login_ui_disabled.py` - UI 비활성화 환경에서도 CLI SSO 동작
- `authorization/test_sso_blank_identity.py` - 빈 identity 입력 edge case
- `mcp/test_mcp_oauth_flows.py` - MCP OAuth 통합 flow
- `authorization/test_claude_code_gateway_oauth.py` - Claude Code gateway OAuth 시나리오
- mock IdP/OAuth2 server: `tests/e2e/idp.py`, `tests/integration/_support/oauth_server.py`

### 9.3 커버리지 평가

- `/sso/*` route와 PKCE authorization server는 **단위 테스트 밀도가 매우 높음**(321 + 125 test). fastapi_sso provider 호출, prisma, cache 모두 mock.
- **실제 IdP와 e2e는 CI에 없다**: SSO provider의 실제 code->token->userinfo 교환이 mock에 의존. AGENTS.md의 "proof of fix" 정책(로컬 proxy 실행 + curl로 실제 provider API 호출)이 이 부분을 보완하는 의도적 trade-off.
- CLI SSO의 user_code re-entry(브라우저 폼) 구간은 `_render_cli_sso_verification_page` 렌더링 결과와 hash 검증만 단위 테스트, 실제 브라우저 interaction은 e2e 대상 아님.
- SAML은 `python3-saml`이 optional이라 설치 환경에 따라 스킵/실행 갈림.

---

## 10. 학습 포인트 & 참고할 설계

1. **세션 수명 single choke point**: `encode_ui_session_jwt`(`login_utils.py:488`)가 SSO/username-password/v2/v3 모든 UI 로그인 경로를 강제한다. docstring이 "exp가 없으면 master key 회전까지 쿠키 유효"라는 과거 결함을 설명. 새 로그인 방식을 만들 때 이 choke point에 붙이는 패턴이 유지보비용

2. **device flow의 2-Factor 재해석**: CLI SSO는 user_code가 URL에 노출되더라도(`verification_uri_complete`) browser_complete_token이 없으면 complete 불가를 만들어, "browser session only attacker"를 차단(`ui_sso.py:2529-2530`, `:814-818`). secrets는 캐시에 해시로만 존재

3. **versioned discovery 계약**: `NATIVE_CLIENT_AUTH_CONTRACT_VERSION`(`gateway_dcr_flow.py:827`) + `NativeClientAuthContract` TypedDict(ReadOnly field, `:832-844`). 언어와 무관한 native client가 "계약만 보고" flow를 실행하게 하는 전제

4. **sealed model의 server-scope 바인딩**: `_ServerScopedSealedModel` 계층(`gateway_dcr_flow.py:383`)이 aggregate(다중 MCP server) flow에서 server scope를 payload에 묶어, 다른 server flow로 세션 탈취를 방지

5. **refresh rotation + family replay detection**: `_family_refusal`(`gateway_dcr_flow.py:1415`)가 refresh family 단위로 재사용을 거부(steal된 refresh token으로 replay해도 가족 전체가 정지)

6. **Protocol-based DI**: authorization server 로직이 `LookupConsentTeams`(`:295`), `MintProxyCredential`(`:195`), `ExchangeSubjectToken`(`:269`), `RedeemAssertion`(`:313`) 등 Protocol seam으로 주입되므로, cache/DB 접촉을 없이 단위 테스트 가능

7. **동일 authorization server 재사용**: MCP OAuth(7-arm credential resolution)와 CLI PKCE 로그인이 같은 discovery/authorize/token 엔드포인트를 공유(`discoverable_endpoints.py`). native client surface을 두 개로 늘리지 않고 `resource` param(RFC 8707)으로 분기

8. **docstring에 보안 의도 명시**: `native_client_authorize`(`gateway_dcr_flow.py:766`) docstring이 "왜 loopback만 허용하는지"(personal key는 본인 기계에만)를 명시. 코드 리뷰 시 의도 파악 비용 절감. 단, `encode_ui_session_jwt`와 같은 핵심 보안 함수의 docstring이 상당히 긴 편

9. **optional dependency graceful degradation**: SAML은 `python3-saml` 미설치 시 import-time 실패가 아니라 설정 미검출로 fallthrough(`ui_sso.py:1176-1187`의 4분기 중 SAML이 마지막). 설치 가이드와 에러 메시지 품질이 중요한 패턴

10. **route 비공개화**: 모든 SSO route가 `include_in_schema=False`로 OpenAPI에서 숨김(`ui_sso.py` 전체). `experimental` tag. 공개 API surface 관리의 의도적 선택

**구조적 리스크(참고)**
- `ui_sso.py`가 **5144행 monster file**: 웹 SSO, CLI SSO, SAML, role/team 매핑, MS Graph group 조회가 한 파일. AGENTS.md의 "monster file 금지" 원칙과 정면으로 충돌. 기능 추가 시 확장 포인트(8장)가 다 이 파일 안이라, 새 코드가 계속 쌓이면 분리 필요
- `/sso/key/generate`에 UI 로그인 + SSO 시작 + CLI SSO 시작 + SAML redirect + Enterprise 분기가 한 route에 집중(`ui_sso.py:1098-1187`). route 분리가 테스트 어려움의 원인
- PKCE surface는 `gateway_dcr_flow.py`가 **2099행**이지만 DCR/authorize/token/revoke/introspect으로 명확히 분리되어 있어 상대적으로 양호

