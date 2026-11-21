package core

import (
	"crypto/rand"
	"fmt"
)

// NewUUID 는 uuid v4 문자열이다(litellm 의 str(uuid.uuid4()) 와 같은 모양). 팀·사용자·초대·모델 배포·요청 id 가 모두 이것이다
func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand 는 실패하지 않는다(Go 1.24+)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
