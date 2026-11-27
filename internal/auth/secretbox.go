package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// purposeUpstreamKeyAES 는 DB 에 넣는 업스트림 키를 봉하는 키의 용도다(마스터 키에서 파생. masterkey.go)
const purposeUpstreamKeyAES = "gateway/upstream-key/aes-256-gcm"

// ErrSecretUnreadable 은 봉한 값을 풀 수 없을 때다. 마스터 키가 바뀌었거나 값이 망가진 경우다
var ErrSecretUnreadable = errors.New("봉한 비밀 값을 풀 수 없다(마스터 키가 바뀌었나)")

// SecretBox 는 DB 에 저장하는 비밀 값을 AES-256-GCM 으로 봉하고 푼다. 봉할 때마다 nonce 가 달라 같은 값도 매번 다르게 저장된다
type SecretBox struct {
	aead cipher.AEAD
}

// UpstreamKeyBox 는 업스트림 키(model_deployment.api_key_encrypted)용 상자다.
// 키를 마스터 키에서 파생하므로 마스터 키를 바꾸면 저장된 업스트림 키는 풀 수 없다. 그때는 모델의 키를 다시 넣는다
func (k MasterKey) UpstreamKeyBox() *SecretBox {
	block, err := aes.NewCipher(k.derive(purposeUpstreamKeyAES))
	if err != nil {
		panic(err) // 32바이트 키라 일어나지 않는다
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return &SecretBox{aead: aead}
}

// Seal 은 nonce 와 암호문을 이어 붙여 돌려준다
func (b *SecretBox) Seal(plain string) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // crypto/rand 는 실패하지 않는다(실패하면 런타임이 멈춘다)
	}
	return b.aead.Seal(nonce, nonce, []byte(plain), []byte(purposeUpstreamKeyAES))
}

// Open 은 Seal 이 만든 값을 푼다
func (b *SecretBox) Open(sealed []byte) (string, error) {
	if len(sealed) < b.aead.NonceSize() {
		return "", ErrSecretUnreadable
	}
	nonce, data := sealed[:b.aead.NonceSize()], sealed[b.aead.NonceSize():]
	plain, err := b.aead.Open(nil, nonce, data, []byte(purposeUpstreamKeyAES))
	if err != nil {
		return "", ErrSecretUnreadable
	}
	return string(plain), nil
}
