package auth

import (
	"bytes"
	"errors"
	"testing"
)

func boxFor(t *testing.T, master string) *SecretBox {
	t.Helper()
	key, err := NewMasterKey(master)
	if err != nil {
		t.Fatal(err)
	}
	return key.UpstreamKeyBox()
}

func TestSecretBox_RoundTripAndFreshNonceEachTime(t *testing.T) {
	box := boxFor(t, "sk-master")
	first, second := box.Seal("upstream-secret"), box.Seal("upstream-secret")

	if bytes.Equal(first, second) {
		t.Error("같은 값을 두 번 봉했는데 결과가 같다(nonce 가 재사용된다)")
	}
	if bytes.Contains(first, []byte("upstream-secret")) {
		t.Error("봉한 값에 평문이 그대로 들어 있다")
	}
	for _, sealed := range [][]byte{first, second} {
		if got, err := box.Open(sealed); err != nil || got != "upstream-secret" {
			t.Errorf("Open = %q, %v", got, err)
		}
	}
}

func TestSecretBox_CannotOpenWithOtherMasterKeyOrTamperedValue(t *testing.T) {
	sealed := boxFor(t, "sk-master").Seal("upstream-secret")

	if _, err := boxFor(t, "sk-other").Open(sealed); !errors.Is(err, ErrSecretUnreadable) {
		t.Errorf("다른 마스터 키로 열렸다: %v", err)
	}
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	if _, err := boxFor(t, "sk-master").Open(tampered); !errors.Is(err, ErrSecretUnreadable) {
		t.Errorf("변조한 값이 열렸다: %v", err)
	}
	if _, err := boxFor(t, "sk-master").Open([]byte("짧음")); !errors.Is(err, ErrSecretUnreadable) {
		t.Errorf("너무 짧은 값이 열렸다: %v", err)
	}
}
