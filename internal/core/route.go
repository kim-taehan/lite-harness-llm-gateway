// Package core 는 모든 모듈이 함께 쓰는 바탕이다: 라우트 권한 선언, litellm 모양 오류 응답, 헬스.
// 다른 모듈을 가져오지 않는다(의존 방향의 맨 아래).
package core

import (
	"net/http"
	"slices"
)

// Role 은 litellm 역할 문자열이다. 대시보드가 이 값으로 메뉴를 나누므로 철자를 바꾸지 않는다
type Role string

const (
	RoleProxyAdmin         Role = "proxy_admin"
	RoleProxyAdminViewer   Role = "proxy_admin_viewer"
	RoleInternalUser       Role = "internal_user"
	RoleInternalUserViewer Role = "internal_user_viewer"
	RoleTeam               Role = "team"
)

// Access 는 라우트를 부를 수 있는 사람이다. 공개, 인증만 되면 누구나(핸들러가 판정), 허용 역할 목록 중 하나다
type Access struct {
	public        bool
	authenticated bool
	roles         []Role
}

// Public 은 인증 없이 여는 라우트다(헬스 프로브처럼 litellm 도 여는 것만)
func Public() Access { return Access{public: true} }

// Authenticated 는 인증만 되면 라우트 단계는 통과하고 권한은 핸들러가 판정하는 라우트다
// (litellm 의 info_routes·self_managed_routes. 예: /key/info 는 소유자·팀원이면 본다)
func Authenticated() Access { return Access{authenticated: true} }

// Roles 는 나열한 역할만 부를 수 있는 라우트다
func Roles(roles ...Role) Access { return Access{roles: roles} }

func (a Access) IsPublic() bool { return a.public }

// Allows 는 라우트 단계에서 이 역할을 들여보내는지다. 역할을 모르는 키(사용자 행 없음)는 빈 문자열이다
func (a Access) Allows(role Role) bool { return a.authenticated || slices.Contains(a.roles, role) }

// Route 는 경로·핸들러·권한을 한 곳에 묶는다. 둘을 따로 선언하면 어긋나기 때문이다.
// 모듈은 라우트를 이 모양으로만 내놓고, 앱이 auth 의 권한 검사로 감싸 등록한다.
// 그래서 권한 선언이 없는 라우트는 만들 수 없다.
type Route struct {
	Method  string
	Path    string
	Access  Access
	Handler http.HandlerFunc
}

// Pattern 은 net/http ServeMux 의 "METHOD /path" 패턴이다
func (r Route) Pattern() string { return r.Method + " " + r.Path }
