package admin

import (
	"reflect"
	"strings"
	"testing"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// DB·네트워크 없이 도는 단위 시험: 목록 도우미, 팀 admin 의 설정 수정 판정, 사용량 합계 키

func TestMatchesIn_AndSortHubRows(t *testing.T) {
	if !matchesIn("", []string{"a"}) || !matchesIn("a,b", []string{"b"}) || matchesIn("a,b", []string{"c"}) || matchesIn("a", nil) {
		t.Error("matchesIn 은 비면 통과, 하나라도 겹치면 통과")
	}
	rows := []row{{"model_group": "b"}, {"model_group": "a"}, {"model_group": "c"}}
	sortHubRows(rows, "-model_group")
	if rows[0]["model_group"] != "c" || rows[2]["model_group"] != "a" {
		t.Errorf("내림차순 = %v", rows)
	}
	tokens, other := int64(5), int64(9)
	numbers := []row{{"max_input_tokens": &other}, {"max_input_tokens": (*int64)(nil)}, {"max_input_tokens": &tokens}}
	sortHubRows(numbers, "max_input_tokens")
	if numbers[0]["max_input_tokens"] != (*int64)(nil) || numbers[1]["max_input_tokens"] != &tokens {
		t.Errorf("값 없는 행이 앞, 숫자는 오름차순이어야 한다: %v", numbers)
	}
}

func TestPageOf_ClampsRange(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	for _, c := range []struct {
		offset, limit int
		want          []int
	}{{0, 2, []int{1, 2}}, {4, 10, []int{5}}, {9, 2, nil}, {-3, 2, []int{1, 2}}, {2, 0, nil}} {
		if got := pageOf(items, c.offset, c.limit); len(got) != len(c.want) || (len(got) > 0 && got[0] != c.want[0]) {
			t.Errorf("pageOf(%d,%d) = %v, 기대 %v", c.offset, c.limit, got, c.want)
		}
	}
}

func TestTeamAdminEditDenial(t *testing.T) {
	budget, soft := 10.0, 3.0
	team := auth.Team{TeamID: "t1", Models: []string{"m1"}}
	policy := limits.Policy{MaxBudget: &budget, SoftBudget: &soft}
	lower, higher := 5.0, 50.0
	var none []string
	cases := []struct {
		name      string
		req       teamUpdate
		permitted []string
		denied    string // 거절 문구에 들어 있어야 하는 것. 빈 문자열이면 허용
	}{
		{"설정이 비면 아무것도 못 바꾼다", teamUpdate{}, none, "cannot edit team settings"},
		{"열어 준 필드 수정", teamUpdate{limitFields: limitFields{TPMLimit: core.To(int64(100))}}, []string{"tpm_limit"}, ""},
		{"열지 않은 필드", teamUpdate{TeamAlias: core.To("새 이름")}, []string{"tpm_limit"}, "'team_alias'"},
		{"그대로 다시 보낸 값은 바뀐 게 아니다", teamUpdate{Models: core.To([]string{"m1"}), limitFields: limitFields{MaxBudget: core.To(budget)}}, []string{"max_budget"}, ""},
		{"예산 낮추기", teamUpdate{limitFields: limitFields{MaxBudget: core.To(lower)}}, []string{"max_budget"}, ""},
		{"예산 올리기는 raise_max_budget 이 필요", teamUpdate{limitFields: limitFields{MaxBudget: core.To(higher)}}, []string{"max_budget"}, "raise_max_budget"},
		{"예산 올리기 허용", teamUpdate{limitFields: limitFields{MaxBudget: core.To(higher)}}, []string{"max_budget", "raise_max_budget"}, ""},
		{"차단은 열 수 없다", teamUpdate{Blocked: core.To(true)}, []string{"tpm_limit", "blocked"}, "'blocked'"},
		// 나중에 더한 필드도 설정에 열려 있지 않으면 팀 admin 이 바꿀 수 없다(검사에서 빠져 있던 구멍)
		{"하루 토큰 한도", teamUpdate{limitFields: limitFields{TPDLimit: core.To(int64(9))}}, []string{"tpm_limit"}, "'tpd_limit'"},
		{"모델별 한도", teamUpdate{limitFields: limitFields{ModelMaxBudget: core.To(map[string]limits.ModelBudget{"m1": {}})}}, []string{"tpm_limit"}, "'model_max_budget'"},
		{"조직 변경", teamUpdate{OrganizationID: core.To("org-1")}, []string{"tpm_limit"}, "'organization_id'"},
	}
	for _, c := range cases {
		got := teamAdminEditDenial(c.req, team, policy, c.permitted)
		if (c.denied == "") != (got == "") || (c.denied != "" && !strings.Contains(got, c.denied)) {
			t.Errorf("%s: %q (기대 %q)", c.name, got, c.denied)
		}
	}
}

// 감사 기록에는 비밀 값을 담는 필드를 어떤 깊이에서도 남기지 않고, max_tokens 처럼 이름만 비슷한 값은 남긴다
func TestScrubbed_RemovesSecretFieldsAtAnyDepthAndKeepsTheRest(t *testing.T) {
	got := scrubbed(map[string]any{
		"model_name": "m1", "max_tokens": 100.0, "key": "sk-x", "metadata": map[string]any{"owner": "kim", "client_secret": "s"},
		"litellm_params": map[string]any{"api_key": "sk-up", "aws_secret_access_key": "a", "vertex_api_key": "v", "api_base": "http://x",
			"nested": []any{map[string]any{"password": "p", "name": "keep"}}},
	})
	want := map[string]any{
		"model_name": "m1", "max_tokens": 100.0, "metadata": map[string]any{"owner": "kim"},
		"litellm_params": map[string]any{"api_base": "http://x", "nested": []any{map[string]any{"name": "keep"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scrubbed = %v\n기대   = %v", got, want)
	}
}

func TestTotalsRow_NamesMatchLitellmMetadata(t *testing.T) {
	totals := totalsRow(limits.Metrics{PromptTokens: 3, CompletionTokens: 4, SuccessfulRequests: 1, FailedRequests: 1, TotalResponseTimeMS: 90, TimedRequests: 1})
	for _, key := range []string{"total_spend", "total_tokens", "total_api_requests", "total_response_time_ms", "total_timed_requests", "total_prompt_tokens"} {
		if _, ok := totals[key]; !ok {
			t.Errorf("%s 가 없다: %v", key, totals)
		}
	}
	if _, bad := totals["total_total_tokens"]; bad || totals["total_tokens"] != int64(7) || totals["total_api_requests"] != int64(2) {
		t.Errorf("합계 = %v", totals)
	}
}
