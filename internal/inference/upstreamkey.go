package inference

import "log/slog"

// UpstreamKey 는 업스트림에 보낼 키다. 로그·출력에 값이 나가지 않게 문자열 표현을 가린다.
// 빈 값은 "키 없음"을 뜻한다(저장된 키를 지울 때 쓴다)
type UpstreamKey struct {
	value string
}

func NewUpstreamKey(value string) UpstreamKey { return UpstreamKey{value: value} }

func (k UpstreamKey) String() string { return "UpstreamKey(****)" }

func (k UpstreamKey) LogValue() slog.Value { return slog.StringValue(k.String()) }

func (k UpstreamKey) isEmpty() bool { return k.value == "" }
