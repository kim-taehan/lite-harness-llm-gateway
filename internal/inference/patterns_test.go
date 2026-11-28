package inference

import "testing"

// matchedBy 는 기본 패턴이 text 안에서 (검증까지 거쳐) 무언가를 잡는지다
func matchedBy(t *testing.T, name, text string) bool {
	t.Helper()
	p, ok := findPrebuilt(name)
	if !ok {
		t.Fatalf("기본 패턴 %q 가 없다", name)
	}
	for _, m := range p.re.FindAllString(text, -1) {
		if p.valid == nil || p.valid(m) {
			return true
		}
	}
	return false
}

func TestPrebuiltPatternsCatchAndSpare(t *testing.T) {
	cases := []struct {
		pattern, text string
		want          bool
	}{
		{"kr_rrn", "주민번호 900101-1234567 입니다", true},
		{"kr_rrn", "9001011234567", true},
		{"kr_rrn", "홍길동900101-2345678", true},
		{"kr_rrn", "991341-1234567", false}, // 13월
		{"kr_rrn", "900101-9234567", false}, // 성별 자리 9
		{"kr_rrn", "주문번호 12345678901234", false},

		{"kr_phone", "010-1234-5678 로 연락", true},
		{"kr_phone", "01012345678", true},
		{"kr_phone", "02-123-4567", true},
		{"kr_phone", "031-123-4567", true},
		{"kr_phone", "+82 10-1234-5678", true},
		{"kr_phone", "버전 2024-11-28", false},

		{"kr_business_number", "사업자 220-81-62517", true}, // 국세청 예시 번호(삼성전자)
		{"kr_business_number", "220-81-62518", false},    // 검증 숫자 불일치

		{"kr_passport", "여권 M12345678", true},
		{"kr_passport", "M123A4567", true},
		{"kr_passport", "ABC12345678", false},

		{"email", "문의 a.b+c@corp.local 으로", true},
		{"email", "골뱅이 없음", false},

		{"credit_card", "카드 4111 1111 1111 1111", true},  // Luhn 통과
		{"credit_card", "카드 4111 1111 1111 1112", false}, // Luhn 실패
		{"credit_card", "9490-0000-0000-0009", false},    // 9로 시작하지만 Luhn 실패
		{"credit_card", "일련번호 1234567890123456", false},  // 카드 접두어가 아님

		{"aws_access_key", "AKIAIOSFODNN7EXAMPLE", true},
		{"github_token", "ghp_" + "abcdefghijklmnopqrstuvwxyzABCDEFGH12", true},
		{"openai_api_key", "키는 sk-abcdefghijklmnopqrstuvwx 입니다", true},
		{"openai_api_key", "sk-short", false},
		{"private_key", "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----", true},
		{"private_key", "-----BEGIN CERTIFICATE-----\nMIIabc\n-----END CERTIFICATE-----", false},

		{"ipv4", "서버 10.20.30.40 에 접속", true},
		{"ipv4", "버전 1.2.3", false},
		{"url", "https://corp.local/a?b=1 참고", true},
	}
	for _, c := range cases {
		if got := matchedBy(t, c.pattern, c.text); got != c.want {
			t.Errorf("%s / %q: 잡힘=%v, 기대=%v", c.pattern, c.text, got, c.want)
		}
	}
}

func TestBusinessNumberChecksum(t *testing.T) {
	if !validBusinessNumber("220-81-62517") {
		t.Error("올바른 사업자등록번호를 거절했다")
	}
	if validBusinessNumber("123-45-67890") {
		t.Error("검증 숫자가 틀린 번호를 받았다")
	}
}
