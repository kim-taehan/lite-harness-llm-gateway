// Package auth 는 신원을 맡는다: 키 검증, 라우트 권한 검사, (뒤 단계) 키·사용자·팀.
package auth

import (
	"crypto/subtle"
	"errors"
	"log/slog"
)

// MasterKey 는 환경 변수로만 받는 관리자 키다. 값이 로그·출력에 나가지 않게 문자열 표현을 가린다
type MasterKey struct {
	value []byte
}

// ErrNoMasterKey 는 마스터 키가 없을 때다. 기동을 거부한다(litellm 은 override 로 풀 수 있지만 우리는 막는다. 설계 6장)
var ErrNoMasterKey = errors.New("GATEWAY_MASTER_KEY 가 없다. 마스터 키 없이는 기동하지 않는다")

func NewMasterKey(value string) (MasterKey, error) {
	if value == "" {
		return MasterKey{}, ErrNoMasterKey
	}
	return MasterKey{value: []byte(value)}, nil
}

// Matches 는 비교 시간이 일치한 글자 수에 따라 달라지지 않게 비교한다(litellm 도 secrets.compare_digest)
func (k MasterKey) Matches(candidate string) bool {
	return subtle.ConstantTimeCompare(k.value, []byte(candidate)) == 1
}

func (k MasterKey) String() string { return "MasterKey(****)" }

func (k MasterKey) LogValue() slog.Value { return slog.StringValue(k.String()) }
