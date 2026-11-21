package app

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 팀원별 한도(litellm 팀 멤버십 예산): 저장·집행·사용액·초기화·삭제

func TestTeamMemberLimits_EnforcedPerMemberAndClearedWithMembership(t *testing.T) {
	upstream, _ := fakeOllama(t)
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL},
		"model_info": map[string]any{"input_cost_per_token": 0.001, "output_cost_per_token": 0.002}})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	for _, user := range []string{"lee", "park"} {
		api.ok("POST", "/user/new", map[string]any{"user_id": user, "user_role": "internal_user"})
		api.ok("POST", "/team/member_add", map[string]any{"team_id": "t1", "member": map[string]any{"user_id": user, "role": "user"}})
	}
	leeKey := api.ok("POST", "/key/generate", map[string]any{"user_id": "lee", "team_id": "t1"})["key"].(string)
	parkKey := api.ok("POST", "/key/generate", map[string]any{"user_id": "park", "team_id": "t1"})["key"].(string)
	api.ok("POST", "/team/member_update", map[string]any{"team_id": "t1", "user_id": "lee", "max_budget_in_team": 0.005, "rpm_limit": 5})

	chat := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	memberSpend := func() float64 {
		var spend float64
		deadline := time.Now().Add(3 * time.Second)
		for { // 배치가 쓰일 때까지 기다린다(시험의 배치 주기는 20ms)
			err := pool.QueryRow(t.Context(), "SELECT spend::float8 FROM limit_policy WHERE scope = 'team_member' AND target_id = 't1:lee'").Scan(&spend)
			if (err == nil && spend > 0) || time.Now().After(deadline) {
				return spend
			}
			<-time.After(20 * time.Millisecond)
		}
	}

	first := chat(leeKey)
	if first.Code != 200 || first.Header().Get("x-ratelimit-team_member-limit-requests") != "5" {
		t.Fatalf("첫 요청: %d %v", first.Code, first.Header())
	}
	if spend := memberSpend(); spend < 0.006 {
		t.Fatalf("팀원 사용액이 쌓이지 않았다: %v", spend)
	}
	// 팀 상세 화면의 팀원 표가 읽는 값: 예산 표와 사용액
	var leeShip map[string]any
	for _, m := range api.ok("GET", "/team/info?team_id=t1", nil)["team_memberships"].([]any) {
		if m.(map[string]any)["user_id"] == "lee" {
			leeShip = m.(map[string]any)
		}
	}
	table, _ := leeShip["litellm_budget_table"].(map[string]any)
	if table["max_budget"] != 0.005 || table["rpm_limit"] != 5.0 || leeShip["budget_source"] != "member" || leeShip["spend"].(float64) < 0.006 {
		t.Errorf("team/info 의 팀원 예산 = %v", leeShip)
	}
	if other := api.ok("GET", "/team/info?team_id=t1", nil)["team_memberships"].([]any); len(other) < 2 {
		t.Errorf("팀원 행 = %v", other)
	}
	over := chat(leeKey)
	if over.Code != 422 || !strings.Contains(over.Body.String(), "Budget has been exceeded! User=lee in Team=t1 Current cost:") {
		t.Errorf("예산 초과: %d %s", over.Code, over.Body)
	}
	if rec := chat(parkKey); rec.Code != 200 || rec.Header().Get("x-ratelimit-team_member-limit-requests") != "" {
		t.Errorf("한도 없는 다른 팀원이 막혔다: %d %v", rec.Code, rec.Header())
	}

	// 사용액을 되돌리면 다시 쓸 수 있다. 사용액보다 큰 값으로는 못 바꾼다
	if status, _ := api.do("POST", "/team/t1/member/lee/reset_spend", map[string]any{"reset_to": 9}); status != 400 {
		t.Errorf("사용액보다 큰 reset_to: %d", status)
	}
	reset := api.ok("POST", "/team/t1/member/lee/reset_spend", map[string]any{"reset_to": 0})
	if reset["spend"] != 0.0 || reset["previous_spend"].(float64) < 0.006 || reset["max_budget"] != 0.005 {
		t.Errorf("reset_spend = %v", reset)
	}
	if rec := chat(leeKey); rec.Code != 200 {
		t.Errorf("사용액을 되돌린 뒤: %d %s", rec.Code, rec.Body)
	}

	// 예산 초기화는 한도를 지운다(사용액은 그대로라 예산이 있었다면 막혔을 값이다)
	_ = memberSpend()
	cleared := api.ok("POST", "/team/t1/member/lee/reset_budget", nil)
	if cleared["budget_source"] != "none" || cleared["previous_budget_id"] == nil {
		t.Errorf("reset_budget = %v", cleared)
	}
	if rec := chat(leeKey); rec.Code != 200 {
		t.Errorf("한도를 지운 뒤: %d %s", rec.Code, rec.Body)
	}

	// 팀원을 빼면 한도·사용액도 지워져 다시 들어와도 물려받지 않는다
	api.ok("POST", "/team/member_update", map[string]any{"team_id": "t1", "user_id": "lee", "max_budget_in_team": 3})
	api.ok("POST", "/team/member_delete", map[string]any{"team_id": "t1", "user_id": "lee"})
	var left int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM limit_policy WHERE scope = 'team_member' AND target_id = 't1:lee'").Scan(&left); err != nil || left != 0 {
		t.Errorf("팀원을 뺐는데 한도 행이 %d 개 남았다 (%v)", left, err)
	}
}

func TestTeamMemberLimits_BulkAddAppliesBudgetAndInvalidValuesAreRejected(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	api.ok("POST", "/user/new", map[string]any{"user_id": "lee", "user_role": "internal_user"})

	added := api.ok("POST", "/team/bulk_member_add", map[string]any{"team_id": "t1", "members": []any{map[string]any{"user_id": "lee", "role": "user"}}, "max_budget_in_team": 2.5})
	if added["successful_additions"] != 1.0 {
		t.Fatalf("bulk_member_add = %v", added)
	}
	bulk := api.ok("POST", "/management/v1/teams/t1/members/bulk_update", map[string]any{"members": []any{map[string]any{"user_id": "lee", "tpm_limit": 100}}})
	row := bulk["data"].([]any)[0].(map[string]any)
	if row["max_budget"] != 2.5 || row["tpm_limit"] != 100.0 || row["max_budget_source"] != "member" {
		t.Errorf("일괄 추가로 정한 예산 위에 병합돼야 한다: %v", row)
	}
	if status, body := api.do("POST", "/team/member_update", map[string]any{"team_id": "t1", "user_id": "lee", "budget_duration": "soon"}); status != 400 {
		t.Errorf("틀린 budget_duration: %d %v", status, body)
	}
}
