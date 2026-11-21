package core

import (
	"strconv"
	"strings"
)

// ListQuery 는 목록 조회의 WHERE 절과 인자를 함께 모은다. 조건 문자열에는 상수만 넣고 값은 항상 $n 인자로 넘긴다.
// 조건마다 $번호를 손으로 세지 않으려고 둔다
type ListQuery struct {
	conditions []string
	Args       []any
}

// Where 는 조건 하나를 더한다. 조건 안의 ? 는 차례대로 $n 으로 바뀐다
func (q *ListQuery) Where(condition string, args ...any) {
	for _, arg := range args {
		condition = strings.Replace(condition, "?", q.Arg(arg), 1)
	}
	q.conditions = append(q.conditions, condition)
}

// Arg 는 인자 하나를 더하고 그 자리표($n)를 돌려준다. WHERE 밖(바깥 쿼리의 조건)에 값을 넣을 때 쓴다
func (q *ListQuery) Arg(value any) string {
	q.Args = append(q.Args, value)
	return "$" + strconv.Itoa(len(q.Args))
}

// Clause 는 모은 조건을 잇는 WHERE 절이다. 조건이 없으면 빈 문자열이다
func (q *ListQuery) Clause() string {
	if len(q.conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(q.conditions, " AND ")
}

// Page 는 쪽 번호(1부터)와 크기로 만든 LIMIT·OFFSET 절이다
func (q *ListQuery) Page(page, size int) string { return q.LimitOffset(size, (page-1)*size) }

// LimitOffset 은 LIMIT·OFFSET 을 인자로 더한 절이다
func (q *ListQuery) LimitOffset(limit, offset int) string {
	return " LIMIT " + q.Arg(limit) + " OFFSET " + q.Arg(offset)
}

// UpdateSet 은 "보낸 필드만 바꾸는" UPDATE 의 SET 절과 인자를 모은다. $1 은 대상 id 이고 updated_at 은 늘 바뀐다
type UpdateSet struct {
	sets []string
	Args []any
}

func NewUpdateSet(id any) *UpdateSet {
	return &UpdateSet{sets: []string{"updated_at = now()"}, Args: []any{id}}
}

// Set 은 열 하나의 새 값을 더한다. 열 이름은 상수만 넣는다
func (u *UpdateSet) Set(column string, value any) {
	u.Args = append(u.Args, value)
	u.sets = append(u.sets, column+" = $"+strconv.Itoa(len(u.Args)))
}

// Assignments 는 SET 뒤에 오는 대입 목록이다
func (u *UpdateSet) Assignments() string { return strings.Join(u.sets, ", ") }

// LikeContains 는 사용자 입력의 와일드카드를 글자 그대로로 바꾼 부분 일치 패턴이다
func LikeContains(v string) string { return "%" + escapeLike(v) + "%" }

// LikePrefix 는 사용자 입력의 와일드카드를 글자 그대로로 바꾼 앞부분 일치 패턴이다
func LikePrefix(v string) string { return escapeLike(v) + "%" }

func escapeLike(v string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(v)
}
