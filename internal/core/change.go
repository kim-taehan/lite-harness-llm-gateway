package core

import (
	"encoding/json"
)

// Change 는 수정 요청의 필드 하나다. litellm 은 보낸 필드만 바꾼다(exclude_unset).
// 그래서 "보내지 않음(그대로 둔다)"과 "null 로 보냄(지운다)"을 구분해야 한다.
//
// JSON 에 필드가 없으면 UnmarshalJSON 이 불리지 않아 Set 이 false 로 남는다.
// null 이면 Set 은 true, Value 는 nil 이다.
type Change[T any] struct {
	Set   bool
	Value *T
}

func (c *Change[T]) UnmarshalJSON(data []byte) error {
	c.Set = true
	if string(data) == "null" {
		c.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	c.Value = &v
	return nil
}

// To 는 값으로 바꾸는 Change 다
func To[T any](v T) Change[T] { return Change[T]{Set: true, Value: &v} }

// Or 는 보낸 값이 있으면 그 값, 보내지 않았거나 null 이면 def 다(기본값이 있는 필드)
func (c Change[T]) Or(def T) T {
	if c.Value == nil {
		return def
	}
	return *c.Value
}

// Apply 는 보낸 필드면 그 값(null 포함), 보내지 않았으면 current 다
func (c Change[T]) Apply(current *T) *T {
	if !c.Set {
		return current
	}
	return c.Value
}

// Page 는 목록 한 쪽과 전체 개수다
type Page[T any] struct {
	Items []T
	Total int64
}
