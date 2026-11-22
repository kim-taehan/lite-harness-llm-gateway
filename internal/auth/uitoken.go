package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// signUIToken 은 대시보드 token 쿠키의 JWT 다(L 3.6). litellm 처럼 HS256, 서명 키는 마스터 키에서 파생한 용도별 키다.
// 대시보드는 서명을 검증하지 않고 클레임만 읽는다. 서버도 이 JWT 를 다시 읽지 않는다(API 는 안의 key 클레임으로 인증한다)
func signUIToken(master MasterKey, claims map[string]any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(payload)
	return unsigned + "." + enc.EncodeToString(signature(master, unsigned)), nil
}

func signature(master MasterKey, unsigned string) []byte {
	mac := hmac.New(sha256.New, master.derive(purposeUITokenHMAC))
	mac.Write([]byte(unsigned))
	return mac.Sum(nil)
}

var errBadUIToken = errors.New("JWT 를 확인할 수 없다")

// verifyUIToken 은 우리가 서명한 JWT 의 클레임이다. 서명이 틀리거나 exp 가 지났으면 errBadUIToken.
// 서버가 JWT 를 다시 읽는 곳은 초대 온보딩 하나다(대시보드 세션은 JWT 가 아니라 안의 key 로 인증한다)
func verifyUIToken(master MasterKey, token string, now time.Time) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errBadUIToken
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(got, signature(master, parts[0]+"."+parts[1])) {
		return nil, errBadUIToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errBadUIToken
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errBadUIToken
	}
	if exp, ok := claims["exp"].(float64); ok && now.Unix() >= int64(exp) {
		return nil, errBadUIToken
	}
	return claims, nil
}
