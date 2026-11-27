package inference

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"llm-gateway/internal/core"
)

// withoutProviders 는 예산을 다 쓴 프로바이더의 배포를 뺀 목록이다
func withoutProviders(all []Deployment, exhausted map[string]*time.Time) []Deployment {
	return slices.DeleteFunc(slices.Clone(all), func(d Deployment) bool {
		_, over := exhausted[d.Provider]
		return over
	})
}

// providerBudgetExceeded 는 이 모델을 맡은 배포가 모두 예산을 다 쓴 프로바이더의 것일 때의 429 다.
// retry-after 는 그 프로바이더들 중 가장 이른 리셋 시각까지 남은 초이고, 리셋 주기를 정하지 않았으면 60초다.
// 문구는 litellm 라우터의 "No deployments available" 계열을 본떴지만 원문은 확인하지 못했다
func providerBudgetExceeded(path, model string, all []Deployment, exhausted map[string]*time.Time, now time.Time) core.ProxyError {
	var providers []string
	retry := 0
	for _, d := range all {
		resetAt, over := exhausted[d.Provider]
		if d.ModelName != model || !over || slices.Contains(providers, d.Provider) {
			continue
		}
		providers = append(providers, d.Provider)
		if resetAt != nil {
			if wait := max(1, int(resetAt.Sub(now).Seconds())); retry == 0 || wait < retry {
				retry = wait
			}
		}
	}
	slices.Sort(providers)
	if retry == 0 {
		retry = 60
	}
	return core.ProxyError{Status: http.StatusTooManyRequests, Type: "rate_limit_error", Param: nil,
		Message: fmt.Sprintf("%s: No deployments available for model=%s. Provider budget exceeded for %s. Try again in %d seconds.",
			path, model, pyQuotedList(providers), retry),
		Headers: map[string]string{"retry-after": strconv.Itoa(retry)}}
}

func pyQuotedList(items []string) string {
	out := "["
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += "'" + item + "'"
	}
	return out + "]"
}
