package inference

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// 가드레일(litellm litellm_content_filter 와 같은 설정 모양). 요청·응답의 글을 정규식·금지어로 검사해 막거나(BLOCK) 가린다(MASK).
// 걸린 글의 내용은 어디에도 남기지 않고 패턴 이름만 쓴다

const guardrailProvider = "litellm_content_filter"

// guardrailModes 는 우리가 동작시키는 검사 시점이다. during_call 은 질문 검사와 같게 처리한다.
// litellm 의 나머지 시점(logging_only·MCP·realtime)은 대응하는 기능이 없어 받지 않는다
var guardrailModes = []string{"pre_call", "post_call", "during_call"}

// GuardrailModes 는 관리 화면에 알려 줄 지원 시점이다
func GuardrailModes() []string { return slices.Clone(guardrailModes) }

const (
	actionBlock = "BLOCK"
	actionMask  = "MASK"
)

// 설정 하나가 담을 수 있는 크기의 상한이다. 요청마다 모두 돌리므로 관리자의 실수가 게이트웨이를 느리게 만들지 않게 한다
const (
	maxGuardrailRules   = 200
	maxRegexLength      = 1000
	maxKeywordLength    = 200
	defaultPatternMask  = "[{pattern_name}_REDACTED]"
	defaultKeywordMask  = "[KEYWORD_REDACTED]"
	patternNamePlacehld = "{pattern_name}"
)

// ConfigError 는 가드레일 설정이 잘못됐다는 오류다. 메시지는 관리 화면에 그대로 보인다
type ConfigError string

func (e ConfigError) Error() string { return string(e) }

func configErrorf(format string, args ...any) error { return ConfigError(fmt.Sprintf(format, args...)) }

// Guardrail 는 검사 규칙 한 묶음이다. 만든 뒤에는 바뀌지 않아 여러 요청이 함께 쓴다
type Guardrail struct {
	ID        string
	Name      string
	DefaultOn bool
	modes     []string
	patterns  []patternRule
	words     []wordRule
	// 가릴 때 쓰는 글. 패턴은 {pattern_name} 자리에 패턴 이름(대문자)이 들어간다
	patternMask, keywordMask string
}

type patternRule struct {
	label  string
	re     *regexp.Regexp
	valid  func(string) bool
	action string
}

type wordRule struct {
	keyword     string
	re          *regexp.Regexp
	description string
	action      string
}

// Violation 은 BLOCK 규칙에 걸린 것이다
type Violation struct {
	Kind        string // "pattern" | "keyword"
	Name        string // 패턴 이름 또는 금지어
	Description string
}

func (g *Guardrail) runsPre() bool {
	return slices.Contains(g.modes, "pre_call") || slices.Contains(g.modes, "during_call")
}

func (g *Guardrail) runsPost() bool { return slices.Contains(g.modes, "post_call") }

// ParseGuardrail 은 대시보드가 보낸 litellm_params 를 검사 규칙으로 만든다. 잘못되면 ConfigError 다
func ParseGuardrail(id, name string, params map[string]any) (*Guardrail, error) {
	if strings.TrimSpace(name) == "" {
		return nil, configErrorf("guardrail_name is required")
	}
	if provider, _ := params["guardrail"].(string); provider != guardrailProvider {
		return nil, configErrorf("Unsupported guardrail %q: only %s is supported", provider, guardrailProvider)
	}
	modes, err := parseModes(params["mode"])
	if err != nil {
		return nil, err
	}
	g := &Guardrail{ID: id, Name: name, modes: modes, patternMask: stringOr(params["pattern_redaction_format"], defaultPatternMask),
		keywordMask: stringOr(params["keyword_redaction_tag"], defaultKeywordMask)}
	g.DefaultOn, _ = params["default_on"].(bool)
	if categories, _ := params["categories"].([]any); len(categories) > 0 {
		return nil, configErrorf("Content categories are not supported: use patterns or blocked words")
	}
	if err := g.parsePatterns(params["patterns"]); err != nil {
		return nil, err
	}
	if err := g.parseWords(params["blocked_words"]); err != nil {
		return nil, err
	}
	if len(g.patterns)+len(g.words) == 0 {
		return nil, configErrorf("Configure at least one pattern or blocked word")
	}
	return g, nil
}

func stringOr(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

func parseModes(v any) ([]string, error) {
	var raw []any
	switch m := v.(type) {
	case string:
		raw = []any{m}
	case []any:
		raw = m
	}
	if len(raw) == 0 {
		return nil, configErrorf("mode is required (one of %s)", strings.Join(guardrailModes, ", "))
	}
	modes := make([]string, 0, len(raw))
	for _, item := range raw {
		mode, _ := item.(string)
		mode = strings.ToLower(strings.TrimSpace(mode))
		if !slices.Contains(guardrailModes, mode) {
			return nil, configErrorf("Unsupported mode %q: supported modes are %s", mode, strings.Join(guardrailModes, ", "))
		}
		modes = append(modes, mode)
	}
	return modes, nil
}

func parseAction(v any) (string, error) {
	action, _ := v.(string)
	switch strings.ToUpper(action) {
	case "", actionBlock:
		return actionBlock, nil
	case actionMask:
		return actionMask, nil
	}
	return "", configErrorf("Unsupported action %q: use BLOCK or MASK", action)
}

func (g *Guardrail) parsePatterns(v any) error {
	list, _ := v.([]any)
	if len(list) > maxGuardrailRules {
		return configErrorf("Too many patterns (max %d)", maxGuardrailRules)
	}
	for _, item := range list {
		entry, _ := item.(map[string]any)
		action, err := parseAction(entry["action"])
		if err != nil {
			return err
		}
		rule, err := patternFromEntry(entry)
		if err != nil {
			return err
		}
		rule.action = action
		g.patterns = append(g.patterns, rule)
	}
	return nil
}

// patternFromEntry 는 기본 패턴(pattern_name)이거나 직접 쓴 정규식(pattern)이다. 직접 쓴 정규식은 litellm 처럼 대소문자를 가리지 않는다
func patternFromEntry(entry map[string]any) (patternRule, error) {
	kind, _ := entry["pattern_type"].(string)
	name, _ := entry["pattern_name"].(string)
	source, _ := entry["pattern"].(string)
	if kind == "prebuilt" || (kind == "" && name != "" && source == "") {
		built, ok := findPrebuilt(name)
		if !ok {
			return patternRule{}, configErrorf("Unknown prebuilt pattern %q", name)
		}
		return patternRule{label: built.name, re: built.re, valid: built.valid}, nil
	}
	if source == "" {
		return patternRule{}, configErrorf("A pattern needs pattern_name (prebuilt) or pattern (regex)")
	}
	if len(source) > maxRegexLength {
		return patternRule{}, configErrorf("Regex is too long (max %d characters)", maxRegexLength)
	}
	re, err := regexp.Compile("(?i)" + source)
	if err != nil {
		return patternRule{}, configErrorf("Invalid regex %q: %v", source, err)
	}
	label, _ := entry["name"].(string)
	return patternRule{label: stringOr(label, "custom_regex"), re: re}, nil
}

func (g *Guardrail) parseWords(v any) error {
	list, _ := v.([]any)
	if len(list) > maxGuardrailRules {
		return configErrorf("Too many blocked words (max %d)", maxGuardrailRules)
	}
	for _, item := range list {
		entry, _ := item.(map[string]any)
		keyword, _ := entry["keyword"].(string)
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			return configErrorf("A blocked word needs a keyword")
		}
		if len(keyword) > maxKeywordLength {
			return configErrorf("Keyword is too long (max %d characters)", maxKeywordLength)
		}
		action, err := parseAction(entry["action"])
		if err != nil {
			return err
		}
		description, _ := entry["description"].(string)
		// litellm 처럼 대소문자를 가리지 않는 부분 문자열이고, *는 글자 0~1개다
		source := strings.ReplaceAll(regexp.QuoteMeta(strings.ToLower(keyword)), `\*`, ".?")
		g.words = append(g.words, wordRule{keyword: strings.ToLower(keyword), re: regexp.MustCompile("(?i)" + source), description: description, action: action})
	}
	return nil
}

// Inspect 는 글을 검사한다. BLOCK 규칙에 걸리면 그 위반을, 아니면 MASK 규칙으로 가린 글을 돌려준다.
// 가리기 전의 글로 BLOCK 을 먼저 모두 본다(앞선 가리기가 뒤의 차단 대상을 지우지 못하게)
func (g *Guardrail) Inspect(text string) (string, *Violation) {
	for _, rule := range g.patterns {
		if rule.action == actionBlock && rule.matches(text) {
			return text, &Violation{Kind: "pattern", Name: rule.label}
		}
	}
	for _, word := range g.words {
		if word.action == actionBlock && word.re.MatchString(text) {
			return text, &Violation{Kind: "keyword", Name: word.keyword, Description: word.description}
		}
	}
	for _, rule := range g.patterns {
		if rule.action == actionMask {
			text = rule.mask(text, strings.ReplaceAll(g.patternMask, patternNamePlacehld, strings.ToUpper(rule.label)))
		}
	}
	for _, word := range g.words {
		if word.action == actionMask {
			text = word.re.ReplaceAllLiteralString(text, g.keywordMask)
		}
	}
	return text, nil
}

func (r patternRule) matches(text string) bool {
	for _, m := range r.re.FindAllString(text, -1) {
		if r.valid == nil || r.valid(m) {
			return true
		}
	}
	return false
}

func (r patternRule) mask(text, replacement string) string {
	return r.re.ReplaceAllStringFunc(text, func(m string) string {
		if r.valid != nil && !r.valid(m) {
			return m
		}
		return replacement
	})
}
