package inference

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// 콘텐츠 필터가 기본으로 주는 패턴이다. litellm patterns.json 에 있는 이름(email, credit_card …)은 같은 이름·정규식을 쓰고,
// litellm 에 없는 한국 개인정보·비밀값은 우리가 더한다(kr_*, openai_api_key, private_key)

// prebuiltPattern 은 대시보드가 고르는 기본 패턴 하나다. valid 가 있으면 정규식에 걸린 것 가운데 그것이 참인 것만 잡는다(오탐을 줄인다)
type prebuiltPattern struct {
	name, displayName, category, description string
	re                                       *regexp.Regexp
	valid                                    func(match string) bool
}

const (
	categoryKoreanPII  = "Korean PII Patterns"
	categoryPII        = "PII Patterns"
	categoryPayment    = "Payment Card Patterns"
	categoryCredential = "Credential Patterns"
	categoryNetwork    = "Network Patterns"
)

var prebuiltPatterns = []prebuiltPattern{
	{name: "kr_rrn", displayName: "주민등록번호·외국인등록번호", category: categoryKoreanPII,
		description: "YYMMDD-NNNNNNN. 생년월일과 성별 자리(1-8)가 맞는 것만",
		re:          regexp.MustCompile(`\b\d{6}[\s-]?[1-8]\d{6}\b`), valid: validRRN},
	{name: "kr_phone", displayName: "전화번호(휴대·유선)", category: categoryKoreanPII,
		description: "010-1234-5678, 02-123-4567, +82 10-1234-5678",
		re:          regexp.MustCompile(`(?:\+82[\s-]?|\b0)(?:1[016789]|2|[3-6][1-5]|70)[\s.-]?\d{3,4}[\s.-]?\d{4}\b`)},
	{name: "kr_passport", displayName: "여권번호", category: categoryKoreanPII,
		description: "M12345678, M123A4567",
		re:          regexp.MustCompile(`\b[MSRODG](?:\d{8}|\d{3}[A-Z]\d{4})\b`)},
	{name: "kr_business_number", displayName: "사업자등록번호", category: categoryKoreanPII,
		description: "123-45-67890. 검증 숫자가 맞는 것만",
		re:          regexp.MustCompile(`\b\d{3}-\d{2}-\d{5}\b`), valid: validBusinessNumber},
	{name: "kr_driver_license", displayName: "운전면허번호", category: categoryKoreanPII,
		description: "11-12-123456-12",
		re:          regexp.MustCompile(`\b\d{2}-\d{2}-\d{6}-\d{2}\b`)},
	{name: "email", displayName: "Email Address", category: categoryPII, description: "Detects email addresses",
		re: regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`)},
	{name: "credit_card", displayName: "Credit Card (Any)", category: categoryPayment,
		description: "Detects any major credit card number. 국내 카드(9로 시작)도 잡고, Luhn 검사가 맞는 것만",
		re:          regexp.MustCompile(`\b(?:4\d{3}|5[1-5]\d{2}|6(?:011|5\d{2})|9\d{3})[\s-]?\d{4}[\s-]?\d{4}[\s-]?\d{4}\b|\b3[47]\d{2}[\s-]?\d{6}[\s-]?\d{5}\b`),
		valid:       validLuhn},
	{name: "aws_access_key", displayName: "AWS Access Key", category: categoryCredential, description: "Detects AWS access keys (AKIA...)",
		re: regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`)},
	{name: "aws_secret_key", displayName: "AWS Secret Key", category: categoryCredential, description: "Detects AWS secret keys (40 characters)",
		re: regexp.MustCompile(`\b([A-Za-z0-9/+=]{40})\b`)},
	{name: "github_token", displayName: "GitHub Token", category: categoryCredential, description: "Detects GitHub personal access tokens",
		re: regexp.MustCompile(`\b(gh[ps]_[a-zA-Z0-9]{36})\b`)},
	{name: "slack_token", displayName: "Slack Token", category: categoryCredential, description: "Detects Slack API tokens",
		re: regexp.MustCompile(`\b(xox[pboa]-[0-9]{10,13}-[0-9]{10,13}-[a-zA-Z0-9]{24,32})\b`)},
	{name: "generic_api_key", displayName: "API Key (Generic)", category: categoryCredential, description: "Detects generic API key patterns",
		re: regexp.MustCompile(`\b([Aa][Pp][Ii][-_]?[Kk][Ee][Yy][\s:=]+['"]?[A-Za-z0-9_\-]{20,}['"]?)\b`)},
	{name: "openai_api_key", displayName: "API Key (sk-…)", category: categoryCredential,
		description: "OpenAI·litellm 형식 키(sk-…). 이 게이트웨이의 가상 키도 이 모양이다",
		re:          regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{name: "private_key", displayName: "Private Key (PEM)", category: categoryCredential,
		description: "-----BEGIN … PRIVATE KEY----- 블록 전체",
		re:          regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{name: "ipv4", displayName: "IP Address (IPv4)", category: categoryNetwork, description: "Detects IPv4 addresses",
		re: regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`)},
	{name: "url", displayName: "URL", category: categoryNetwork, description: "Detects URLs (http/https)",
		re: regexp.MustCompile(`\b(?:https?://|www\.)[^\s/$.?#].[^\s]*\b`)},
}

// PatternInfo 는 대시보드 폼이 보여 줄 기본 패턴 한 줄이다
type PatternInfo struct{ Name, DisplayName, Category, Description string }

// PrebuiltPatternInfos 는 고를 수 있는 기본 패턴과 분류(보여 줄 순서)다
func PrebuiltPatternInfos() (patterns []PatternInfo, categories []string) {
	for _, p := range prebuiltPatterns {
		patterns = append(patterns, PatternInfo{Name: p.name, DisplayName: p.displayName, Category: p.category, Description: p.description})
		if !slices.Contains(categories, p.category) {
			categories = append(categories, p.category)
		}
	}
	return patterns, categories
}

func findPrebuilt(name string) (prebuiltPattern, bool) {
	for _, p := range prebuiltPatterns {
		if p.name == name {
			return p, true
		}
	}
	return prebuiltPattern{}, false
}

// digitsOf 는 숫자만 남긴다(구분자 제거)
func digitsOf(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// validRRN 은 생년월일이 있을 수 있는 날짜인지 본다. 2020년 10월 이후 번호는 뒷자리가 무작위라 검증 숫자는 보지 않는다
func validRRN(match string) bool {
	d := digitsOf(match)
	month, _ := strconv.Atoi(d[2:4])
	day, _ := strconv.Atoi(d[4:6])
	return month >= 1 && month <= 12 && day >= 1 && day <= 31
}

// validBusinessNumber 는 사업자등록번호 검증 숫자(국세청 가중치 1,3,7,1,3,7,1,3,5)다
func validBusinessNumber(match string) bool {
	d := digitsOf(match)
	weights := []int{1, 3, 7, 1, 3, 7, 1, 3, 5}
	sum := 0
	for i, w := range weights {
		sum += int(d[i]-'0') * w
	}
	sum += int(d[8]-'0') * 5 / 10
	return (10-sum%10)%10 == int(d[9]-'0')
}

// validLuhn 은 카드번호 Luhn 검사다
func validLuhn(match string) bool {
	d := digitsOf(match)
	sum := 0
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if (len(d)-1-i)%2 == 1 {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
	}
	return sum%10 == 0
}
