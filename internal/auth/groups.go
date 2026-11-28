package auth

import (
	"fmt"
	"slices"
	"strings"
)

// GroupMapping 은 Keycloak 그룹 → 팀·관리자 매핑표다. JWT 직접 호출(요청마다 토큰의 그룹)과 SSO 로그인(로그인 때 DB 를 맞춤)이 같은 표를 쓴다.
// 환경 변수 GATEWAY_GROUP_MAP·GATEWAY_GROUPS_CLAIM 으로 받는다. 표가 비어 있으면 그룹을 보지 않는다
type GroupMapping struct {
	Claim  string // 그룹 목록이 든 클레임(점으로 중첩 경로). 비면 groups
	Groups map[string]GroupGrant
}

// GroupGrant 는 그룹 하나가 주는 것이다. 팀에 들어가거나(TeamID) 관리자가 된다(Admin)
type GroupGrant struct {
	TeamID string `json:"team_id"`
	Admin  bool   `json:"admin"`
}

func (m GroupMapping) Enabled() bool { return len(m.Groups) > 0 }

func (m GroupMapping) claim() string {
	if m.Claim == "" {
		return "groups"
	}
	return m.Claim
}

// Validate 는 기동 때 설정 실수를 막는다
func (m GroupMapping) Validate() error {
	for group, grant := range m.Groups {
		if group == "" || (grant.TeamID == "" && !grant.Admin) {
			return fmt.Errorf("그룹 표: 그룹 %q 는 team_id 나 admin 이 있어야 한다", group)
		}
	}
	return nil
}

// granted 는 토큰의 그룹 중 표에 있는 것이 주는 팀(그룹 이름 순)과 관리자 여부다
func (m GroupMapping) granted(tokenGroups []string) ([]string, bool) {
	names := make([]string, 0, len(m.Groups))
	for name := range m.Groups {
		names = append(names, name)
	}
	slices.Sort(names)
	var teams []string
	admin := false
	for _, name := range names {
		if !slices.Contains(tokenGroups, name) {
			continue
		}
		grant := m.Groups[name]
		admin = admin || grant.Admin
		if grant.TeamID != "" && !slices.Contains(teams, grant.TeamID) {
			teams = append(teams, grant.TeamID)
		}
	}
	return teams, admin
}

// managedTeams 는 표에 나오는 팀이다. 이 팀들만 "그룹이 기준"이라 그룹이 없으면 팀원에서 뺀다. 표에 없는 팀의 수동 소속은 건드리지 않는다
func (m GroupMapping) managedTeams() []string {
	var teams []string
	for _, grant := range m.Groups {
		if grant.TeamID != "" && !slices.Contains(teams, grant.TeamID) {
			teams = append(teams, grant.TeamID)
		}
	}
	slices.Sort(teams)
	return teams
}

// groupsIn 은 클레임 경로(a.b.c)의 문자열 목록과, 그 클레임이 있었는지다. 문자열 하나만 있어도 목록으로 본다.
// 클레임이 없는 것(매퍼를 빼먹음)과 빈 목록(어떤 그룹에도 없음)을 가르려고 present 를 따로 돌려준다
func groupsIn(payload map[string]any, path string) (groups []string, present bool) {
	var current any = payload
	for _, key := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		if current, ok = object[key]; !ok {
			return nil, false
		}
	}
	switch v := current.(type) {
	case string:
		return []string{v}, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}
