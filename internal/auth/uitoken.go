package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

// signUIToken 은 대시보드 token 쿠키의 JWT 다(L 3.6). litellm 처럼 HS256, 서명 키는 마스터 키다.
// 대시보드는 서명을 검증하지 않고 클레임만 읽는다. 서버도 이 JWT 를 다시 읽지 않는다(API 는 안의 key 클레임으로 인증한다)
func signUIToken(master MasterKey, claims map[string]any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, master.value)
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil)), nil
}
