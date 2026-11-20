# L. 대시보드 로그인·세션 유지 HTTP 계약 (litellm 원본 기준)

수정하지 않은 litellm 대시보드가 우리 서버로 로그인하고, 로그인을 유지하고, 로그아웃한다. 이 문서는 그 과정에서 litellm 이 **실제로** 주고받는 모양을 적는다. 문서가 아니라 코드 동작을 따랐다.
근거는 모두 `파일:줄`로 달았다. 코드로 확인하지 못한 것은 "미확인"으로 적었다. 오류 본문 모양 (A)~(D)와 역할 문자열은 `H_team_user_contract.md` §2.1·§2.3 의 정의를 그대로 쓴다. 가상 키 검증 순서는 `J_key_contract.md` §5, CLI(데스크탑) SSO 는 `K_sso_cli_contract.md` 를 따른다. 포크 커밋 `ec2476c468`(CLI 비밀번호 로그인)은 K 에서 다뤘으므로 여기서는 뺀다.
코드 기준은 작업 트리(`litellm_airgap` 브랜치)다.

## 0. 약어 (모든 경로는 `<workspace>/` 기준)

| 약어 | 파일 |
|---|---|
| PS | `gateway/litellm/proxy/proxy_server.py` |
| LU | `gateway/litellm/proxy/auth/login_utils.py` |
| LT | `gateway/litellm/proxy/auth/login_throttle.py` |
| PP | `gateway/litellm/proxy/auth/password_policy.py` |
| UAK | `gateway/litellm/proxy/auth/user_api_key_auth.py` |
| TE | `gateway/litellm/proxy/management_endpoints/team_endpoints.py` |
| RC | `gateway/litellm/proxy/auth/route_checks.py` |
| AU | `gateway/litellm/proxy/auth/auth_utils.py` |
| SSO | `gateway/litellm/proxy/management_endpoints/ui_sso.py` |
| SESS | `gateway/litellm/proxy/management_endpoints/session_endpoints.py` |
| PWD | `gateway/litellm/proxy/management_endpoints/password_endpoints.py` |
| IU | `gateway/litellm/proxy/management_endpoints/internal_user_endpoints.py` |
| ME | `gateway/litellm/proxy/management/users/endpoints.py` (+ `exceptions.py`, `schemas.py`) |
| DISC | `gateway/litellm/proxy/discovery_endpoints/ui_discovery_endpoints.py` |
| DISCT | `gateway/litellm/types/proxy/discovery_endpoints/ui_discovery_endpoints.py` |
| PSE | `gateway/litellm/proxy/ui_crud_endpoints/proxy_setting_endpoints.py` |
| UINV | `gateway/litellm/proxy/management_helpers/user_invitation.py` |
| HINT | `gateway/litellm/proxy/common_utils/html_forms/default_credentials_hint.py` |
| ULF | `gateway/litellm/proxy/common_utils/html_forms/ui_login.py` |
| UTT | `gateway/litellm/types/proxy/ui_sso.py` |
| SAT | `gateway/litellm/types/integrations/slack_alerting.py` |
| T | `gateway/litellm/proxy/_types.py` |
| PU | `gateway/litellm/proxy/utils.py` |
| CONST | `gateway/litellm/constants.py` |
| LINIT | `gateway/litellm/__init__.py` |
| SP | `gateway/schema.prisma` |
| NW | `gateway/ui/litellm-dashboard/src/components/networking.tsx` |
| CK | `gateway/ui/litellm-dashboard/src/utils/cookieUtils.ts` |
| JU | `gateway/ui/litellm-dashboard/src/utils/jwtUtils.ts` |
| RU | `gateway/ui/litellm-dashboard/src/utils/returnUrlUtils.ts` |
| AUTH | `gateway/ui/litellm-dashboard/src/contexts/AuthContext.tsx` |
| LP | `gateway/ui/litellm-dashboard/src/app/login/LoginPage.tsx` |
| DL | `gateway/ui/litellm-dashboard/src/app/(dashboard)/layout.tsx` |
| HK/… | `gateway/ui/litellm-dashboard/src/app/(dashboard)/hooks/…` |
| OF | `gateway/ui/litellm-dashboard/src/app/onboarding/OnboardingForm.tsx` |
| OL | `gateway/ui/litellm-dashboard/src/components/onboarding_link.tsx` |
| CPF | `gateway/ui/litellm-dashboard/src/app/(dashboard)/change-password/ChangePasswordForm.tsx` |
| HAPI | `gateway/ui/litellm-dashboard/src/lib/http/api.ts` |
| CL | `gateway/ui/litellm-dashboard/src/lib/http/client.ts` |
| UI/… | `gateway/ui/litellm-dashboard/src/…` |

---

## 1. 요약

### 1.1 엔드포인트

| 엔드포인트 | 대시보드 호출 위치 | 인증 | 성공 응답 | 대표 오류 |
|---|---|---|---|---|
| `GET /litellm/.well-known/litellm-ui-config` (`/.well-known/…` 도 같음) | NW:562-597 (부팅 때 매번) | 없음 (T:733-734 public) | JSON `UiDiscoveryEndpoints` (§2.1) | — |
| `POST /v2/login` | NW:7112-7170 (LP 제출) | 없음 | 200 JSON `{redirect_url, token}` + `Set-Cookie: token=…` | (A) 401 자격 증명 오류, (A) 429 시도 초과 |
| `POST /login` | 대시보드는 부르지 않음. 서버 HTML 폼(ULF:249)만 쓴다 | 없음 | **303** → `/ui?login=success` + `Set-Cookie: token=…` | 429 HTML, 그 밖은 (A) |
| `GET /sso/key/generate?return_to=…` | LP:348 (SSO 버튼), LP:139 (자동 리다이렉트) | 없음 | 303 → IdP | §5 |
| `GET /sso/callback` | 브라우저 (IdP 리다이렉트) | 없음 | **303** → `/ui/?login=success` + `Set-Cookie: token=…` | 401 (B) |
| `GET /management/v1/users/me` | HK/currentUser/useCurrentUser.ts:11-17 | Bearer 세션 키 | `{user_id, user_role, user_email}` | 실패하면 대시보드가 로그아웃시킴 (§4.3) |
| `GET /sso/get/ui_settings` | NW:3646-3657 (HK/proxySettings, 로그아웃 URL 등) | Bearer | `{PROXY_BASE_URL, PROXY_LOGOUT_URL, …}` (§2.3) | (D) |
| `GET /get/ui_settings` | NW:7194-7207 (헤더 없이) | 없음 | `{values, field_schema, source}` (§2.4) | 500 DB 없음 |
| `GET /get/ui_theme_settings` | UI/contexts/ThemeContext.tsx:37 | 없음 | `{values, field_schema}` | — |
| `POST /session/logout` | NW:1538-1543 (3초 타임아웃) | Bearer 세션 키 | `{"message": "Session revoked."}` + 쿠키 삭제 | 403 (B) 세션 키가 아님 |
| `POST /invitation/new` | NW:834-851 | Bearer (관리자) | `InvitationModel` (§6.1) | (A) `handle_exception_on_proxy` |
| `GET /onboarding/get_token?invite_link=<id>` | NW:1479-1508 | 없음 | `{login_url, token, user_email}` | 401 (B) `{"detail":{"error":…}}` |
| `POST /onboarding/claim_token` | NW:1510-1532 | `Authorization: Bearer <온보딩 JWT>` (핸들러가 직접 읽음) | `{login_url, token, user_email, user}` | 401 (B), 400 (A) 비밀번호 정책 |
| `POST /user/password/change` | NW:1545-1557 (CPF:47) | Bearer (비밀번호 로그인 세션만) | `{user_id, message:"Password updated successfully."}` | 403/400 (B) |

### 1.2 가장 중요한 계약 사항

- **세션 = `token` 쿠키 안의 HS256 JWT.** 서명 키는 마스터 키다(LU:488-498). 대시보드는 서명을 검증하지 않는다. `jwtDecode` 로 클레임만 읽는다(JU:1-23).
- JWT 의 `key` 클레임이 **DB 에 저장된 진짜 가상 키(`sk-…`)** 다. 대시보드는 모든 API 호출에 `Authorization: Bearer <key>` 를 붙인다(NW:544, HAPI:32-38, CL:165-166). 서버는 일반 API 호출에서 쿠키를 읽지 않는다. 일반 가상 키처럼 검증한다(J §5).
- 세션 키는 `team_id="litellm-dashboard"`, 유효 기간 `LITELLM_UI_SESSION_DURATION`(기본 `24h`), 키 예산 `max_ui_session_budget`(기본 **1.0 USD**)로 만든다(LU:338-353, LU:424-439, CONST:1841, LINIT:447-449).
- JWT `exp` 도 같은 24h 다(LU:478-485). 대시보드는 `exp` 가 지나면 쿠키를 지우고 로그인으로 보낸다(AUTH:68-74).
- **로그인 직후 `/management/v1/users/me` 가 실패하면 대시보드는 즉시 로그아웃시킨다**(DL:250-254). 이 엔드포인트는 꼭 있어야 한다.
- 쿠키는 `HttpOnly` 가 **아니다**(SSO:3199-3214). `SameSite=Lax`, `Path=/`(Starlette 기본값), `Max-Age`/`Expires` 없음(브라우저 세션 쿠키), HTTPS 일 때만 `Secure`.
- `premium_user` 는 JWT 클레임 하나에서만 온다(AUTH:120-122, HK/useAuthorized.ts:49). 우리는 늘 `true` 를 넣는다(§8).

---

## 2. 부팅 순서와 설정 엔드포인트

### 2.1 순서

1. 루트 레이아웃이 `AuthProvider` 를 감싼다(UI/app/layout.tsx:35). favicon 은 `/get_favicon` 이다(UI/app/layout.tsx:18).
2. `AuthProvider` 가 먼저 `getUiConfig()` 를 기다린다. 그다음 쿠키를 읽는다(AUTH:56-83).
   - `getUiConfig` 는 빌드 시 `NEXT_PUBLIC_BASE_URL` 이 없으면 먼저 `GET /ui/litellm-runtime-config.json` 을 읽는다. 응답이 ok 가 아니거나 `backend_url` 문자열이 없으면 무시한다(NW:562-584). litellm 서버에는 이 파일을 내는 코드가 없다(작업 트리 검색 결과. 정적 파일 동봉 여부는 미확인). 우리 서버는 404 를 내도 된다.
   - 그다음 `GET {base}/litellm/.well-known/litellm-ui-config` 를 부른다(NW:585-589). `SERVER_ROOT_PATH` 가 있으면 서버가 UI 정적 파일 안의 이 문자열을 `{root}/.well-known/litellm-ui-config` 로 바꿔 둔다(PS:2677-2681).
   - 응답의 `server_root_path` 로 경로 접두어를 정하고, `proxy_base_url || runtimeBackendUrl` 로 API 기준 URL 을 정한다. `gateway_base_url` 도 읽는다(NW:590-596, NW:208-218). `proxy_base_url` 이 null 이면 `window.location.origin` 을 쓴다(NW:224-230).
3. 로그인 페이지(`/ui/login`)는 같은 설정을 `useUIConfig`(24시간 캐시)로 읽는다(LP:67, HK/uiConfig/useUIConfig.ts:7-13).
4. 로그인된 뒤 `(dashboard)` 레이아웃이 `GET /management/v1/users/me` 를 부른다. 이것이 끝나기 전에는 로딩 화면이다(DL:221-262). `useAuthorized` 도 같은 쿼리를 쓴다(HK/useAuthorized.ts:18-22).
5. 셸(헤더·사이드바)이 부르는 것: `/get/ui_theme_settings`(public), `/get_image`, `/get/ui_settings`(SidebarProvider), `/sso/get/ui_settings`(로그아웃 URL·LiteAdmin), `/get/user_banner`, `/health/readiness/details`, `/health/deployment_warnings`, `/api/plugins`, `/telemetry/settings`, `/telemetry/ui_events/enabled`. 이 문서는 로그인에 필요한 앞의 셋만 다룬다. 나머지 모양은 범위 밖이다(미확인, 다른 계약 문서에서 정리 필요).

### 2.2 `GET /.well-known/litellm-ui-config` (DISC:16-52)

인증 없다(T:733-734). 응답 모델은 DISCT:7-17 이다.

```json
{
  "server_root_path": "",            // SERVER_ROOT_PATH, 기본 ""  (PU:9188-9195)
  "proxy_base_url": null,            // PROXY_BASE_URL env 또는 null (PU:9181-9185)
  "gateway_base_url": null,          // LITELLM_GATEWAY_URL 을 trim·끝 "/" 제거, 빈 문자열이면 null (DISC:36-39)
  "auto_redirect_to_sso": false,     // sso_configured AND (AUTO_REDIRECT_UI_LOGIN_TO_SSO=true 또는 general_settings.auto_redirect_ui_login_to_sso) (DISC:23-26,45)
  "admin_ui_disabled": false,        // DISABLE_ADMIN_UI=true (DISC:27)
  "sso_configured": false,           // MICROSOFT/GOOGLE/GENERIC_CLIENT_ID 가 있거나 SAML_IDP_METADATA_URL/XML 이 비어 있지 않음 (AU:1678-1698)
  "hide_default_credentials_hint": false, // LITELLM_HIDE_DEFAULT_CREDENTIALS_HINT=true 또는 general_settings.hide_default_credentials_hint 또는 UI_PASSWORD 설정 (HINT:5-10)
  "is_control_plane": false,         // worker_registry 가 비어 있지 않음 (DISC:34)
  "mcp_stdio_enabled": false,
  "workers": []
}
```

대시보드가 읽는 필드:
| 필드 | 읽는 곳 | 효과 |
|---|---|---|
| `server_root_path`, `proxy_base_url`, `gateway_base_url` | NW:590-596 | API 기준 URL |
| `admin_ui_disabled` | LP:90, LP:194, HK/useAuthorized.ts:25 | true 면 로그인 화면 대신 "Admin UI Disabled". 로그인 상태도 무효 |
| `auto_redirect_to_sso` | LP:136-145 | 로그인 화면을 건너뛰고 `/sso/key/generate?redirect_to=…` 로 이동. **서버 쿼리 이름은 `return_to` 라서 `redirect_to` 는 무시된다**(SSO:1129-1136) |
| `sso_configured` | LP:322-358 | "Login with SSO" 버튼 활성화 + 안내 배너 |
| `hide_default_credentials_hint` | LP:239 | "Default Credentials" 안내 숨김 |
| `is_control_plane`, `workers` | LP:118, LP:269 | 워커 선택(우리는 false·[]) |

우리 값: `proxy_base_url` 은 공개 URL 또는 null, `sso_configured` 는 Keycloak 을 켤 때 true, `is_control_plane=false`, `workers=[]`. `hide_default_credentials_hint` 는 true 를 권장한다(litellm 도 `UI_PASSWORD` 를 설정하면 true 다).

### 2.3 `GET /sso/get/ui_settings` (SSO:3017-3045)

`user_api_key_auth` 가 필요하다. 모든 역할의 라우트 목록에 있다(T:582).
```json
{"PROXY_BASE_URL": null, "PROXY_LOGOUT_URL": null, "LITELLM_UI_API_DOC_BASE_URL": null,
 "DEFAULT_TEAM_DISABLED": false, "SSO_ENABLED": false, "NUM_SPEND_LOGS_ROWS": 0, "DISABLE_EXPENSIVE_DB_QUERIES": false}
```
- 값은 각각 env `PROXY_BASE_URL`, `PROXY_LOGOUT_URL`, `LITELLM_UI_API_DOC_BASE_URL`, `general_settings.default_team_disabled`(env `PROXY_DEFAULT_TEAM_DISABLED=true` 가 덮어씀), `has_user_setup_sso()`, spend log 행 수, 행 수 > `MAX_SPENDLOG_ROWS_TO_QUERY` 에서 온다(SSO:3023-3045).
- 로그인과 관련해 대시보드가 읽는 것은 `PROXY_LOGOUT_URL` 하나다(HK/useLogout.ts:36). 비어 있으면 `""` 로 보낸다. 나머지는 플레이그라운드·API 문서·사용량 화면이 읽는다(HK/proxySettings/useProxySettings.ts:7-13).

### 2.4 `GET /get/ui_settings` (PSE:1803-1877)

인증 의존성이 없다(PSE:1803-1808, 라우터 PSE:63 에도 없음). 대시보드도 헤더 없이 부른다(NW:7194-7207).
- 응답: `{"values": {...}, "field_schema": {...}, "source": {key: "db"|"config"|…}}` (PSE:203-211, PSE:381-384, PSE:1873-1877).
- `values` 키는 `ALLOWED_UI_SETTINGS_FIELDS`(PSE:388-406)에 `enable_ptu_cost_attribution`, `apply_user_budget_to_team_keys` 를 더한 것이다(PSE:1856-1863). 로그인 흐름은 이 값을 읽지 않는다. 사이드바가 `enabled_ui_pages_internal_users` 등을 읽는다(UI/app/(dashboard)/components/SidebarProvider.tsx:12). 세부 모양은 범위 밖이다.
- DB 가 없으면 500 `{"detail":{"error":"Database not connected. Please connect a database."}}` 다(PSE:1815-1819).

### 2.5 `GET /get/ui_theme_settings` (PSE:1368-1395)

인증 없다. `{values: {logo_url, …}, field_schema}` 이다. 대시보드는 `logoUrl` 을 읽는다(UI/contexts/ThemeContext.tsx:37). 비어 있으면 `/get_image` 를 쓴다(UI/components/navbar.tsx:50-51). 세부는 범위 밖이다.

---

## 3. 아이디·비밀번호 로그인

### 3.1 `POST /v2/login` (PS:18457-18518) — 대시보드가 쓰는 것

- 요청: `Content-Type: application/json`, `credentials: "include"`, 본문 `{"username": "...", "password": "..."}` (NW:7112-7130). 서버는 두 필드를 `str()` 로 바꾼다. 없으면 문자열 `"None"` 이 된다(PS:18465-18467, PS:1336-1338).
- 성공: **200** JSON
  ```json
  {"redirect_url": "<PROXY_BASE_URL 또는 요청 base_url>/ui?login=success", "token": "<JWT>"}
  ```
  + `Set-Cookie: token=<JWT>` (PS:18489-18498). `redirect_url` 은 `get_custom_url(base_url)` 의 끝 `/` 를 떼고 `/ui?login=success` 를 붙인 것이다(PS:18484-18486). `get_custom_url(base)` 는 route 가 없으면 `join_paths(base, SERVER_ROOT_PATH)` 를 돌려준다. 그래서 root path 가 있으면 그 접두어가 들어간다(PU:9149-9179, PU:9126-9137).
- 대시보드는 응답의 `token` 을 직접 저장한다(§4.1). 그다음 `redirect_url` 을 UI 기준 상대 경로로 바꿔 이동한다(LP:166-173). `?login=success` 는 아무 곳에서도 읽지 않는다(검색 결과 없음).
- 실패: 모든 예외를 (A) ProxyException 으로 다시 감싼다(PS:18499-18518). 대시보드는 `deriveErrorMessage` 로 메시지를 꺼내 로그인 화면에 그대로 띄운다(NW:7132-7136, LP:186, LP:260-265).

| 조건 | 상태 | message | type / param |
|---|---|---|---|
| 아이디 없음 또는 비밀번호 틀림 | 401 | `"Invalid credentials used to access UI. Check 'UI_USERNAME' and 'UI_PASSWORD', or the password set for your user"` (env 로그인이 꺼져 있으면 `"Invalid credentials used to access UI. Check the password set for your user"`) | `auth_error` / `invalid_credentials` (LU:51-54, LU:163-167, LU:460-475) |
| DB 사용자가 비밀번호 없음 | 401 | `"User has no password set. Please set a password for the user via `/user/update`."` | `auth_error` / `password` (LU:406-412) |
| 마스터 키 없음 | 500 | `"Master Key not set for Proxy. …"` | `auth_error` / `master_key` (LU:271-277) |
| DB 없음 | 500 | `"No Database connected. Set DATABASE_URL in .env. …"` | `auth_error` / `DATABASE_URL` (LU:354-360) |
| SSO 설정 + `disable_password_login_when_sso_enabled` | 403 | `"Username/password login is disabled because SSO is configured and 'disable_password_login_when_sso_enabled' is set. Sign in via SSO."` | `auth_error` / 같은 이름 (LU:260-269) |
| 실패 시도 초과 | 429 + `Retry-After` | `"Too many failed sign-in attempts. Try again later."` | (LT:419-426) |

- 시도 제한(LT): 기본 출처(IP)당 60초 창에 10회, 차단 300초(LT:44-46). `trusted_proxy_ranges` 가 없으면 출처 단위는 꺼지고 사용자 단위만 남는다(LT:270-276). `LITELLM_DISABLE_LOGIN_RATE_LIMIT=true` 로 끈다(LT:109-110). 미확인: 사용자 단위 한도 공식(`user_limit_for`, LT:241)과 이 기능이 upstream 에도 있는지.

### 3.2 `POST /login` (PS:18363-18454) — HTML 폼용

- 요청은 **form-urlencoded** 다. 필드 `username`, `password` 다(PS:18369-18371). 이 폼은 서버가 내는 HTML(`build_ui_login_form`, action=`{PROXY_BASE_URL}{SERVER_ROOT_PATH}/login`, ULF:6-10, ULF:249-263)이다. `/fallback/login`(PS:18337-18360)과 SSO 미설정 시 `/sso/key/generate`(SSO:1263-1276)가 이 폼을 낸다. **대시보드 자체는 `/login` 을 부르지 않는다.**
- 성공: **303** `Location: …/ui?login=success` + `Set-Cookie: token=…` (PS:18450-18454). 본문에 토큰이 없다.
- 429 는 HTML `<h1>Too many sign-in attempts</h1>` 로 돌려준다(PS:18382-18393). 그 밖의 ProxyException 은 그대로 (A) 다.
- `litellm_cp_return_to` 쿠키가 있으면 그곳으로 보낸다(PS:18419-18447). 우리는 쓰지 않는다(§5.3).

### 3.3 `/v3/login`, `/v3/login/exchange` (PS:18521-18658)

`general_settings.control_plane_url` 이 없으면 404 (A) 다(PS:18527-18533, PS:18601-18607). 대시보드는 control plane 의 워커를 고를 때만 부른다(NW:7114, LP:158). 우리는 control plane 이 아니므로 **구현하지 않는다**(404 이면 충분. 원본도 404).

### 3.4 인증 판정 (`authenticate_user` LU:213-280 → `_sign_in` LU:283-475)

순서:
1. `disable_password_login_when_sso_enabled` 검사(LU:260).
2. 마스터 키 검사(LU:271).
3. 시도 제한 슬롯 잡기(LU:279).
4. DB 에서 `user_email` 을 **대소문자 무시**로 찾는다(`find_first`, LU:304-310). username 칸에 이메일을 넣는 것이다. user_id 로는 찾지 않는다.
5. **env 관리자 일치**가 먼저다(LU:291, LU:317). 조건: `disable_env_credential_login` 이 true 가 아니고, `username == UI_USERNAME`(기본 `"admin"`)이고 `password == UI_PASSWORD`(없으면 **마스터 키**)다. 비교는 `compare_digest` 다(LU:121-160).
   - 역할 `proxy_admin`, JWT `user_id = "default_user_id"`(LITELLM_PROXY_ADMIN_NAME, CONST:1817), `user_email = null` (LU:319-320, LU:387-393).
   - 키의 user_id 는 `PROXY_ADMIN_ID` env(기본 `"default_user_id"`)다(LU:323). 먼저 `user_update(user_id=…, user_role=proxy_admin)` 로 그 사용자 행을 proxy_admin 으로 맞춘다. 행이 없으면 만든다(LU:328-336, IU:1584-1587).
   - 세션 키: `user_role=proxy_admin, duration=24h, key_max_budget=1.0, models=[], aliases={}, config={}, spend=0, user_id=<PROXY_ADMIN_ID>, team_id="litellm-dashboard"` (LU:338-353). `metadata` 는 비어 있다. 그래서 env 관리자 세션은 `/user/password/change` 를 쓸 수 없다(PWD:57-61, §6.5).
6. **DB 사용자**(LU:395-467): 행의 `password` 를 `verify_password` 로 확인한다.
   - 맞으면: 레거시 SHA256 이면 scrypt 로 다시 저장한다(LU:108-118). HIBP 유출 검사를 한다(§6.6). 세션 키를 만든다: `user_role=<DB 역할>, duration=24h, key_max_budget=1.0, spend=0, user_id, team_id="litellm-dashboard", metadata={"login_method":"username_password"[, "password_reset_required":true]}`, 재설정이 필요하면 `allowed_routes=["/user/password/change","/session/logout","/management/v1/users/me"]` (LU:60-61, LU:424-439). `models` 를 넘기지 않으므로 기본값을 쓴다. 미확인: `generate_key_helper_fn` 의 models 기본값(빈 목록으로 추정).
   - DB 역할이 null 이면 `getattr` 기본값이 아니라 null 이 그대로 간다. 미확인: 그때 JWT `user_role` 이 null 인지. 주석은 viewer 로 가정한다고 적혀 있다(LU:398-402).
7. 둘 다 아니면 401 이다(LU:468-475). 존재하지 않는 사용자와 틀린 비밀번호의 메시지가 같다(열거 방지).

### 3.5 세션 가상 키 (DB 저장)

- 저장 위치는 `LiteLLM_VerificationToken`(키 해시) 이다. 키 형식·해시는 `J_key_contract.md` §2.1 을 따른다. `team_id="litellm-dashboard"` 는 `UI_TEAM_ID`(T:4634)·`UI_SESSION_TOKEN_TEAM_ID`(CONST:1804)다.
- 이 팀 이름으로는 팀을 만들 수 없다(H §3.1 표, TE:1672-1678). `/key/list`·`/user/info` 에서 이 키는 빠진다(J §3.6, H §4.4). 미확인: `litellm-dashboard` 팀 행이 DB 에 없어도 키 인증이 통과하는 코드 경로(실제로는 통과한다고 보고 있다).
- 만료 키 정리는 기본 꺼짐이다(`LITELLM_EXPIRED_UI_SESSION_KEY_CLEANUP_ENABLED`, F_extras.md 참고). 로그인할 때마다 키가 하나씩 쌓인다.
- `EXPERIMENTAL_UI_LOGIN=true` 이면 DB 키 대신 JWT 를 `key` 에 넣는다(LU:364-383, SSO:4108-4124). 기본 꺼짐. 우리는 구현하지 않는다.

### 3.6 `token` JWT

생성: `create_ui_token_object` (LU:501-530) → `encode_ui_session_jwt` (LU:488-498). 형태는 `ReturnedUITokenObject`(UTT:6-20) + `exp` 다.

| 클레임 | 값 | 근거 |
|---|---|---|
| `user_id` | 사용자 id (env 관리자는 `"default_user_id"`) | LU:520 |
| `key` | 세션 가상 키 평문 `sk-…` | LU:521 |
| `user_email` | 이메일 또는 null | LU:522 |
| `user_role` | 역할 문자열(H §2.1) | LU:523 |
| `login_method` | `"username_password"` 또는 `"sso"` | LU:524, SSO:4130 |
| `premium_user` | bool | LU:525 |
| `auth_header_name` | `general_settings.litellm_key_header_name`, 기본 `"Authorization"` | LU:526 |
| `disabled_non_admin_personal_key_creation` | `key_generation_settings.personal_key_generation.allowed_user_roles` 에 `"proxy_admin"` 이 있으면 true | LU:517, SSO:1910-1916 |
| `server_root_path` | `SERVER_ROOT_PATH` (기본 `""`) | LU:528, PU:9188-9195 |
| `password_reset_required` | bool | LU:529 |
| `exp` | 지금 + `LITELLM_UI_SESSION_DURATION`(기본 24h)의 unix 초 | LU:478-485, LU:497 |

- 알고리즘 **HS256**, 비밀 키 = **마스터 키** 문자열(LU:498). `iat`·`iss`·`sub` 는 없다.
- 대시보드는 서명을 검증하지 않으므로 서명 키를 바꿔도 화면은 동작한다. 다만 서버가 이 쿠키를 다시 읽는 곳(MCP 대화형 로그인 PS 밖, `byok_oauth_endpoints.py:112`, `mcp_management_endpoints.py:2305`)은 서명을 검증한다. 우리는 MCP 를 하지 않으므로 서명 키는 자유롭게 정해도 된다(우리 결정 사항).

### 3.7 쿠키 속성 (`set_session_token_cookie` SSO:3199-3214)

```
Set-Cookie: token=<JWT>; Path=/; SameSite=lax[; Secure]
```
- `httponly=False`. 대시보드가 `document.cookie` 로 읽어야 한다(SSO:3202-3205).
- `secure` 는 요청이 HTTPS 일 때만이다(`IPAddressUtils.is_request_https`). 미확인: 신뢰 프록시의 `X-Forwarded-Proto` 를 따르는 정확한 조건.
- `max_age`·`expires`·`domain` 을 넘기지 않는다. 그래서 브라우저 세션 쿠키다. `Path` 는 Starlette `set_cookie` 기본값 `/` 다.
- 모든 로그인 경로(`/login`, `/v2/login`, `/v3/login/exchange`, SSO 콜백)가 이 함수 하나를 쓴다.

---

## 4. 대시보드의 쿠키 읽기·API 인증·로그아웃 조건

### 4.1 저장 (`storeLoginToken` CK:86-106)

`/v2/login`·`/v3/login/exchange`·온보딩 claim 응답 본문의 `token` 을 받으면 대시보드가 직접 저장한다(NW:7163-7165, NW:7188-7190, OF:51-52).
1. `document.cookie = "token=<encodeURIComponent(JWT)>; path=/ui; SameSite=Lax[; Secure]"`. 경로는 현재 URL 의 `/ui` 세그먼트까지다(CK:13-22).
2. `sessionStorage.token = JWT`.
그래서 브라우저에는 서버가 심은 `Path=/` 쿠키와 대시보드가 심은 `Path=/ui` 쿠키가 같이 있을 수 있다. 값은 같다.

### 4.2 읽기

- `getCookie("token")`: `document.cookie` 를 `"; "` 로 나눠 처음 나오는 `token=` 을 쓴다. `decodeURIComponent` 를 한다. 없으면 `sessionStorage.token` 을 쓴다(CK:117-147).
- 디코드는 `jwt-decode` 다. 서명 검증 없음. `exp` 가 숫자이고 지났으면 만료다. `exp` 가 없으면 **만료 아님**이다. 디코드 실패는 만료로 본다(JU:3-14).
- `AuthContext` 가 읽는 클레임(AUTH:97-129): `key`→accessToken, `disabled_non_admin_personal_key_creation`, `user_role`(→`effectiveSessionRole`), `user_email`, `login_method`(`"username_password"` 이면 SSO 안내 배너), `premium_user`(truthy 일 때만 설정), `auth_header_name`(→전역 헤더 이름), `user_id`, `password_reset_required === true`.
- `useAuthorized` 는 역할·user_id·이메일을 **JWT 가 아니라 `/management/v1/users/me` 응답**에서 가져온다. JWT 에서는 `key`, `auth_header_name`, `premium_user`, `disabled_non_admin_personal_key_creation`, `login_method`, `password_reset_required` 만 쓴다(HK/useAuthorized.ts:16-54).

### 4.3 `GET /management/v1/users/me` (ME endpoints.py:14-20)

- 요청: `<auth_header_name>: Bearer <key>` (HK/currentUser/useCurrentUser.ts:11-17). `retry: 1`, `staleTime: Infinity` (같은 파일 :19-27).
- 응답: `{"user_id": str, "user_role": 역할|null, "user_email": str|null}` (ME schemas.py:6-11). 값은 인증된 키의 `UserAPIKeyAuth` 에서 바로 온다(ME endpoints.py:20).
- 키에 user_id 가 없으면 404 problem+json(`type=…user-not-found`, `title="User not found"`, `detail="The key used for this request is not bound to a user."`)이다(ME exceptions.py:5-13). 미확인: `PROBLEM_TYPE_BASE` 값과 Content-Type.
- 모든 역할의 라우트 목록과 재설정 세션 허용 라우트에 있다(T:570, LU:60).

### 4.4 API 호출 인증

- `apiClient`·`fetchClient` 는 `<auth_header_name>: Bearer <JWT.key>` 를 붙인다(NW:530-547, HAPI:32-38, CL:156-166). 헤더 이름은 JWT `auth_header_name` 이다(AUTH:123-125).
- 몇몇 직접 `fetch` 는 `accessToken` 인자를 그대로 쓴다(예: NW:3665-3671). 같은 값이다.
- 서버는 일반 API 에서 `token` 쿠키를 읽지 않는다(쿠키 읽는 곳은 MCP 두 곳뿐, §3.6). 세션 키 검증은 J §5 그대로다.

### 4.5 로그아웃되는 경우

| 계기 | 동작 | 근거 |
|---|---|---|
| 부팅 시 JWT `exp` 지남 / 디코드 실패 | 쿠키 삭제 → 토큰 null → 로그인으로 | AUTH:68-74, AUTH:91-104 |
| `/management/v1/users/me` 실패(재시도 1회 후) | `clearTokenCookies` + `redirectToLogin()` | DL:250-254, HK/useAuthorized.ts:25-36 |
| 토큰 무효 또는 `admin_ui_disabled` | 같음 | HK/useAuthorized.ts:25-36 |
| 어떤 API 오류 문자열이든 `"Authentication Error - Expired Key"` 를 포함 | 토스트 "UI Session Expired. Logging out." + 쿠키 삭제 + 현재 URL 다시 열기 (60초에 한 번) | NW:434-450. 서버 문구는 UAK:2660 등 `"Authentication Error - Expired Key. Key Expiry time … and current time …"` |
| 사용자가 로그아웃 클릭 | §7 | HK/useLogout.ts:31-38 |

- `redirectToLogin` 은 현재 URL 을 return URL 로 저장한 뒤 `{proxyBase}/ui/login/` 으로 `location.replace` 한다(HK/redirectToLogin.ts:4-8, RU:16-18).
- 로그인 화면은 유효한 쿠키가 이미 있으면 바로 `/ui` 로 돌려보낸다(LP:124-134).
- `clearTokenCookies` 는 경로 `/`, `/ui`(+현재 디렉터리)와 SameSite 조합마다 `token` 을 지운다. `sessionStorage.token` 도 지운다(CK:27-73).

---

## 5. 웹 SSO 로그인 (CLI 아님)

### 5.1 시작: `GET /sso/key/generate?return_to=<origin>/ui/login/` (SSO:1128-1276)

- 대시보드 버튼은 `return_to = encodeURIComponent(window.location.origin + "/ui/login/")` 를 붙인다(LP:346-348, RU:16-18). 자동 리다이렉트는 `?redirect_to=` 를 쓰는데 서버는 이 이름을 읽지 않는다(LP:139-143).
- `source` 가 `litellm-cli` 가 아니다. 그래서 다음 순서다.
  1. `DISABLE_ADMIN_UI` 면 안내 HTML(SSO:1156-1159).
  2. SSO 설정 시 무료 사용자 수 검사. `premium_user=true` 면 건너뛴다(SSO:1161-1168, SSO:1106-1109).
  3. DB·마스터 키 없음 안내(SSO:1171-1173).
  4. `state` 는 CLI 가 아니므로 None 이다(SSO:1185-1189). 그러면 generic OIDC 의 state 는 `GENERIC_CLIENT_STATE` 또는 `uuid4().hex` 다(K §2.2 표).
  5. SSO 핸들러로 303 리다이렉트를 만든다. PKCE 쿠키(`litellm_oauth_state`, `sso_state`)는 K §2.2 와 같다(SSO:1236-1257).
  6. `_persist_return_to_cookie` 가 `return_to` 를 저장할지 정한다(SSO:1255-1256, SSO:3217-3239). 상대 경로(`/`로 시작, `//`·`\`·제어 문자 없음)이거나 `control_plane_url` 과 출처가 같을 때만 `litellm_cp_return_to` 쿠키(암호화, `Max-Age=600; HttpOnly; SameSite=lax`)를 심는다(SSO:3132-3146, SSO:3300-3328). **대시보드가 보내는 값은 절대 URL 이고 우리는 control_plane_url 이 없다. 그래서 저장하지 않는다.**
- SSO 가 설정돼 있지 않으면 리다이렉트 대신 200 HTML 로그인 폼을 준다(SSO:1263-1276). 이 폼은 `/login` 으로 POST 한다(§3.2).

### 5.2 콜백: `GET /sso/callback` (SSO:2245-2348 → `get_redirect_response_from_openid` SSO:3948-4153)

1. OAuth error·DB·마스터 키·PKCE·클레임 처리는 K §2.3 과 같다(SSO:2250-2311).
2. state 가 `litellm-session-token:` 으로 시작하지 않으므로 웹 분기로 간다(SSO:2320-2348).
3. 사용자 조회·생성(`get_user_info_from_db`, SSO:4016-4024)과 JWT 역할 매핑(SSO:4031-4038)을 한다. 사용자 생성 기본값(역할 `internal_user_viewer` 등)은 K §2.4 와 같다. user_id 가 비면 401 (B) `"SSO login failed: this sign-in did not resolve to a user id"` 다(SSO:3286-3291, SSO:4017, SSO:4064).
4. 세션 키 (SSO:4051-4067): `duration=24h, key_max_budget=1.0, aliases={}, config={}, spend=0, team_id="litellm-dashboard", models=<사용자 models>, user_id, user_email, user_role, max_budget=<사용자 max_budget>, budget_duration=<사용자 budget_duration>`. **metadata 에 `login_method` 가 없다.** 그래서 SSO 세션은 비밀번호 변경이 403 이다(PWD:57-61).
5. 역할 = `user_role` 또는 `internal_user_viewer`. `PROXY_ADMIN_ID` 와 user_id 가 같으면 DB 를 proxy_admin 으로 올린다(SSO:4072-4076, SSO:2180-2198). `ui_access_mode` 가 admin 전용이면 비관리자는 401 (B) `{"detail":{"error":"User not allowed to access proxy. …"}}` 다(SSO:4078-4089).
6. JWT 클레임은 §3.6 과 같고 `login_method="sso"`, `password_reset_required=false` 다(SSO:4126-4136).
7. `return_to` 쿠키가 없으면 **303** `Location: {PROXY_BASE_URL 또는 base_url}{root}/ui/?login=success` + `Set-Cookie: token=<JWT>` 다. 거부된 `litellm_cp_return_to` 쿠키는 지운다(SSO:4106, SSO:4155-4157, SSO:3263-3270). user_id 가 문자열이 아니면 `?login=success` 를 붙이지 않는다.
8. 대시보드는 `/ui/` 에서 서버가 심은 `Path=/` 쿠키를 `document.cookie` 로 읽는다(§4.2). 응답 본문으로 토큰을 받지 않는다. 그래서 웹 SSO 에서는 쿠키가 HttpOnly 이면 안 된다.

### 5.3 우리 구현에서 뺄 것

- `return_to` 처리 전체(같은 출처 상대 경로 = MCP DCR, 교차 출처 = control plane). 대시보드 버튼이 보내는 절대 URL 은 원본에서도 무시되므로 결과가 같다.
- SAML, Microsoft/Google, `user_custom_sso`, `user_custom_ui_sso_sign_in_handler`, Microsoft subject 등록, ID-JAG assertion 보관(SSO:1209-1234, SSO:4091-4103).

---

## 6. 초대·온보딩·비밀번호

### 6.1 `POST /invitation/new` (PS:19160-19232, UINV:11-49)

- 요청: Bearer 관리자 키, 본문 `{"user_id": "<LiteLLM_UserTable.user_id>"}` (NW:834-851, SAT:364-365).
- 권한: proxy_admin, 또는 DB 상 org/team 관리자. 아니면 400 `{"error": "<not_allowed_access>, your role=…"}` 다. org/team 관리자는 자기 조직·팀 사용자만 초대한다(PS:19193-19226). 오류는 `handle_exception_on_proxy` 로 (A) 가 된다(PS:19231-19232).
- 동작: `LiteLLM_InvitationLink` 행을 만든다. `id` = uuid(SP:835), `expires_at = now + 7일`, `created_by`/`updated_by` = 호출자 user_id(없으면 proxy admin name)(UINV:26-39). 사용자가 없으면 400 `"User id does not exist in 'LiteLLM_UserTable'. Fix this by creating user via `/user/new`."` (UINV:42-48).
- 응답 `InvitationModel`: `{id, user_id, is_accepted, accepted_at, expires_at, created_at, created_by, updated_at, updated_by}` (SAT:368-377).
- 대시보드는 `id`, `user_id` 를 읽는다. 링크는 `{base}/ui/onboarding?invitation_id=<id>[&action=reset_password]` 로 **클라이언트가** 만든다(OL:49-53, UI/components/bulk_create_users_button.tsx:380). SSO 가 설정된 경우 링크는 그냥 `{base}/ui` 다(OL:46-48). `has_user_setup_sso` 는 서버 응답이 아니라 대시보드가 채운다(UI/components/CreateUserButton.tsx:123, :222).
- 옛 링크 `/ui/?invitation_id=` 는 대시보드가 `/ui/onboarding?…` 로 바꾼다(DL:234-240).
- `GET /invitation/info?invitation_id=`(PS:19234-19277, admin view 필요), `/invitation/update`, `/invitation/delete` 는 대시보드 로그인 흐름이 부르지 않는다(검색 결과 없음).

### 6.2 `GET /onboarding/get_token?invite_link=<id>` (PS:18661-18755)

- 인증 없다(`@app.get`, Depends 없음). 대시보드는 헤더 없이 부른다(NW:1479-1508).
- 검사와 오류(모두 401 (B) `{"detail":{"error":…}}`): 링크 없음 `"Invitation link does not exist in db."`, 만료(**날짜만 비교**, `expires_at.date() < 오늘`) `"Invitation link has expired."`, 사용됨(`is_accepted` 또는 `accepted_at`) `"Invitation link has already been used."`, 사용자 없음 `"User does not exist in db."` (PS:18686-18712). 마스터 키 없음 500 (A), DB 없음 500 (B).
- 토큰 두 겹:
  - 안쪽 **온보딩 JWT**: `{"token_type":"litellm_onboarding","invitation_link":<id>,"user_id":…,"exp":now+15분}`, HS256, 마스터 키 (PS:18719-18729).
  - 바깥 JWT: `ReturnedUITokenObject` 에 `key=<온보딩 JWT>`, `login_method="username_password"`, `password_reset_required=false`. **`exp` 가 없다**(PS:18732-18748).
- 응답: `{"login_url": "{base}/ui/onboarding?token=<바깥 JWT>&user_email=<email>", "token": <바깥 JWT>, "user_email": …}` (PS:18714-18755).
- 대시보드는 `token` 만 쓴다. `jwtDecode` 해서 `user_email`, `user_id`, `key` 를 꺼낸다(OF:29-32).

### 6.3 `POST /onboarding/claim_token` (PS:18863-19005)

- 요청: `Authorization: Bearer <온보딩 JWT(바깥 토큰의 key)>`, 본문 `{"invitation_link": <id>, "user_id": …, "password": …}` (NW:1510-1532, T:3506-3509). 핸들러가 `litellm_key_header_name`(기본 Authorization) 헤더를 직접 읽는다. `Bearer ` 접두어는 대소문자 무시로 뗀다(PS:18758-18788). `user_api_key_auth` 는 거치지 않는다.
- 검사 순서: 링크 존재·만료·사용 여부(get_token 과 같은 401 문구) → `invite.user_id != user_id` 면 401 `"Invalid invitation link. The user id submitted does not match …"` → 온보딩 JWT 디코드 실패(만료 포함) 401 `"Invalid onboarding session for invitation link."`, 헤더 없음 401 `"Missing onboarding session for invitation link."` → 클레임 `token_type`·`invitation_link`·`user_id` 불일치 401 (같은 문구) → 비밀번호 정책 400 (A) → HIBP 유출 400 (A) (PS:18884-18924).
- 쓰기(트랜잭션, PS:18925-18966): `is_accepted=false` 인 행만 `is_accepted=true` 로 바꾼다(0 행이면 401 "already been used"). 사용자 행에 `password=hash_password(pw)`, `password_reset_required=false`, `last_breach_check_at=null` 을 쓴다. 그다음 `accepted_at=now` 를 쓴다.
- 그 사용자의 **모든 UI 세션 키를 폐기**한다(SESS:54-114, PS:18970-18981).
- 새 세션 키: `user_role=<DB 역할>, duration=24h, key_max_budget=1.0, models=[], team_id="litellm-dashboard"`. **metadata 에 `login_method` 가 없다**(PS:18818-18834). 그래서 온보딩 직후 세션으로는 `/user/password/change` 가 403 이다. 미확인: 의도 여부.
- 새 JWT 도 **`exp` 가 없다**(PS:18836-18855). 대시보드는 `exp` 가 없으면 만료로 보지 않는다(JU:6-9). 세션은 키가 24h 뒤 만료될 때 "Expired Key" 오류로 끝난다(§4.5).
- 키 생성이 실패하면 초대 수락을 되돌리고 500 (B) `"Failed to create onboarding session. Please retry the invitation link."` 다(PS:18983-18998, PS:18790-18815).
- 응답: `{"login_url": "{base}/ui?login=success", "token": <JWT>, "user_email": …, "user": <사용자 행, password 제외>}` (PS:18999-19005, PS:18967-18968).
- 대시보드: `token` 이 없으면 오류 표시. 있으면 기존 쿠키를 지우고 `storeLoginToken` 한 뒤 `/ui/?login=success` 로 간다(OF:42-57). 비밀번호 입력 검사는 비어 있지 않음뿐이다(UI/app/onboarding/OnboardingFormBody.tsx:24). 정책은 서버가 본다.

### 6.4 비밀번호 해시 (PU:7846-7874)

- 저장 형식: `"scrypt:" + base64(salt16 ‖ dk32)`. scrypt 파라미터 `N=16384, r=8, p=1, dklen=32`, salt 는 `os.urandom(16)` 이다.
- 검증: `scrypt:` 접두어면 같은 파라미터로 다시 계산해 `compare_digest` 한다. 아니면 64자 소문자 hex 를 `sha256(password).hexdigest()` 로 비교한다(레거시). 그 밖의 형식은 거짓이다.
- 레거시 SHA256 은 로그인 성공 때 scrypt 로 바뀐다(LU:108-118). 평문 저장값은 시작 시 마이그레이션한다(PU:7877-, `migrate_passwords_to_scrypt_async`). 미확인: 마이그레이션 실행 조건.

### 6.5 비밀번호 정책과 변경

- 정책(PP:30-107): 기본 최소 12자(설정 `password_policy_min_length`, 하한 8), 대문자·소문자·숫자·특수 문자(유니코드 기준 비영숫자) 각각 필요(설정 `password_policy_require_*` 를 false 로 끔). 위반은 400 (A) `"Password does not meet the required policy: must be at least 12 characters long, include an uppercase letter, …."`, `type=validation_error`, `param=password` 다.
- HIBP 유출 검사는 **기본 켜짐**이다(`password_policy_check_breached_passwords`, PP:145-146). 폐쇄망에서는 끄거나 무조건 통과시켜야 한다(우리 결정 사항). 미확인: 외부 호출 실패 시 거부인지 통과인지(로그인 검사는 통과, LU:86).
- 관리자 설정: `POST /user/update {user_id, password}` → 정책 검사 → 해시 → **`password_reset_required=true`** → 대상의 UI 세션 전부 폐기(IU:171-189, IU:1589-1600). `/user/new` 의 `password` 는 422 로 거부된다(IU:518).
- **`POST /user/password/change`** (PWD:71-170): 본문 `{"current_password","new_password"}` (NW:1545-1557).
  - 403 (B) `{"detail":{"error":"Passwords can only be changed from a dashboard session created by logging in with a password."}}`: 키의 `team_id != "litellm-dashboard"` 이거나 metadata `login_method != "username_password"` (PWD:57-61, PWD:105-111). 즉 **DB 사용자 비밀번호 로그인 세션만** 된다.
  - 400 (B): user_id 없음, 저장된 비밀번호 없음, `"Current password is incorrect."`, `"New password must be different from the current password."` (PWD:113-139). 정책·유출 위반은 400 (A).
  - 성공: 해시 저장, `password_reset_required=false`, `last_breach_check_at=null`, **자기 세션을 뺀** 나머지 UI 세션 폐기, 감사 로그(PWD:144-169). 응답 `{"user_id", "message": "Password updated successfully."}`.
  - 라우트 단계에서 모든 역할에 허용된다(T:894, RC:1057-1059).

### 6.6 `password_reset_required` 흐름

1. 관리자가 비밀번호를 정하거나(§6.5), 로그인 때 HIBP 가 유출을 찾으면(24시간에 한 번 검사, LU:59, LU:64-105) 사용자 행에 `password_reset_required=true` 가 선다.
2. 다음 로그인의 세션 키는 `allowed_routes` 가 3개로 묶이고 metadata 에 `password_reset_required: true` 가 들어간다. JWT 클레임도 true 다(LU:423-439, LU:529).
3. 그 키로 다른 라우트를 부르면 403 `"This account's password must be changed before the session can be used: … Change it via POST /user/password/change (UI: /ui/change-password), then log in again."` 다(RC:238-246).
4. 대시보드는 클레임을 보고 `/ui/change-password` 로만 보낸다(DL:242-248, AUTH:129).
5. 변경 성공 후 대시보드는 `/session/logout` → 쿠키 삭제 → 로그인 화면으로 간다(CPF:47-54). 새로 로그인해야 제한이 풀린다.

---

## 7. 로그아웃

### 7.1 `POST /session/logout` (SESS:117-176)

- 요청: Bearer 세션 키, 본문 없음, 3초 타임아웃(NW:1538-1543).
- `team_id != "litellm-dashboard"` 면 403 (B) `{"detail":{"error":"Only UI session tokens can be revoked through this endpoint."}}` (SESS:141-145). DB 없으면 500 (B).
- 키 행이 있으면 `LiteLLM_DeletedVerificationToken` 에 기록하고 지운다. 캐시도 지운다(SESS:147-169). 이미 없으면 그대로 성공한다(멱등).
- 응답: `{"message": "Session revoked."}` 또는 `"Session already revoked."` + `Set-Cookie: token=""; Max-Age=0; Path=/`(`response.delete_cookie("token")`, SESS:173-176).
- 모든 역할에 허용된다(T:895, RC:1061-1063).

### 7.2 대시보드 동작 (HK/useLogout.ts:13-38)

1. `/session/logout` 를 부른다. 실패는 무시한다.
2. `clearTokenCookies()`, 저장된 return URL 삭제, `localStorage` 의 `litellm_selected_worker_id`·`litellm_worker_url` 삭제.
3. `window.location.href = getUiPageUrl(PROXY_LOGOUT_URL || "")` (`/sso/get/ui_settings` 값). 미확인: 빈 문자열일 때 실제 도착 URL(현재 페이지를 다시 열고 §4.5 로 로그인 화면에 가는 것으로 추정).
- IdP(Keycloak) 세션은 끊지 않는다. `PROXY_LOGOUT_URL` 에 Keycloak 로그아웃 URL 을 넣으면 그쪽으로 간다.
- 그 밖의 폐기: 비밀번호 변경·관리자 비밀번호 설정·온보딩 claim 이 `revoke_ui_session_keys` 로 그 사용자의 `litellm-dashboard` 키를 지운다(SESS:54-114).

---

## 8. `premium_user`

### 8.1 출처

- 서버: `premium_user = LicenseCheck().is_premium()` 를 모듈 로드 때와 시작 때 정한다(PS:1105-1108, PS:1480-1482). 라이선스 키 검증이다.
- 대시보드에 가는 길은 **JWT 클레임 `premium_user` 하나**다. 넣는 곳: `/login`·`/v2/login`(LU:525), SSO 콜백(SSO:4131), 온보딩 get_token·claim(PS:18738, PS:18849). 다른 응답에서 premium 을 받지 않는다(대시보드 `premium_user` 문자열 검색: AUTH:120-121, HK/useAuthorized.ts:49 두 곳뿐). `J_key_contract.md` §7 의 "premiumUser 를 어느 응답에서 받는지" 는 이것으로 확인된다.
- 서버 쪽 로그인 경로에서 premium 을 보는 곳은 SSO 무료 사용자 수 상한 하나다(SSO:1106-1109, 5명 초과 시 403). `true` 면 건너뛴다.

### 8.2 대시보드가 읽는 곳

- `AuthContext.premiumUser`: 기본 false, 클레임이 truthy 일 때만 true (AUTH:47, AUTH:120-122).
- `useAuthorized().premiumUser`: `decoded.premium_user ?? null` (HK/useAuthorized.ts:49). 대부분의 화면이 이것을 쓴다.
- 쓰는 파일(테스트 제외 55개, `grep -rln premiumUser`): 키(`components/organisms/create_key_button.tsx:229`, `components/templates/key_info_view.tsx:104`, `key_edit_view.tsx`, `keyEditFieldNormalizers.ts`, `key_team_helpers/ModelMaxBudgetEditor.tsx`), 팀(`components/Teams.tsx`, `team/TeamInfo.tsx`, `team/EditMembership.tsx`, `team/memberFormValues.ts`, `app/(dashboard)/teams/page.tsx:7`), 사용자(`users/_components/BulkEditUsers.tsx:49`, `user_edit_view.tsx`, `view_users/user_info_view.tsx:91`), 조직(`organizations/page.tsx:7`, `OrganizationsPanel.tsx`, `hooks/organizations/useOrganizations.ts:14,31`), 모델(`models-and-endpoints/page.tsx:92`, `PassThroughPanel.tsx:7`, `add_model/AddModelForm.tsx:96`, `model-hub-table/page.tsx:10`, `model_hub_table/page.tsx`, `AIHub/ModelHubTable.tsx`), 사용량(`usage/…/UsagePageView.tsx:105`, `EntityUsage.tsx`, `old-usage/page.tsx:8`, `old-usage/_components/usage.tsx`), 로그(`logs/page.tsx:16`, `logs/audit/AuditLogsPanel.tsx`), 캐시(`caching/page.tsx:7`, `cache_dashboard.tsx`), 알림·로깅(`logging-and-alerts/page.tsx:7`, `alerting/*`, `email_settings.tsx:35`, `common_components/PremiumLoggingSettings.tsx`), 패스스루(`add_pass_through.tsx`, `pass_through_info.tsx`, `PassThroughSettings.tsx`, `PassThroughSecuritySection.tsx`), 설정(`settings.tsx`, `admin-panel/_components/AdminPanel.tsx:68`), 텔레메트리(`TelemetrySettingsPage.tsx:200`, `TelemetryEnterpriseDialog.tsx`), 삭제 목록(`DeletedKeysPage.tsx:12`, `DeletedTeamsPage.tsx:13`), 채팅(`app/chat/layout.tsx:20`, `contexts/ChatShellContext.tsx`, `chat/KeysPanel.tsx`, `app/chat/api-keys/page.tsx:7`), 계정 메뉴(`Navbar/UserDropdown/UserDropdown.tsx:100,207`, `SidebarAccountMenu/SidebarAccountMenu.tsx:214,251`), LiteAdmin(`liteadmin/LiteAdmin.tsx:55`).
- 모두 화면 잠금·배지 표시용이다. 대부분 props 로 아래로 내려준다. 개별 화면의 잠금 내용은 각 화면 계약 문서에서 다룬다.

### 8.3 `true` 로 보낼 때 생기는 일 (주의)

- 계정 메뉴에 "Premium" 배지가 뜬다(UserDropdown.tsx:100-104).
- **LiteAdmin(대시보드 안 AI 관리 비서)** 이 proxy_admin(쓰기 가능) 세션에서 켜진다. 조건은 `premium_user === true`, 비 viewer, proxy_admin 이다(LiteAdmin.tsx:52-62, UserDropdown.tsx:85, :207). 켜지면 `/sso/get/ui_settings` 와 모델 목록 등을 부른다(LiteAdmin.tsx:144, :408). 이 기능의 서버 계약은 이 문서 범위 밖이다. 숨기려면 계정별 토글 `useDisableLiteAdmin` 을 써야 한다(미확인: 저장 위치).
- 키 regenerate 등 premium 전용 버튼이 열린다. 그 서버 엔드포인트를 우리가 구현하지 않으면 눌렀을 때 오류가 난다(J §2.5).

---

## 9. 우리 서버가 맞출 최소 집합

1. `GET /litellm/.well-known/litellm-ui-config` 와 `/.well-known/litellm-ui-config` (인증 없음, §2.2 모양).
2. `POST /v2/login` (JSON) 과 `POST /login` (form, 303). 성공 본문 `{redirect_url, token}`, 쿠키 `token`(HttpOnly 아님, SameSite=Lax, Path=/, Secure 는 HTTPS 일 때).
3. JWT: HS256, 클레임 11개(§3.6), `exp`=24h, `premium_user=true`.
4. 세션 키: DB 저장 가상 키, `team_id="litellm-dashboard"`, 24h, 예산 1.0, 비밀번호 로그인은 metadata `login_method=username_password`.
5. `GET /management/v1/users/me` (실패하면 대시보드가 로그아웃).
6. `GET /sso/get/ui_settings` (`PROXY_LOGOUT_URL` 포함), `GET /get/ui_settings`, `GET /get/ui_theme_settings` (public).
7. 웹 SSO: `/sso/key/generate`(return_to 무시) → `/sso/callback` → 303 `/ui/?login=success` + 쿠키.
8. `POST /session/logout`, `POST /user/password/change`, `password_reset_required` 제한(allowed_routes 3개 + RC 403 문구).
9. 초대·온보딩: `/invitation/new`, `/onboarding/get_token`, `/onboarding/claim_token` (scrypt 해시 형식 그대로).
10. 오류 문구: 401 자격 증명, 403 재설정 필요, `"Authentication Error - Expired Key…"`(대시보드가 이 문자열로 세션 만료를 감지한다).

---

## 10. 미확인 항목

- `/ui/litellm-runtime-config.json` 을 litellm 이 정적 파일로 동봉하는지(§2.1).
- 로그인 시도 제한의 사용자 단위 한도 공식과 이 기능이 upstream 에 있는지(§3.1).
- DB 사용자 역할이 null 일 때 JWT `user_role` 값, `generate_key_helper_fn` 의 models 기본값(§3.4).
- `litellm-dashboard` 팀 행 없이 세션 키 인증이 통과하는 코드 경로(§3.5).
- `is_request_https` 가 `X-Forwarded-Proto` 를 믿는 조건(§3.7).
- `/management/v1/users/me` 404 problem 의 `type` 접두어와 Content-Type(§4.3).
- 온보딩 세션에 `login_method` metadata 가 없어 비밀번호 변경이 막히는 것이 의도인지, 온보딩 JWT 에 `exp` 가 없는 것이 의도인지(§6.3).
- HIBP 호출 실패 시 비밀번호 설정을 거부하는지(§6.5). 평문 비밀번호 마이그레이션 실행 조건(§6.4).
- `PROXY_LOGOUT_URL` 이 비었을 때 로그아웃 후 실제 도착 URL(§7.2).
- `useDisableLiteAdmin` 토글 저장 위치(§8.3).
