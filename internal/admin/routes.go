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
	rows        rows
	now         func() time.Time
	logger      *slog.Logger
}

func NewHandlers(db *core.DB, teams *auth.Teams, users *auth.Users, keys *auth.Keys, policies *limits.Policies,
	deployments *inference.Deployments, usage *limits.UsageQuery, settings *core.Settings, now func() time.Time, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, teams: teams, users: users, keys: keys, limits: policies, deployments: deployments, usage: usage, settings: settings,
		rows: rows{teams: teams, users: users, limits: policies}, now: now, logger: logger}
}

// 라우트 단계 권한은 litellm 표(docs/litellm-inventory/J_key_contract.md 6.2)를 따른다.
// proxy_admin 은 모두 통과하고, proxy_admin_viewer 의 쓰기는 auth.Guard 가 403 으로 막는다.
// Authenticated 는 인증만 되면 라우트 단계를 통과하고 권한은 핸들러가 판정한다(litellm 의 info·self_managed 라우트)
var (
	adminOnly     = core.Roles(core.RoleProxyAdmin)
	keyManagement = core.Roles(core.RoleInternalUser)
	keyAliases    = core.Roles(core.RoleProxyAdminViewer, core.RoleInternalUser)
	handlerDecide = core.Authenticated()
	spendTracking = core.Roles(core.RoleInternalUser, core.RoleInternalUserViewer)
)

// Routes 는 관리 API 라우트 전부다. 새 엔드포인트는 여기에 한 줄을 더한다
func (h *Handlers) Routes() []core.Route {
	route := func(method, path string, access core.Access, handler http.HandlerFunc) core.Route {
		return core.Route{Method: method, Path: path, Access: access, Handler: handler}
	}
	return []core.Route{
		route(http.MethodPost, "/team/new", adminOnly, h.createTeam),
		route(http.MethodPost, "/team/update", handlerDecide, h.updateTeam),
		route(http.MethodPost, "/team/delete", adminOnly, h.deleteTeams),
		route(http.MethodPost, "/team/block", adminOnly, h.blockTeam),
		route(http.MethodPost, "/team/unblock", adminOnly, h.unblockTeam),
		route(http.MethodPost, "/team/member_add", handlerDecide, h.addMember),
		route(http.MethodPost, "/team/member_delete", handlerDecide, h.deleteMember),
		route(http.MethodPost, "/team/member_update", handlerDecide, h.updateMember),
		route(http.MethodGet, "/team/info", handlerDecide, h.teamInfo),
		route(http.MethodGet, "/team/list", handlerDecide, h.listTeamsV1),
		route(http.MethodGet, "/v2/team/list", handlerDecide, h.listTeamsV2),
		route(http.MethodPost, "/user/new", adminOnly, h.createUser),
		route(http.MethodPost, "/user/update", adminOnly, h.updateUser),
		route(http.MethodPost, "/user/delete", adminOnly, h.deleteUsers),
		route(http.MethodGet, "/user/info", handlerDecide, h.userInfo),
		route(http.MethodGet, "/v2/user/info", handlerDecide, h.userInfoV2),
		route(http.MethodGet, "/user/list", handlerDecide, h.listUsers),
		route(http.MethodPost, "/key/generate", keyManagement, h.generateKey),
		route(http.MethodPost, "/key/update", keyManagement, h.updateKey),
		route(http.MethodPost, "/key/delete", keyManagement, h.deleteKeys),
		route(http.MethodGet, "/key/info", handlerDecide, h.keyInfo),
		route(http.MethodGet, "/key/list", handlerDecide, h.listKeys),
		route(http.MethodGet, "/key/aliases", keyAliases, h.keyAliases),
		route(http.MethodPost, "/key/block", keyManagement, h.blockKey),
		route(http.MethodPost, "/key/unblock", keyManagement, h.unblockKey),
		// 키는 sk-… 나 sha256 hex 라 / 가 없다
		route(http.MethodPost, "/key/{key}/reset_spend", keyManagement, h.resetKeySpend),
		route(http.MethodPost, "/model/new", adminOnly, h.createModel),
		route(http.MethodPost, "/model/delete", adminOnly, h.deleteModel),
		route(http.MethodPatch, "/model/{model_id}/update", adminOnly, h.updateModel),
		route(http.MethodGet, "/v2/model/info", handlerDecide, h.modelInfoV2),
		route(http.MethodGet, "/v1/model/info", handlerDecide, h.modelInfoV1),
		// 사용량(K 9장). /spend/logs 는 litellm spend_tracking_routes(internal_user·viewer), 일별 사용량은 self_managed
		route(http.MethodGet, "/user/daily/activity", handlerDecide, h.dailyActivity),
		route(http.MethodGet, "/spend/logs", spendTracking, h.spendLogs),
		// 아직 없는 기능은 빈 목록. litellm 처럼 /guardrails/list 는 internal_user_viewer 에게 401 이다(K 9.1)
		route(http.MethodGet, "/guardrails/list", core.Roles(core.RoleInternalUser), h.emptyGuardrails),
		route(http.MethodGet, "/v1/mcp/server", handlerDecide, h.emptyMCPServers),
		// 대시보드 설정. 화면 설정·테마·로고는 로그인 전에도 부른다(litellm 도 공개). 나머지는 litellm 처럼 관리자만(viewer 는 GET 만)
		route(http.MethodGet, "/get/ui_settings", core.Public(), h.getSettings(uiSettingsForm)),
		route(http.MethodPatch, "/update/ui_settings", adminOnly, h.updateSettings(uiSettingsForm)),
		route(http.MethodGet, "/get/ui_theme_settings", core.Public(), h.getSettings(uiThemeForm)),
		route(http.MethodPatch, "/update/ui_theme_settings", adminOnly, h.updateSettings(uiThemeForm)),
		route(http.MethodGet, "/get/default_team_settings", adminOnly, h.getSettings(defaultTeamForm)),
		route(http.MethodPatch, "/update/default_team_settings", adminOnly, h.updateSettings(defaultTeamForm)),
		route(http.MethodGet, "/get/internal_user_settings", adminOnly, h.getSettings(internalUserForm)),
		route(http.MethodPatch, "/update/internal_user_settings", adminOnly, h.updateSettings(internalUserForm)),
		route(http.MethodGet, "/get_image", core.Public(), serveLogo),
		route(http.MethodGet, "/get_favicon", core.Public(), serveLogo),
	}
}

// internalError 는 예상 밖의 오류다. 원인은 로그에만 남기고 응답에는 내부 내용을 싣지 않는다
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("관리 API 처리 실패", "method", r.Method, "path", r.URL.Path, "err", err)
	proxyError(http.StatusInternalServerError, "Internal Server Error").Write(w)
}
