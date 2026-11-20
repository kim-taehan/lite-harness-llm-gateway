package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// 로그인 키(SSO·비밀번호 로그인으로 데스크탑이 받는 키). litellm 처럼 DB 에 저장하지 않는 자기 완결 암호문이다(K 4장):
// "litellm_login_" + base64url(nonce || AES-256-GCM(JSON) || tag). 암호 키는 sha256(마스터 키).
// 요청마다 사용자·팀 행을 다시 읽어 역할·팀·차단을 반영하므로 발급 뒤 바뀐 권한도 바로 적용된다
const (
	loginKeyPrefix = "litellm_login_"
	loginKeyTTL    = 7 * 24 * time.Hour // litellm 기본(CLI_JWT_EXPIRATION_HOURS 24시간)보다 길게: 데스크탑 재로그인을 주 1회로(사용자 결정, 설계 9장)
)

type loginClaims struct {
	UserID  string    `json:"user_id"`
	TeamID  string    `json:"team_id,omitempty"`
	Expires time.Time `json:"expires"`
	// 대시보드 세션만 있다(데스크탑 키는 빈 값). litellm login_method 값: "username_password" 또는 "sso"
	UIMethod string    `json:"ui_method,omitempty"`
	IssuedAt time.Time `json:"issued_at,omitzero"`
}

const (
	uiMethodPassword = "username_password"
	uiMethodSSO      = "sso"
)

var errBadLoginKey = errors.New("로그인 키를 풀 수 없다")

type loginKeys struct {
	aead cipher.AEAD
}

func newLoginKeys(master MasterKey) *loginKeys {
	sum := sha256.Sum256(master.value)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		panic(err) // 32바이트 키라 일어나지 않는다
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &loginKeys{aead: aead}
}

func (l *loginKeys) issue(c loginClaims) (string, error) {
	plain, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, l.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := l.aead.Seal(nonce, nonce, plain, []byte(loginKeyPrefix))
	return loginKeyPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (l *loginKeys) open(key string) (loginClaims, error) {
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, loginKeyPrefix))
	if err != nil || len(data) < l.aead.NonceSize() {
		return loginClaims{}, errBadLoginKey
	}
	nonce, sealed := data[:l.aead.NonceSize()], data[l.aead.NonceSize():]
	plain, err := l.aead.Open(nil, nonce, sealed, []byte(loginKeyPrefix))
	if err != nil {
		return loginClaims{}, errBadLoginKey
	}
	var c loginClaims
	if err := json.Unmarshal(plain, &c); err != nil {
		return loginClaims{}, errBadLoginKey
	}
	return c, nil
}
