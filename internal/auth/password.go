package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 비밀번호는 표준 라이브러리 PBKDF2-SHA256 으로 저장한다(설계 6장, 외부 패키지 없음).
// 저장 문자열 모양: pbkdf2_sha256$<반복 횟수>$<소금 base64>$<해시 base64>. 반복 횟수를 함께 남겨 나중에 올려도 옛 해시를 읽는다
const (
	passwordIterations = 600_000 // OWASP 2023 권고값
	passwordSaltBytes  = 16
	passwordKeyBytes   = 32
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyBytes)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", passwordIterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// PasswordPolicyError 는 비밀번호 정책 위반이다. Message 는 litellm 문구 그대로다(400 validation_error, param=password)
type PasswordPolicyError struct{ Message string }

func (e *PasswordPolicyError) Error() string { return e.Message }

// passwordMinLength 는 litellm 기본 정책이다(password_policy.py): 12자 이상, 대문자·소문자·숫자·특수 문자 각각.
// litellm 이 기본으로 켜는 유출 비밀번호 검사(haveibeenpwned 호출)는 폐쇄망이라 하지 않는다(설계 9장)
const passwordMinLength = 12

func checkPasswordPolicy(password string) error {
	var upper, lower, digit, special bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			special = true
		}
	}
	var violations []string
	for _, c := range []struct {
		failed bool
		text   string
	}{
		{utf8.RuneCountInString(password) < passwordMinLength, "be at least 12 characters long"},
		{!upper, "include an uppercase letter"},
		{!lower, "include a lowercase letter"},
		{!digit, "include a number"},
		{!special, "include a special character"},
	} {
		if c.failed {
			violations = append(violations, c.text)
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return &PasswordPolicyError{Message: "Password does not meet the required policy: must " + strings.Join(violations, ", ") + "."}
}

// verifyPassword 는 저장 문자열과 맞는지 본다. 모양이 틀린 저장값은 맞지 않는 것으로 본다
func verifyPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}
