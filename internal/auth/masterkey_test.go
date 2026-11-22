package auth

import (
	"bytes"
	"testing"
)

func TestMasterKey_DerivesSeparateKeyPerPurpose(t *testing.T) {
	master, _ := NewMasterKey(testMasterKey)
	other, _ := NewMasterKey(testMasterKey + "x")

	aes, hmac := master.derive(purposeLoginKeyAES), master.derive(purposeUITokenHMAC)
	if len(aes) != 32 || bytes.Equal(aes, hmac) {
		t.Errorf("용도마다 다른 32바이트 키여야 한다: %x %x", aes, hmac)
	}
	if bytes.Equal(aes, master.value) || !bytes.Equal(aes, master.derive(purposeLoginKeyAES)) {
		t.Error("마스터 키 그대로가 아니고, 같은 입력엔 같은 키여야 한다")
	}
	if bytes.Equal(aes, other.derive(purposeLoginKeyAES)) {
		t.Error("마스터 키가 다르면 파생 키도 달라야 한다")
	}
}
