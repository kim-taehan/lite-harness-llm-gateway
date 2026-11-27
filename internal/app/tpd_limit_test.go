package app

import (
	"net/http"
	"testing"
	"time"
)

// 하루 토큰 한도(tpd_limit): 키·팀 생성·수정 요청으로 정하고, 넘으면 429, 응답 화면에 값이 보인다

func TestTPDLimit_SetOnKeyAndTeamBlocksAfterTheDayTotal(t *testing.T) {
	upstream, _ := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1",
		"litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})

	generated := api.ok("POST", "/key/generate", map[string]any{"tpd_limit": 1})
	key := generated["key"].(string)
	if generated["tpd_limit"] != 1.0 {
		t.Errorf("생성 응답의 tpd_limit = %v", generated["tpd_limit"])
	}
	if rec := chatWithKey(api, key, "m1"); rec.Code != http.StatusOK {
		t.Fatalf("첫 요청 = %d %s", rec.Code, rec.Body.String())
	}
	// 사용량은 응답 뒤에 비동기로 더해진다
	var rec = chatWithKey(api, key, "m1")
	for deadline := time.Now().Add(3 * time.Second); rec.Code == http.StatusOK && time.Now().Before(deadline); {
		<-time.After(20 * time.Millisecond)
		rec = chatWithKey(api, key, "m1")
	}
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("retry-after") == "" {
		t.Fatalf("한도를 넘었는데 %d %s", rec.Code, rec.Body.String())
	}

	// 수정으로 풀고, 팀에도 같은 필드가 저장되고 보인다
	api.ok("POST", "/key/update", map[string]any{"key": key, "tpd_limit": nil})
	if rec := chatWithKey(api, key, "m1"); rec.Code != http.StatusOK {
		t.Errorf("한도를 지웠는데 %d %s", rec.Code, rec.Body.String())
	}
	team := api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "tpd_limit": 500})
	if team["tpd_limit"] != 500.0 {
		t.Errorf("팀 응답의 tpd_limit = %v", team["tpd_limit"])
	}
	if info := api.ok("GET", "/team/info?team_id=t1", nil)["team_info"].(map[string]any); info["tpd_limit"] != 500.0 {
		t.Errorf("team/info 의 tpd_limit = %v", info["tpd_limit"])
	}
}
