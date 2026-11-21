package inference

import (
	"testing"

	"llm-gateway/internal/auth"
)

// DB·네트워크 없이 도는 단위 시험: 배포 선택, 업스트림 주소, 스트림 usage 청크, 모델 접근

func TestPick_FollowsWeights(t *testing.T) {
	candidates := []Deployment{{ID: "a", Weight: 1}, {ID: "b", Weight: 9}}
	counts := map[string]int{}
	for i := 0; i < 5000; i++ {
		counts[pick(candidates).ID]++
	}
	if counts["b"] < 4000 || counts["a"] < 100 {
		t.Errorf("weight 1:9 로 나눠져야 한다: %v", counts)
	}
	if only := pick([]Deployment{{ID: "x", Weight: 3}}); only.ID != "x" {
		t.Errorf("후보가 하나면 그것: %v", only)
	}
}

func TestEndpoint_AppendsV1OnlyWhenMissing(t *testing.T) {
	for base, want := range map[string]string{
		"http://ollama:11434":     "http://ollama:11434/v1/chat/completions",
		"http://ollama:11434/":    "http://ollama:11434/v1/chat/completions",
		"http://vllm:8000/v1":     "http://vllm:8000/v1/chat/completions",
		"http://vllm:8000/v1/":    "http://vllm:8000/v1/chat/completions",
		"http://gw.local/prefix":  "http://gw.local/prefix/v1/chat/completions",
		"http://gw.local/api/v1/": "http://gw.local/api/v1/chat/completions",
	} {
		if got := (Deployment{BaseURL: base}).endpoint("chat/completions"); got != want {
			t.Errorf("%s → %s (기대 %s)", base, got, want)
		}
	}
}

func TestRequestUsageChunk_OnlyForStreamingChat(t *testing.T) {
	chat := func(body map[string]any, stream bool) (bool, map[string]any) {
		wants := requestUsageChunk(body, apiChat, stream)
		return wants, body
	}
	if wants, body := chat(map[string]any{}, true); wants || body["stream_options"].(map[string]any)["include_usage"] != true {
		t.Errorf("클라이언트가 안 켰으면 우리가 켜고(include_usage), 클라이언트에는 숨긴다: %v %v", wants, body)
	}
	if wants, _ := chat(map[string]any{"stream_options": map[string]any{"include_usage": true}}, true); !wants {
		t.Error("클라이언트가 켰으면 usage 청크를 보여 준다")
	}
	if wants, body := chat(map[string]any{}, false); !wants || body["stream_options"] != nil {
		t.Errorf("스트림이 아니면 건드리지 않는다: %v %v", wants, body)
	}
	body := map[string]any{}
	if wants := requestUsageChunk(body, apiResponses, true); !wants || body["stream_options"] != nil {
		t.Errorf("Responses 는 stream_options 를 넣지 않는다: %v %v", wants, body)
	}
}

func TestModelDenied_KeyThenTeamWithWildcards(t *testing.T) {
	cases := []struct {
		name      string
		principal auth.Principal
		model     string
		denied    string // 빈 문자열이면 허용
	}{
		{"목록 없음은 모두 허용", auth.Principal{}, "m1", ""},
		{"키 목록에 있음", auth.Principal{KeyModels: []string{"m1"}}, "m1", ""},
		{"키 목록에 없음", auth.Principal{KeyModels: []string{"m2"}}, "m1", "key_model_access_denied"},
		{"와일드카드", auth.Principal{KeyModels: []string{"*"}}, "m1", ""},
		{"all-proxy-models", auth.Principal{KeyModels: []string{"all-proxy-models"}}, "m1", ""},
		{"all-team-models 는 팀 목록을 따른다", auth.Principal{KeyModels: []string{"all-team-models"}, TeamID: "t", TeamModels: []string{"m1"}}, "m1", ""},
		{"팀 목록에 없음", auth.Principal{TeamID: "t", TeamModels: []string{"m2"}}, "m1", "team_model_access_denied"},
		{"키는 통과해도 팀이 막는다", auth.Principal{KeyModels: []string{"m1"}, TeamID: "t", TeamModels: []string{"m2"}}, "m1", "team_model_access_denied"},
		{"팀 모델은 팀 키에만 적용", auth.Principal{TeamModels: []string{"m2"}}, "m1", ""},
	}
	for _, c := range cases {
		denial, denied := modelDenied(c.principal, c.model)
		if (c.denied != "") != denied || (denied && denial.Type != c.denied) {
			t.Errorf("%s: 거절=%v(%s), 기대 %q", c.name, denied, denial.Type, c.denied)
		}
	}
}

func TestMatchesIn_AndSortRows(t *testing.T) {
	if !matchesIn("", []string{"a"}) || !matchesIn("a,b", []string{"b"}) || matchesIn("a,b", []string{"c"}) || matchesIn("a", nil) {
		t.Error("matchesIn 은 비면 통과, 하나라도 겹치면 통과")
	}
	rows := []map[string]any{{"model_group": "b"}, {"model_group": "a"}, {"model_group": "c"}}
	sortRows(rows, "-model_group")
	if rows[0]["model_group"] != "c" || rows[2]["model_group"] != "a" {
		t.Errorf("내림차순 = %v", rows)
	}
	tokens, other := int64(5), int64(9)
	numbers := []map[string]any{{"max_input_tokens": &other}, {"max_input_tokens": (*int64)(nil)}, {"max_input_tokens": &tokens}}
	sortRows(numbers, "max_input_tokens")
	if numbers[0]["max_input_tokens"] != (*int64)(nil) || numbers[1]["max_input_tokens"] != &tokens {
		t.Errorf("값 없는 행이 앞, 숫자는 오름차순이어야 한다: %v", numbers)
	}
}
