package admin

import (
	"log/slog"
	"net/http"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// Handlers 는 관리 API 핸들러다
type Handlers struct {
	db     *core.DB
	teams  *auth.Teams
	users  *auth.Users
	keys   *auth.Keys
	limits *limits.Policies
	rows   rows
	now    func() time.Time
	logger *slog.Logger
}

func NewHandlers(db *core.DB, teams *auth.Teams, users *auth.Users, keys *auth.Keys, policies *limits.Policies, now func() time.Time, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, teams: teams, users: users, keys: keys, limits: policies,
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
	}
}

// internalError 는 예상 밖의 오류다. 원인은 로그에만 남기고 응답에는 내부 내용을 싣지 않는다
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("관리 API 처리 실패", "method", r.Method, "path", r.URL.Path, "err", err)
	proxyError(http.StatusInternalServerError, "Internal Server Error").Write(w)
}
