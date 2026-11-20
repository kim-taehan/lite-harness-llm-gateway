# LiteLLM Premium(유료) 벽 검토 (PREMIUM_WALL_REVIEW)

## 0. 요약

- `LITELLM_LICENSE` 환경변수로 premium이 결정되고, license는 Berri AI가 서명한 토큰 (RSA-PSS 서명 검증)
- 게이트는 **83개 체크 지점 / 31개 파일**. 전부 `if premium_user is not True: raise` 패턴이라 구현이 단순
- 우리 목적(비용 추적/키 관리/로깅/SSO 로그인)에 걸리는 벽: **SSO 5명 초과만 실질적**
- 포크 관점: license는 위조 불가(비공개 키가 Berri AI 보유, 공개 키는 리포에 포함) → **라이선스 위조 경로 없고, 게이트 코드 제거(포크)가 정당한 경로**
- `enterprise/` 디렉터리는 오픈소스 이미지/패키지에 포함됨 (기능은 license 게이트에 막혀 있음)

---

## 1. License 판정 메커니즘

`litellm/proxy/proxy_server.py:1106-1108`:

```python
_license_check = LicenseCheck()
premium_user: bool = _license_check.is_premium()
```

`litellm/proxy/auth/litellm_license.py`의 `LicenseCheck`:

| 동작 | 근거 |
|---|---|
| `LITELLM_LICENSE` env 또는 config `general_settings.litellm_license` (proxy_server.py:6471, 7180) | 리프레시 가능 |
| 1단계: **오프라인 서명 검증** — license는 `base64(json_payload"."signature)` 형식, `litellm/proxy/auth/public_key.pem` (OSS 리포에 포함)으로 RSA-PSS/SHA256 검증 | litellm_license.py:176-223 |
| 2단계: **온라인 검증** — `GET https://license.litellm.ai/verify_license/{key}` (3회 재시도), 1단계 실패 시 폴백 | litellm_license.py:60-98 |
| 둘 다 실패 → `premium_user = False` (fail-closed) | litellm_license.py:130-131 |
| license payload (`EnterpriseLicenseData`, _types.py:5274): `expiration_date`, `user_id`, `allowed_features` (예: `auto_router`, `*`), `max_users`, `max_teams` | _types.py:5274-5279 |
| feature별 권한: `grants_feature()` (allow list 또는 `*` 와일드카드) | litellm_license.py:158-163 |
| 시트/팀 제한: `is_over_limit(total_users)`, `is_team_count_over_limit()` | litellm_license.py:139-156 |
| auto-router 기능 제한: license 없으면 classifier/커스터마이즈 기능 **개별 1개**만 허용 (`auto_router_capability_limit() = 1`) | litellm_license.py:165-174 |
| 7일 트라이얼: 에러 메시지에 트라이얼 링크 (`litellm.ai/enterprise#trial`) 포함 | common_proxy_errors.py:12 |

**핵심**: 공개 공개 키가 리포에 있으므로 license 형식은 공개되어 있지만, 서명하려면 **비공개 키**가 필요 → license 위조 불가. `premium_user = True` 패치는 포크 안에서만 가능.

---

## 2. 게이트된 기능 전체 카탈로그 (83 체크 / 31 파일)

### 2.1 Identity / Auth

| 기능 | 근거 |
|---|---|
| **SSO 사용자 5명 초과** (FREE: 5 billable users 이하. 웹 SSO + SAML + CLI SSO) | ui_sso.py:1077-1091 (`_raise_if_sso_exceeds_free_user_limit`), 1138, 2355에서 호출 |
| OAuth2 토큰 인트로스펙션 (RFC 7662) | oauth2_check.py:129-133, user_api_key_auth.py:2234-2240 |
| JWT Auth (자체 JWT 검증 인증) | user_api_key_auth.py:2254-2260 |
| `allowed_routes` (키별 라우트 권한) | auth_utils.py:848-851 |
| `admin_only_routes` (관리자 전용 라우트) | route_checks.py:415-421 |
| `allowed_ips` (IP 기반 접근 제어) | proxy_server.py:7122 |
| `enforced_params` (요구 파라미터 강제) | litellm_pre_call_utils.py:3739, proxy_server.py:7176 |
| `worker_registry` (다중 워커) | proxy_server.py:7391 |
| `max_request_size_mb` / `max_file_size_mb` (크기 제한) | auth_utils.py:1100-1104, http_parsing_utils.py:501 |
| `get_spend_routes` 키 권한 | key_management_endpoints.py:5246 |
| 팀 스코프 모델 액션 (team_id가 있는 모델 파라미터) | model_management_endpoints.py:2258 |

### 2.2 Keys / Teams / Budgets

| 기능 | 근거 |
|---|---|
| **Key tag** + **Tag budget** (태깅/태그별 예산) | key_management_endpoints.py:1653, router_strategy/budget_limiter.py:978-981 |
| `model_max_budget` (키·모델별 예산) | key_management_endpoints.py:8388 |
| 팀 `model_max_budget` | management_endpoints/common_utils.py:92 |
| Team admin (팀 관리자 지정) | team_endpoints.py:2910-2916 |
| `custom_team_metadata_validate` | team_metadata_validation.py:113 |
| **Virtual Key 재생성** | key_management_endpoints.py:6373-6374 |

### 2.3 Cost Tracking

| 기능 | 근거 |
|---|---|
| `/spend/report` (비용 리포트) | spend_management_endpoints.py:1525, 1833-1843 |
| (나머지: /spend/logs, /global/spend, 팀/사용자/모델별 비용, 예산 **자체**는 **무료**) | |

### 2.4 Guardrails / Callbacks

| 기능 | 근거 |
|---|---|
| Llama Guard, Llm Guard (프롬프트/출력 가드레일) | callback_utils.py:212, 291 |
| Secret hiding/detection | callback_utils.py:227 |
| OpenAI Moderation, Google Text Moderation | callback_utils.py:243, 278 |
| BlockedUser list, BannedKeyword | callback_utils.py:306, 321 |
| Custom Guardrail 동적 extra_body 키 | integrations/custom_guardrail.py:1314-1336 |
| 구현체는 `enterprise/enterprise_hooks/` (banned_keywords, blocked_user_list, google_text_moderation, openai_moderation, aporia_ai 등) | enterprise/enterprise_hooks/ |
| PagerDuty 알림 | enterprise/litellm_enterprise/enterprise_callbacks/pagerduty/ |

### 2.5 Secret Managers

| 기능 | 근거 |
|---|---|
| HashiCorp Vault | secret_managers/hashicorp_secret_manager.py:121-123 |
| Google Cloud Secret Manager | secret_managers/google_secret_manager.py:32-36 |
| CyberArk | secret_managers/cyberark_secret_manager.py:61-63 |

### 2.6 Logging / Alerting

| 기능 | 근거 |
|---|---|
| GCS Bucket 로깅 | integrations/gcs_bucket/gcs_bucket.py:50, 60 |
| Azure Blob Storage 로깅 | integrations/azure_storage/azure_storage.py:354-362 |
| 이메일 알림 커스텀 (로고/지원 연락처) | integrations/SlackAlerting/slack_alerting.py:1236-1238 |
| (나머지 로깅: Datadog, Langfuse, OTel, CSV/JSON, Posthog 등 대부분 무료) | |

### 2.7 Endpoints

| 기능 | 근거 |
|---|---|
| **Fine-tuning** (4개 엔드포인트) | fine_tuning_endpoints/endpoints.py:114, 266, 420, 541 |
| Pass-through endpoint (premium_user 전파) | pass_through_endpoints.py:3475, 3596-3623 |
| auto-router classifier/커스터마이즈 개수 제한 (license 없이 각 1개) | litellm_license.py:165-174 |

---

## 3. enterprise/ 디렉터리 구조

```
enterprise/
├── enterprise_hooks/                    # premium callback 구현체
│   ├── aporia_ai.py
│   ├── banned_keywords.py
│   ├── blocked_user_list.py
│   ├── google_text_moderation.py
│   └── openai_moderation.py
├── enterprise_ui/
│   └── _enterprise_colors.json          # 브랜드 오버라이드 (UI 빌드 시 적용)
└── litellm_enterprise/                  # workspace 패키지 (litellm-enterprise==0.1.79)
    ├── enterprise_callbacks/            # llama_guard, secret_detection, pagerduty, send_emails
    ├── integrations/custom_guardrail.py
    └── proxy/, types/
```

- `pyproject.toml:82` → `litellm-enterprise==0.1.79` (workspace 멤버, root 의존성)
- Dockerfile:163-166 → `COPY --from=builder /app/enterprise /app/enterprise` (OSS 이미지에 **소스 경로로 포함**, `uv sync --no-install-workspace`라 venv에는 설치 X, 런타임 import만 가능)
- 즉 "enterprise 코드" 자체가 OSS에 공개됨, 막히는 것은 **license 게이트뿐**

---

## 4. 무료(Community) 범위 vs 유료

| 영역 | 무료 | 유료만 |
|---|---|---|
| 라우팅/Proxy | 전체 | — |
| 비용 추적 | /spend/logs, /global/spend, 사용자/팀/모델/키별 비용, 예산 | `/spend/report`, tag budget |
| 키/팀/사용자 | CRUD, 팀, 예산, 모델별 예산 X | model_max_budget, key tag, virtual key 재생성, team admin |
| SSO | 5 billable users 이하, 웹 SSO + CLI SSO + SAML | 5명 초과, OAuth2 인트로스펙션, JWT Auth |
|_guardrails | Custom Guardrail (정적), LangChain, Presidio 등 | Llama Guard, Moderation, BlockedUser, BannedKeyword, Secret Detection |
| Secret Manager | — | HashiCorp, GCP, CyberArk |
| 로깅 | Datadog, Langfuse, OTel, CSV/JSON, Posthog, Webhook 등 | GCS Bucket, Azure Blob |
| 기타 | Fine-tuning X, allowed_routes X, worker_registry X, max_*_size_mb X | 전부 |
| auto-router | base (classifier 각 1개 제한) | unlimited (license feature) |

---

## 5. 포크 관점 시사

1. **게이트 제거는 트리비안**: 전부 `if premium_user is not True: raise` 단일 체크. 포크에서 `litellm/proxy/proxy_server.py:1107`의 `premium_user`를 `True`로 만들거나 개별 지점을 제거하면 됨
2. **권장 접근**: blanket on(전부)보다 **필요 기능 단위**로 게이트 제거 (예: key tag + tag budget만) → upstream 재머지 시 충돌 최소화, 감사 추적 용이
3. **SSO 5명 초과**: 현재 우리 환경은 dev라 무관, production에서 팀원이 5명 넘는 경우에만 포크 패치 또는 7일 트라이얼(연장 불가?) 검토
4. **license 위조 불가** (공개 키만 공개, 서명 불가) → 합법적 경로 = 포크 패치 (MIT/Apache-2.0 license 하에서 내부 fork 사용은 가능, 다만 "LiteLLM" 브랜드로 재배포 시 license 조항 확인 필요)
5. **trial**: 에러 메시지에 `litellm.ai/enterprise#trial` (7일) 링크 — PoC/검증용 무료 경로
6. **상호운영 주의 point**: `premium_user`를 blanket True로 만들면 `enterprise/enterprise_hooks` lazy import가 실제로 실행되어 해당 package import 실패 가능 (Docker 이미지는 /app/enterprise 소스 경로 포함 → OK. pip install litellm만 쓴 환경은 `litellm-enterprise` package가 함께 설치되어 → OK)

---

## 6. 폐쇄망(air-gapped) 환경 적합성

### 6.1 런타임 외부 호출 전수

| 외부 호출 | 폐쇄망 영향 | 근거 |
|---|---|---|
| License server `license.litellm.ai` | **무관** — `LITELLM_LICENSE` unset이면 호출 자체 안 함. license가 있으면 **오프라인 서명 검증 통과 시 온라인 폴백을 안 함** (short-circuit) | litellm_license.py:124-127 |
| Telemetry (PostHog 기반) | **기본 OFF** (opt-in, `LITELLM_TELEMETRY_*`). ON이라 해도 destination이 `local_table`(내부 DB) 또는 자사 endpoint 지정 가능 | proxy/telemetry/settings.py, consent.py |
| PostHog/Langfuse/Datadog 등 callback | 사용자 설정 시에만 (폐쇄망에선 내부 엔드포인트로 연결) | — |
| 비용표 `model_prices_and_context_window.json` | **패키지에 번들** (오프라인 OK) | litellm/model_prices_and_context_window.json |
| SSO | **자체 IdP(Keycloak, Azure AD on-prem 등) GENERIC_CLIENT_ID/SAML로 가능**, Google/Microsoft 클라우드 IdP만 불가 | ui_sso.py (generic_sso, SAMLAuthHandler) |
| 모델 호출 | 폐쇄망 내부 LLM (Ollama, vLLM 등)로 라우팅 — 이미 devstral:24b 운영 중 | config.yaml |

**결론: 런타임 phone-home이 폐쇄망에서 차단되어 무 기능화되는 코드는 없음**

### 6.2 배포(일회성) 이슈

- Docker 이미지(`ghcr.io/berriai/litellm`, `postgres:16-alpine`, `nginx`) + Ollama 모델 → **외부 네트워크가 되는 한 대에서 pull 후 `docker save` / `docker load`** (또는 소스에서 오프라인 빌드, 의존성 벤딩 필요)
- Prisma engine/Node runtime은 이미지에 포함 (빌드 시 해결됨)

### 6.3 폐쇄망 + premium 공식 지원 존재

- `airgapped_license_data` 메커니즘이 폐쇄망 프리미엄을 위한 것 (오프라인 서명 검증 = public_key.pem, 리포 포함)
- 즉 Berri AI는 air-gapped 상용 배포를 공식 지원: **license 문자열 사전 발급 → env 주입 → 오프라인 검증**
- 포크 패치(게이트 제거)와 license 2택 1

### 6.4 폐쇄망에서 실질 제약

| 제약 | 규모 | 대응 |
|---|---|---|
| SSO 시트 5명 | DB user 수 초과 시 신규 SSO 로그인 403 | 포크 1줄 제거 또는 premium license (오프라인 검증 OK) |
| tag budget / model_max_budget / JWT Auth 등 | 게이트 83개 중 해당 기능 | 필요 시 개별 제거 |
| Google/Microsoft 클라우드 SSO | 불가 (외부 IdP) | 자체 OIDC IdP (Keycloak 추천) |

---

## 7. 검증 체크리스트

- [x] `premium_user` 결정 경로 (proxy_server.py:1106, litellm_license.py 전체 읽기)
- [x] public_key.pem이 OSS에 포함됨 확인
- [x] 83 체크 지점 / 31 파일 전수 grep
- [x] SSO free tier 5 users (ui_sso.py:1077, 1091)
- [x] enterprise/ 디렉터리 구조 + Dockerfile 포함 확인
- [x] `EnterpriseLicenseData` schema (expiration, allowed_features, max_users, max_teams)
- [x] fail-closed 동작 (두 검증 모두 실패시 False)
- [x] enterprise/LICENSE.md 전문, MIT 본문의 확인
- [x] 공개 문서(docs.litellm.ai/docs/enterprise) 대조
- [x] SSO 경로 enterprise/ 코드 참조 전수 확인

---

## 8. License / 약관 검토 (Berri AI)

### 8.1 라이선스 구조 (디렉터리 기반 이중 라이선스)

최상위 `LICENSE` 본문:

> "All content that resides under the `enterprise/` directory ... is licensed under the license defined in `enterprise/LICENSE`. Content outside of the above mentioned directories ... is available under the MIT license"

| 범위 | 라이선스 | 저작권 |
|---|---|---|
| `enterprise/` 디렉터리 | **BerriAI Enterprise License** (상업) | (c) 2024- Berrie AI Inc. |
| 그 외 전부 (`litellm/`, `ui/` 등) | **MIT** | (c) 2023 Berri AI |

### 8.2 BerriAI Enterprise License 핵심 조항

1. **production 사용** = ① Subscription ToS 동의 + ② "correct number of user seats" 분량의 유효 license 요건
2. **enterprise 코드 수정 자체는 허용** ("free to modify this Software and publish patches")
3. 단, **그 수정본의 사용/복사/배포도 유효한 license와 함께만** (dev/test는 제외)
4. **dev/test 목적 복제·수정은 무료**
5. copy/merge/publish/distribute/sublicense/sell 금지 (위 조항의 범위 내에서만 허용)
6. 보증 없음 (AS IS)

### 8.3 공개 문서 확인 (docs.litellm.ai/docs/enterprise, 2026-10-23)

- Enterprise = **"same Gateway image + license key"** (이동 없음, 이미지 동일)
- **SSO free for up to 5 users** (공식 문서 명시)
- 30일 무료 트라이얼
- 가격: 사용량/규모 기반 견적 (100+ users 또는 10+ production use case 타겟)
- 구매: AWS/Azure Marketplace 또는 invoice
- SOC 2 Type II 감사, signed Docker image
- license guardrail 명시: llmguard_moderations, llamaguard_moderations, hide_secrets, openai_moderations, google_text_moderation, lakera_prompt_injection, aporia_prompt_injection (OAS side: custom guardrail + Presidio는 free)

### 8.4 우리 패치(SSO 5명 한도 해제)의 라이선스 판정

| 확인 사항 | 결과 |
|---|---|
| 패치 대상 파일 `litellm/proxy/management_endpoints/ui_sso.py` 은 `enterprise/` 밖 | **MIT 적용** |
| `_raise_if_sso_exceeds_free_user_limit` 은 MIT 코드의 비즈니스 제한 로직 | enterprise 코드가 아님 |
| generic OIDC/SAML 인증 플로우가 `enterprise/` import 하는 곳 = **1곳뿐** (ui_sso.py:1164, `user_custom_ui_sso_sign_in_handler` 설정 시에만) | 표준 generic/SAML 구성에서는 **enterprise 코드 비활성** |
| `count_billable_users()` (repositories/user_repository.py) | MIT, license 시트·Prometheus 지표로도 사용 (변경 불필요) |

**결론**: SSO 5명 한도 해제는 MIT 라이선스 범위 내 수정 + 내부 production 사용 → **BerriAI license 불필요**. copyright notice 보존(MIT 요건)만 하면 됨.

### 8.5 금지선 (넘으면 안 되는 것)

- `enterprise/` 안 코드(llama_guard, pagerduty, vault/gcp/cyberark secret manager, GCS/Azure 로깅 등)를 **production에서 활성화/사용** → BerriAI subscription + license 필요
- `premium_user`를 blanket `True`로 → enterprise 기능들이 실질 운영됨 → 위 금지선 초과
- `enterprise/` 파일 자체의 수정 → 수정본 활용도 license 요건 (dev/test만 무료)
- fork 이미지를 대외 **재배포/판매** → MIT는 허용하나 BerriAI 상표/기업 정책 검토 필요 (내부 폐쇄망 사용은 해당 없음)
- MIT portion이라도 `LITELLM_LICENSE` 관련 코드 구조를 훼손하면 upstream 재동기화 시 충돌만 생김 (라이선스 문제는 아님)
