package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"llm-gateway/internal/core"
)

// TeamRole 은 팀 안 역할이다. litellm 은 이 둘뿐이다
type TeamRole string

const (
	TeamRoleAdmin TeamRole = "admin"
	TeamRoleUser  TeamRole = "user"
)

func (r TeamRole) Valid() bool { return r == TeamRoleAdmin || r == TeamRoleUser }

type Team struct {
	TeamID    string
	TeamAlias *string
	Models    []string
	Metadata  map[string]any
	Blocked   bool
	// 팀 일반 멤버(role=user)가 부를 수 있는 키 라우트. nil 이면 litellm 기본값을 쓴다
	MemberPermissions []string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type User struct {
	UserID    string
	UserEmail *string
	UserAlias *string
	// nil 이면 역할을 정하지 않은 것이다. litellm 은 인증 때 internal_user 로 본다
	UserRole  *core.Role
	Models    []string
	Metadata  map[string]any
	SSOUserID *string
	// 이 시각보다 먼저 발급된 대시보드 세션 키는 거부한다(로그아웃)
	UISessionsRevokedAt *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type TeamMember struct {
	TeamID    string
	UserID    string
	UserEmail *string
	UserAlias *string
	Role      TeamRole
}

// MemberRef 는 팀원을 가리키는 값이다. UserID 와 UserEmail 중 하나 이상이 있다
type MemberRef struct {
	UserID    *string
	UserEmail *string
	Role      TeamRole
}

type NewTeam struct {
	TeamID    *string
	TeamAlias *string
	Models    []string
	Metadata  map[string]any
	Blocked   bool
	Members   []MemberRef
}

type TeamPatch struct {
	TeamAlias core.Change[string]
	Models    core.Change[[]string]
	Metadata  core.Change[map[string]any]
	Blocked   core.Change[bool]
	// MemberPermissions 는 /team/permissions_update 가 바꾼다
	MemberPermissions core.Change[[]string]
}

type NewUser struct {
	UserID    *string
	UserEmail *string
	UserAlias *string
	UserRole  *core.Role
	Models    []string
	Metadata  map[string]any
	TeamID    *string
}

type UserPatch struct {
	UserEmail core.Change[string]
	UserAlias core.Change[string]
	UserRole  core.Change[core.Role]
	Models    core.Change[[]string]
	Metadata  core.Change[map[string]any]
}

type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

type TeamQuery struct {
	MemberUserID      *string
	TeamID            *string
	TeamAliasContains *string
	// team_id 일치(SearchTeamIDPrefix 면 접두어) 이거나 별칭 부분 일치
	Search             *string
	SearchTeamIDPrefix bool
	SortBy             string // team_id · team_alias · created_at, 비면 created_at 최신순
	SortOrder          SortOrder
	Page, PageSize     int
}

type UserQuery struct {
	Role              *string
	UserIDs           []string
	SSOUserIDs        []string
	UserEmailContains *string
	Search            *string
	TeamID            *string
	SortBy            string // user_id · user_email · created_at · user_alias · user_role, 비면 created_at 최신순
	SortOrder         SortOrder
	Page, PageSize    int
}

// 예상되는 실패. 호출하는 쪽(admin)이 litellm 의 상태 코드·문구로 바꾼다
var (
	ErrTeamNotFound   = errors.New("팀이 없다")
	ErrTeamIDTaken    = errors.New("이미 있는 team_id 다")
	ErrTeamIDReserved = errors.New("예약된 team_id 다")
	ErrNotMember      = errors.New("팀원이 아니다")
	ErrUserNotFound   = errors.New("사용자가 없다")
	ErrUserIDTaken    = errors.New("이미 있는 user_id 다")
)

// NotFoundError 는 여러 id 를 한 번에 처리할 때 처음 찾지 못한 id 다
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return "없다: " + e.ID }

// EmailTakenError 는 이메일이 이미 다른 사용자 것이다(대소문자 무시)
type EmailTakenError struct{ Existing string }

func (e *EmailTakenError) Error() string { return "이미 있는 이메일이다: " + e.Existing }

// EmailOwnerMismatchError 는 팀원 추가 때 이메일과 user_id 가 서로 다른 사람이다
type EmailOwnerMismatchError struct{ Email, UserID string }

func (e *EmailOwnerMismatchError) Error() string {
	return fmt.Sprintf("user_email %q 와 user_id %q 가 다른 사람이다", e.Email, e.UserID)
}

// AlreadyMemberError 는 넣으려는 팀원이 이미 있다. Single 이면 하나를 넣으려 했고, 아니면 모두 이미 있다
type AlreadyMemberError struct {
	Single   bool
	Member   MemberRef
	Existing []TeamMember
}

func (e *AlreadyMemberError) Error() string { return "이미 팀원이다" }

// litellm 이 대시보드 세션 키용으로 예약한 팀 id
const dashboardTeamID = "litellm-dashboard"

// newID 는 uuid v4 문자열이다(litellm 의 str(uuid.uuid4()) 와 같은 모양)
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
