# K. CLI SSO 로그인(데스크탑) HTTP 계약 + 계정 화면 엔드포인트 (litellm 원본 기준)

"lite desktop" 앱은 litellm 의 **CLI SSO 흐름**(`lite login` 과 같은 것)으로 로그인한다. 이 흐름은 Keycloak 을 거쳐 로그인 키를 받는다. 우리 서버도 같은 HTTP 계약을 내야 한다. 이 문서는 그 흐름과, 로그인 뒤 계정 화면이 부르는 엔드포인트를 적는다. 문서가 아니라 코드 동작을 따랐다.
근거는 모두 `파일:줄`로 달았다. 코드로 확인하지 못한 것은 "미확인"으로 적었다. 오류 본문 모양 (A)~(D)와 역할 문자열은 `H_team_user_contract.md` §2.1·§2.3 의 정의를 그대로 쓴다. 키 검증 순서는 `J_key_contract.md` §5 를 따른다.

## 0. 약어 (모든 경로는 `<workspace>/` 기준)

| 약어 | 파일 |
|---|---|
| SSO | `gateway/litellm/proxy/management_endpoints/ui_sso.py` (작업 트리 = `litellm_airgap` 브랜치) |
| PATCH | `gateway/docker-dev/patches/ui_sso.py` |
| CONST | `gateway/litellm/constants.py` |
| AC | `gateway/litellm/proxy/auth/auth_checks.py` |
| UAK | `gateway/litellm/proxy/auth/user_api_key_auth.py` |
| GR | `gateway/litellm/proxy/auth/resolvers/grants.py` |
| LU | `gateway/litellm/proxy/auth/login_utils.py` |
| EDU | `gateway/litellm/proxy/common_utils/encrypt_decrypt_utils.py` |
| SUCC | `gateway/litellm/proxy/common_utils/html_forms/cli_sso_success.py` |
| AUI | `gateway/litellm/proxy/common_utils/admin_ui_utils.py` |
| PS | `gateway/litellm/proxy/proxy_server.py` |
| PU | `gateway/litellm/proxy/utils.py` |
| T | `gateway/litellm/proxy/_types.py` |
| RC | `gateway/litellm/proxy/auth/route_checks.py` |
| ROLES | `gateway/litellm/types/proxy/auth/user_roles.py` |
| UREPO | `gateway/litellm/repositories/user_repository.py` |
| IU | `gateway/litellm/proxy/management_endpoints/internal_user_endpoints.py` |
| CDA | `gateway/litellm/proxy/management_endpoints/common_daily_activity.py` |
| CDAT | `gateway/litellm/types/proxy/management_endpoints/common_daily_activity.py` |
| SME | `gateway/litellm/proxy/spend_tracking/spend_management_endpoints.py` |
| HE | `gateway/litellm/proxy/health_endpoints/_health_endpoints.py` |
| GE | `gateway/litellm/proxy/guardrails/guardrail_endpoints.py` |
| MCP | `gateway/litellm/proxy/management_endpoints/mcp_management_endpoints.py` |
| FSSO | `gateway/.venv/lib/python3.12/site-packages/fastapi_sso/sso/base.py` (fastapi-sso 0.19.0) |
| SP | `gateway/schema.prisma` |
| DD/… | `gateway/docker-dev/…` |
| DS | `desktop/src/services/sso/sso.ts` |
| DA | `desktop/src/services/sso/ssoAccount.ts` |
| DSH | `desktop/shared/sso.ts` |

---

## 1. 요약

### 1.1 엔드포인트

| 엔드포인트 | 부르는 쪽 | 성공 응답 | 대표 오류 |
|---|---|---|---|
| `POST /sso/cli/start` (SSO:771-814) | 데스크탑 (DS:151) | JSON `{login_id, poll_secret, user_code, expires_in:600}` (+선택 `verification_uri_complete`) | 429 (B) `"Too many CLI login attempts. Try again later."` |
| `GET /sso/key/generate?source=litellm-cli&key=&user_code=` (SSO:1128-1276) | 브라우저 (URL 은 데스크탑이 만든다, DS:90) | **303** → Keycloak authorize (또는 포크 설정 시 200 비밀번호 폼 HTML) | 400 (B) 세션 없음/형식 오류 |
| `GET /sso/callback?code=&state=litellm-session-token:<login_id>[:<user_code>]` (SSO:2245-2348) | 브라우저 (Keycloak 리다이렉트) | **200 HTML** "Complete CLI Login" 폼 (`browser_complete_token` 숨김 + `user_code` 입력) | 401 (B) OAuth error, 400 (A) state 쿠키 불일치 |
| `POST /sso/cli/complete/{login_id}` (SSO:817-853) | 브라우저 (위 폼 제출, form-urlencoded) | **200 HTML** "Authentication Successful!" (`window.close()`) | 400 (B) `"CLI login is not ready"` / `"Invalid verification code"` |
| `GET /sso/cli/poll/{login_id}[?team_id=]` + 헤더 `x-litellm-cli-poll-secret` (SSO:2765-2895) | 데스크탑 (1.5초마다, DS:159-181) | `{"status":"pending"}` / 팀 선택 요구 / `{"status":"ready","key":…}` | 403 (B) 비밀 불일치, 400 (B) 세션 없음·만료·이미 사용 |
| `POST /sso/cli/password/{login_id}` (SSO:2697-2762) | 브라우저 (포크 전용, §6) | `/sso/callback` 과 같은 "Complete CLI Login" HTML | 인증 실패 시 비밀번호 폼 HTML + 그 상태 코드 |

### 1.2 가장 중요한 계약 사항

- **받는 `key` 는 가상 키(`sk-…`)가 아니다.** `litellm_login_` 접두어 + AES-256-GCM 암호문(base64url, 패딩 없음)이다. DB 에 저장되지 않는다. 기본 유효 기간은 24시간이다(§4).
- 로그인 상태는 **캐시에만** 있다(`DualCache`, TTL 600초). DB 에는 없다(§3).
- 사용자 행은 SSO 콜백 때 만든다(없으면 생성). 기본 역할은 **`internal_user_viewer`** 다. 팀은 DB 의 기존 멤버십을 쓴다. dev 설정에서는 Keycloak 그룹으로 팀을 넣지 않는다(§2.4).
- 폴링은 키를 한 번만 준다. 키를 내준 순간 캐시 항목이 지워진다. 그 뒤 폴링은 400 이다.

---

## 2. 전체 순서 (Keycloak 경로)

```
데스크탑                     게이트웨이(:4100)                       브라우저                    Keycloak(:4115)
  │ POST /sso/cli/start {}  ──▶ flow 생성(캐시)
  │ ◀── login_id, poll_secret, user_code, expires_in
  │ 브라우저 열기 ───────────────────────────────────────────▶ GET /sso/key/generate?source=litellm-cli&key=<login_id>&user_code=<code>
  │                            303 Location: authorize?…state=litellm-session-token:<login_id>   ──▶ 로그인 폼
  │                                                                                  POST 자격 증명 ──▶ 302 /sso/callback?code&state
  │                            code 교환(PKCE) + userinfo, 사용자 upsert, flow.sso_complete=true
  │                            200 HTML "Complete CLI Login" ──────▶ 사용자가 코드 입력
  │                                                     POST /sso/cli/complete/<login_id> (user_code, browser_complete_token)
  │                            flow.user_code_verified=true, 200 성공 HTML
  │ GET /sso/cli/poll/<login_id> (x-litellm-cli-poll-secret) — 그 전까지는 pending
  │ ◀── ready (+requires_team_selection) → ?team_id= 로 다시 → ready + key (flow 삭제)
```

### 2.1 `POST /sso/cli/start` (SSO:771-814)

- 요청 본문은 읽지 않는다. 데스크탑은 `{}` 를 JSON 으로 보낸다(DS:151). 인증이 필요 없다.
- **IP 별 속도 제한**: 60초 창에 30회를 넘으면 429 `{"detail":"Too many CLI login attempts. Try again later."}` 다(SSO:183-184, SSO:357-375). 키는 클라이언트 IP 의 sha256 이다. `general_settings.use_x_forwarded_for` 가 켜져 있으면 XFF 를 쓴다(SSO:351-354, SSO:775-779).
- 생성 값 (SSO:782-784):
  - `login_id = "cli-" + secrets.token_urlsafe(24)` → `cli-` + 32자. 검증 정규식 `^cli-[A-Za-z0-9_-]{12,124}$` (SSO:186).
  - `poll_secret = secrets.token_urlsafe(32)` (43자).
  - `user_code`: 알파벳 `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`(I, O, 0, 1 없음) 8자를 `XXXX-XXXX` 로 묶는다(SSO:185, SSO:305-307).
- 캐시에 넣는 flow (SSO:785-792):
  ```json
  {"poll_secret_hash": sha256hex(poll_secret),
   "user_code_hash": sha256hex(정규화 user_code),
   "sso_complete": false, "user_code_verified": false, "session_data": null}
  ```
  정규화는 대문자로 바꾸고 영숫자만 남기는 것이다(`AB-cd`→`ABCD`, SSO:301-302). 해시는 sha256 hex 다(SSO:297-298).
- 응답 (SSO:328-348):
  ```json
  {"login_id": "cli-…", "poll_secret": "…", "user_code": "ABCD-EFGH", "expires_in": 600}
  ```
  `general_settings.allow_cli_sso_verification_uri_complete=true` 일 때만 `verification_uri_complete` 가 `expires_in` 앞에 붙는다. 값은 `{PROXY_BASE_URL}/sso/key/generate?source=litellm-cli&key=<login_id>&user_code=<code>` 다(SSO:322-325, SSO:794-808). dev 설정에는 이 항목이 없다(DD/config.yaml). 데스크탑은 이 필드를 쓰지 않고 URL 을 직접 만든다(DS:14, DS:90).
- 데스크탑 검사: `login_id`·`poll_secret`·`user_code` 가 문자열이고 `expires_in` 이 0 보다 큰 숫자여야 한다. 아니면(또는 2xx 가 아니면, 404 포함) `failed/unreachable` 이다(DS:150-156).

### 2.2 `GET /sso/key/generate` (CLI 분기, SSO:1128-1276)

쿼리는 `source`, `key`, `existing_key`, `return_to`, `user_code` 다(SSO:1129-1136). CLI 는 `source=litellm-cli`(CONST:1823)다. 처리 순서는 다음과 같다.

1. `DISABLE_ADMIN_UI` 가 켜져 있어도 **CLI 로그인은 계속 진행한다**(SSO:1156-1159, AUI:7-8).
2. SSO 가 설정돼 있으면(`MICROSOFT_CLIENT_ID`/`GOOGLE_CLIENT_ID`/`GENERIC_CLIENT_ID`/SAML) 무료 사용자 수 상한을 검사한다(SSO:1161-1168, §7). [premium, 적용 안 함]
3. DB·마스터 키가 없으면 안내 HTML 을 200 으로 돌려준다(SSO:1171-1173, AUI:11-29).
4. `redirect_uri = {PROXY_BASE_URL 또는 요청 base_url}/sso/callback` 이다(SSO:1176-1179, SSO:3651-3670, PU:9149-9174).
5. CLI 이면 `key` 로 flow 를 찾는다. 없으면 400 이다(§5.1, SSO:1181-1182).
6. state = `litellm-session-token:<login_id>` 다. `allow_cli_sso_verification_uri_complete` 가 켜져 있고 `user_code` 가 `XXXX-XXXX` 형식이면 `:<user_code>` 를 더 붙인다(SSO:1185-1189, SSO:3859-3883, CONST:1824). **dev 설정에서는 데스크탑이 보낸 `user_code` 를 무시한다.** 그래서 확인 폼의 코드 칸은 비어 있고, 사용자가 코드를 직접 입력해야 한다.
7. (포크) `CLI_SSO_PASSWORD_LOGIN=true` 이거나 SSO 가 설정돼 있지 않으면 리다이렉트하지 않고 비밀번호 폼을 낸다(SSO:1191-1207, §6).
8. generic OIDC 리다이렉트(SSO:1236-1257, SSO:3405-3466). 응답은 **303** 이다(FSSO:341).

#### Keycloak authorize URL

`{GENERIC_AUTHORIZATION_ENDPOINT}?response_type=code&client_id=…&redirect_uri=…&scope=openid+email+profile&state=…&code_challenge=…&code_challenge_method=S256`

| 파라미터 | 값 | 근거 |
|---|---|---|
| `response_type` | `code` | oauthlib `WebApplicationClient.prepare_request_uri` (FSSO:310-318). 미확인: 파라미터 순서 |
| `client_id` | `GENERIC_CLIENT_ID` (dev: `litellm-proxy`) | SSO:3456-3463 |
| `redirect_uri` | `{PROXY_BASE_URL}/sso/callback` (dev: `http://localhost:4100/sso/callback`. Keycloak 에 등록된 값과 같다, DD/keycloak/litellm-realm.json) | SSO:3651-3670 |
| `scope` | `GENERIC_SCOPE` 를 공백으로 나눈 것. 기본값 `openid email profile` | SSO:3410 |
| `state` | `litellm-session-token:<login_id>` (CLI). 웹 로그인은 `GENERIC_CLIENT_STATE` 또는 `uuid4().hex` | SSO:3614-3622 |
| `code_challenge`, `code_challenge_method=S256` | `GENERIC_CLIENT_USE_PKCE=true` 일 때만 붙는다(dev: 켜짐). verifier 는 `base64url(token_bytes(32))`(43자)이고 challenge 는 `base64url(sha256(verifier))`(패딩 없음)다 | SSO:3624-3637, SSO:4365-4384, SSO:3536-3556 |

PKCE 가 켜져 있으면 다음 일도 한다.
- verifier 를 캐시 키 `pkce_verifier:<state>` 에 `{"code_verifier": …}` 로 600초 둔다. Redis 가 있으면 Redis, 없으면 `user_api_key_cache` 다(SSO:3515-3531).
- 응답에 쿠키 `litellm_oauth_state=<state>` 를 심는다(`Max-Age=600; HttpOnly; SameSite=Lax`, HTTPS 일 때만 `Secure`, SSO:3565-3583). fastapi-sso 도 `sso_state=<state>` 쿠키를 심는다(FSSO:344-345).

### 2.3 `GET /sso/callback` (SSO:2245-2348) — 코드 교환과 클레임

1. 쿼리에 `error` 가 있으면 401 (B) `"OAuth error: <error>[, error_description: <desc>]"` 다(SSO:2250-2260).
2. DB 가 없으면 500, 마스터 키가 없으면 500 (A) 다(SSO:2276-2299).
3. **PKCE 코드 교환** (SSO:1702-1811, SSO:4448-4560):
   - URL `state` 와 쿠키 `litellm_oauth_state` 를 `compare_digest` 로 비교한다. 다르면 400 (A) `"Invalid OAuth state parameter — does not match the browser-bound state cookie."`, `type=auth_error`, `param=state` 다(SSO:1719-1735). `code` 가 없으면 400 (A) `"Missing authorization code in callback"` 이다.
   - 토큰 요청: `POST GENERIC_TOKEN_ENDPOINT`, form 본문 `grant_type=authorization_code&code=…&code_verifier=…&redirect_uri=…`. `GENERIC_INCLUDE_CLIENT_ID` 가 false(기본값)이고 secret 이 있으면 `Authorization: Basic base64(client_id:secret)` 를 붙인다. secret 이 없으면 본문에 `client_id` 를 넣는다(SSO:4473-4498). 200 이 아니면 401 (A) `"Token exchange failed: <status> - <본문 500자>"` 다(SSO:4520-4530).
   - userinfo: `GET GENERIC_USERINFO_ENDPOINT` 에 `Authorization: Bearer <access_token>` 을 붙인다. 실패하거나 비어 있으면 id_token 디코드로 대신한다(SSO:4577-4620). 미확인: id_token 서명 검증 여부.
   - verifier 캐시는 처리가 모두 성공한 뒤에 지운다(SSO:1805-1810).
4. **클레임 → 사용자 필드** (`generic_response_convertor`, SSO:1279-1396):

   | 필드 | 환경 변수 (기본값) | dev 값 | Keycloak 기본 userinfo 에서 나오는 값 |
   |---|---|---|---|
   | `id` → user_id | `GENERIC_USER_ID_ATTRIBUTE` (`preferred_username`) | `email` | `kim@example.com` |
   | `email` | `GENERIC_USER_EMAIL_ATTRIBUTE` (`email`) | `email` | 소문자로 정규화 (SSO:856-873) |
   | `display_name` | `GENERIC_USER_DISPLAY_NAME_ATTRIBUTE` (`sub`) | 기본값 | Keycloak `sub`(UUID) |
   | `first_name` / `last_name` | `GENERIC_USER_FIRST_NAME_ATTRIBUTE` (`first_name`) / `…LAST_NAME…` (`last_name`) | 기본값 | Keycloak 은 `given_name`/`family_name` 을 쓰므로 **없음** |
   | role | `GENERIC_USER_ROLE_ATTRIBUTE` (`role`) | 기본값 | 없음 → None |
   | team_ids | JWT 핸들러의 `team_ids_jwt_field`/`team_id_jwt_field`, 또는 DB SSO 설정의 team_mappings | 설정 없음 | 빈 목록 |

   - 이름은 점 경로(`a.b`)로 찾는다(`get_nested_value`). `name`, `groups` 클레임은 기본 설정에서 **쓰지 않는다**. `groups` 는 DB SSO 설정에 `role_mappings`(group_claim)가 있을 때만 역할 결정에 쓴다(SSO:1327-1362).
   - `GENERIC_INCLUDE_TOKEN_CLAIMS=true` 이면 id_token·access_token 클레임을 userinfo 와 합친다(SSO:1654-1676).
   - user_id 가 비어 있으면 `first_name+last_name` 으로, 그것도 비어 있으면 email 로 채운다(SSO:3930-3937).
   - `ALLOWED_EMAIL_DOMAINS`(쉼표 구분)가 있으면 이메일 도메인을 검사한다. 어긋나면 401 `{"detail":{"message":"The email domain=…, is not an allowed email domain=[…]. Contact your admin to change this."}}` 이다(SSO:3899-3909).
   - `general_settings.ui_access_mode`(dict)가 있으면 제한 그룹 검사를 한다(SSO:2280-2290, SSO:3737-). dev 설정에는 없다.
5. state 가 `litellm-session-token:` 로 시작하면 CLI 분기다. `state.split(":", 2)` 의 [1] 이 login_id, [2] 가 미리 채울 user_code 다(SSO:2320-2335).

### 2.4 사용자 행 생성·갱신 (`cli_sso_callback` SSO:2618-2688 → `_complete_cli_sso_callback_session` SSO:2500-2579)

- user_id 후보 순서는 (파싱한 user_id, provider id) 다. `get_user_object(user_id, user_email, sso_user_id=…)` 로 기존 행을 찾는다(SSO:1947-1953, SSO:1919-1944, SSO:1956-1984). 미확인: `get_user_object` 가 email 로도 찾는지의 정확한 조건.
- **기존 행이 있으면**: `user_email` 을 갱신한다. `user_alias` 는 비어 있을 때만 채운다. 역할은 SSO 응답에 유효한 LiteLLM 역할이 있을 때만 바꾼다(SSO:2061-2097, SSO:3673-3716).
- **없으면** `insert_sso_user` 로 만든다(SSO:2898-2965). 이때 `/user/new` 를 내부 호출한다(proxy_admin 권한, `auto_create_key=False`).
  - `litellm.default_internal_user_params` 가 있으면 그 값을 적용한다. 역할은 SSO 응답 역할이 유효할 때만 그것을 유지한다(SSO:2924-2938).
  - 역할이 여전히 None 이면 **`internal_user_viewer`** 다(SSO:2946-2947, ROLES:35). dev 설정(DD/config.yaml)에는 `default_internal_user_params` 가 없다. 그래서 Keycloak 로그인 사용자는 `internal_user_viewer` 가 된다.
  - internal_user 이면 `max_budget`·`budget_duration` 기본값을 채운다(SSO:2940-2944).
  - `user_alias = _get_sso_user_alias`: display_name 이 있고 user_id 와 다르면 display_name, 아니면 "first last" 다(SSO:2043-2058). **dev 기본 설정에서는 display_name=`sub` 라서 alias 가 Keycloak sub UUID 가 된다.** 데스크탑은 이 alias 를 이름으로 보여 준다(DA:91, DA:130). (litellm 동작이므로 그대로 둘지 결정 필요.)
  - `sso_user_id = user_id`, `metadata = {"auth_provider": provider}` (SSO:2948-2961).
  - 동시에 같은 사용자를 넣다가 충돌하면 기존 행을 쓰고, 빠진 기본 팀에 넣는다(SSO:2997-3014).
- 팀 반영: SSO 응답의 `team_ids` 중 사용자가 아직 속하지 않은 팀에 `role="user"` 로 `team_member_add` 를 호출한다(실패는 무시, SSO:3720-3735, SSO:1876-1907). dev 에서는 team_ids 가 비어 있어서 아무 일도 없다.
- 세션에 넣는 팀 = 사용자 행의 `teams` 중 **팀 행이 실제로 있는 것만**이다(SSO:2540-2555, SSO:2478-2485). 팀 행 조회가 실패하면 500 (B) `"Could not resolve team model grants for this login. Please try again"` 이다(SSO:2545-2549).
- `CLI_SSO_CLAIM_MAP`(또는 `LITELLM_CLI_SSO_CLAIM_MAP`, 형식 `claim.path->dest.key,…`)이 있으면 허용된 스칼라 클레임을 사용자 `metadata` 에 병합하고 세션에도 둔다(SSO:443-468, SSO:569-651, CONST:1835).
- 세션 저장 (`_store_cli_sso_session`, SSO:2582-2615):
  ```json
  flow.session_data = {"user_id","user_role","models","user_email","teams":[id…],
                       "team_details":[{"team_id","team_alias","team_models":[…],"team_model_aliases":{…}|null}…],
                       "attribution_metadata": {…}|null}
  flow.sso_complete = true
  flow.browser_complete_token_hash = sha256hex(token_urlsafe(32))
  ```
  `team_details` 원소는 `CliSsoTeamDetail` 이다(SSO:258-264, SSO:2449-2457).
- 예외 처리: HTTPException·ProxyException 은 그대로 던진다. 그 밖의 예외는 500 (B) `"Failed to process CLI SSO: <e>"` 다(SSO:2682-2688). 사용자 조회·생성이 실패해 user_info 가 None 이면 500 (B) `"Failed to retrieve user information from SSO"` 다(SSO:2531-2533).

### 2.5 브라우저가 보는 화면

1. **콜백 뒤 확인 폼** (SSO:663-753): `<title>LiteLLM CLI Login</title>`, `<h1>Complete CLI Login</h1>`, `<form method="post" action="{PROXY_BASE_URL}/sso/cli/complete/{login_id}">` 이다. 폼 필드:
   - `<input type="hidden" name="browser_complete_token" value="…">`
   - `<input id="user_code" name="user_code" …>`: 미리 채울 코드가 있으면 `value=` 가 붙고 안내 문구가 "Confirm the verification code below…" 로 바뀐다. 없으면 "Enter the verification code shown in your terminal…" 이다.
   - 버튼 `Continue`.
2. **`POST /sso/cli/complete/{login_id}`** (SSO:817-853): 본문은 `application/x-www-form-urlencoded` 이고 `user_code`, `browser_complete_token` 을 받는다.
   - flow 가 없으면 400 (§5.1)이다. `sso_complete` 이 아니거나 `session_data` 가 없으면 400 (B) `"CLI login is not ready"` 다.
   - user_code(정규화 후 sha256)와 browser_complete_token(sha256)을 `compare_digest` 로 비교한다. 하나라도 틀리면 400 (B) `"Invalid verification code"` 다(같은 문구, SSO:836-847). **시도 횟수 제한은 없다**(미확인: 의도 여부).
   - 성공하면 `flow.user_code_verified = true` 로 다시 저장(TTL 600 초부터 다시 셈)하고 200 성공 HTML 을 준다: `<title>CLI Authentication Successful - LiteLLM</title>`, `<h1>Authentication Successful!</h1>`, 마지막에 `<script>window.close();</script>` (SUCC:6-197).
3. **데스크탑은 `/sso/cli/complete` 를 부르지 않는다.** 사용자가 브라우저에서 코드를 입력해야 한다. 데스크탑은 대기 화면에 `user_code` 를 보여 준다(DS:91).

### 2.6 `GET /sso/cli/poll/{login_id}` (SSO:2765-2895)

쿼리는 `team_id`(선택), 헤더는 `x-litellm-cli-poll-secret`(FastAPI `Header` 라서 헤더 이름은 대소문자 무관, SSO:2767-2771)다. 인증 의존성은 없다.

| 순서 | 조건 | HTTP | 본문 |
|---|---|---|---|
| 1 | login_id 가 `sk-` 로 시작 | 400 (B) | `"Your litellm CLI is out of date and uses a login flow this proxy no longer supports. Upgrade it with `pip install -U 'litellm[proxy]'` and run `lite login` again."` (SSO:390-397) |
| 2 | login_id 형식 오류 | 400 (B) | `"Invalid CLI login session id"` (SSO:398-399) |
| 3 | 캐시에 없음(모름·만료·이미 키를 받음) | 400 (B) | `"CLI login session not found or expired. Run `lite login` again. If this happens immediately after starting a login, the proxy is likely running multiple replicas without a shared cache; configure a Redis cache so every replica can see the login session."` (SSO:401-422) |
| 4 | 헤더 없음 또는 sha256 불일치 | **403** (B) | `"Invalid CLI polling secret"` (SSO:2788-2789, SSO:435-440) |
| 5 | `sso_complete` 또는 `user_code_verified` 가 false | 200 | `{"status":"pending"}` (SSO:2791-2792) |
| 6 | `team_id` 없음 + 세션 팀이 **2개 이상** | 200 | 팀 선택 요구 (아래). flow 는 그대로 둔다 (SSO:2813-2832) |
| 7 | `team_id` 있음 + 세션 팀 목록에 없음 | 403 (B) | `"User does not belong to team: <id>. Available teams: ['a', 'b']"` (파이썬 list repr, SSO:2835-2840) |
| 8 | `team_id` 없음 + 팀 0개 또는 1개 | — | team_id = 첫 팀 또는 None (SSO:2841-2843) |
| 9 | 고른 팀의 grant 를 못 찾음 | 500 (B) | `"Could not resolve the model grants for team: <id>. Please run `lite login` again"` (SSO:2845-2853) |
| 10 | 키 발급 → **flow 삭제(일회성)** → 200 | 200 | 키 응답 (아래) (SSO:2855-2886) |
| — | 그 밖의 예외 | 500 (B) | `"Error checking session status: <e>"` (SSO:2891-2895) |

- **순서 주의**: 세션 조회(1~3)가 비밀 검사(4)보다 먼저다. 모르는 login_id 는 비밀과 상관없이 400 이고, 403 은 세션이 있을 때만 나온다.
- 만료는 따로 검사하지 않는다. 캐시 TTL(600초)이 지나 항목이 없어지면 3번의 400 이 된다. `/sso/cli/complete` 와 콜백이 flow 를 다시 쓸 때마다 TTL 이 다시 600초가 된다(SSO:426-432).

팀 선택 요구 응답 (`status` 가 `"ready"` 인데 `key` 가 없다):
```json
{"status": "ready", "user_id": "kim@example.com", "teams": ["t1", "t2"],
 "team_details": [{"team_id":"t1","team_alias":"A","team_models":["m"],"team_model_aliases":null}, …],
 "requires_team_selection": true,
 "attribution_metadata": {"dest.key": "v"}}      // 있을 때만
```
`team_details` 가 비어 있으면 `[{"team_id": t, "team_alias": null}…]` 로 대신 만든다(SSO:2818-2823).

키 응답:
```json
{"status": "ready", "key": "litellm_login_…", "user_id": "kim@example.com",
 "team_id": "t1" | null, "teams": [...], "team_details": [...],
 "attribution_metadata": {...}}                   // 있을 때만
```
`user_email`, `user_alias`, `team_alias` 는 **없다**(SSO:2874-2885). 데스크탑은 이 값들을 읽으려 하지만 없으면 id 로 대신하고, 로그인 직후 `/v2/user/info`·`/team/info` 로 다시 읽는다(DS:191-193, DS:266-273, DA:85-92).

### 2.7 데스크탑이 각 상태를 다루는 방식 (DS:159-181, DS:205-224)

- `fetch(…, redirect:'manual')` 이고 요청마다 10초 시간 제한이 있다(DS:22, DS:206-212).
- **2xx 가 아니면(400·403·500 모두) 곧바로 `failed/unreachable` 로 끝낸다.** 다시 묻지 않는다(DS:168, DS:213-217). 연결 실패·시간 초과(`offline`)는 만료 전까지 다시 묻는다.
- `status==="ready"` 이고 `requires_team_selection===true` 이면 팀 목록을 읽는다. `teams[]` 는 문자열 id 또는 `{team_id, team_alias|team_name}`, `team_details[]` 는 `{team_id, team_alias|team_name}` 이다(DS:250-264). 설정에서 고른 팀(없거나 목록에 없으면 첫 팀)으로 `?team_id=` 를 붙여 **기다리지 않고 곧바로** 다시 묻는다(DS:170-175).
- `status==="ready"` 이고 `key` 가 비어 있지 않은 문자열이면 저장하고 끝낸다(DS:176-177). 그 밖의 응답(pending 등)은 1.5초 쉬고 다시 묻는다(DS:21, DS:179).
- 만료는 앱이 직접 잰다: `Date.now() >= 시작 시각 + expires_in*1000` 이면 `failed/expired` 다(DS:91, DS:164).
- key 는 불투명한 문자열로만 다룬다(해석하지 않는다). 키 만료는 게이트웨이의 401 로만 안다(DS:17). 계정 화면에서 `/v2/user/info` 가 401 이면 "로그인 필요(expired)" 다(DA:47).
- 루트 URL = provider baseURL 의 끝 `/` 와 `/v1` 을 뗀 것이다(DSH:5-7).

---

## 3. 로그인 상태 저장소·TTL

| 대상 | 저장소 | 키 | TTL | 근거 |
|---|---|---|---|---|
| CLI flow | `cli_sso_session_cache = DualCache(default_in_memory_ttl=600)`. Redis 가 설정돼 있으면 Redis 에만 JSON 문자열로 쓰고 읽는다 | `cli_sso_session:flow:<login_id>` | 600초 (`CLI_SSO_SESSION_TTL_SECONDS`), 다시 쓸 때마다 갱신 | PS:2970, PS:5133-5136, SSO:181, SSO:378-432, CONST:1826-1827 |
| start 속도 제한 | 같은 캐시 | `cli_sso_session:flow:start_rate_limit:<sha256(ip)>` | 60초 | SSO:182-184, SSO:351-375 |
| PKCE verifier | Redis(`redis_usage_cache`) 또는 `user_api_key_cache` | `pkce_verifier:<state>` | 600초 | SSO:3515-3531, SSO:4196-4202 |
| state 쿠키 | 브라우저 | `litellm_oauth_state` | 600초 | SSO:3576-3583 |
| 사용자 행 | DB `LiteLLM_UserTable` | user_id | — | §2.4 |
| 발급한 key | **어디에도 저장하지 않는다**(자기 완결 암호문) | — | 24시간 | §4 |

- Redis 가 없으면 **프로세스 메모리**에 둔다. 그래서 여러 레플리카가 공유하지 못한다(§5.1 의 400 문구가 이 경우를 안내한다). dev 설정에는 Redis 가 없다(DD/compose.yaml).
- flow 에는 poll_secret·user_code·browser_complete_token 의 **해시만** 둔다. 평문은 서버에 남지 않는다.

---

## 4. 발급하는 key (가상 키가 아니다)

`ExperimentalUIJWTToken.get_cli_jwt_auth_token` (AC:4006-4077). 이름에 JWT 가 들어 있지만 실제로는 JWT 가 아니다.

- 형식: `"litellm_login_" + base64url(nonce(12) || AES-256-GCM(평문 JSON) || tag(16))`, 패딩 없음. AAD 는 접두어 문자열 `litellm_login_` 이다(AC:3969, EDU:84-89, EDU:113-119).
- 암호 키: `sha256(LITELLM_SALT_KEY 또는 없으면 master_key)` (EDU:32-40, EDU:68-81). dev 는 `LITELLM_MASTER_KEY=sk-local-dev-master-key` 다(DD/compose.yaml).
- 평문 JSON 은 `UserAPIKeyAuth.model_dump_json(exclude_none=True)` 로 만든다. 내용 (AC:4044-4075):

  | 필드 | 값 |
  |---|---|
  | `token` | `"cli-session-" + token_urlsafe(16)` (CONST:1828) |
  | `key_name` / `key_alias` | `"cli-session-<user_id>"` |
  | `expires` | 지금(UTC) + `CLI_JWT_EXPIRATION_HOURS`(또는 `LITELLM_CLI_JWT_EXPIRATION_HOURS`, 기본 **24**)시간. 형식 `YYYY-MM-DDTHH:MM:SS.mmm+00:00` (CONST:1830-1832) |
  | `max_budget` | None (폴링이 None 을 넘김, SSO:2867) |
  | `user_id`, `user_role` | 세션 값. role 이 None 이면 예외가 나서 500 이다(AC:4041-4042) |
  | `team_id`, `team_alias`, `team_models`, `team_model_aliases` | 고른 팀. 팀이 없으면 `team_models=[]` |
  | `models` | 팀이 있으면 `[]`, 없으면 사용자 `models` |
  | `is_session_token` | `true` |
  | `allowed_routes` | None (CLI 폴링은 넘기지 않는다) |

- 요청 때 검증 (UAK:2618-2637):
  - `sk-` 로 시작하지 않고 캐시에 없으면 복호를 시도한다. `EXPERIMENTAL_UI_LOGIN=false` 이면 시도하지 않는다.
  - 복호에 성공하고 `is_session_token` 이면 `_refresh_session_token_grants` 로 **사용자·팀 행을 매 요청 다시 읽어** 팀 grant, 역할, 모델을 덮어쓴다(UAK:2022-2065).
    - 사용자가 없으면 401 (A) `"Authentication Error, user '<id>' no longer exists."`, `param=user_id` 다(GR:156-164).
    - 팀원이 아니면 403 `"Team '<id>' is not in your team memberships."` 다(GR:166-170). 팀이 없으면 TeamNotFoundError 다.
    - DB 를 못 읽으면 발급 당시 grant 를 그대로 쓴다.
  - 만료는 `"Authentication Error - Expired Key. Key Expiry time … and current time …"` 401 이다. proxy_admin 은 UAK:2641-2662 에서, 나머지는 J §5.1 의 13단계(UAK:3016-3034)에서 검사한다. 미확인: 비관리자 세션 토큰이 정확히 어느 만료 검사를 지나는지(UAK:4586-4600 에도 같은 문구가 있다).
- 이 key 는 DB 에 없다. 그래서 `/key/info` 로 조회하면 404 다(DA:17). 사용량 기록의 `api_key` 칸에는 `sha256(key 문자열)` 이 들어간다고 추정한다(미확인).
- **우리 구현 메모**: 데스크탑은 key 를 해석하지 않는다(DS:17). 그래서 형식(`litellm_login_` + 암호문)을 비트 단위로 맞출 필요는 없다. 지킬 것은 다음뿐이다: `sk-` 로 시작하지 않는 불투명 문자열, Bearer 로 모든 계정·추론 API 에 통한다, 만료되면 401 이다.

---

## 5. 오류 정리

### 5.1 flow 조회 오류 (start 를 뺀 모든 CLI 엔드포인트 공통, `_get_cli_sso_flow_or_raise` SSO:389-422)

모두 (B) `{"detail": "<문자열>"}` 400 이다: `sk-` 로 시작하는 옛 CLI → 업그레이드 안내, 형식 오류 → `Invalid CLI login session id`, 캐시에 없음 → `CLI login session not found or expired. …` (§2.6 표의 1~3).
`/sso/key/generate` 와 `/sso/callback` 에서 이 400 이 나면 브라우저에는 JSON 이 그대로 보인다.

### 5.2 엔드포인트별

§2.1~§2.6 에 적었다. 요약하면 다음과 같다.
- HTTPException 경로는 (B), ProxyException 경로(state 쿠키·토큰 교환·마스터 키·무료 사용자 상한)는 (A) 다.
- `/sso/cli/*` 는 인증 의존성이 없다. 그래서 (D) 는 나오지 않는다.

---

## 6. 포크 패치와 dev 환경이 실제로 쓰는 동작

### 6.1 세 가지 버전

| 버전 | 내용 | 근거 |
|---|---|---|
| upstream `b6f91a221b` | 원본 | `git log` |
| `c7211f1cd7` = **PATCH 파일과 같다** | upstream + 무료 SSO 사용자 상한을 `SSO_MAX_BILLABLE_USERS` 로 설정 가능(`0` = 상한 없음, 기본 5) | `git show c7211f1cd7:…/ui_sso.py \| diff - PATCH` 가 0줄. SSO:1106-1126 |
| `ec2476c468` (현재 작업 트리 SSO) | 위 + **CLI 비밀번호 로그인**: `CLI_SSO_PASSWORD_LOGIN=true` 이거나 SSO 가 설정돼 있지 않으면 `/sso/key/generate` 가 IdP 로 보내지 않고 LiteLLM 이메일·비밀번호 폼을 낸다. `POST /sso/cli/password/{login_id}` 가 `authenticate_user` 로 확인한 뒤 같은 확인 폼을 낸다 | SSO:663-769, SSO:1191-1207, SSO:2582-2615, SSO:2691-2762 |

- `DD/patches/ui_sso.py` 는 compose 도 `Dockerfile.patch` 도 참조하지 않는다. `Dockerfile.patch` 는 `ghcr.io/berriai/litellm:main-latest` 위에서 같은 billable-users 변경만 인라인으로 적용한다(DD/Dockerfile.patch). PATCH 파일은 그 변경의 참고 사본이다.
- `DD/compose.yaml` 은 이미지 `litellm:airgap` 을 쓴다. 이 태그를 어떻게 만드는지는 `docker-dev/` 에 없다. **미확인**(README 는 포크 저장소에서 `docker build -t legacy-gateway .` 로 빌드하라고만 한다, `gateway/README.ko.md:38-44`). 이 세션에는 docker 가 없어서 컨테이너 안의 파일도 확인하지 못했다.
- compose 에 `CLI_SSO_PASSWORD_LOGIN: "true"` 가 있다. 이미지가 `ec2476c468` 를 포함한다면 **지금 dev 설정의 CLI 로그인은 Keycloak 으로 가지 않고 비밀번호 폼을 낸다.**
- 그런데 `verify_cli_sso_e2e.py` 는 Keycloak 계정 `kim / kim-pass-123` 을 보낸다(DD/verify_cli_sso_e2e.py:106-107). 비밀번호 경로에서는 이 값이 `user_email` 대소문자 무시 검색(LU:304-310)이나 UI_USERNAME(`admin`)과 맞지 않아 실패한다. 파일 시각도 e2e 스크립트(10-31 08:39)가 비밀번호 커밋(10-31 18:07)과 compose 수정(11-02)보다 앞선다. 그래서 **e2e 로 검증된 동작은 Keycloak 경로(upstream + billable 패치)** 로 판단한다. 이 문서의 §2 는 그 동작이다.
- 결론(우리 구현): Keycloak 경로를 기준으로 구현한다. 비밀번호 폴백(§6.2)은 포크 확장이고 litellm 원본에는 없다. 넣을지는 따로 정한다. 지금 compose 로 e2e 를 돌리려면 `CLI_SSO_PASSWORD_LOGIN` 을 빼야 Keycloak 경로를 탄다.

### 6.2 (포크) 비밀번호 폼 계약 — 참고

- `GET /sso/key/generate?source=litellm-cli…` → 200 HTML `<h1>Sign in to LiteLLM</h1>`, `<form method="post" action="{PROXY_BASE_URL}/sso/cli/password/{login_id}">`, 필드 `username`(type=email), `password` (SSO:756-769, SSO:1198-1207).
- `POST /sso/cli/password/{login_id}` (form):
  - flow 를 찾는다(§5.1). `authenticate_user` 가 실패하면 같은 폼에 오류 문구를 넣고 그 ProxyException 의 상태 코드·헤더로 돌려준다(SSO:2710-2727).
  - `password_reset_required` 이면 403 폼 `"This password must be changed before it can be used. Change it in the Admin UI, then sign in again."` 이다(SSO:2728-2735).
  - 성공하면 사용자 행의 teams·models 로 §2.4 와 같은 session_data 를 만들고(attribution 없음) 확인 폼을 낸다(SSO:2737-2762). 그 뒤는 §2.5~2.6 과 같다.
  - `disable_password_login_when_sso_enabled` 가 켜져 있으면 403 이다(LU:259-268).

---

## 7. 설정·환경 변수

| 변수 | 쓰는 곳 | 기본값 | dev compose 값 |
|---|---|---|---|
| `PROXY_BASE_URL` | redirect_uri, 폼 action URL (PU:9149-9174) | 요청 base_url | `http://localhost:4100` |
| `SERVER_ROOT_PATH` | 위 URL 앞부분 | `""` | 없음 |
| `GENERIC_CLIENT_ID` | SSO 켜짐 판정, authorize·토큰 교환 | — | `litellm-proxy` |
| `GENERIC_CLIENT_SECRET` | 없으면 리다이렉트 때 500 (A) `"GENERIC_CLIENT_SECRET not set…"` (SSO:3413-3419) | — | `dev-public-secret` (Keycloak 클라이언트는 `publicClient: true` 다. secret 을 Basic 으로 보냈을 때 Keycloak 이 받아들이는지는 미확인, e2e 통과로 추정) |
| `GENERIC_AUTHORIZATION_ENDPOINT` | 브라우저 리다이렉트 대상 | — | `http://localhost:4115/realms/litellm/protocol/openid-connect/auth` (브라우저에서 닿는 주소) |
| `GENERIC_TOKEN_ENDPOINT` | 서버 간 토큰 교환 | — | `http://keycloak:8080/realms/litellm/protocol/openid-connect/token` (컨테이너 망 주소) |
| `GENERIC_USERINFO_ENDPOINT` | 서버 간 userinfo | — | `http://keycloak:8080/realms/litellm/protocol/openid-connect/userinfo` |
| `GENERIC_SCOPE` | scope | `openid email profile` | 없음 |
| `GENERIC_CLIENT_USE_PKCE` | PKCE S256 | `false` | `true` |
| `GENERIC_CLIENT_STATE` | 웹 로그인 고정 state (CLI 는 무시) | uuid | 없음 |
| `GENERIC_INCLUDE_CLIENT_ID` | 토큰 교환 때 본문에 client_id/secret (SSO:1408, SSO:4489-4498) | `false` | 없음 |
| `GENERIC_INCLUDE_TOKEN_CLAIMS` | 토큰 클레임 병합 | `false` | 없음 |
| `GENERIC_USER_ID_ATTRIBUTE` | user_id | `preferred_username` | `email` |
| `GENERIC_USER_EMAIL_ATTRIBUTE` | email | `email` | `email` |
| `GENERIC_USER_DISPLAY_NAME_ATTRIBUTE` / `…FIRST_NAME…` / `…LAST_NAME…` / `…ROLE…` / `…PROVIDER…` / `GENERIC_USER_EXTRA_ATTRIBUTES` | §2.3 | `sub` / `first_name` / `last_name` / `role` / `provider` / 없음 | 없음 |
| `ALLOWED_EMAIL_DOMAINS` | 도메인 제한 | 없음 | 없음 |
| `SSO_MAX_BILLABLE_USERS` (포크) | 무료 사용자 상한 | `5` | `"0"` (상한 없음) |
| `CLI_SSO_PASSWORD_LOGIN` (포크) | §6 | 없음 | `"true"` |
| `CLI_JWT_EXPIRATION_HOURS` / `LITELLM_CLI_JWT_EXPIRATION_HOURS` | key 수명 | `24` | 없음 |
| `CLI_SSO_CLAIM_MAP` / `LITELLM_CLI_SSO_CLAIM_MAP` | attribution metadata | 없음 | 없음 |
| `LITELLM_SALT_KEY` | key 암호화 (없으면 master key) | — | 없음 → `LITELLM_MASTER_KEY=sk-local-dev-master-key` |
| `DISABLE_ADMIN_UI` | 웹 로그인 차단(CLI 는 통과) | — | 없음 |
| `EXPERIMENTAL_UI_LOGIN` | `false` 면 세션 토큰 복호를 끈다 | 미설정 = 켜짐 | 없음 |
| `general_settings.allow_cli_sso_verification_uri_complete` | start 응답 URL, 코드 미리 채우기 | `false` | 없음 |
| `general_settings.use_x_forwarded_for` | start 속도 제한 IP | `false` | 없음 |
| `general_settings.ui_access_mode` | 제한 그룹 | 없음 | 없음 |
| `litellm_settings.default_internal_user_params` | 새 SSO 사용자 기본값(역할 포함) | 없음 → `internal_user_viewer` | 없음 |

dev 의 나머지 값: `DATABASE_URL=postgresql://litellm:litellm@db:5432/litellm`, `UI_USERNAME=admin`, `UI_PASSWORD=admin-pass-123`, 포트 `127.0.0.1:4100→4000`(litellm), `4115→8080`(Keycloak 26.1, `start-dev --import-realm`, 관리자 `admin/admin`), `127.0.0.1:5434→5432`(Postgres 16) (DD/compose.yaml).
Keycloak realm `litellm` (DD/keycloak/litellm-realm.json): `sslRequired=none`. 클라이언트 `litellm-proxy` 는 `publicClient=true`, standard flow 만 켜져 있다. `redirectUris=["http://localhost:4100/sso/callback"]`, `webOrigins=["http://localhost:4100"]`. 사용자 `kim` (email `kim@example.com`, `Kim Taehan`, 비밀번호 `kim-pass-123`). **그룹·그룹 매퍼·역할 매퍼는 없다.**

---

## 8. 인수 시험 (e2e 스크립트)

둘 다 표준 라이브러리만 쓴다(`http.client`, 쿠키를 직접 들고 다니며 리다이렉트를 손으로 따라감). **assert 가 없다.** 단계별 상태를 출력하고 끝에 `E2E-DONE` 을 찍는다. 그래서 통과 여부는 출력을 보고 판정한다. 대상 주소는 `http://localhost:4100` 고정이고 환경 변수는 받지 않는다. 쿠키 jar 는 호스트를 구분하지 않고 모든 쿠키를 모든 호스트에 보낸다.

### 8.1 `DD/verify_cli_sso_e2e.py` — CLI 흐름 (이번 계약의 인수 시험)

| 단계 | 하는 일 | 성공 판정(출력) |
|---|---|---|
| 1 (:82-90) | `POST /sso/cli/start` `{}` (JSON) | status 200, keys = `['expires_in','login_id','poll_secret','user_code']` (정렬) |
| 2 (:92-104) | `GET /sso/key/generate?source=litellm-cli&key=<login_id>&user_code=<code>` 를 리다이렉트 끝까지 따라간다(최대 14번) | Keycloak 로그인 폼에 도착. `form fields` 에 `username` 이 있다 |
| 2b (:106-113) | 폼 action 에 `{…숨김 필드, username:"kim", password:"kim-pass-123"}` 를 POST 하고 리다이렉트를 따라간다(→ `/sso/callback`) | 마지막 화면 = "Complete CLI Login" 폼. `completion form fields` 에 `browser_complete_token`, `user_code` |
| 3 (:115-125) | 폼 action(절대 URL `…/sso/cli/complete/<login_id>`)에 `user_code=<start 값>`, `browser_complete_token=<폼 값>` 을 form 으로 POST | status 200, `success? True` (본문에 "success" 가 있다) |
| 4 (:127-133) | `GET /sso/cli/poll/<login_id>` + `x-litellm-cli-poll-secret` | status 200, `got JWT: True` (= 팀이 1개 이하라서 곧바로 `key` 가 온다. 팀 선택 분기는 시험하지 않는다) |
| 5 (:135-149) | `POST /v1/chat/completions` (`model:"qwen3-coder"`, `Authorization: Bearer <key>`) | status 200, reply·usage 출력. **주의: 지금 `DD/config.yaml` 에는 `qwen3-coder` 가 없다**(`lfm3-local`, `qwen3.8-27b`, `qwen3.5-122b` 만 있다). 지금 설정으로는 이 단계가 실패한다. 모델 이름을 바꾸거나 이 단계를 따로 판정해야 한다 |

### 8.2 `DD/verify_sso_e2e.py` — 웹(대시보드) SSO 흐름 (참고)

- `GET /sso/key/generate`(쿼리 없음) → Keycloak 폼 → 숨김 필드 + `kim/kim-pass-123` POST(`Referer`·`Origin` 헤더 포함) → 리다이렉트 끝(:70-104).
- 마지막 URL·상태·바이트 수를 출력하고, 본문 앞 3000자에 "error" 가 있으면 그 앞뒤를 보여 준다. 기대 결과는 대시보드로 리다이렉트되고 세션 쿠키가 심어지는 것이다(웹 SSO 계약은 이 문서 범위 밖).

### 8.3 실행

```bash
cd <workspace>/gateway/docker-dev
docker compose up -d            # db, litellm(:4100), keycloak(:4115). Keycloak 경로를 보려면 compose 에서 CLI_SSO_PASSWORD_LOGIN 을 빼거나 false 로
python3 verify_cli_sso_e2e.py   # CLI SSO 계약
python3 verify_sso_e2e.py       # 웹 SSO (참고)
```
우리 Go 게이트웨이로 돌리려면 4100 포트에 우리 서버를 띄우고 같은 Keycloak 과 env(`PROXY_BASE_URL=http://localhost:4100`, redirect_uri 일치)를 쓴다. 스크립트가 주소를 고정하고 있기 때문이다.

---

## 9. 계정 화면 엔드포인트 (데스크탑 `fetchAccount`, DA:33-81)

모든 요청은 `GET {루트}{경로}`, 헤더 `authorization: Bearer <login key>`, `redirect:'manual'`, 10초 시간 제한이다(DA:94-114). 2xx 가 아니면 그 영역만 실패로 표시한다(DA:116-121). **`/v2/user/info` 가 401 이면 전체가 "로그인 필요(expired)"** 다(DA:47). 연결 실패이면 전체가 offline 이다(DA:46).
순서: `/v2/user/info` 를 먼저 부르고, 그다음 7개를 동시에 부른다. 끝으로 프로필의 `teams[]` 마다 `/team/info` 를 부른다(DA:44-66).

### 9.1 호출 표 (정확한 쿼리와 읽는 필드)

| 엔드포인트 | 쿼리 | 데스크탑이 읽는 필드 | litellm 응답 원본 | 라우트 권한 (session token 의 역할 기준) |
|---|---|---|---|---|
| `/v2/user/info` (DA:44, DA:87) | 없음 (본인) | 최상위 `user_id`(필수), `user_email`, `user_alias`, `user_role`, `spend`, `max_budget`, `budget_duration`, `budget_reset_at`, `tpm_limit`, `rpm_limit`, `models[]`, `created_at`, `teams[]` (DA:123-141, DA:64) | `UserInfoV2Response` 평면 객체: 위 필드 + `metadata`, `updated_at`, `sso_user_id`, `teams`(id 목록), `object_permission`, `model_max_budget`, `model_max_budget_usage` (IU:1062-1166, T:3172-3198). user_id 를 안 주면 키의 user_id. 없으면 400, 권한이 없거나 없는 사용자면 404 `"User not found: <id>"` | info_route → 통과 (J §6.2) |
| `/user/info` (DA:54) | 없음 | `keys[]` 의 `user_id`(내 것만 남김), `key_name`(필수), `key_alias`, `spend`, `expires`, `blocked`, `models[]`. `token` 은 읽지 않는다 (DA:161-178) | `{"user_id","user_info","keys","teams"}` — H §4.4 (IU:922-1003, T:3165) | 본인은 통과 |
| `/team/info` (DA:66, DA:88) | `team_id=<id>` | `team_info.team_alias`, `.models[]`, `.spend`, `.max_budget`, `.members_with_roles[]`(내 줄의 `role`, 없으면 `'user'`), `team_memberships[]`(최상위 또는 `team_info` 안. 내 줄의 `spend`). 403·404 는 "안 속한 팀"이라 조용히 뺀다 (DA:180-201) | `{"team_id","team_info","keys","team_memberships"}` — H §3.9 (오류는 (A) `auth_error`) | info_route → 통과, 핸들러가 멤버십 판정 |
| `/model_group/info` (DA:55) | 없음 | `data[]` 의 `model_group`(필수), `providers[]`, `max_input_tokens`, `max_output_tokens`, `input_cost_per_token`, `output_cost_per_token`, `supports_vision`/`supports_reasoning`/`supports_function_calling`(=== true), `tpm`, `rpm` (DA:203-220) | 이미 구현함(이전 커밋 "/model_group/info") | llm_api_routes(model_info_routes) → 통과 (T:540-557) |
| `/user/daily/activity` (DA:52, DA:58) | `start_date=<YYYY-MM-DD>&end_date=<오늘>&page_size=1000&user_id=<내 id>` — start = 이달 1일과 29일 전 중 이른 날(기기 시간대). 프로필에서 id 를 못 읽었으면 부르지 않는다 | §9.2 | §9.2 | self_managed_routes → 모든 역할 통과 (T:870), 핸들러가 범위 판정 |
| `/spend/logs` (DA:59) | `start_date=<어제>&end_date=<내일>&summarize=false` | §9.3 | §9.3 | spend_tracking_routes → internal_user·internal_user_viewer 통과 (T:686-708, T:788-832) |
| `/health/readiness` (DA:60) | 없음 | `status`(필수), `db` (DA:280-284) | **인증 없음**. `{"status":"healthy","db":"connected"\|…}`. DB 가 안 붙고 허용 설정도 없으면 503. 종료 중이면 `{"status":"shutting_down"}` 503. `allow_public_health_readiness_details` 가 켜져 있으면 상세 모양 (HE:1965-1990) | 공개 |
| `/guardrails/list` (DA:61) | 없음 | `guardrails` 배열의 **개수만** (DA:78) | `{"guardrails":[{guardrail_name, guardrail_info, …}]}` (GE:162-200) | internal_user 통과(T:804). **internal_user_viewer 는 401 (D)** — 목록에 없다(T:818-832). SSO 로 새로 만든 사용자의 기본 역할이 viewer 라서 이 칸은 실패로 나온다(litellm 동작 그대로) |
| `/v1/mcp/server` (DA:62) | 없음 | 최상위 배열의 **개수만** (DA:79) | `list[LiteLLM_MCPServerTable]` (MCP:95, MCP:1379-1384) | `/v1/mcp/` 접두어는 라우트 단계 통과, 핸들러가 범위 판정 (RC:387-388) |

### 9.2 `GET /user/daily/activity` (IU:2845-2957)

**요청 파라미터** (IU:2852-2887): `start_date`, `end_date`(필수, `YYYY-MM-DD`), `model`, `api_key`, `user_id`, `page`(기본 1, ≥1), `page_size`(기본 50, 1..1000), `timezone`(UTC 기준 분 오프셋, JS `getTimezoneOffset` 규약), `include_current_utc_day`(기본 false, timezone 필요).

**검사·범위**
- 날짜가 없으면 400 (B) `{"detail":{"error":"Please provide start_date and end_date"}}` (IU:2919-2922) 다.
- 날짜 형식이 틀리면 `"start_date and end_date must be valid YYYY-MM-DD dates"`, end<start 면 `"end_date must be on or after start_date"` 다(CDA:86-95, `raise_public` 로 나감. 미확인: 정확한 상태 코드·본문 모양).
- **양 끝 날짜를 포함한다**(날짜 버킷 비교).
- 범위 (IU:2819-2828):
  - admin_view 역할은 `user_id` 가 있으면 그 사용자, 없으면 **전체 합계**다.
  - 비관리자는 호출자 id 로 고정한다. 다른 `user_id` 를 주면 403 `"Non-admin users can only view their own spend data."` 다.
  - 데스크탑은 관리자 키도 전체 합계를 받지 않도록 항상 `user_id` 를 붙인다(DA:56-58).
- DB 가 없으면 500, 그 밖의 예외는 500 `{"detail":{"error":"Failed to fetch analytics: <e>"}}` 다(IU:2950-2957).

**페이지 단위**: 표 `litellm_dailyuserspend` 의 원시 행(날짜 × api_key × model …)으로 페이지를 나눈 뒤, 그 페이지의 행을 날짜별로 합친다(CDA:933-972, IU:2933-2948). 그래서 행이 page_size 를 넘으면 어떤 날짜는 일부만 합쳐진다. 데스크탑이 `page_size=1000` 을 주는 이유다(DA:51).

**응답** `SpendAnalyticsPaginatedResponse` (CDAT:126-128):
```json
{"results": [
   {"date": "2026-11-20",
    "metrics": {"spend":0.0,"flat_cost":0.0,"ptu_hours":0.0,"prompt_tokens":0,"completion_tokens":0,
                "cache_read_input_tokens":0,"cache_creation_input_tokens":0,"compression_saved_tokens":0,
                "compression_savings_spend":0.0,"prompt_caching_savings_spend":0.0,
                "gateway_injected_caching_savings_spend":0.0,"autorouter_savings_spend":0.0,
                "total_tokens":0,"successful_requests":0,"failed_requests":0,"api_requests":0,
                "total_response_time_ms":0,"timed_requests":0},
    "breakdown": {"mcp_servers":{},"models":{},"model_groups":{"<name>":{"metrics":{…},"metadata":{},"api_key_breakdown":{}}},
                  "providers":{},"endpoints":{},"api_keys":{"<hash>":{"metrics":{…},"metadata":{"key_alias","team_id","user_id","user_email","key_exists"}}},
                  "entities":{}}}],
 "metadata": {"total_spend":…, "total_flat_cost", "total_ptu_hours", "total_prompt_tokens", "total_completion_tokens",
              "total_tokens", "total_api_requests", "total_successful_requests", "total_failed_requests",
              "total_cache_read_input_tokens", "total_cache_creation_input_tokens", "total_compression_saved_tokens",
              "total_compression_savings_spend", "total_prompt_caching_savings_spend",
              "total_gateway_injected_caching_savings_spend", "total_autorouter_savings_spend",
              "total_response_time_ms", "total_timed_requests",
              "page":1, "total_pages":…, "has_more":false,
              "api_key_limit":null, "total_api_keys":null, "entity_total_api_keys":null}}
```
필드와 기본값은 CDAT:21-39(SpendMetrics), CDAT:46-77(breakdown), CDAT:80-83(day), CDAT:86-123(metadata) 다. `total_pages = ceil(total_count/page_size)`, `has_more = page*page_size < total_count` 다(CDA:970-972).

**데스크탑이 읽는 것** (DA:222-244, DA:75): `results[]` 의 `date`(필수), `metrics`(필수)의 `spend`, `prompt_tokens`, `completion_tokens`, `total_tokens`, `successful_requests`, `failed_requests`, `total_response_time_ms`, `timed_requests`, 그리고 `breakdown.model_groups.<name>.metrics.total_tokens`. `metadata` 는 읽지 않는다.

### 9.3 `GET /spend/logs` (SME:3485-3692)

**요청 파라미터** (SME:3493-3519): `api_key`, `user_id`, `request_id`, `start_date`, `end_date`, `summarize`(기본 **true**).

**동작**
- 호출자 역할이 `internal_user` 또는 `internal_user_viewer` 이면 **`user_id` 를 호출자 id 로 덮어쓴다**(SME:3565-3569). 다른 역할(proxy_admin 등)은 주어진 대로 쓰거나 전체다.
- 두 날짜가 모두 있으면 `datetime.strptime(d, "%Y-%m-%d")` UTC 자정으로 바꾸고, 필터를 `startTime >= start 00:00 AND startTime <= end 00:00` 로 건다(SME:3577-3595). **end_date 날짜 자체는 사실상 빠진다**(자정 정각만 포함). 데스크탑이 `end_date=내일` 을 주는 이유다(DA:40-42). 형식이 틀리면 ValueError → 500 (A) `"/spend/logs Error<e>"`, `type=internal_error` 다(SME:3674-3692).
- `api_key` 가 `sk-` 로 시작하면 sha256 해시로 바꾼다. `request_id` 는 `request_id` 또는 `litellm_call_id` 와 맞춘다. `user_id` 는 `user` 칸과 맞춘다(SME:3597-3607, SME:384-387).
- `summarize=false`: `LiteLLM_SpendLogs` 행 배열을 `startTime` 내림차순으로 최대 **10,000** 행 준다. 정확히 10,000 이면 응답 헤더 `x-litellm-spend-logs-truncated: true` 를 붙인다(SME:3611-3620, SME:358-373, SME:86).
- `summarize=true`(기본): 날짜별 요약 `[{startTime, spend, users:{}, models:{}}…]` 이고, end_date 까지 0 으로 채운다(SME:3622-3651). 데스크탑은 쓰지 않는다.
- 날짜가 없으면 필터만 걸고 전체에서 최근 10,000 행이다(SME:3653-3670).
- 행 모양 = Prisma `LiteLLM_SpendLogs` 전체 칸(SP:720-763): `request_id, call_type, api_key(해시), spend, total_tokens, prompt_tokens, completion_tokens, startTime, endTime, request_duration_ms, completionStartTime, model, model_id, model_group, custom_llm_provider, api_base, user, metadata, cache_hit, cache_key, request_tags, team_id, organization_id, end_user, requester_ip_address, messages, response, session_id, status, mcp_namespaced_tool_name, billing_agent_id, agent_id, proxy_server_request, litellm_call_id, created_at, updated_at`. 미확인: datetime 직렬화 형식(`Z` 접미어 여부).

**데스크탑이 읽는 것** (DA:246-274): 최상위 **배열**이어야 한다. 각 행에서 다음을 읽는다.
- 내 줄인지 판정: `user`(없으면 `metadata.user_api_key_user_id`)가 내 user_id 와 같아야 한다(앞뒤 공백·대소문자 무시). 아닌 줄은 버리고 개수만 경고한다.
- `startTime`(필수), `model_group`, `total_tokens`, `request_duration_ms`, `status`(없으면 `metadata.status`)를 읽고, `=== 'success'` 면 성공이다.
- 새것 먼저 20건만 남긴다(DA:21). `messages`·`response` 는 읽지 않는다.

---

## 10. premium(유료) 검사 지점 — 우리는 강제하지 않는다. 기록만 남긴다 ("premium, 적용 안 함")

| 위치 | 내용 |
|---|---|
| SSO:1106-1126, SSO:1161-1168, SSO:2402 | SSO 가 설정돼 있고 premium 이 아니면 billable 사용자 수(전체 − SCIM 비활성, UREPO:132-144)가 **5**(포크: `SSO_MAX_BILLABLE_USERS`, `0`=없음)를 넘을 때 403 (A) `"You must be a LiteLLM Enterprise user to use SSO for more than 5 users. …"`, `param=premium_user` 다. `/sso/key/generate` 진입 시 검사한다(CLI 포함). **적용 안 함** |
| SSO:1209-1222 | `user_custom_ui_sso_sign_in_handler`(커스텀 SSO 로그인)는 enterprise 패키지가 필요하다(없으면 ValueError) |
| SSO:5150-5158 | `/sso/debug/login` 은 premium 전용(범위 밖) |
| SSO:1327-1362, SSO:1458-1550 | DB SSO 설정의 role_mappings·team_mappings(그룹 → 역할·팀). 미확인: premium 검사 여부. dev 에서는 쓰지 않는다 |
| RC:313-316 | `admin_only_routes` 설정은 enterprise (J §6.1) |

---

## 11. 미확인 항목

- `litellm:airgap` 이미지의 빌드 출처와 실제로 들어 있는 `ui_sso.py` 버전(비밀번호 로그인 포함 여부). docker 가 없어 확인하지 못했다(§6.1).
- Keycloak public 클라이언트가 Basic 인증(secret)이 붙은 토큰 요청을 받아들이는지. e2e 가 통과했다면 받아들인다.
- authorize URL 의 정확한 파라미터 순서·인코딩(oauthlib 구현 의존). Keycloak 은 순서를 보지 않는다.
- PKCE 경로에서 userinfo 대신 id_token 을 쓸 때 서명을 검증하는지(SSO:4577-4620, SSO:1021-1065).
- `get_user_object` 가 기존 사용자를 user_id·sso_user_id·email 중 무엇으로 찾는지의 정확한 우선순위.
- 비관리자 세션 토큰의 만료 검사가 UAK:3016-3034 와 UAK:4586-4600 중 어디를 지나는지, 그리고 그때 `param` 값.
- 세션 토큰으로 기록된 사용량의 `api_key` 칸 값(sha256(key) 로 추정).
- `/user/daily/activity` 날짜 검증 실패(`raise_public(InvalidDateRange)`)의 정확한 상태 코드와 본문.
- `/sso/cli/complete` 에 시도 횟수 제한이 없는 것이 의도인지.
- `/spend/logs`·`/user/daily/activity` 응답의 datetime·date 직렬화 형식.
