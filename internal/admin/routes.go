package admin

import (
	"log/slog"
	"net/http"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// Handlers 는 관리 API 핸들러다
type Handlers struct {
	db     *core.DB
	teams  *auth.Teams
	users  *auth.Users
	limits *limits.Policies
	rows   rows
	logger *slog.Logger
}

func NewHandlers(db *core.DB, teams *auth.Teams, users *auth.Users, policies *limits.Policies, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, teams: teams, users: users, limits: policies, rows: rows{teams: teams, users: users, limits: policies}, logger: logger}
}

// M1 에는 마스터 키(proxy_admin)만 있다. 쓰기는 proxy_admin, 읽기는 proxy_admin 과 proxy_admin_viewer 로 선언한다.
// 팀 admin 셀프 관리 같은 비관리자 경로는 키가 생기는 M2 에서 litellm 규칙(H 2.4)대로 넓힌다
var (
	write = core.Roles(core.RoleProxyAdmin)
	read  = core.Roles(core.RoleProxyAdmin, core.RoleProxyAdminViewer)
)

// Routes 는 관리 API 라우트 전부다. 새 엔드포인트는 여기에 한 줄을 더한다
func (h *Handlers) Routes() []core.Route {
	route := func(method, path string, access core.Access, handler http.HandlerFunc) core.Route {
		return core.Route{Method: method, Path: path, Access: access, Handler: handler}
	}
	return []core.Route{
		route(http.MethodPost, "/team/new", write, h.createTeam),
		route(http.MethodPost, "/team/update", write, h.updateTeam),
		route(http.MethodPost, "/team/delete", write, h.deleteTeams),
		route(http.MethodPost, "/team/block", write, h.blockTeam),
		route(http.MethodPost, "/team/unblock", write, h.unblockTeam),
		route(http.MethodPost, "/team/member_add", write, h.addMember),
		route(http.MethodPost, "/team/member_delete", write, h.deleteMember),
		route(http.MethodPost, "/team/member_update", write, h.updateMember),
		route(http.MethodGet, "/team/info", read, h.teamInfo),
		route(http.MethodGet, "/team/list", read, h.listTeamsV1),
		route(http.MethodGet, "/v2/team/list", read, h.listTeamsV2),
		route(http.MethodPost, "/user/new", write, h.createUser),
		route(http.MethodPost, "/user/update", write, h.updateUser),
		route(http.MethodPost, "/user/delete", write, h.deleteUsers),
		route(http.MethodGet, "/user/info", read, h.userInfo),
		route(http.MethodGet, "/v2/user/info", read, h.userInfoV2),
		route(http.MethodGet, "/user/list", read, h.listUsers),
	}
}

// internalError 는 예상 밖의 오류다. 원인은 로그에만 남기고 응답에는 내부 내용을 싣지 않는다
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("관리 API 처리 실패", "method", r.Method, "path", r.URL.Path, "err", err)
	proxyError(http.StatusInternalServerError, "Internal Server Error").Write(w)
}
