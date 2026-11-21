package auth

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
