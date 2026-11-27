package admin

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/limits"
)

// keyRow 는 litellm LiteLLM_VerificationToken 행 모양이다(J 2.3). 한도·사용액은 limits 값이다
func keyRow(k auth.Key, p limits.Policy) row {
	return row{
		"token":                  k.Token,
		"key_name":               k.KeyName,
		"key_alias":              k.KeyAlias,
		"spend":                  p.Spend,
		"expires":                utc(k.Expires),
		"models":                 nonNil(k.Models),
		"aliases":                map[string]any{},
		"config":                 map[string]any{},
		"user_id":                k.UserID,
		"team_id":                k.TeamID,
		"permissions":            map[string]any{},
		"max_parallel_requests":  p.MaxParallelRequests,
		"metadata":               k.Metadata,
		"blocked":                k.Blocked,
		"tpm_limit":              p.TPMLimit,
		"tpd_limit":              p.TPDLimit,
		"rpm_limit":              p.RPMLimit,
		"max_budget":             p.MaxBudget,
		"budget_duration":        p.BudgetDuration,
		"budget_reset_at":        utc(p.BudgetResetAt),
		"allowed_cache_controls": []string{},
		"allowed_routes":         nonNil(k.AllowedRoutes),
		"model_spend":            map[string]any{},
		"model_max_budget":       modelBudgetsView(p.ModelBudgets),
		"budget_id":              nil,
		"organization_id":        nil,
		"project_id":             nil,
		"key_type":               k.KeyType,
		"created_at":             k.CreatedAt.UTC(),
		"created_by":             k.CreatedBy,
		"updated_at":             k.UpdatedAt.UTC(),
		"updated_by":             k.UpdatedBy,
		"last_active":            nil,
		"litellm_budget_table":   nil,
		"object_permission":      nil,
	}
}

// keyStatus 는 litellm /key/info 의 status 다: blocked → revoked, 만료 → expired, 나머지 active
func keyStatus(k auth.Key, now time.Time) string {
	switch {
	case k.Blocked != nil && *k.Blocked:
		return "revoked"
	case k.Expires != nil && k.Expires.Before(now):
		return "expired"
	default:
		return "active"
	}
}

func (b rows) keyRows(ctx context.Context, keys []auth.Key) ([]row, error) {
	ids := make([]string, len(keys))
	for i, k := range keys {
		ids[i] = k.Token
	}
	policies, err := b.limits.Find(ctx, limits.ScopeKey, ids)
	if err != nil {
		return nil, err
	}
	result := make([]row, len(keys))
	for i, k := range keys {
		result[i] = keyRow(k, policies[k.Token])
	}
	return result, nil
}

func (b rows) keyRowOf(ctx context.Context, k auth.Key) (row, error) {
	r, err := b.keyRows(ctx, []auth.Key{k})
	if err != nil {
		return nil, err
	}
	return r[0], nil
}

// litellm duration 파서(DP:17-82): 정규식 (\d+)(mo|[smhdw]?) 을 앞에서부터 맞추고, 단어 별칭도 받는다.
// re.match 라서 뒤에 붙은 글자는 무시된다(litellm 과 같게 둔다)
var durationPrefix = regexp.MustCompile(`^(\d+)(mo|[smhdw]?)`)

var durationWords = map[string]string{"hourly": "1h", "daily": "24h", "weekly": "7d", "monthly": "30d"}

// durationError 는 litellm 이 ValueError 로 내는 기간 오류다. litellm 은 이것을 500 으로 돌려준다(J 2.2)
type durationError struct{ message string }

func (e *durationError) Error() string { return e.message }

func expiresAfter(duration string, now time.Time) (time.Time, error) {
	if word, ok := durationWords[duration]; ok {
		duration = word
	}
	match := durationPrefix.FindStringSubmatch(duration)
	if match == nil {
		return time.Time{}, &durationError{"Invalid duration format"}
	}
	n, _ := strconv.Atoi(match[1])
	switch match[2] {
	case "s":
		return now.Add(time.Duration(n) * time.Second), nil
	case "m":
		return now.Add(time.Duration(n) * time.Minute), nil
	case "h":
		return now.Add(time.Duration(n) * time.Hour), nil
	case "d":
		return now.AddDate(0, 0, n), nil
	case "w":
		return now.AddDate(0, 0, 7*n), nil
	case "mo":
		return now.AddDate(0, n, 0), nil
	default:
		return time.Time{}, &durationError{fmt.Sprintf("Unsupported duration unit, passed duration: %s", duration)}
	}
}

// litellm is_valid_api_key(PU:9219-9235): sk- 키나 sha256 hex, 길이 3..100
var validKeyPattern = regexp.MustCompile(`^(sk-[A-Za-z0-9_-]+|[a-fA-F0-9]{64})$`)

func validKeyFormat(key string) bool {
	return len(key) >= 3 && len(key) <= 100 && validKeyPattern.MatchString(key)
}
