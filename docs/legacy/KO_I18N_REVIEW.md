# LiteLLM Admin UI 전체 한글화(i18n) 검토

조사 대상: `ui/litellm-dashboard/` (LiteLLM proxy에 정적 서빙되는 Admin UI)
조사 방식: 코드 실측 (스크립트 측정 + file grep), GitHub issue 검색 (gh CLI), 2026-10-23 기준

## 1. 결론 요약

**조건부 Go (Conditional Go).**

기술적으로 완전히 구현 가능한데, 규모가 "한두 주짜리"가 아니다. 실측 결과 번역 대상 문자열 발생 횟수는 약 41,702건이고 유니크 키는 약 22~23k 수준. 페이지 41개 중 다수가 얇은 wrapper라서 실제 작업은 `src/components/` 공유 컴포넌트(671개 파일, 118k 라인)와 페이지 자체에 함께 잡혀 있으며, 공유 컴포넌트가 전체 문자열의 약 절반(48%)을 가진다. 핵심 제약은 Next.js **static export**(`output: "export"`)라 URL에 locale을 넣는 next-intl의 `[locale]` 패턴은 불가하고, 클라이언트사이드 react-i18next + cookie/switcher 조합이 정답이다. 전체 커버리지 기준 1인 40~60인일, 고빈도 핵심 페이지만(Phase 0+1)이면 18~28인일로 좁힐 수 있다.

결정 변수는 "전체 40개 페이지를 다 번역할 것인가"이다. 답이 yes라면 i18n 인프라는 반드시 들어가야 하고(대체안 없음), no라면(주로 4~8개 뷰만) `ui/ko-dashboard` 확장이 여전히 가장 싸다.

## 2. UI 스택 및 구조

### 스택 (package.json 실측)

| 항목 | 값 | i18n 관련 의미 |
|---|---|---|
| 프레임워크 | Next.js **16.3.8** (App Router) | App Router 컨벤션 적용 |
| React | 19.2.8 | 최신, i18n 라이브러리 호환 문제 없음 |
| TypeScript | 5.9.3 | 타입 기반의 키 정의 가능 |
| CSS | Tailwind CSS 4.3 (@tailwindcss/postcss) | 클래스명 텍스트는 번역 대상 아님 |
| **i18n 라이브러리** | **없음** (next-intl, react-i18next, i18next 모두 미의존) | 기존 이니셔티브 없음, 청신호 |
| UI primitives | @base-ui/react + shadcn 스타일 래퍼, lucide icons | a11y 속성(aria-label)도 번역 대상 |
| 상태/데이터 | TanStack Query 5, Table, Virtual; nuqs (URL state); react-hook-form + zod | 폼 라벨/오류 문구 다수 |
| 차트 | recharts 3.9 | 차트 라벨은 적음 (실측 92개) |
| 토스트 | sonner 2.0 | `toast.error()` 호출 410곳 |
| 날짜 | **date-fns + dayjs + moment 혼용** + 브라우저 `Intl` | 포맷 정책 통일 필요 (295 call site) |
| 테스트 | Vitest 4 (jsdom), 테스트 파일 **944개** | 텍스트 어설션 약 1만개 (Section 6) |

### 배포 구조 (핵심 제약)

`next.config.mjs`에 `output: "export"` (default, devProxyUrl 없을 때) → **정적 HTML/JS 빌드**. FastAPI proxy가 이 빌드를 runtime UI 디렉토리로 복사한 뒤 `/ui/{route}` 경로에서 서빙하고, extensionless 라우트(`/ui/login`)는 `<route>/index.html` 매핑으로 동작한다(`proxy_server.py:2565` 주석 확인). `trailingSlash: true`, `assetPrefix: "/litellm-asset-prefix"`.

이것이 i18n 설계의 최대 제약이다 (Section 5 참조).

### 라우트 구조

- `(dashboard)` 그룹: 41개 페이지 라우트 (인덱스 page 포함) + `layout.tsx` (275라인, 인증/사이드바 셸)
- 루트 라우트: `login`, `onboarding`, `chat/*`(페이지 6개), `model_hub`, `model_hub_table`, `connect`, `mcp/oauth/callback`, `moyai` = 페이지 파일 13개
- 내비게이션은 **path based**: `uiHref(routeSegment)` → `/ui/api-keys` 형태. `?page=` 쿼리 파라미터는 레거시 리다이렉트 전용(`legacyPageRoutes.ts`, 40개 alias map)
- `routeSegmentForPathname()`이 telemetry, full-bleed 판단에 사용 → URL 구조 변경을 꺼려야 하는 또 다른 이유
- 41개 페이지 중 다수(teams, tool-policies, model-hub-table, lens, api-keys, change-password 등)는 10~30라인의 얇은 wrapper. 실제 UI는 `src/components/`의 공유 컴포넌트에 있다

## 3. 문자열 규모 실측

### 측정 방법

Node.js 스크립트(single-pass line scanner)를 `ui/litellm-dashboard/src` 전체에 적용했다. 과정은 다음과 같다.

1. 대상: `.ts`/`.tsx`, 테스트 파일(944개)·`.d.ts`·mock/fixture 제외, 1,389개 프로덕션 파일, 약 236k 라인
2. 라인 단위 스캐너로 문자열 리터럴(`'`, `"`, `` ` ``) 내용을 분리하고(코멘트 처리 포함), 나머지 코드에서 번역 후보를 잡았다
3. 분류:
   - **JSX 텍스트**: 태그 사이 문자(`>문자<`) + 순수 텍스트 라인 (멀티라인 텍스트 노드)
   - **JS 문자열 리터럴**: 오류 메시지, 토스트 문구, 선택지, 라벨 전달 문자열
   - **속성 문자열**: `placeholder` / `aria-label` / `alt` / `label` / `title` / `aria-description`의 문자열 인자
4. "인간 문장" 필터: 대문자 또는 문장 종료 구두점(`.`, `!`, `?` + 공백/끝)을 포함하고, import 경로(`@/...`), URL, CSS 클래스(전체 소문자), 상수, TS 타입 조각(`href?: string;`)을 제거. 단일 토큰은 `^[A-Z][a-z0-9]{2,}$` (예: Save, Playground)만 포함

한계와 편향:

- **과소 측정**: 전문장 소문자("failed to load the list" 등)는 필터에서 빠져 측정 미포함. 실제는 측정치보다 약간 클 것
- **오포착(오탐) 약 1~2%**: className 템플릿 보간 문자열(`flex ... ${x}`) 일부, 예시 데이터
- 중복 제거 전 "발생 횟수"가므로 실제 번역 작업의 키 수보다 크다 (유니크 값 별도 측정)

### 실측 수치 (2026-10-23, 현재 작업트리 기준)

| 구분 | 발생 횟수 | 비고 |
|---|---:|---|
| JSX 텍스트 노드 | 26,451 | 버튼, 헤더, 테이블 열, 빈 상태 문구 |
| JS 문자열 리터럴 | 13,298 | 오류/토스트/라벨/선택지, 동적 템플릿 포함 |
| 속성 문자열(placeholder 등) | 1,953 | a11y 속성 포함 (문자열 리터럴 인자) |
| **합계(발생 횟수)** | **41,702** | |
| **유니크 키** (소문자 정규화 후) | **약 22.9k** | "Name", "Save" 등 중복 다수 |
| `toast.*()` 호출 사이트 | 410 | 대부분 영어 고정 문구 + 일부 백엔드 detail |
| 백엔드 오류 참조(`.detail`, `error.message` 등) | 510 | i18n 범위 밖, 별도 전략 필요 (Section 6) |

### 지역별 분포

| 지역 | 발생 횟수 | 파일 수 | 라인 수 |
|---|---:|---:|---:|
| **`src/components/` (공유 컴포넌트)** | **19,835** | 671 | 118,331 |
| `src/data/` (compliance 프롬프트 샘플) | 2,671 | 6 | 7,597 |
| 페이지(41) + 루트 라우트(13개 파일) 합계 | 약 19,200 | — | — |

가장 중요한 구조적 사실: **공유 컴포넌트가 전체의 약 절반(48%)**을 차지한다. `api-keys` 페이지 폴더 자체는 14개 문자열에 불과하지만, 실제로 렌더링하는 `src/components/VirtualKeysPage/` + `templates/key_*` + `key_team_helpers/` (공유, 합계 약 900개)가 진짜 일이다. "페이지 40개 번역"이 아니라 "671개 공유 컴포넌트 파일(문자열이 있는 것 대부분) + 페이지 폴더"가 실질 작업 대상인 것과 같다.

최대 단일 파일: `src/components/networking.tsx` 1,538개 (API 클라이언트 + 에러 처리 + 공통 UI가 8,001줄에 쌓인 god file. 이 파일 추출/리팩토링 여부가 작업 속도를 좌우). 다음으로 `src/data/insultsCompliancePrompts.ts` 1,191 (예시 데이터, 번역 제외 대상), `playground/.../ChatUI.tsx` 577, `team/TeamInfo.tsx` 545, `provider_info_helpers.tsx` 442, `policies/.../pipeline_flow_builder.tsx` 400.

`src/data/*CompliancePrompts.ts` 3개(약 5.3k 문자열)는 compliance 기능의 **장문 프롬프트 예시 데이터**다. UI 상에서 보이긴 하지만 성격상 "번역"이라기보다 "재작성"에 가깝고, 실무적으로는 영문 유지(LLM 입력 콘텐츠)가 합리적이다. 본 검토 노력량에서 제외했다.

## 4. 페이지별 규모

측정 방법: 각 페이지 디렉터리(`(dashboard)/<name>/`) + 해당 페이지가 렌더링하는 주요 공유 컴포넌트(`src/components/<sub>/`)를 각각 실측. "공유" 값은 해당 페이지의 주 렌더링 대상만 할당한 것이고, 공용으로 쓰이는 컴포넌트(templates/key_*, add_model 등)는 2개 이상 페이지에 중복 속성될 수 있다. 엄밀한 파티션 합계는 Section 3의 41,702건.

전체 페이지를 가로지르는 전역 공유 컴포넌트(페이지만 해도 반드시 처리): `networking.tsx` 1,538 + `shared/` 1,103 + `common_components/` 713 + `ui/`(primitives) 392 + `leftnav` 212 + `liteadmin` 192 + `Navbar` 156 + CommandPalette 95 ≈ **4.4k**.

| # | 페이지 | 페이지 폴더 실측 | 주요 공유 컴포넌트(실측) | 추정 합계 | 난이도 |
|---|---|---:|---|---:|---|
| 1 | models-and-endpoints | 690 | add_model 2,212, model_add 322, provider_info_helpers 442, model_info_view 184, ModelInfoEditForm 199 | **~4.0k** | 높음 (가장 큼) |
| 2 | playground | 2,928 | chat_ui 계열 (포함) | ~2.9k | 높음 (LLM 스트리밍, 동적 컨텐트) |
| 3 | guardrails | 2,650 | guardrail_garden_data 290 (데이터) | ~2.7k | 높음 (폼 다수, 커스텀 코드) |
| 4 | mcp-servers | 2,545 | mcp_server_management 99, mcp_tools 179 | ~2.8k | 높음 (CRUD + 권한) |
| 5 | teams | 2 | Teams.tsx 253, team/ 1,118, TeamsPage 135, organization 177 | ~1.7k | 중간 |
| 6 | admin-panel | 105 | Settings 1,099, settings 170, PassThrough 96, router_settings 84, SSO 175 | ~1.6k | 중간~높음 (탭 20+개) |
| 7 | policies | 1,468 | pipeline_flow_builder 400 (포함) | ~1.5k | 중간~높음 |
| 8 | logs | 12 | c/logs 1,473, DeletedKeys/DeletedTeams | ~1.5k | 중간 (대용량 테이블, 동적 데이터) |
| 9 | model-hub-table | 3 | AIHub 749, public_model_hub 258, columns 106 | ~1.1k | 중간 (퍼블릭 페이지) |
| 10 | usage | 845 | UsagePage 225, Export 83 | ~1.1k | 중간 |
| 11 | agents | 972 | agent_info 등 (포함) | ~1.0k | 중간 |
| 12 | users | 736 | CreateUserButton 87, bulk_create 168 | ~1.0k | 중간 (SSO 연계) |
| 13 | api-keys | 14 | VirtualKeysPage 205, templates(key_*) 579, key_team_helpers 123 | ~0.9k | 중간 |
| 14 | prompts | 696 | — | ~0.7k | 중간 |
| 15 | vector-stores | 606 | vector_store_providers 99 | ~0.7k | 중간 |
| 16 | cost-optimization | 528 | ShadowEval 등 (포함) | ~0.5k | 중간 |
| 17 | caching | 405 | — | ~0.4k | 중간 |
| 18 | cost-tracking | 386 | CloudZero 119 | ~0.5k | 중간 |
| 19 | search-tools | 343 | ToolDetail 89 | ~0.4k | 중간 |
| 20 | projects | 267 | — | ~0.3k | 중간 |
| 21 | access-groups | 265 | — | ~0.3k | 낮음~중간 |
| 22 | guardrails-monitor | 245 | — | ~0.2k | 중간 (차트) |
| 23 | tag-management | 217 | — | ~0.2k | 낮음 |
| 24 | skills | 204 | claude_code_plugins 91 | ~0.3k | 낮음 |
| 25 | budgets | 192 | — | ~0.2k | 낮음 |
| 26 | old-usage | 168 | — | ~0.2k | 낮음 (레거시) |
| 27 | memory | 163 | — | ~0.2k | 낮음 |
| 28 | organizations | 121 | — | ~0.1k | 낮음 |
| 29 | chat (루트 6개) | 101 | — | ~0.1k | 중간 |
| 30 | workflows | 88 | — | ~0.1k | 낮음 |
| 31 | router-settings | 76 | router_settings 84 | ~0.2k | 낮음 |
| 32 | telemetry | 74 | — | ~0.1k | 낮음 |
| 33 | ui-theme | 64 | — | ~0.1k | 낮음 |
| 34 | login / onboarding | 55 / 37 | — | ~0.1k | 낮음 (첫인상, 우선순위) |
| 35 | transform-request | 48 | — | ~0.05k | 낮음 |
| 36 | api-reference | 34 | — | ~0 | 낮음 (대부분 코드 샘플, 번역 제외) |
| 37 | change-password | 32 | — | ~0.05k | 낮음 |
| 38 | model-insights | 27 | — | ~0.05k | 낮음 |
| 39 | home | 22 | liteadmin 192 (전역) | ~0.2k | 낮음 (UpdatesFeed는 영문 원문 유지 권장) |
| 40 | tool-policies | 2 | ToolPolicies 128 | ~0.1k | 낮음 |
| 41 | lens | 2 | vendor `@litellm/lens-ui` (외부 패키지) | ~0 | 불가 (제3자 컴포넌트) |
| 42 | model_hub / moyai / connect / mcp-cb | ~19 | moyai 92 | ~0.1k | 낮음 |

요약:

- 페이지(폴더) + 해당 공유 컴포넌트 합계가 **1천 개 이상인 화면은 11개** (models-and-endpoints, playground, mcp-servers, guardrails, teams, admin-panel, policies, logs, model-hub-table, usage, api-keys/users) → 전체 UI 문자열의 약 절반
- 나머지 30여 화면은 각 200~700개 수준 → 기계적 추출로 빠르게 처리 가능
- 렌더링 폴더 자체는 얇지만 **공유 컴포넌트 번역 없이는 동작이 불완전**한 페이지가 다수 (api-keys, teams, logs, model-hub-table, tool-policies, admin-panel, lens 등)

## 5. i18n 도입 방식 비교 및 추천

### 옵션 A. next-intl (App Router `[locale]` 경로)

- 방식: `app/[locale]/...` 구조, `middleware.ts`에서 locale 협상, `generateStaticParams`
- **저장소에서는 부적합.** 이유:
  - `output: "export"`는 middleware 미지원 (정적 빌드로 서빙되기 때문에)
  - 모든 URL이 `/ui/ko/api-keys`처럼 바뀌는데, 이를 받는 쪽이 정적 export를 `<route>/index.html` 맵핑으로 서빙하는 FastAPI proxy이며, proxy 측에도 `/ui/...` 절대 경로가 하드코딩되어 있다 (`proxy_server.py:1164` model_hub_link, `:18716` onboarding 링크 등). URL 구조 변경 = 프록시 배포 코드 + 레거시 리다이렉트(`legacyPageRoutes.ts`) + 외부 링크 + telemetry(`routeSegmentForPathname`)까지 연쇄 수정
- 결론: **배제**

### 옵션 B. react-i18next (클라이언트사이드 프로바이더, URL 불변) — **추천**

- 방식: `I18nextProvider` 루트 레이아웃에 1개만 감싸고, `initReactI18next` + `useTranslation()`. 로케일 상태를 **cookie(또는 localStorage)** 에 두고, 첫 접속 시 `Accept-Language`로 초기화. URL에는 로케일을 넣지 않음
- static export와 완전 호환 (전부 클라이언트 JS), 기존 라우트/링크/쿼리 파라미터(`?page=`, nuqs URL state)와 충돌 0
- 생태계: 키 namespacing, 복수형, 보간(`{{name}}`), missing-key 로그, CI 체크 스크립트(`i18next-parser` 추출) 모두 기존 도구 존재
- 커뮤니티 선례 일치: GitHub issue [#19730](https://github.com/BerriAI/litellm/issues/19730) 에도 react-i18next 방식 제안이 올라와 있고, 중국어(native) 기여자가 내부 포크에서 2,000+ 키 zh-CN 레이어를 이미 만들어 두었다가 코어팀 수락 여부가 불확실해 PR 미제출 상태
- 단점: 41k 발생/23k 키 규모에서 초기 추출 작업이 가장 큰 리소스 (Section 7). 동적 key(`t(\`errors.${code}\`)`) 남발하면 런타임 누락이 발생하므로, **정적 키 + 이름공간(페이지별 JSON 파일)** 으로 규율해야 함

### 옵션 C. 자체 훅/컨텍스트 (dict + Provider)

- 구조: `{ ko: { "save": "저장" } }` 맵 + `useT()` 훅. 의존성 0
- 200개 키 규모의 mini 앱에는 적합하지만, 23k 키 + 400개 토스트 사이트 + 복수형/보간 필요 규모에서는 복수 처리, 보간, missing-key 감지, 추출 파이프라인을 자체 구축하게 되어 오히려 위험
- ko-dashboard(4뷰) 확장에는 이 방식이 맞고, 공식 UI 전체에는 부적합
- 결론: **전체 한글화에는 배제**

### 언어 전환 UI 및 상태 저장

- 전환 위치: `leftnav.tsx` 하단 또는 `Navbar`/`DashboardHeader` (둘 중 하나, 기존 theme toggle 패턴 참고, `next-themes`가 이미 URL 외부 상태 저장 선례)
- 저장: 로케일 자체는 설정이라 localStorage 적정 (AGENTS.md의 "토큰 금지" 규칙은 인증 정보만 대상). cookie를 쓰면 로그인 전/후, 서브페이지(퍼블릭 model hub)에서도 유지
- 기본값: `en`, `navigator.language`가 `ko`면 `ko`로 초기화(선택적, 사용자 명시 전환 우선)
- 미번역 키 폴백: 영어 원문(react-i18next `fallbackLng: "en"`) → 단계별 커버리지 확대 시 사용자 체감 깨짐 없이 진행 가능

### 테스트와의 관계 (선행 조건)

- 기본 로케일을 `en`으로 고정하고, 테스트 셋업에 `I18nextProvider`(en)을 마운트하면 기존 1만개 텍스트 어설션은 유지된다. 다만 컴포넌트가 `t()`를 쓰기 시작하면, 테스트가 어설션하는 영어 문자열이 **en JSON의 값**이 되어야 하며, en JSON을 영문 주석 없이 그대로 만들어야 "en = 현행 렌더링 결과"와 동일함(현 코드 문자열을 en 값으로 원본 보존하면 회귀 없음)

## 6. 위험 요소 & 주의점

### 6.1 백엔드 영어 오류 메시지 (가장 큰 체감 잔여 영문)

- `.detail` / `error.message` 참조 510곳. FastAPI/Pydantic validation 오류, LLM provider 오류, 인증 오류가 전부 **백엔드에서 영어로** 오며, 프론트에서 그대로 렌더링 (14개 토스트가 detail 직접 출력)
- i18n 키로는 커버 불가 (메시지가 동적이며 provider별 수십~수천 개). 현실적 전략:
  - 프론트 고정 문구("요청 실패:", "불러오지 못했습니다.")는 키화, 백엔드 원문은 뒤에 원문 그대로 표시 (한국어 요약 + 영문 원문 병기)
  - 고빈도 오류(인증 만료, rate limit, invalid API key 등)만 사전 정의된 Korean 매핑 표로 교체 — 별도 작업으로 분리하고 1차 범위에서 제외 권장
- **영향**: 전체를 "번역"해도 런타임에 보이는 영문은 상당수 남을 수 있음. 기대 설정이 중요

### 6.2 동적/코드 생성 텍스트

- 보간 템플릿(`View All ({count})`, `e.g. 100000`) 약 수천 개 → i18n 보간 문법(`{{count}}`)으로 1:1 변환. 한국어 어순이 달라 **문장 전체를 각 언어 버전으로** 써야지 단어 교체 불가
- `UpdatesFeed`(home), compliance 예시 데이터(`src/data/*CompliancePrompts.ts` 5.3k), API Reference 코드 샘플, LLM 응답, 사용자 생성 콘텐츠(모델명, 태그, 메모)는 번역 대상 아님/유지 권장
- `lens` 페이지의 `@litellm/lens-ui`는 vendored 외부 패키지(`file:vendor/...tgz`) → 공식 UI i18n 범위에 안 잡힘 (별도 벤더 요청 필요)

### 6.3 날짜/통화 포맷

- 295개 포맷 call site가 **moment(59) + dayjs(5) + date-fns + 브라우저 Intl/toLocale**(225)로 4종 혼용
- 일부는 이미 `Intl.DateTimeFormat(undefined, ...)` (브라우저 기본 로케일) → 한국 브라우저 사용자 일부는 이미 한국어 날짜를 보는 불일치 상태
- i18n 진행 시 "로케일 따라 포맷" 정책으로 정리하면 UX가 좋아지지만, **3개 날짜 라이브러리 통일이 선행 리팩토링**이 되어 작업량이 증가. 최소한은 `toLocaleString` 계열은 그대로, `Intl.*`의 undefined 로케일을 명시적 로케일로 변경

### 6.4 테스트 (944개 파일)

- `getByText`/`findByText` 5,173 + `getByRole(name:)` 4,001 + `toHaveTextContent` 854 ≈ 1만개 어설션
- en 기본 로케일 + i18n provider 테스트 셋업만 있으면 **수정 불필요**. 위험은 "사람이 한국어 값으로 테스트를 고치게 되는" 장기적 파손. CI에 en fixture 무결성 체크(번역 PR이 en 값을 바꾸면 실패)를 둘 것
- `eslint-plugin-testing-library`가 이미 role/label 기반 어설션을 권장 중이라, 새 코드는 영향 적음

### 6.5 유지보수 세금

- 이후 모든 UI 코드 변경(업스트림 sync)에서 새 문자열 발생 → 키+ko 값 추가 필요. 누락 시 영어가 섞여 나감
- 대응: CI 게이트 (미등록 문자열 literal 탐지 하드, 또는 최소한 월간 `i18next-parser` diff 리뷰). 이 세금이 실질 비용의 대부분
- fork 운영 방식이라면: upstream rebase 시 `src/locales/` 외 코드가 계속 바뀌므로 **매 릴리스 재번역/재합병 노력** 발생 (기여자가 아니라 fork로 운영할 때)

### 6.6 기타

- `networking.tsx`(8,001줄 god file)에 문자열 1,538개 집중 → 이 파일 추출 작업이 병목. 추출과 동시에 최소한 error-message 모듈로 분리하는 것이 병목 해소에 유리
- a11y 속성(`aria-label` 1.9k)도 사용자가 스크린리더/툴팁으로 읽는 문자열이므로 포함 (검증 시 스크린리더 체크)
- `trailingSlash: true` + 서브경로 정적 서빙이라 정적 asset에 영향 없음 확인됨 (로케일 JSON만 asset으로 추가)

## 7. 노력량 추정

개발자 1명(React/TS 숙련) 기준 인일. 추출 속도는 1인당 800~1,200개 문자열 발생지(보간·조건부 텍스트 검토 포함)/인일 가정. god-file(networking.tsx)과 동적 템플릿이 많은 화면은 이보다 느림.

| 단계 | 내용 | 인일 |
|---|---|---:|
| **Phase 0** 인프라 | react-i18next 셋업, I18nextProvider(루트 레이아웃), 언어 스위처 UI, persist(Accept-Language 초기화), i18next-parser/키 컨벤션, 테스트 셋업(en provider), CI 게이트 | **3~5** |
| **Phase 1** 전역 + 핵심 페이지 | 전역 공유 네트워크/내비/프림이티브(약 4.4k) + api-keys, models-and-endpoints, usage, logs, teams, users, home, login/onboarding(약 10.4k), 합계 발생 횟수 약 14.8k | **12~18** |
| **Phase 2** 나머지 페이지 | Phase 1 대상(models-and-endpoints 등)을 제외한 playground ~2.9k, mcp-servers ~2.8k, guardrails ~2.7k, admin-panel ~1.6k, policies ~1.5k, model-hub-table ~1.1k + 나머지 ~25페이지(합산 발생 횟수 약 19k) | **14~20** |
| **번역 (ko)** | 23k 유니크 키. LLM/MT 초안 + native 리뷰 가정 (단문 UI 문자열 기준 3~4k 키/인일). 순수 수동이면 25~45 | **5~10** |
| **Phase 3** 마무리 | 날짜/통화 로케일 정책(최소), 백엔드 오류 병기 전략, KO E2E 스모크 40페이지, 레이아웃 깨짐(너비·truncation) 수정, a11y 속성 검토 | **5~8** |
| **합계 (전체 커버리지)** | | **39~61** |

해석:

- **1인 풀타임 기준 약 2~3 개월**. 2명이면 (Phase 1/2를 페이지별로 병렬) 1~1.5 개월. 번역은 개발과 병렬 가능 (키가 고정되는 대로)
- **Phase 0+1(고빈도 핵심, 사용자가 매일 여는 화면) + 비례 번역**: 18~28 인일, 대략 4~6 주
- `networking.tsx`(1,538 키) 1개 파일이 단독으로 2 인일 안팎을 먹는 등 **tail risk**가 공유 god-file에 있음. 추출과 함께 최소 리팩토링(error 모듈 분리)을 결합하면 전체 품질도 좋아짐
- **지속 유지보수**(개별 단가 아님, 상시): 이후 UI 개발의 10~20%가 "키 + ko 값"이 됨. fork 운영 시 추가로 매 upstream 릴리스 rebase·재합병 (Section 6.5)

## 8. 권장 로드맵

1. **사전 검증 (1~2 인일, 가장 먼저)** — Phase 0의 일부(인프라 + 언어 스위처)를 만들고 `login` 1페이지(약 55 키)만 번역, 실제 프록시에서 KO 전환 확인. 추출 1개 페이지의 실측 속도로 전체 추정 정밀화 + "이 패턴으로 진행할지" GO/NO-GO 결정
2. **Phase 0 완료 (3~5)**
3. **Phase 1 (2~4 주)** — 순서: login/onboarding → home → api-keys → models-and-endpoints → usage → logs → teams → users. 각 페이지마다 KO 스모크(주요 CRUD 1회전) 후 다음으로. 번역은 추출과 **병렬** (키 namespace가 고정되는 즉시)
4. **Go/No-Go 게이트** — Phase 1 완료 시 실제 사용자와 확인. (a) 핵심 시나리오가 커버됐으면 Phase 2 유예, (b) 나머지 페이지를 원하면 Phase 2 착수
5. **Phase 2 (2~3 주)** — playground가 동적 콘텐츠라 마지막까지 미루기 좋음 (번역 비중이 상대적으로 낮음)
6. **Phase 3 (1 주)** — KO에서 40페이지 E2E 스모크(Playwright, `tests/e2e/ui/` 스펙에 `?locale=ko` or cookie 시나리오 추가), 레이아웃 수정
7. **장기** — CI에서 미등록 문자열 literal을 탐지하는 게이트를 점진적으로 강화 (우선 warning, 다음 error)

우선순위 근거: 사용자가 실제로 매일 여는 화면 (키 관리, 모델, 비용, 로그)이 Phase 1에 모두 들어간다. playground·guardrails·mcp-servers는 고빈도 화면이지만 문자열 대비 "동적/영문 잔여"가 많아 i18n 체감 효과가 낮아 뒤로 미룸.

## 9. 대안 비교

| 항목 | A. 공식 UI i18n (Section 5 옵션 B) | B. ko-dashboard 확장 | C. 병행 (권장) |
|---|---|---|---|
| 초기 비용 | 39~61 인일 (핵심만 18~28) | 뷰 추가당 1~3 인일 (4뷰 이미 존재) | 18~28 인일 (A의 Phase 0+1) |
| 커버리지 | 41+13개 전체 가능 | 현재 4개 뷰 (개요/로그/비용/키) 수준 | Phase 1 범위(8~10개) |
| 기능 갭 | 없음 (본체) | **매 upstream 릴리스 갭 확대** (공식 UI는 playground/guardrails/mcp 등 활발히 변화 중) | Phase 1 화면은 갭 없음, 그 외 ko-dashboard 유지 |
| 유지보수 | 키+ko 지속 (10~20% 세금) + fork 시 rebase | 별도 코드베이스 상시 이중 유지 | A의 세금 |
| 품질/일관성 | 공식과 동일 UI | API 호출·인증 로직 재구현 (이미 재구현됨) | 공식 |
| upstream 기여 가능성 | issue #19730 연동, PR 기여 가능 (커뮤니티 수요 실재) | 불가 | 가능 |
| 적합 상황 | "전체 한글화"가 목표일 때 | 범위 고정 4~8개 뷰, 저예산 | "핵심 화면은 공식 UI로" 타협 가능할 때 |

**권고**:

- 만약 "40개 페이지 전체를 실제로 다 사용할" 계획이라면 **A의 단계별 실행(Section 8)**. 그 외에는 전체 번역의 체감 대비 비용이 맞지 않는다
- 만약 핵심이 "한국어로 쓰는 대시보드"뿐이라면 **C**: `ui/ko-dashboard`를 전이 기간에 유지하면서 공식 UI Phase 0+1(18~28 인일)을 실행하고, Phase 1 완료 시 해당 4개 뷰의 ko-dashboard를 내리고 사용자를 `/ui` KO로 유도. ko-dashboard는 그 이후에도 "경량 뷰"로 남거나 폐기
- B 단독(ko-dashboard만 계속 확장)은 기능 갭이 upstream 릴리스 속도로 벌어지기 때문에 **중장기 비추천**



