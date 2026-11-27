package admin

import (
	"log/slog"
	"net/http"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
	"llm-gateway/internal/limits"
)

// Handlers 는 관리 API 핸들러다
type Handlers struct {
	db     *core.DB
	teams  *auth.Teams
	users  *auth.Users
	keys   *auth.Keys
	limits *limits.Policies
	// deployments 는 모델 관리 API 가 inference 의 공개 API 로 다룬다
	deployments *inference.Deployments
	usage       *limits.UsageQuery
	settings    *core.Settings
	ipAllow     *auth.IPAllowlist
	rows        rows
	audit       *auditLog
	now         func() time.Time
	logger      *slog.Logger
}

// Deps 는 관리 API 가 쓰는 다른 모듈들이다. 인자가 많아 이름을 붙여 넘긴다
type Deps struct {
	DB          *core.DB
	Teams       *auth.Teams
	Users       *auth.Users
	Keys        *auth.Keys
	Policies    *limits.Policies
	Deployments *inference.Deployments
	Usage       *limits.UsageQuery
	Settings    *core.Settings
	IPAllow     *auth.IPAllowlist
	Now         func() time.Time
	Logger      *slog.Logger
}

func NewHandlers(d Deps) *Handlers {
	return &Handlers{db: d.DB, teams: d.Teams, users: d.Users, keys: d.Keys, limits: d.Policies, deployments: d.Deployments, usage: d.Usage,
		settings: d.Settings, ipAllow: d.IPAllow, rows: rows{teams: d.Teams, users: d.Users, limits: d.Policies}, audit: &auditLog{db: d.DB, now: d.Now}, now: d.Now, logger: d.Logger}
}

// 라우트 단계 권한은 litellm 표(docs/litellm-inventory/J_key_contract.md 6.2)를 따른다.
// proxy_admin 은 모두 통과하고, proxy_admin_viewer 의 쓰기는 auth.Guard 가 403 으로 막는다.
// Authenticated 는 인증만 되면 라우트 단계를 통과하고 권한은 핸들러가 판정한다(litellm 의 info·self_managed 라우트)
var (
	adminOnly     = core.Roles(core.RoleProxyAdmin)
	keyManagement = core.Roles(core.RoleInternalUser)
	keyAliases    = core.Roles(core.RoleProxyAdminViewer, core.RoleInternalUser)
	auditViewer   = core.Roles(core.RoleProxyAdminViewer)
	handlerDecide = core.Authenticated()
	spendTracking = core.Roles(core.RoleInternalUser, core.RoleInternalUserViewer)
)

// Routes 는 관리 API 라우트 전부다. 새 엔드포인트는 여기에 한 줄을 더한다
func (h *Handlers) Routes() []core.Route {
	route := func(method, path string, access core.Access, handler http.HandlerFunc) core.Route {
		return core.Route{Method: method, Path: path, Access: access, Handler: handler}
	}
	return append(h.stubRoutes(),
		route(http.MethodPost, "/team/new", adminOnly, h.audited("created", auditTeamTable, idsFrom("team_id"), h.createTeam)),
		route(http.MethodPost, "/team/update", handlerDecide, h.audited("updated", auditTeamTable, idsFrom("team_id"), h.updateTeam)),
		route(http.MethodPost, "/team/delete", adminOnly, h.audited("deleted", auditTeamTable, idsFrom("team_ids"), h.deleteTeams)),
		route(http.MethodPost, "/team/block", adminOnly, h.audited("blocked", auditTeamTable, idsFrom("team_id"), h.blockTeam)),
		route(http.MethodPost, "/team/unblock", adminOnly, h.audited("unblocked", auditTeamTable, idsFrom("team_id"), h.unblockTeam)),
		route(http.MethodPost, "/team/member_add", handlerDecide, h.audited("updated", auditTeamTable, idsFrom("team_id"), h.addMember)),
		route(http.MethodPost, "/team/member_delete", handlerDecide, h.audited("updated", auditTeamTable, idsFrom("team_id"), h.deleteMember)),
		route(http.MethodPost, "/team/member_update", handlerDecide, h.audited("updated", auditTeamTable, idsFrom("team_id"), h.updateMember)),
		route(http.MethodGet, "/team/info", handlerDecide, h.teamInfo),
		route(http.MethodGet, "/team/list", handlerDecide, h.listTeamsV1),
		route(http.MethodGet, "/v2/team/list", handlerDecide, h.listTeamsV2),
		route(http.MethodGet, "/team/available", handlerDecide, h.availableTeams),
		route(http.MethodGet, "/team/metadata_schema", handlerDecide, h.teamMetadataSchema),
		route(http.MethodGet, "/team/permissions_list", handlerDecide, h.teamPermissionsList),
		route(http.MethodPost, "/team/permissions_update", handlerDecide, h.teamPermissionsUpdate),
		route(http.MethodGet, "/team/{team_id}/members/me", handlerDecide, h.myTeamMembership),
		route(http.MethodPost, "/team/{team_id}/member/{user_id}/reset_spend", handlerDecide, h.resetTeamMemberSpend),
		route(http.MethodPost, "/team/{team_id}/member/{user_id}/reset_budget", handlerDecide, h.resetTeamMemberBudget),
		route(http.MethodPost, "/management/v1/teams/{team_id}/members/bulk_update", handlerDecide, h.bulkUpdateMemberBudgets),
		route(http.MethodPost, "/user/new", adminOnly, h.audited("created", auditUserTable, idsFrom("user_id"), h.createUser)),
		route(http.MethodPost, "/user/update", adminOnly, h.audited("updated", auditUserTable, idsFrom("user_id"), h.updateUser)),
		route(http.MethodPost, "/user/delete", adminOnly, h.audited("deleted", auditUserTable, idsFrom("user_ids"), h.deleteUsers)),
		route(http.MethodGet, "/user/info", handlerDecide, h.userInfo),
		route(http.MethodGet, "/v2/user/info", handlerDecide, h.userInfoV2),
		route(http.MethodGet, "/user/list", handlerDecide, h.listUsers),
		route(http.MethodGet, "/user/available_roles", handlerDecide, h.availableRoles),
		route(http.MethodGet, "/user/filter/ui", handlerDecide, h.filterUsers),
		route(http.MethodPost, "/user/bulk_update", adminOnly, h.bulkUpdateUsers),
		route(http.MethodPost, "/team/bulk_member_add", adminOnly, h.bulkAddTeamMembers),
		route(http.MethodPost, "/invitation/new", handlerDecide, h.createInvitation),
		route(http.MethodPost, "/key/generate", keyManagement, h.audited("created", auditKeyTable, keyTargets, h.generateKey)),
		route(http.MethodPost, "/key/update", keyManagement, h.audited("updated", auditKeyTable, keyTargets, h.updateKey)),
		route(http.MethodPost, "/key/delete", keyManagement, h.audited("deleted", auditKeyTable, keyTargets, h.deleteKeys)),
		route(http.MethodGet, "/key/info", handlerDecide, h.keyInfo),
		route(http.MethodGet, "/key/list", handlerDecide, h.listKeys),
		route(http.MethodGet, "/key/aliases", keyAliases, h.keyAliases),
		route(http.MethodPost, "/key/block", keyManagement, h.audited("blocked", auditKeyTable, keyTargets, h.blockKey)),
		route(http.MethodPost, "/key/unblock", keyManagement, h.audited("unblocked", auditKeyTable, keyTargets, h.unblockKey)),
		// 키는 sk-… 나 sha256 hex 라 / 가 없다
		route(http.MethodPost, "/key/{key}/reset_spend", keyManagement, h.resetKeySpend),
		route(http.MethodPost, "/key/{key}/regenerate", keyManagement, h.audited("rotated", auditKeyTable, keyTargets, h.regenerateKey)),
		route(http.MethodPost, "/key/regenerate", keyManagement, h.audited("rotated", auditKeyTable, keyTargets, h.regenerateKey)),
		route(http.MethodPost, "/key/service-account/generate", keyManagement, h.audited("created", auditKeyTable, keyTargets, h.generateServiceAccountKey)),
		route(http.MethodGet, "/audit", auditViewer, h.listAudit),
		route(http.MethodGet, "/audit/{id}", auditViewer, h.getAudit),
		route(http.MethodPost, "/model/new", adminOnly, h.createModel),
		route(http.MethodPost, "/model/delete", adminOnly, h.deleteModel),
		route(http.MethodPatch, "/model/{model_id}/update", adminOnly, h.updateModel),
		route(http.MethodGet, "/v2/model/info", handlerDecide, h.modelInfoV2),
		route(http.MethodGet, "/v1/model/info", handlerDecide, h.modelInfoV1),
		route(http.MethodPost, "/model_group/make_public", adminOnly, h.makeModelGroupsPublic),
		route(http.MethodPost, "/model_hub/update_useful_links", adminOnly, h.updateUsefulLinks),
		route(http.MethodGet, "/public/model_hub/info", core.Public(), h.modelHubInfo),
		// 공개 모델 허브 목록은 litellm 처럼 로그인 없이 본다
		route(http.MethodGet, "/public/v1/model_hub", core.Public(), h.publicModelHub),
		route(http.MethodGet, "/public/v1/model_hub/{facet}", core.Public(), h.publicModelHubFacet),
		// 사용량(K 9장). /spend/logs 는 litellm spend_tracking_routes(internal_user·viewer), 일별 사용량은 self_managed
		route(http.MethodGet, "/user/daily/activity", handlerDecide, h.dailyActivity),
		route(http.MethodGet, "/spend/logs", spendTracking, h.spendLogs),
		// 대시보드 사용량·로그 화면(N 문서). 범위(자기 것·팀)는 핸들러가 정한다
		route(http.MethodGet, "/user/daily/activity/aggregated", handlerDecide, h.activity(entityUser, "aggregated")),
		route(http.MethodGet, "/user/daily/activity/aggregated/keys", handlerDecide, h.activity(entityUser, "keys")),
		route(http.MethodGet, "/user/daily/activity/aggregated/search", handlerDecide, h.activity(entityUser, "search")),
		route(http.MethodGet, "/user/daily/activity/aggregated/model_top_keys", handlerDecide, h.activity(entityUser, "model_top_keys")),
		route(http.MethodGet, "/user/daily/activity/aggregated/users", handlerDecide, h.activity(entityUser, "users")),
		route(http.MethodGet, "/user/daily/activity/export", handlerDecide, h.activityExport(entityUser)),
		route(http.MethodGet, "/team/daily/activity/aggregated", handlerDecide, h.activity(entityTeam, "aggregated")),
		route(http.MethodGet, "/team/daily/activity/aggregated/keys", handlerDecide, h.activity(entityTeam, "keys")),
		route(http.MethodGet, "/team/daily/activity/aggregated/search", handlerDecide, h.activity(entityTeam, "search")),
		route(http.MethodGet, "/team/daily/activity/aggregated/model_top_keys", handlerDecide, h.activity(entityTeam, "model_top_keys")),
		route(http.MethodGet, "/team/daily/activity/export", handlerDecide, h.activityExport(entityTeam)),
		route(http.MethodGet, "/team/spend/by_user", handlerDecide, h.teamSpendByUser),
		route(http.MethodGet, "/model-insights", handlerDecide, h.modelInsights),
		route(http.MethodGet, "/spend/logs/ui", spendTracking, h.spendLogsUI),
		route(http.MethodGet, "/spend/logs/ui/{request_id}", spendTracking, h.spendLogDetail),
		route(http.MethodGet, "/spend/logs/session/ui", spendTracking, h.sessionLogs),
		route(http.MethodGet, "/management/v1/spend_logs/users", spendTracking, h.spendLogFacet(false)),
		route(http.MethodGet, "/management/v1/spend_logs/end_users", spendTracking, h.spendLogFacet(true)),
		// 아직 없는 기능은 빈 목록. litellm 처럼 /guardrails/list 는 internal_user_viewer 에게 401 이다(K 9.1)
		route(http.MethodGet, "/guardrails/list", core.Roles(core.RoleInternalUser), h.emptyGuardrails),
		route(http.MethodGet, "/v1/mcp/server", handlerDecide, h.emptyMCPServers),
		// 대시보드 설정. 화면 설정·테마·로고는 로그인 전에도 부른다(litellm 도 공개). 나머지는 litellm 처럼 관리자만(viewer 는 GET 만)
		route(http.MethodGet, "/get/ui_settings", core.Public(), h.getSettings(uiSettingsForm)),
		route(http.MethodPatch, "/update/ui_settings", adminOnly, h.updateSettings(uiSettingsForm)),
		route(http.MethodGet, "/get/ui_theme_settings", core.Public(), h.getSettings(uiThemeForm)),
		route(http.MethodPatch, "/update/ui_theme_settings", adminOnly, h.updateSettings(uiThemeForm)),
		route(http.MethodGet, "/access_group/list", adminOnly, h.listAccessGroups),
		route(http.MethodGet, "/access_group/{access_group}/info", adminOnly, h.accessGroupInfo),
		route(http.MethodGet, "/get/allowed_ips", adminOnly, h.getAllowedIPs),
		route(http.MethodPost, "/add/allowed_ip", adminOnly, h.audited("updated", auditConfigTable, configTarget, h.addAllowedIP)),
		route(http.MethodPost, "/delete/allowed_ip", adminOnly, h.audited("deleted", auditConfigTable, configTarget, h.deleteAllowedIP)),
		route(http.MethodGet, "/get/default_team_settings", adminOnly, h.getSettings(defaultTeamForm)),
		route(http.MethodPatch, "/update/default_team_settings", adminOnly, h.updateSettings(defaultTeamForm)),
		route(http.MethodGet, "/get/internal_user_settings", adminOnly, h.getSettings(internalUserForm)),
		route(http.MethodPatch, "/update/internal_user_settings", adminOnly, h.updateSettings(internalUserForm)),
		route(http.MethodGet, "/get_image", core.Public(), serveEmbedded("logo.png", "image/png")),
		route(http.MethodGet, "/get_favicon", core.Public(), serveEmbedded("logo.png", "image/png")),
		// 모델 추가 폼(litellm 도 인증 없이 연다)
		route(http.MethodGet, "/public/providers/fields", core.Public(), serveEmbedded("provider_fields.json", "application/json")),
		route(http.MethodGet, "/public/litellm_model_cost_map", core.Public(), serveEmbedded("model_cost_map.json", "application/json")),
	)
}

// internalError 는 예상 밖의 오류다. 원인은 로그에만 남기고 응답에는 내부 내용을 싣지 않는다
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("관리 API 처리 실패", "method", r.Method, "path", r.URL.Path, "err", err)
	proxyError(http.StatusInternalServerError, "Internal Server Error").Write(w)
}
