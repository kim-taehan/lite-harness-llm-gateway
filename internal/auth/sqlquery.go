package auth

import (
	"strconv"
	"strings"
)

// listQuery 는 목록 조회의 WHERE 절과 인자를 함께 모은다. 조건 문자열에는 상수만 넣고 값은 항상 $n 인자로 넘긴다
type listQuery struct {
	conditions []string
	args       []any
}

// where 는 조건 하나를 더한다. 조건 안의 ? 는 차례대로 $n 으로 바뀐다
func (q *listQuery) where(condition string, args ...any) {
	for _, arg := range args {
		q.args = append(q.args, arg)
		condition = strings.Replace(condition, "?", "$"+strconv.Itoa(len(q.args)), 1)
	}
	q.conditions = append(q.conditions, condition)
}

func (q *listQuery) clause() string {
	if len(q.conditions) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(q.conditions, " AND ")
}

// page 는 LIMIT·OFFSET 을 인자로 더한 절이다
func (q *listQuery) page(page, size int) string {
	q.args = append(q.args, size, (page-1)*size)
	return " LIMIT $" + strconv.Itoa(len(q.args)-1) + " OFFSET $" + strconv.Itoa(len(q.args))
}

// orderBy 는 허용한 열만 정렬에 쓴다. 비거나 모르는 열이면 created_at 최신순이다(litellm 기본)
func orderBy(column string, allowed []string, order SortOrder, tieBreaker string) string {
	for _, a := range allowed {
		if a == column {
			dir := "ASC"
			if order == SortDesc {
				dir = "DESC"
			}
			return " ORDER BY " + column + " " + dir + ", " + tieBreaker
		}
	}
	return " ORDER BY created_at DESC, " + tieBreaker
}

// likeContains 는 사용자 입력의 와일드카드를 글자 그대로로 바꾼 부분 일치 패턴이다
func likeContains(v string) string { return "%" + escapeLike(v) + "%" }

func likePrefix(v string) string { return escapeLike(v) + "%" }

func escapeLike(v string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(v)
}
