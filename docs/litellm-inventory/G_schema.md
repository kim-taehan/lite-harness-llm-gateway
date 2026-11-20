# G. DB 스키마 전수조사 (litellm Prisma)

## 요약
- 스키마 파일: `schema.prisma` 3벌(루트, `litellm/proxy/`, `litellm-proxy-extras/litellm_proxy_extras/`) 모두 2062줄, `diff` 결과 동일. 실제 배포 정본은 litellm-proxy-extras 쪽(마이그레이션 SQL 동봉), 나머지는 사본.
- datasource postgresql, generator prisma-client-py. 모델 **101개**, enum 1개(JobStatus: ACTIVE/INACTIVE, CronJob 용).
- 마이그레이션: `litellm-proxy-extras/litellm_proxy_extras/migrations/` 에 migration.sql **222개**(최신 20261016000000_background_interaction_settlement_settled_at_index), migration_lock.toml. 프록시 기동 시 `prisma migrate deploy`(기본, `--use_prisma_db_push` 면 db push). `DISABLE_SCHEMA_UPDATE` 로 끌 수 있음. SpendLogs 는 파티션 테이블이 될 수 있어 db push 를 막는 가드가 있음(prisma_client.py). 부분 인덱스·트리거 등은 schema.prisma 로 못 적고 migration SQL 에만 있음(ShadowEvalJob 주석).
- 군별 수: 인증·신원 19 / 예산·spend 13 / 모델·설정·운영 10 / 로그·감사 9 / 기타 50(MCP 6, 에이전트 4, 파일·벡터 6, 가드레일·정책·툴 7, 프롬프트·검색·스킬·플러그인·메모리 5, 라우터 7, ShadowEval 3, 워크플로 4, Lens 8)
- 공통 패턴: 거의 모든 엔티티가 `max_budget/spend/tpm_limit/rpm_limit/budget_duration/budget_reset_at/model_spend/model_max_budget/metadata Json/models String[]` 를 반복. 키·팀·유저·조직·프로젝트 5계층 + 별도 BudgetTable 공유.

## A. 인증·신원 (19)
| 테이블 | 용도 | 핵심 컬럼 | 쓰는 기능 | 새 스키마 대응 | 권장 |
|---|---|---|---|---|---|
| VerificationToken | 가상 키(virtual key) | token(해시 PK), key_alias, models[], spend/total_spend, expires, max_budget, budget_duration/reset_at, tpm/rpm/tpd, user_id/team_id/org/project/agent_id, permissions·metadata·aliases·router_settings Json, blocked, key_type, allowed_routes, rotation 6컬럼, last_active. 인덱스 (user_id,team_id),(team_id),(budget_reset_at,expires) | 키 인증, 키 CRUD, 예산 리셋, 키 로테이션, spend 누적 | 부분(api_key) | 필수. tpm/rpm·blocked·키 로테이션·budget_duration(주기 리셋) 은 api_key 에 없음 |
| JWTKeyMapping | JWT claim -> 가상 키 매핑 | jwt_issuer, claim_name/value, token(FK), is_active; unique(issuer,claim,value) | JWT 인증 | 없음 | 제외. SSO 는 세션으로 대체 |
| DeprecatedVerificationToken | 키 로테이션 유예기간의 옛 키 | token, active_token_id, revoke_at | 로테이션 grace | 없음 | 후순위. 로테이션을 하면 필요 |
| DeletedVerificationToken | 삭제 키 감사 사본(키 전 컬럼 복제 + deleted_by) | VerificationToken 복제, deleted_at/by | 키 삭제 이력 | 부분(deleted_at soft delete 로 대체) | 제외 |
| UserTable | 사용자/내부 유저 | user_id, user_email, sso_user_id unique, user_role, teams[], password, spend, max_budget, models[], tpm/rpm, metadata | 로그인, SSO upsert, 유저 CRUD | 있음(app_user) | 필수. password·budget·tpm 은 빠짐(SSO 전용이면 포기) |
| VerifiedSubject | IdP 주체(issuer,tenant,oid) -> user 검증 이력 | issuer, tenant_id, oid, kind, user_id, verified_via; unique(issuer,tenant,oid) | 에이전트/Entra 신원 검증 | 부분(app_user.sso_subject) | 제외 |
| SSOIdentityAssertion | SSO 로그인 때 IdP id_token/refresh 암호화 보관 | user_id PK, assertion_b64 | MCP 아웃바운드 OBO 토큰 교환 | 없음 | 제외(MCP 포기 시) |
| SSOConfig | UI 로 저장한 SSO 설정 | id="sso_config", sso_settings Json | SSO 설정 UI | 없음(env 설정) | 제외. Keycloak 은 env/yml |
| InvitationLink | 초대 링크 | user_id, is_accepted, expires_at, created_by | 유저 초대·온보딩 | 없음 | 제외. SSO upsert 로 대체 |
| OrganizationTable | 조직(팀 위 계층) | organization_id, alias, budget_id, models[], spend, model_spend, metadata | 멀티 조직 | 없음 | 제외(폐쇄망 단일 조직) |
| OrganizationMembership | 유저-조직 소속/역할/spend | user_id, organization_id, user_role, spend, budget_id | 조직 멤버 | 없음 | 제외 |
| TeamTable | 팀 | team_id, team_alias, members[]/admins[]/members_with_roles Json, models[], max/soft_budget, spend, tpm/rpm/tpd, budget_duration, blocked, model_aliases(ModelTable), router_settings, team_member_permissions, policies; 인덱스 org,alias,created_at | 팀 CRUD, 팀 예산·spend, 키 소속 | 있음(team) | 필수. 멤버를 배열/JSON 으로 둠(team_member 로 정규화됨). blocked·tpm/rpm 은 빠짐 |
| TeamMembership | 팀 내 유저별 spend·예산 | (user_id,team_id) PK, spend, budget_id | 팀 멤버별 예산 | 부분(team_member, 예산 없음) | 후순위 |
| DeletedTeamTable | 삭제 팀 사본 | TeamTable 복제 + deleted_at/by | 감사 | 없음 | 제외 |
| ProjectTable | 팀과 키 사이의 프로젝트 | project_id, team_id, budget_id, models[], spend, model_rpm/tpm_limit, blocked | 프로젝트 단위 관리 | 없음 | 제외 |
| EndUserTable | 요청 body 의 `user` 필드 최종 고객 예산/블록 | user_id, alias, spend, budget_id, models[], blocked, allowed_model_region | end-user 예산 | 없음 | 제외 |
| ObjectPermissionTable | MCP/벡터스토어/에이전트 접근 권한 묶음 | object_permission_id, mcp_servers[], vector_stores[], agents[] 등 | 키·팀·유저·조직 권한 | 없음 | 제외 |
| AccessGroupTable | 통합 access group(모델+MCP+에이전트) | access_group_id/name, access_model_names[], mcp 등 | 모델 접근 그룹 | 없음 | 후순위(모델 그룹 필요 시) |
| UserNotifications | 모델 접근 요청(Beta) | request_id, user_id, models[], justification, status | 접근 요청 승인 | 없음 | 제외 |

## B. 예산·spend (13)
| 테이블 | 용도 | 핵심 컬럼 | 쓰는 기능 | 새 스키마 대응 | 권장 |
|---|---|---|---|---|---|
| BudgetTable | 키/팀/조직/태그/멤버가 공유하는 예산·rate limit 템플릿 | budget_id, max/soft_budget, tpm/rpm/tpd, model_max_budget, budget_duration/reset_at, temp_budget_increase | 예산 강제, 주기 리셋 | 부분(각 테이블 max_budget 인라인) | 필수(개념). 별도 테이블은 불필요. budget_duration(월간 리셋)은 후순위 |
| ModelAccessGroupBudgetTable | 모델 그룹별 예산 | access_group_name, spend, budget_id | 그룹 예산 | 없음 | 제외 |
| TagTable | 태그별 예산/spend | tag_name, models[], spend, budget_id | 태그 예산 | 없음 | 제외 |
| BudgetWindowSpend | 슬라이딩 윈도 예산 누적 | entity_type, entity_id, window_duration, window_start, spend | 롤링 예산 | 없음 | 제외 |
| DailyUserSpend | 일별 유저·키·모델별 사용량 롤업 | date(String), user_id, api_key, model, provider, endpoint, tokens(캐시 포함), spend, api_requests, success/failed, response_time; unique 복합 | 사용량 대시보드, 리포트 | 없음(request_log 에서 집계) | 후순위. request_log GROUP BY 로 충분하나 로그 크면 롤업 필요 |
| DailyGlobalSpend | key 없는 전체 일별 롤업 | DailyUserSpend 유사 | 글로벌 사용량 뷰 | 없음 | 제외 |
| DailyOrganizationSpend | 조직별 일별 | 동일 | 조직 사용량 | 없음 | 제외 |
| DailyEndUserSpend | end-user 별 일별 | 동일 | end-user 사용량 | 없음 | 제외 |
| DailyAgentSpend | 에이전트별 일별 | 동일 | 에이전트 사용량 | 없음 | 제외 |
| DailyTeamSpend | 팀별 일별 | team_id,date,api_key,model... | 팀 사용량 | 없음 | 후순위(팀 대시보드용) |
| DailyTagSpend | 태그별 일별 | tag,date,... | 태그 사용량 | 없음 | 제외 |
| DailyModelUsage | 모델 사용량 일별 | (스키마 확인만, 세부 컬럼 확인 못함) | 모델 사용량 뷰 | 없음 | 제외 |
| DailyToolSpend | 툴별 일별 | tool, date | Cost Optimization 카드 | 없음 | 제외 |

## C. 모델·설정·운영 (10)
| 테이블 | 용도 | 핵심 컬럼 | 쓰는 기능 | 새 스키마 대응 | 권장 |
|---|---|---|---|---|---|
| ProxyModelTable | DB 에 저장된 모델 배포(UI 로 추가) | model_id, model_name, litellm_params Json(model, api_base, api_key...), model_info Json, blocked | 모델 CRUD, 라우터 로드 | 있음(model_deployment) | 필수. litellm_params JSON 을 base_url/model_id/api_key 컬럼으로 정규화 |
| CredentialsTable | 재사용 가능한 자격증명 | credential_name unique, credential_values Json(암호화), credential_info | 모델이 credential 참조 | 부분(deployment.api_key 인라인) | 제외 |
| ModelTable | 팀 모델 별칭 | id Int, aliases Json | 팀별 model alias | 없음 | 제외 |
| Config | config.yaml 내용 저장 | param_name, param_value Json, reload_revision | 설정 DB 저장·핫리로드 | 없음 | 제외(env/yml) |
| ConfigOverrides | 설정 유형별 override | config_type, config_value Json | 설정 UI | 없음 | 제외 |
| CacheConfig | 캐시 설정 | cache_settings Json | 캐시 UI | 없음 | 제외 |
| UISettings | 대시보드 설정 | ui_settings Json | UI 설정 | 없음 | 제외 |
| HealthCheckTable | 모델 헬스체크 결과 | (세부 확인 못함) | 모델 상태 | 없음 | 후순위 |
| ProxyWorkerHeartbeat | 워커별 heartbeat | worker_id, hostname, last_heartbeat_at | 멀티워커 경고 숨김 | 없음 | 제외 |
| CronJob | 파드 리더 선출 lease | pod_id, status(JobStatus), ttl | 예산 리셋 등 크론 단일 실행 | 없음 | 후순위. 다중 인스턴스면 필요(ShedLock 류) |

## D. 로그·감사 (9)
| 테이블 | 용도 | 핵심 컬럼 | 쓰는 기능 | 새 스키마 대응 | 권장 |
|---|---|---|---|---|---|
| SpendLogs | 요청별 로그(+ 파티션 가능) | request_id PK, api_key(해시), spend, tokens, startTime/endTime, request_duration_ms, model, model_group, provider, api_base, user, team_id, org, end_user, messages/response/proxy_server_request Json, session_id, status, request_tags, requester_ip; 인덱스 startTime, (startTime,request_id), end_user, session_id, litellm_call_id, (api_key,startTime) | spend 기록, 로그 UI, 일별 롤업 원천 | 있음(request_log) | 필수. messages/response 본문 저장(옵션)·session_id·ip 는 빠짐. 폐쇄망 감사에 본문이 필요한지 결정 사항 |
| ErrorLogs | 실패 요청 | request_id, model_group, exception_type/string, status_code, request_kwargs Json | 에러 로그 | 부분(request_log.status/error) | 제외 |
| AuditLog | 관리 변경 감사(누가 무엇을) | changed_by, action, table_name, object_id, before_value, updated_values | 관리자 감사 | 없음 | 후순위. 폐쇄망 보안감사에 요구될 가능성 큼 |
| DailyGatewayRequests | 엣지 미들웨어의 일별 요청 수(SGR 과금용) | day, category, route 등 | SGR 계량 | 없음 | 제외 |
| DailyGatewayFailedRequests | 실패 요청 수 | 동일 | 계량 | 없음 | 제외 |
| DailyRequestErrors | 일별 에러 집계 | 확인 못함(세부) | 에러 통계 | 없음 | 제외 |
| SpendLogGuardrailIndex | 가드레일별 최근 로그 인덱스 | request_id, guardrail_id | 가드레일 로그 | 없음 | 제외 |
| SpendLogToolIndex | 툴별 최근 로그 인덱스 | request_id, tool | 툴 로그 | 없음 | 제외 |
| TelemetryReport | 프록시 텔레메트리 리포트(window, report Json) | window_start/end, report | litellm/proxy/telemetry/{store,endpoints}.py | 없음 | 제외. 폐쇄망 원칙(telemetry 0)과 충돌 소지, 외부 전송 여부는 확인 못함 |

## E. 기타 (50) - 새 스키마에 전부 없음, 대부분 제외
| 테이블 | 용도 | 권장 |
|---|---|---|
| MCPServerTable | MCP 서버 등록(url, transport, auth, 헤더 등) | 제외(MCP 포기) 또는 후순위 |
| MCPToolsetTable | MCP 툴 묶음 | 제외 |
| MCPUserCredentials | 유저별 MCP BYOK 자격 | 제외 |
| MCPUserEnvVars | 유저별 MCP 환경변수(암호화) | 제외 |
| MCPServerOAuthClient | MCP OAuth 클라이언트 | 제외 |
| UserProviderCredentials | 유저별 프로바이더 연결(Copilot OAuth 등) | 제외 |
| AgentsTable | A2A 에이전트 등록(agent_card_params, spend, tpm/rpm) | 제외 |
| AgentIdentity | 에이전트 IdP 바인딩(tenant, client_id) | 제외 |
| RetiredAgentIdentity / RetiredAgent | 폐기된 에이전트 신원 | 제외 |
| ManagedFileTable | 통합 파일 API(file_object, model_mappings, storage_url) | 제외(files API 범위 밖이면) |
| ManagedObjectTable | 배치/파인튜닝 job 객체 | 제외 |
| ManagedFileContentTable | 파일 본문 | 제외 |
| ManagedVectorStoreTable / ManagedVectorStoresTable / ManagedVectorStoreIndexTable | 벡터스토어/인덱스 | 제외 |
| GuardrailsTable | 가드레일 설정 | 제외(후순위: 폐쇄망 PII 필터 필요 시) |
| DailyGuardrailMetrics / DailyGuardrailUsageUnits / DailyPolicyMetrics | 가드레일·정책 일별 지표 | 제외 |
| PolicyTable / PolicyAttachmentTable | 버전 있는 가드레일 정책, 적용 대상 | 제외 |
| ToolTable | 응답에서 자동 발견된 툴 레지스트리·정책 | 제외 |
| PromptTable | 프롬프트 템플릿 | 제외 |
| SearchToolsTable | 검색 툴 설정 | 제외(외부 검색은 폐쇄망 원칙과 충돌) |
| SkillsTable | litellm 관리 스킬 | 제외 |
| ClaudeCodePluginTable | Claude Code 플러그인 마켓(plugins-repo 와 중복 소지) | 제외, 플러그인 카탈로그는 별도 저장소가 담당 |
| MemoryTable | 사용자/팀 메모리 key-value | 제외 |
| AdaptiveRouterState / AdaptiveRouterSession | 적응형 라우터(베타 분포) 상태 | 제외 |
| AutoRouterBaselineComparison / BaselineObservation / Session / UserSession / DailySpend | 오토라우터 세션·절감 비교 | 제외 |
| ShadowEvalJob / Attempt / Funnel | 라우터 섀도 평가(LLM 심판) | 제외 |
| WorkflowRun / WorkflowEvent / WorkflowMessage | 에이전트 워크플로 상태 추적(session_id 로 spend 연결) | 제외 |
| BackgroundInteractionSettlement | 백그라운드 interaction 과금 정산(claimed_at 로 exactly-once) | 제외 |
| Lens / LensRun / LensReview / LensWorker / LensIngestionKey / LensDataset / LensSignalConfig / LensTraceSignal | 트레이스 품질 평가(Lens) 기능, 대부분 data Json | 제외 |

## 새 스키마 7개 대비 정리
- 대응: app_user=UserTable(부분), api_key=VerificationToken(부분), team=TeamTable(부분), team_member=TeamMembership + TeamTable.members(부분), model_deployment=ProxyModelTable, request_log=SpendLogs(부분), sso_session=**대응 없음**(litellm 은 서버 세션 테이블이 없고 UI 로그인은 JWT 쿠키).
- 새 스키마에 없는 중요한 것: ① 키/팀/유저 tpm·rpm 한도(`rate limit`), ② budget_duration 월간·일간 주기 리셋(budget_reset_at) 과 CronJob, ③ 키 blocked/팀 blocked, ④ 키 로테이션·유예(Deprecated 테이블), ⑤ AuditLog(관리 감사), ⑥ 일별 롤업 Daily*Spend(usage 대시보드), ⑦ 조직·프로젝트 계층, ⑧ SpendLogs 본문(messages/response)·session_id·요청 IP, ⑨ 모델 alias/access group.
- 새 스키마에만 있는 것: sso_session(refresh 해시 rotation 재사용 감지) - litellm 에 대응 없음, 신규 설계.
- 포기 확정 기능(테이블 기준): MCP, A2A 에이전트, Files/Batch/Vector Store, 가드레일·정책, 프롬프트/스킬/메모리, 오토라우터·ShadowEval, 워크플로, Lens, 조직/프로젝트/end-user, JWT 인증, 초대 링크, 태그 예산. 판단 필요: AuditLog, rate limit, 주기 예산 리셋, Daily 롤업.

## 놀라운 발견
- 101개 중 인증·예산·모델·로그 핵심은 약 15개 뿐이고, 나머지 80여 개는 최근 추가된 부가 기능(오토라우터·ShadowEval·Lens·Workflow·MCP BYOK 등). 새 7개 스키마는 핵심 면에서는 타당하나 rate limit·주기 리셋·감사가 누락.
- Team 멤버가 TeamTable 안에 String[]/JSON(members, admins, members_with_roles)으로 중복 저장됨, TeamMembership 은 예산 용도. 이관 불필요(데이터 이전 없음)하므로 정규화 설계가 옳음.
- Float 로 돈을 저장(spend Float). 새 설계의 numeric(12,6) 이 더 정확.
- 삭제 사본 테이블이 키/팀 전 컬럼을 복제(유지 부담). 새 설계는 soft delete.
- TelemetryReport 테이블 존재, 폐쇄망 원칙과 대조 필요(전송 여부 확인 못함).
- schema.prisma 가 3곳에 동일 사본, 편집 시 동기화 필요(upstream 규칙).
