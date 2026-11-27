package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"llm-gateway/internal/core"
)

// 만들지 않는 기능의 엔드포인트(M 문서 STUB). 수정하지 않은 대시보드의 모든 화면이 깨지지 않도록,
// 대시보드가 부르는 경로마다 화면이 견디는 응답(모양을 다 갖춘 빈 값 또는 JSON 오류)을 준다(설계 9장).
// 반쯤 찬 200 은 렌더 예외를 일으키므로 주지 않는다(M 2.2). 오류 본문도 JSON 이어야
// 대시보드가 response.json() 으로 메시지를 읽는다(M 0장)

// stub 은 고정 응답 하나다. feature 가 있으면 body 대신 {"detail":{"error":"<feature> is not supported…"}} 를 준다
type stub struct {
	pattern  string // "METHOD /path"
	isPublic bool
	status   int
	body     json.RawMessage
	feature  string
}

// stubOK 는 화면이 그대로 읽는 빈 값을 200 으로 준다
func stubOK(pattern, body string) stub { return stubReply(http.StatusOK, pattern, body) }

// stubReply 는 대시보드가 상태 코드와 상관없이 본문을 읽는 곳에 정해진 본문을 준다
func stubReply(status int, pattern, body string) stub {
	return stub{pattern: pattern, status: status, body: json.RawMessage(body)}
}

// stubNotFound 는 JSON 404 다. 데이터가 undefined 면 버티는 화면이 많아서 모양을 모르는 GET 은 이것으로 답한다
func stubNotFound(feature, pattern string) stub {
	return stub{pattern: pattern, status: http.StatusNotFound, feature: feature}
}

// stubUnsupported 는 없는 기능의 쓰기에 주는 JSON 501 이다
func stubUnsupported(feature, pattern string) stub {
	return stub{pattern: pattern, status: http.StatusNotImplemented, feature: feature}
}

// public 은 대시보드가 인증 헤더 없이 부르는 경로다
func (s stub) public() stub {
	s.isPublic = true
	return s
}

func (s stub) route() core.Route {
	method, path, _ := strings.Cut(s.pattern, " ")
	access := core.Authenticated()
	if s.isPublic {
		access = core.Public()
	}
	return core.Route{Method: method, Path: path, Access: access, Handler: s.handler()}
}

func (s stub) handler() http.HandlerFunc {
	if s.feature != "" {
		e := detailError(s.status, s.feature+" is not supported by this gateway")
		return func(w http.ResponseWriter, _ *http.Request) { e.Write(w) }
	}
	return func(w http.ResponseWriter, _ *http.Request) { core.WriteJSON(w, s.status, s.body) }
}

// stubRoutes 는 빈 응답 라우트다
func (h *Handlers) stubRoutes() []core.Route {
	var routes []core.Route
	for _, s := range stubs() {
		routes = append(routes, s.route())
	}
	return routes
}

// dailyActivityStubs 는 우리에게 없는 단위(태그·조직·고객·에이전트)의 사용량 묶음이다. 모양은 user·team 의 빈 응답과 같다
func dailyActivityStubs(entity string) []stub {
	base := "GET /" + entity + "/daily/activity"
	return []stub{
		stubOK(base+"/aggregated", `{"results":[],"metadata":{}}`),
		stubOK(base+"/aggregated/keys", `{"api_keys":[],"total_api_keys":0,"offset":0,"limit":50}`),
		stubOK(base+"/aggregated/search", `{"api_keys":[]}`),
		stubOK(base+"/aggregated/model_top_keys", `{"model":"","by_model_group":true,"api_keys":[]}`),
		stubNotFound("Daily activity export", base+"/export"),
	}
}

func stubs() []stub {
	version, _ := json.Marshal(core.Version) // 문자열 마샬은 실패하지 않는다
	const (
		budgets    = "Budgets"
		mcp        = "MCP"
		agents     = "Agents"
		guardrails = "Guardrails"
		policies   = "Policies"
		lens       = "Lens"
		credential = "Credentials"
		autoRouter = "Auto router"
	)
	list := []stub{
		// 부팅·레이아웃: 모든 화면에서 먼저 불린다. 알림 종·배너·사이드바 카드는 404 면 숨는다
		stubOK("GET /api/plugins", `[]`),
		stubNotFound("Plugins", "GET /api/plugins/auth-token"),
		stubOK("GET /health/readiness/details", `{"status":"healthy","litellm_version":`+string(version)+`}`),
		stubNotFound("Release notes", "GET /get/latest_release_info").public(),
		stubOK("GET /public/litellm_blog_posts", `{"posts":[]}`).public(),
		stubOK("GET /health/deployment_warnings", `{"warnings":[]}`),
		stubUnsupported("Deployment warnings", "PATCH /health/deployment_warnings/{warning_id}"),
		stubOK("GET /get/user_banner", `{"enabled":false,"message":"","severity":"info","revision":""}`),
		stubUnsupported("User banner", "PATCH /update/user_banner"),
		stubNotFound("License", "GET /health/license"),
		stubNotFound("License", "GET /user/available_users"),
		// 폐쇄망이라 텔레메트리는 늘 꺼져 있다(vetoed). 모양을 다 채워야 텔레메트리 화면이 그려진다({} 면 모든 화면 위 배너가 깨진다).
		// environment_variables 를 비워 두면 모든 화면 위 배너는 나오지 않는다
		stubOK("GET /telemetry/settings", `{"available_destinations":[],"destination":null,"destination_set_by_environment":true,`+
			`"editable":false,"environment_variables":[],"flush_interval_seconds":0,"groups":[],"report":null,"report_is_sample":false,`+
			`"retention_days":0,"set_by_environment":true,"stored_destination":null,"stored_groups":null,"vetoed":true}`),
		stubUnsupported("Telemetry", "PUT /telemetry/settings"),
		stubOK("GET /telemetry/ui_events/enabled", `{"enabled":false}`).public(),
		stubUnsupported("Telemetry", "POST /telemetry/ui_events"),
		stubOK("GET /openapi.json", `{"components":{"schemas":{"GenerateKeyRequest":{"properties":{}}}}}`).public(),

		// 로그인: 컨트롤 플레인 워커(workers:[] 라 불리지 않는다)
		stubUnsupported("Control plane login", "POST /v3/login").public(),
		stubUnsupported("Control plane login", "POST /v3/login/exchange").public(),

		// home: LiteAdmin 도구. 실패는 모델에 문자열로 돌아간다
		stubNotFound("Spend reports", "GET /global/spend/report"),
		stubNotFound("Spend reports", "GET /team/spend/report"),
		stubNotFound("Spend reports", "GET /key/spend/report"),

		// 키·팀·사용자 폼의 드롭다운. .map·.find 로 읽는 것은 맨 배열이어야 한다
		stubOK("GET /budget/list", `[]`),
		stubOK("POST /budget/info", `[]`),
		stubOK("GET /project/list", `[]`),
		stubOK("GET /prompts/list", `{"prompts":[]}`),
		stubOK("GET /organization/list", `[]`),
		stubNotFound("Organizations", "GET /organization/info"),
		stubNotFound(policies, "GET /policy/info/{policy_name}"),
		stubOK("GET /policies/list", `{"policies":[]}`),
		stubOK("GET /v2/guardrails/list", `{"guardrails":[]}`),
		stubOK("GET /router/settings", `{"fields":[],"routing_strategy_descriptions":{}}`),
		stubOK("GET /config/pass_through_endpoint", `{"endpoints":[]}`),
		stubOK("GET /config/pass_through_endpoint/team/{team_id}", `{"endpoints":[]}`),
		stubUnsupported("Pass-through endpoints", "POST /config/pass_through_endpoint"),
		stubUnsupported("Pass-through endpoints", "DELETE /config/pass_through_endpoint"),
		stubUnsupported("Pass-through endpoints", "POST /config/pass_through_endpoint/{endpoint_path}"),
		stubOK("GET /v1/access_group", `[]`),
		stubUnsupported("Access groups", "POST /v1/access_group"),
		stubOK("GET /v1/mcp/toolset", `[]`),
		stubOK("GET /v1/mcp/access_groups", `{"access_groups":[]}`),
		stubOK("GET /mcp-rest/tools/list", `{"tools":[]}`),
		stubOK("GET /v1/agents", `[]`),
		stubUnsupported(agents, "POST /v1/agents"),
		stubOK("GET /vector_store/list", `{"data":[]}`),
		stubOK("GET /search_tools/list", `{"search_tools":[]}`),
		stubOK("GET /claude-code/plugins", `{"plugins":[],"count":0}`),
		stubUnsupported("Skills", "POST /claude-code/plugins"),

		// budgets: meta 가 없으면 목록 훅이 data?.meta.total_count 에서 깨진다
		stubOK("GET /management/v1/budgets", `{"data":[],"links":{"first":"","last":""},"meta":{"page":1,"page_size":50,"total_count":0,"total_pages":0}}`),
		stubUnsupported(budgets, "POST /budget/new"),
		stubUnsupported(budgets, "POST /budget/update"),
		stubUnsupported(budgets, "POST /budget/delete"),

		// models-and-endpoints: 자격 증명·헬스·비용 맵·접근 그룹 예산
		stubOK("GET /model/{model_id}/kubernetes_pods", `{"service_host":"","port":null,"pod_ips":[],"error":null}`),
		stubOK("GET /credentials", `{"credentials":[]}`),
		stubUnsupported(credential, "POST /credentials"),
		stubUnsupported(credential, "PATCH /credentials/{credential_name}"),
		stubUnsupported(credential, "DELETE /credentials/{credential_name}"),
		stubOK("GET /credentials/by_model/{model_id}", `{}`),
		stubOK("GET /credentials/user_connections", `{"connections":[]}`),
		stubUnsupported(credential, "POST /credentials/{credential_name}/user_connection/start"),
		stubUnsupported(credential, "POST /credentials/{credential_name}/user_connection/poll"),
		stubUnsupported(credential, "DELETE /credentials/{credential_name}/user_connection"),
		stubOK("GET /health/latest", `{"latest_health_checks":{}}`),
		// {} 면 model_count.toLocaleString() 에서 깨진다. 404 면 카드를 숨긴다
		stubNotFound("Model cost map source", "GET /model/cost_map/source"),
		stubOK("GET /schedule/model_cost_map_reload/status", `{"scheduled":false,"interval_hours":null,"last_run":null,"next_run":null}`),
		// 상태 코드를 보지 않고 본문의 status 로 성공을 가른다
		stubReply(http.StatusNotImplemented, "POST /reload/model_cost_map", `{"status":"error","message":"Model cost map reload is not supported by this gateway"}`),
		stubReply(http.StatusNotImplemented, "POST /schedule/model_cost_map_reload", `{"status":"error","message":"Model cost map reload is not supported by this gateway"}`),
		stubReply(http.StatusNotImplemented, "DELETE /schedule/model_cost_map_reload", `{"status":"error","message":"Model cost map reload is not supported by this gateway"}`),
		stubUnsupported("Access group budgets", "PUT /access_group/{access_group}/budget"),
		stubUnsupported("Access group budgets", "DELETE /access_group/{access_group}/budget"),

		// 자동 라우터: 200 인데 allowances 가 없으면 .find 에서 깨진다
		stubOK("POST /auto_router/availability", `{"allowances":[],"error":null}`),
		stubOK("GET /auto_router/classifier/default_prompt", `{"system_prompt":""}`),
		stubUnsupported(autoRouter, "POST /auto_router/classifier/default_prompt"),
		stubUnsupported(autoRouter, "POST /auto_router/test_routing"),
		stubUnsupported(autoRouter, "POST /auto_router/validate_complexity_router_config"),
		stubOK("GET /public/autorouter_presets", `{}`).public(),
		stubOK("GET /public/complexity_router/scorer_defaults", `{"tier_boundaries":{},"token_thresholds":{},"dimension_weights":{}}`).public(),
		stubOK("GET /public/complexity_router/fuse_presets", `{"version":"","harnesses":[],"models":[]}`).public(),

		// model-hub-table: 에이전트·MCP·스킬 허브
		stubOK("GET /config/field/info", `{"field_name":"enable_public_model_hub","field_value":false}`),
		stubOK("GET /public/model_hub", `[]`).public(),
		stubOK("GET /public/agent_hub", `[]`).public(),
		stubOK("GET /public/mcp_hub", `[]`).public(),
		stubOK("GET /public/skill_hub", `{"plugins":[]}`).public(),
		stubUnsupported(agents, "POST /v1/agents/make_public"),
		stubUnsupported(mcp, "POST /v1/mcp/make_public"),
		stubUnsupported("Skills", "POST /claude-code/plugins/{plugin_name}/enable"),
		stubUnsupported("Skills", "POST /claude-code/plugins/{plugin_name}/disable"),
		stubUnsupported("Skills", "DELETE /claude-code/plugins/{plugin_name}"),
		stubOK("GET /guardrails/ui/add_guardrail_settings", `{"supported_entities":[],"supported_actions":[],"pii_entity_categories":[],"decision_model_providers":[],"supported_modes_by_provider":{},"content_filter_settings":{"content_categories":[]}}`),

		// usage: 부가 지표. 일부 필드만 있는 200 은 렌더 예외라 전체 모양이나 404 로 답한다
		stubNotFound("Gateway activity", "GET /gateway/daily/activity"),
		stubOK("GET /gateway/errors/activity", `{"total_successful_requests":0,"total_failed_requests":0,"by_date":[],"by_status_code":[],"by_key":[],"by_team":[],"by_user":[],"by_model":[]}`),
		stubOK("GET /customer/list", `[]`),
		stubOK("GET /tag/list", `{}`),
		stubOK("GET /tag/summary", `{"results":[]}`),
		stubOK("GET /tag/distinct", `{"results":[]}`),
		stubOK("GET /tag/dau", `{"results":[]}`),
		stubOK("GET /tag/wau", `{"results":[]}`),
		stubOK("GET /tag/mau", `{"results":[]}`),
		stubOK("GET /tag/user-agent/per-user-analytics", `{"results":[],"total_count":0}`),
		stubUnsupported("Usage AI chat", "POST /usage/ai/chat"),
		stubOK("GET /cloudzero/settings", `{}`),
		stubUnsupported("CloudZero", "POST /cloudzero/init"),
		stubUnsupported("CloudZero", "PUT /cloudzero/settings"),
		stubUnsupported("CloudZero", "POST /cloudzero/export"),
		stubUnsupported("CloudZero", "POST /cloudzero/dry-run"),
		stubUnsupported("CloudZero", "DELETE /cloudzero/delete"),

		// logs: 규정 준수(감사 로그는 auditlog.go)
		stubUnsupported("Compliance checks", "POST /compliance/eu-ai-act"),
		stubUnsupported("Compliance checks", "POST /compliance/gdpr"),

		// old-usage: 렌더에서 바로 .map 하는 것은 맨 배열
		stubOK("GET /global/spend/logs", `[]`),
		stubOK("GET /global/spend/keys", `[]`),
		stubOK("GET /global/spend/models", `[]`),
		stubOK("GET /global/spend/provider", `[]`),
		stubOK("GET /global/activity", `{"daily_data":[],"sum_api_requests":0,"sum_total_tokens":0}`),
		stubOK("GET /global/activity/model", `[]`),
		stubOK("GET /global/spend/teams", `{"daily_spend":[],"teams":[],"total_spend_per_team":[]}`),
		stubOK("GET /global/spend/all_tag_names", `{"tag_names":[]}`),
		stubOK("GET /global/spend/tags", `{"spend_per_tag":[]}`),
		stubOK("POST /global/spend/end_users", `[]`),

		// cost-optimization
		stubOK("GET /user/daily/activity/aggregated/cache_leakage_keys", `{"api_keys":[]}`),
		stubOK("GET /cost_optimization/prompt_caching/requests", `{"requests":[],"has_more":false,"next_cursor":null}`),
		stubOK("GET /v1/tool/spend", `{"by_tool":[],"daily":[],"start_date":null,"end_date":null}`),
		stubNotFound(autoRouter, "GET /auto_router/benchmarks"),
		stubOK("GET /auto_router/shadow_eval", `[]`),
		stubNotFound(autoRouter, "GET /auto_router/shadow_eval/{job_id}"),
		stubUnsupported(autoRouter, "POST /auto_router/shadow_eval/start"),
		stubUnsupported(autoRouter, "POST /auto_router/shadow_eval/{job_id}/stop"),

		// caching(사용량 탭): 캐시가 없어 늘 0 이다. filter_options 까지 다 채운 빈 모양이어야 깨지지 않는다
		stubOK("GET /global/activity/cache_hits", `{"error_breakdown":[],"filter_options":{"key_aliases":[],"models":[]},"groups":[],`+
			`"totals":{"api_requests":0,"cache_hit_ratio":0,"cache_hits":0,"cached_completion_tokens":0,"failed_requests":0}}`),

		// guardrails-monitor·tool-policies
		stubOK("GET /guardrails/usage/overview", `{"rows":[],"chart":[],"totalRequests":0,"totalBlocked":0,"passRate":0,"totalCost":null,"totalUntrackedUsageUnits":0}`),
		stubNotFound(guardrails, "GET /guardrails/usage/detail/{guardrail_id}"),
		stubOK("GET /guardrails/usage/logs", `{"logs":[],"total":0}`),
		stubOK("GET /v1/tool/list", `{"tools":[]}`),
		stubOK("GET /v1/tool/policy/options", `{"input_policies":[],"output_policies":[]}`),
		stubNotFound("Tool policies", "GET /v1/tool/{tool_name}/detail"),
		stubOK("GET /v1/tool/{tool_name}/logs", `{"logs":[],"total":0}`),
		stubUnsupported("Tool policies", "POST /v1/tool/policy"),
		stubUnsupported("Tool policies", "DELETE /v1/tool/{tool_name}/overrides"),

		// tag-management: 태그 목록은 이름→태그 사전이라 객체다
		stubUnsupported("Tags", "POST /tag/new"),
		stubUnsupported("Tags", "POST /tag/update"),
		stubOK("POST /tag/info", `{}`),
		stubUnsupported("Tags", "POST /tag/delete"),

		// playground: 우리에게 없는 추론 종류
		stubUnsupported("Anthropic messages", "POST /v1/messages"),
		stubUnsupported("Image generation", "POST /images/generations"),
		stubUnsupported("Image editing", "POST /images/edits"),
		stubUnsupported("Speech", "POST /audio/speech"),
		stubUnsupported("Transcription", "POST /audio/transcriptions"),
		stubUnsupported("Interactions", "POST /v1beta/interactions"),
		stubUnsupported("Decision models", "POST /v1/systemone"),
		stubUnsupported("Decision models", "POST /v1/decisions"),
		stubUnsupported(agents, "POST /a2a/{agent_id}"),
		stubUnsupported(agents, "POST /a2a/{agent_id}/message/send"),
		stubUnsupported(mcp, "POST /mcp-rest/tools/call"),
		stubOK("POST /utils/test_policies_and_guardrails", `{"guardrail_errors":[],"results":[]}`),
		stubNotFound("Containers", "GET /v1/containers/{container_id}/files/{file_id}/content"),

		// mcp-servers: 목록·상태는 맨 배열, 탭 요약은 모든 필드를 채운다
		stubUnsupported(mcp, "POST /v1/mcp/server"),
		stubUnsupported(mcp, "PUT /v1/mcp/server"),
		stubUnsupported(mcp, "POST /v1/mcp/toolset"),
		stubUnsupported(mcp, "PUT /v1/mcp/toolset"),
		stubOK("GET /v1/mcp/server/health", `[]`),
		stubOK("GET /v1/mcp/user-env-vars/status", `[]`),
		stubOK("GET /v1/mcp/user-credentials", `[]`),
		stubOK("GET /get/mcp_semantic_filter_settings", `{"field_schema":{},"values":{}}`),
		stubUnsupported(mcp, "PATCH /update/mcp_semantic_filter_settings"),
		stubOK("GET /get/mcp_tool_search_settings", `{"values":{}}`),
		stubUnsupported(mcp, "PATCH /update/mcp_tool_search_settings"),
		stubReply(http.StatusNotImplemented, "POST /mcp-rest/test/tools/list", `{"tools":[],"error":"not supported","message":"MCP is not supported by this gateway"}`),
		stubOK("GET /v1/mcp/openapi-registry", `{"apis":[]}`),
		stubOK("GET /v1/mcp/discover", `{"servers":[],"categories":[]}`),
		stubOK("GET /v1/mcp/network/client-ip", `{"ip":null}`),
		stubUnsupported(mcp, "POST /v1/mcp/server/import"),
		stubUnsupported(mcp, "POST /v1/mcp/server/register"),
		stubOK("GET /v1/mcp/server/submissions", `{"total":0,"pending_review":0,"active":0,"rejected":0,"items":[]}`),
		stubUnsupported(mcp, "PUT /v1/mcp/server/{server_id}/approve"),
		stubUnsupported(mcp, "PUT /v1/mcp/server/{server_id}/reject"),
		stubUnsupported(mcp, "DELETE /v1/mcp/server/{server_id}"),
		stubUnsupported(mcp, "DELETE /v1/mcp/toolset/{toolset_id}"),
		stubOK("GET /v1/mcp/sessions", `{"total_sessions":0,"worker_pid":0,"by_client":[],"by_user":[],"sessions":[]}`),
		stubUnsupported(mcp, "DELETE /v1/mcp/sessions"),
		stubOK("GET /v1/mcp/server/{server_id}/user-credentials", `[]`),
		stubUnsupported(mcp, "POST /v1/mcp/server/{server_id}/user-credential"),
		stubUnsupported(mcp, "DELETE /v1/mcp/server/{server_id}/user-credential"),
		stubOK("GET /v1/mcp/server/{server_id}/user-env-vars", `{"server_id":"","required":[],"missing_count":0}`),
		stubUnsupported(mcp, "POST /v1/mcp/server/{server_id}/user-env-vars"),
		stubUnsupported(mcp, "DELETE /v1/mcp/server/{server_id}/user-env-vars"),
		stubNotFound(mcp, "GET /v1/mcp/server/{server_id}/oauth-user-credential/status"),
		stubUnsupported(mcp, "POST /v1/mcp/server/{server_id}/oauth-user-credential"),
		stubUnsupported(mcp, "DELETE /v1/mcp/server/{server_id}/oauth-user-credential"),
		stubUnsupported(mcp, "POST /v1/mcp/server/oauth/session"),
		stubUnsupported(mcp, "POST /v1/mcp/server/oauth/{server_id}/register"),
		stubUnsupported(mcp, "GET /v1/mcp/server/oauth/{server_id}/authorize"),
		stubUnsupported(mcp, "POST /v1/mcp/server/oauth/{server_id}/token"),

		// agents: 목록이 비어 상세에는 닿지 않는다
		stubOK("GET /public/agents/fields", `[]`).public(),
		stubNotFound(agents, "GET /v1/agents/{agent_id}"),
		stubUnsupported(agents, "PATCH /v1/agents/{agent_id}"),
		stubUnsupported(agents, "DELETE /v1/agents/{agent_id}"),
		stubUnsupported(agents, "POST /v1/agents/{agent_id}/kill_switch"),
		stubNotFound(agents, "GET /v1/agents/{agent_id}/identity"),
		stubOK("GET /v1/agents/identity/providers", `[]`),
		stubUnsupported(agents, "POST /v1/a2a/discover"),

		// vector-stores·search-tools·memory·prompts·workflows
		stubOK("GET /v1/indexes", `{"data":[]}`),
		stubUnsupported("Vector stores", "POST /vector_store/new"),
		stubNotFound("Vector stores", "POST /vector_store/info"),
		stubUnsupported("Vector stores", "POST /vector_store/update"),
		stubUnsupported("Vector stores", "POST /vector_store/delete"),
		stubUnsupported("Vector stores", "POST /rag/ingest"),
		stubUnsupported("Vector stores", "POST /v1/vector_stores/{vector_store_id}/search"),
		stubOK("GET /search_tools/ui/available_providers", `{"providers":[]}`),
		stubUnsupported("Search tools", "POST /search_tools"),
		stubUnsupported("Search tools", "PUT /search_tools/{search_tool_id}"),
		stubUnsupported("Search tools", "DELETE /search_tools/{search_tool_id}"),
		stubUnsupported("Search tools", "POST /search_tools/test_connection"),
		stubUnsupported("Search tools", "POST /v1/search/{search_tool_name}"),
		stubOK("GET /v1/memory", `{"memories":[],"total":0}`),
		stubUnsupported("Memory", "POST /v1/memory"),
		stubUnsupported("Memory", "PUT /v1/memory/{key}"),
		stubUnsupported("Memory", "DELETE /v1/memory/{key}"),
		stubUnsupported("Prompts", "POST /prompts"),
		stubUnsupported("Prompts", "PUT /prompts/{prompt_id}"),
		stubUnsupported("Prompts", "DELETE /prompts/{prompt_id}"),
		stubNotFound("Prompts", "GET /prompts/{prompt_id}/info"),
		stubOK("GET /prompts/{prompt_id}/versions", `{"prompts":[]}`),
		stubUnsupported("Prompts", "POST /utils/dotprompt_json_converter"),
		stubUnsupported("Prompts", "POST /prompts/test"),
		stubOK("GET /v1/workflows/runs", `{"runs":[]}`),
		stubOK("GET /v1/workflows/runs/{run_id}/events", `{"events":[]}`),
		stubOK("GET /v1/workflows/runs/{run_id}/messages", `{"messages":[]}`),

		// 대시보드 밖 페이지(connect·moyai)
		stubNotFound("Connect flow", "GET /authorize/flow"),
		stubUnsupported("Connect flow", "POST /authorize/complete"),
		stubUnsupported("Moyai", "POST /moyai/connect/start"),

		// admin-panel: values 를 옵셔널 없이 읽는 곳이 있어 values 를 꼭 준다
		stubOK("GET /get/sso_settings", `{"values":{},"field_schema":{}}`),
		stubUnsupported("SSO settings", "PATCH /update/sso_settings"),
		stubOK("GET /config_overrides/hashicorp_vault", `{"values":{},"field_schema":{"properties":{}}}`),
		stubUnsupported("HashiCorp Vault", "POST /config_overrides/hashicorp_vault"),
		stubUnsupported("HashiCorp Vault", "DELETE /config_overrides/hashicorp_vault"),
		stubUnsupported("HashiCorp Vault", "POST /config_overrides/hashicorp_vault/test_connection"),
		stubOK("GET /config_overrides/cyberark", `{"config_type":"cyberark","values":{},"field_schema":{"properties":{}}}`),
		stubUnsupported("CyberArk", "POST /config_overrides/cyberark"),
		stubUnsupported("CyberArk", "DELETE /config_overrides/cyberark"),
		stubUnsupported("CyberArk", "POST /config_overrides/cyberark/test_connection"),
		stubOK("GET /get/websearch_interception_settings", `{"values":{},"field_schema":{},"active_on_this_pod":true}`),
		stubUnsupported("Web search interception", "PATCH /update/websearch_interception_settings"),
		stubOK("GET /config/list", `[]`),
		stubUnsupported("Proxy config", "POST /config/field/update"),
		stubUnsupported("Proxy config", "POST /config/field/delete"),
		stubUnsupported("Proxy config", "POST /config/update"),

		// logging-and-alerts: alerts 가 없으면 화면 전체가 깨진다
		stubOK("GET /callbacks/configs", `[]`),
		stubOK("GET /get/config/callbacks", `{"callbacks":[],"available_callbacks":{},"alerts":[],"router_settings":{},"active_alerting_destinations":[]}`),
		stubUnsupported("Callbacks", "POST /config/callback/delete"),
		stubOK("GET /alerting/settings", `[]`),
		stubOK("GET /email/event_settings", `{"settings":[]}`),
		stubUnsupported("Email events", "PATCH /email/event_settings"),
		stubUnsupported("Email events", "POST /email/event_settings/reset"),
		stubUnsupported("Service health checks", "GET /health/services"),

		// caching·router-settings
		stubOK("GET /cache/settings", `{"current_values":{}}`),
		stubUnsupported("Cache", "POST /cache/settings"),
		stubOK("POST /cache/settings/test", `{"status":"failed","message":"Cache is not supported by this gateway"}`),
		stubUnsupported("Cache", "GET /cache/ping"),
		stubOK("GET /coordination_redis/settings", `{"values":{}}`),
		stubUnsupported("Coordination Redis", "POST /coordination_redis/settings"),
		stubOK("POST /coordination_redis/settings/test", `{"status":"unhealthy","error":"Coordination Redis is not supported by this gateway"}`),
		stubOK("GET /router/fields", `{"fields":[],"routing_strategy_descriptions":{}}`),

		// cost-tracking: 404 면 로드마다 오류 토스트가 떠서 빈 값을 준다
		stubOK("GET /config/cost_discount_config", `{"values":{}}`),
		stubUnsupported("Cost discounts", "PATCH /config/cost_discount_config"),
		stubOK("GET /config/cost_margin_config", `{"values":{}}`),
		stubUnsupported("Cost margins", "PATCH /config/cost_margin_config"),
		stubOK("GET /config/block_requests_for_models_without_pricing", `{"enabled":false}`),
		stubUnsupported("Pricing enforcement", "PATCH /config/block_requests_for_models_without_pricing"),
		stubUnsupported("Cost estimation", "POST /cost/estimate"),

		// guardrails: summary 가 없으면 렌더가 깨진다
		stubOK("GET /guardrails/submissions", `{"submissions":[],"summary":{"total":0,"pending_review":0,"active":0,"rejected":0}}`),
		stubUnsupported(guardrails, "POST /guardrails/submissions/{guardrail_id}/approve"),
		stubUnsupported(guardrails, "POST /guardrails/submissions/{guardrail_id}/reject"),
		stubUnsupported(guardrails, "POST /guardrails/register"),
		stubOK("GET /guardrails/ui/provider_specific_params", `{}`),
		stubUnsupported(guardrails, "POST /guardrails"),
		stubUnsupported(guardrails, "PATCH /guardrails/{guardrail_id}"),
		stubUnsupported(guardrails, "DELETE /guardrails/{guardrail_id}"),
		stubNotFound(guardrails, "GET /guardrails/{guardrail_id}/info"),
		stubUnsupported(guardrails, "POST /guardrails/apply_guardrail"),
		stubUnsupported(guardrails, "POST /guardrails/test_custom_code"),
		stubNotFound(guardrails, "GET /guardrails/ui/category_yaml/{category_name}"),
		stubNotFound(guardrails, "GET /guardrails/ui/major_airlines"),
		stubUnsupported(guardrails, "POST /guardrails/validate_blocked_words_file"),

		// policies: 템플릿은 맨 배열
		stubOK("GET /policy/templates", `[]`),
		stubOK("GET /policies/attachments/list", `{"attachments":[]}`),
		stubUnsupported(policies, "POST /policies"),
		stubNotFound(policies, "GET /policies/{policy_id}"),
		stubUnsupported(policies, "PUT /policies/{policy_id}"),
		stubUnsupported(policies, "DELETE /policies/{policy_id}"),
		stubUnsupported(policies, "PUT /policies/{policy_id}/status"),
		stubNotFound(policies, "GET /policies/name/{policy_name}/versions"),
		stubUnsupported(policies, "POST /policies/name/{policy_name}/versions"),
		stubNotFound(policies, "GET /policies/{policy_id}/resolved-guardrails"),
		stubUnsupported(policies, "POST /policies/attachments"),
		stubUnsupported(policies, "DELETE /policies/attachments/{attachment_id}"),
		stubUnsupported(policies, "POST /policies/attachments/estimate-impact"),
		stubUnsupported(policies, "POST /policies/resolve"),
		stubUnsupported(policies, "POST /policies/test-pipeline"),
		stubUnsupported(policies, "POST /policy/templates/enrich"),
		stubUnsupported(policies, "POST /policy/templates/enrich/stream"),
		stubUnsupported(policies, "POST /policy/templates/suggest"),
		stubUnsupported(policies, "POST /policy/templates/test"),

		// organizations·access-groups·projects
		stubUnsupported("Organizations", "POST /organization/new"),
		stubUnsupported("Organizations", "PATCH /v2/organization/{organization_id}"),
		stubUnsupported("Organizations", "DELETE /organization/delete"),
		stubUnsupported("Organizations", "POST /organization/member_add"),
		stubUnsupported("Organizations", "DELETE /organization/member_delete"),
		stubUnsupported("Organizations", "PATCH /organization/member_update"),
		stubNotFound("Access groups", "GET /v1/access_group/{access_group_id}"),
		stubUnsupported("Access groups", "PUT /v1/access_group/{access_group_id}"),
		stubUnsupported("Access groups", "DELETE /v1/access_group/{access_group_id}"),
		stubNotFound("Projects", "GET /project/info"),
		stubUnsupported("Projects", "POST /project/new"),
		stubUnsupported("Projects", "POST /project/update"),
		stubUnsupported("Request transformation", "POST /utils/transform_request"),

		// lens(@litellm/lens-ui): /lens 가 빈 목록이면 하위 호출은 거의 없다. 나머지 GET 은 모양을 몰라 404 로 답한다
		stubOK("GET /lens", `{"lenses":[],"workers":[],"tracing_enabled":false}`),
		stubOK("GET /lens/activity/available", `{"requests":false,"traces":false}`),
		// lens_id 아래 GET(runs·reviews·executions)은 /lens/datasets/{id}·/lens/evals/{name} 과 패턴이 겹쳐(ServeMux 충돌) 하나로 받는다
		stubNotFound(lens, "GET /lens/{lens_id}/{rest...}"),
		stubNotFound(lens, "GET /lens/signals"),
		stubNotFound(lens, "GET /lens/agents"),
		stubNotFound(lens, "GET /lens/models"),
		stubNotFound(lens, "GET /lens/model_group/info"),
		stubNotFound(lens, "GET /lens/datasets"),
		stubNotFound(lens, "GET /lens/datasets/{dataset_id}"),
		stubNotFound(lens, "GET /lens/datasets/{dataset_id}/export"),
		stubNotFound(lens, "GET /lens/datasets/{dataset_id}/revisions/{revision}/cases"),
		stubNotFound(lens, "GET /lens/evals"),
		stubNotFound(lens, "GET /lens/evals/{name}"),
		stubNotFound(lens, "GET /lens/evals/runs"),
		stubNotFound(lens, "GET /lens/evals/runs/{run_id}"),
		stubNotFound(lens, "GET /lens/evals/runs/{run_id}/cases/{case_id}"),
		stubNotFound(lens, "GET /lens/github/status"),
		stubNotFound(lens, "GET /lens/github/authorizations/{authorization_id}"),
		stubNotFound(lens, "GET /lens/feedback"),
		stubNotFound(lens, "GET /lens/service"),
		stubUnsupported(lens, "POST /lens"),
		stubUnsupported(lens, "PUT /lens/{lens_id}"),
		stubUnsupported(lens, "POST /lens/{lens_id}/runs"),
		stubUnsupported(lens, "POST /lens/{lens_id}/cancel"),
		stubUnsupported(lens, "PATCH /lens/{lens_id}/findings/{finding_id}"),
		stubUnsupported(lens, "POST /lens/preview/sample"),
		stubUnsupported(lens, "POST /lens/watch-all"),
		stubUnsupported(lens, "PUT /lens/signals"),
		stubUnsupported(lens, "POST /lens/datasets"),
		stubUnsupported(lens, "POST /lens/datasets/build"),
		stubUnsupported(lens, "POST /lens/datasets/{dataset_id}/revisions"),
		stubUnsupported(lens, "PUT /lens/evals/{name}"),
		stubUnsupported(lens, "POST /lens/github/authorize"),
		stubUnsupported(lens, "PUT /lens/github/connections/{agent}"),
		stubUnsupported(lens, "DELETE /lens/github/connections/{agent}"),
		stubUnsupported(lens, "POST /lens/traces/findings"),
		stubUnsupported(lens, "POST /lens/traces/signals"),
		stubUnsupported(lens, "POST /lens/feedback/summary"),
		stubUnsupported(lens, "POST /lens/tracing/keys"),
		// 트레이싱: 404·501 이면 "tracing not enabled" 로 보고 재시도를 멈춘다
		stubNotFound("Tracing", "GET /v1/traces"),
		stubUnsupported("Tracing", "POST /v1/traces"),
		stubNotFound("Tracing", "GET /v1/traces/agents"),
		stubNotFound("Tracing", "GET /v1/traces/{trace_id}"),
		stubNotFound("Tracing", "GET /v1/traces/{trace_id}/spans/{span_id}"),
		stubNotFound("Tracing", "GET /v1/traces/{trace_id}/spans/{span_id}/error"),
	}
	for _, entity := range []string{"tag", "organization", "customer", "agent"} {
		list = append(list, dailyActivityStubs(entity)...)
	}
	return list
}
