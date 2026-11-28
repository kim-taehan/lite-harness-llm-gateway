package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 가드레일: 관리 API 로 규칙을 만들면 요청·응답의 내용을 검사해 막거나 가린다. 대시보드가 보내는 본문 모양을 그대로 쓴다

// replyingUpstream 은 늘 같은 글을 답하는 업스트림이다(응답 검사용). 받은 본문을 남긴다
func replyingUpstream(t *testing.T, reply string) (*httptest.Server, *upstreamLog) {
	t.Helper()
	received := &upstreamLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		received.add(body)
		if stream, _ := body["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\ndata: [DONE]\n\n", reply)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"c2","model":"x","choices":[{"index":0,"message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, received
}

// contentFilterBody 는 대시보드 Add Guardrail 폼이 POST /guardrails 로 보내는 모양이다
func contentFilterBody(name string, params map[string]any) map[string]any {
	lp := map[string]any{"guardrail": "litellm_content_filter", "mode": "pre_call", "default_on": false}
	for k, v := range params {
		lp[k] = v
	}
	return map[string]any{"guardrail": map[string]any{"guardrail_name": name, "litellm_params": lp, "guardrail_info": map[string]any{}}}
}

func prebuiltRule(name, action string) map[string]any {
	return map[string]any{"pattern_type": "prebuilt", "pattern_name": name, "name": name, "action": action}
}

func chatText(api apiClient, key, content string, extra map[string]any) *httptest.ResponseRecorder {
	body := map[string]any{"model": "m1", "messages": []any{map[string]any{"role": "user", "content": content}}}
	for k, v := range extra {
		body[k] = v
	}
	data, _ := json.Marshal(body)
	return chatWithTags(api, key, string(data), nil)
}

// lastUserText 는 업스트림이 마지막으로 받은 질문 글이다
func lastUserText(t *testing.T, log *upstreamLog) string {
	t.Helper()
	all := log.all()
	if len(all) == 0 {
		t.Fatal("업스트림이 아무것도 받지 못했다")
	}
	msgs := all[len(all)-1]["messages"].([]any)
	return msgs[len(msgs)-1].(map[string]any)["content"].(string)
}

func TestGuardrail_ManagementAPIMatchesDashboardContract(t *testing.T) {
	api := newAPI(t)
	if list := api.ok("GET", "/v2/guardrails/list", nil)["guardrails"].([]any); len(list) != 0 {
		t.Fatalf("빈 목록 = %v", list)
	}

	// 폼이 읽는 선택지: 기본 패턴 목록(한국 패턴 포함)과 "LiteLLM Content Filter" 종류
	settings := api.ok("GET", "/guardrails/ui/add_guardrail_settings", nil)
	cf := settings["content_filter_settings"].(map[string]any)
	names := map[string]bool{}
	for _, p := range cf["prebuilt_patterns"].([]any) {
		names[p.(map[string]any)["name"].(string)] = true
	}
	if !names["kr_rrn"] || !names["email"] || !names["openai_api_key"] || len(cf["pattern_categories"].([]any)) == 0 {
		t.Errorf("기본 패턴 = %v", cf)
	}
	if modes := settings["supported_modes_by_provider"].(map[string]any)["litellm_content_filter"].([]any); len(modes) != 3 {
		t.Errorf("지원 시점 = %v", modes)
	}
	providers := api.ok("GET", "/guardrails/ui/provider_specific_params", nil)
	if providers["litellm_content_filter"].(map[string]any)["ui_friendly_name"] != "LiteLLM Content Filter" {
		t.Errorf("종류 선택지 = %v", providers)
	}

	created := api.ok("POST", "/guardrails", contentFilterBody("pii", map[string]any{
		"patterns":      []any{prebuiltRule("email", "MASK"), map[string]any{"pattern_type": "regex", "pattern": `\d{6}-\d{7}`, "name": "rrn", "action": "BLOCK"}},
		"blocked_words": []any{map[string]any{"keyword": "projectx", "action": "BLOCK", "description": "코드명"}},
	}))
	id := created["guardrail_id"].(string)
	if id == "" || created["guardrail_name"] != "pii" || created["litellm_params"] == nil {
		t.Fatalf("생성 응답 = %v", created)
	}

	// 표는 litellm_params.guardrail·mode·default_on 을 늘 읽고, 목록은 DB 출처임을 알린다
	row := api.ok("GET", "/v2/guardrails/list", nil)["guardrails"].([]any)[0].(map[string]any)
	lp := row["litellm_params"].(map[string]any)
	if row["guardrail_definition_location"] != "db" || lp["guardrail"] != "litellm_content_filter" || lp["mode"] != "pre_call" || lp["default_on"] != false ||
		row["created_at"] == nil || row["updated_at"] == nil {
		t.Errorf("목록 행 = %v", row)
	}
	if got := api.ok("GET", "/guardrails/"+id+"/info", nil); got["guardrail_id"] != id {
		t.Errorf("info = %v", got)
	}

	// 대시보드는 PATCH 로 바뀐 것만 보낸다. litellm_params 는 키별로 덮어쓰고 null 은 지운다
	patched := api.ok("PATCH", "/guardrails/"+id, map[string]any{"guardrail_name": "pii-v2", "litellm_params": map[string]any{"default_on": true, "mode": []string{"pre_call", "post_call"}}})
	plp := patched["litellm_params"].(map[string]any)
	if patched["guardrail_name"] != "pii-v2" || plp["default_on"] != true || len(plp["patterns"].([]any)) != 2 || len(plp["mode"].([]any)) != 2 {
		t.Errorf("PATCH 결과 = %v", patched)
	}
	api.ok("PATCH", "/guardrails/"+id, map[string]any{"litellm_params": map[string]any{"blocked_words": nil}})
	if got := api.ok("GET", "/guardrails/"+id+"/info", nil)["litellm_params"].(map[string]any); got["blocked_words"] != nil {
		t.Errorf("null 로 지운 키가 남았다: %v", got)
	}
	replaced := api.ok("PUT", "/guardrails/"+id, contentFilterBody("pii-v3", map[string]any{"patterns": []any{prebuiltRule("kr_phone", "MASK")}}))
	if replaced["guardrail_name"] != "pii-v3" || len(replaced["litellm_params"].(map[string]any)["patterns"].([]any)) != 1 {
		t.Errorf("PUT 결과 = %v", replaced)
	}

	// 잘못된 입력은 저장하지 않는다
	bad := map[string]map[string]any{
		"패턴·금지어 없음":  {},
		"없는 기본 패턴":   {"patterns": []any{prebuiltRule("nope", "MASK")}},
		"깨진 정규식":     {"patterns": []any{map[string]any{"pattern_type": "regex", "pattern": "([", "name": "x", "action": "BLOCK"}}},
		"지원 안 하는 시점": {"mode": "logging_only", "patterns": []any{prebuiltRule("email", "MASK")}},
		"카테고리":       {"patterns": []any{prebuiltRule("email", "MASK")}, "categories": []any{map[string]any{"category": "harm", "enabled": true}}},
	}
	for what, params := range bad {
		if status, body := api.do("POST", "/guardrails", contentFilterBody("bad", params)); status != http.StatusBadRequest ||
			!strings.Contains(fmt.Sprint(body), "Guardrail configuration error") {
			t.Errorf("%s = %d %v", what, status, body)
		}
	}
	if status, _ := api.do("POST", "/guardrails", map[string]any{"guardrail": map[string]any{"guardrail_name": "x"}}); status != http.StatusBadRequest {
		t.Errorf("설정 없음 = %d", status)
	}
	if status, _ := api.do("POST", "/guardrails", contentFilterBody("pii-v3", map[string]any{"patterns": []any{prebuiltRule("email", "MASK")}})); status != http.StatusConflict {
		t.Errorf("같은 이름 = %d, 409", status)
	}
	if status, _ := api.do("PATCH", "/guardrails/"+id, map[string]any{"litellm_params": map[string]any{"patterns": []any{prebuiltRule("nope", "MASK")}}}); status != http.StatusBadRequest {
		t.Errorf("잘못된 PATCH = %d, 400", status)
	}
	if got := api.ok("GET", "/guardrails/"+id+"/info", nil); got["guardrail_name"] != "pii-v3" {
		t.Errorf("거절한 PATCH 가 저장됐다: %v", got)
	}

	if status, _ := api.do("GET", "/guardrails/nope/info", nil); status != http.StatusNotFound {
		t.Errorf("없는 가드레일 info = %d", status)
	}
	if status, _ := api.do("PATCH", "/guardrails/nope", map[string]any{"guardrail_name": "z"}); status != http.StatusNotFound {
		t.Errorf("없는 가드레일 PATCH = %d", status)
	}
	if got := api.ok("DELETE", "/guardrails/"+id, nil); !strings.Contains(fmt.Sprint(got["message"]), "deleted successfully") {
		t.Errorf("삭제 응답 = %v", got)
	}
	if status, _ := api.do("DELETE", "/guardrails/"+id, nil); status != http.StatusNotFound {
		t.Errorf("두 번 지우기 = %d, 404", status)
	}
}

func TestGuardrail_OnlyAdminsManageAndOnlyViewersSeeRules(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/user/new", map[string]any{"user_id": "member", "user_role": "internal_user"})
	userKey := api.ok("POST", "/key/generate", map[string]any{"user_id": "member"})["key"].(string)
	api.ok("POST", "/guardrails", contentFilterBody("pii", map[string]any{"patterns": []any{prebuiltRule("email", "MASK")}}))

	for method, path := range map[string]string{"POST": "/guardrails", "PATCH": "/guardrails/x", "PUT": "/guardrails/x", "DELETE": "/guardrails/x"} {
		if status, _ := callAs(api, userKey, method, path, contentFilterBody("hack", nil)); status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Errorf("일반 사용자의 %s %s = %d", method, path, status)
		}
	}
	// 규칙(사내 금지어·정규식)은 일반 사용자에게 보이지 않는다. 목록은 빈 배열이라 화면은 깨지지 않는다
	status, list := callAs(api, userKey, "GET", "/v2/guardrails/list", nil)
	if status != http.StatusOK || len(list.(map[string]any)["guardrails"].([]any)) != 0 {
		t.Errorf("일반 사용자의 목록 = %d %v", status, list)
	}
	if status, _ := callAs(api, userKey, "GET", "/guardrails/x/info", nil); status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Errorf("일반 사용자의 info = %d", status)
	}
}

func TestGuardrail_DefaultOnMasksAndBlocksBeforeTheUpstreamSeesIt(t *testing.T) {
	upstream, received := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)
	api.ok("POST", "/guardrails", contentFilterBody("pii", map[string]any{"default_on": true,
		"patterns":      []any{prebuiltRule("email", "MASK"), prebuiltRule("kr_rrn", "BLOCK")},
		"blocked_words": []any{map[string]any{"keyword": "ProjectX", "action": "BLOCK", "description": "코드명"}}}))

	if rec := chatText(api, key, "메일은 a@corp.local 입니다", nil); rec.Code != http.StatusOK {
		t.Fatalf("가리기 = %d %s", rec.Code, rec.Body)
	}
	if got := lastUserText(t, received); got != "메일은 [EMAIL_REDACTED] 입니다" {
		t.Errorf("업스트림이 받은 글 = %q", got)
	}

	before := len(received.all())
	rec := chatText(api, key, "번호 900101-1234567 확인", nil)
	var body map[string]map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	fields, _ := body["error"]["provider_specific_fields"].(map[string]any)
	if rec.Code != http.StatusBadRequest || body["error"]["message"] != "Content blocked: kr_rrn pattern detected" || body["error"]["type"] != "invalid_request_error" ||
		body["error"]["code"] != "400" || fields["pattern"] != "kr_rrn" || fields["guardrail_name"] != "pii" || fields["guardrail_mode"] != "pre_call" {
		t.Errorf("패턴 차단 = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "900101") {
		t.Error("걸린 글의 내용이 오류 응답에 들어갔다")
	}
	rec = chatText(api, key, "이건 PROJECTX 문서", nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "keyword 'projectx' detected (코드명)") {
		t.Errorf("금지어 차단 = %d %s", rec.Code, rec.Body)
	}
	if len(received.all()) != before {
		t.Error("막힌 요청이 업스트림에 닿았다")
	}

	// 표준 /metrics 에 막은 횟수가 나온다
	metrics := get(api.h, "/metrics")
	if !strings.Contains(metrics.Body.String(), `gateway_guardrail_blocked_total{guardrail="pii",mode="pre_call"} 2`) {
		t.Errorf("지표에 막은 횟수가 없다")
	}

	// 규칙을 지우면 바로 통과한다(이 인스턴스에서 바꾼 것은 캐시 없이 반영)
	list := api.ok("GET", "/v2/guardrails/list", nil)["guardrails"].([]any)
	api.ok("DELETE", "/guardrails/"+list[0].(map[string]any)["guardrail_id"].(string), nil)
	if rec := chatText(api, key, "번호 900101-1234567", nil); rec.Code != http.StatusOK {
		t.Errorf("지운 뒤에도 막힌다: %d %s", rec.Code, rec.Body)
	}
}

func TestGuardrail_NamedGuardrailRunsOnlyWhenKeyTeamOrRequestAsksForIt(t *testing.T) {
	upstream, received := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	api.ok("POST", "/guardrails", contentFilterBody("mask-mail", map[string]any{"patterns": []any{prebuiltRule("email", "MASK")}}))
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "guardrails": []string{"mask-mail"}})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t2"})
	plain := api.ok("POST", "/key/generate", map[string]any{"team_id": "t2"})["key"].(string)
	keyBound := api.ok("POST", "/key/generate", map[string]any{"team_id": "t2", "guardrails": []string{"mask-mail"}})["key"].(string)
	teamBound := api.ok("POST", "/key/generate", map[string]any{"team_id": "t1"})["key"].(string)

	cases := []struct {
		what  string
		key   string
		extra map[string]any
		want  string
	}{
		{"아무 지정 없음", plain, nil, "a@b.com"},
		{"요청 본문 guardrails", plain, map[string]any{"guardrails": []string{"mask-mail"}}, "[EMAIL_REDACTED]"},
		{"요청 metadata.guardrails", plain, map[string]any{"metadata": map[string]any{"guardrails": []string{"mask-mail"}}}, "[EMAIL_REDACTED]"},
		{"요청이 {이름: 설정} 으로", plain, map[string]any{"guardrails": []any{map[string]any{"mask-mail": map[string]any{}}}}, "[EMAIL_REDACTED]"},
		{"키에 붙임", keyBound, nil, "[EMAIL_REDACTED]"},
		{"팀에 붙임", teamBound, nil, "[EMAIL_REDACTED]"},
		{"없는 이름 지정", plain, map[string]any{"guardrails": []string{"nope"}}, "a@b.com"},
	}
	for _, c := range cases {
		if rec := chatText(api, c.key, "a@b.com", c.extra); rec.Code != http.StatusOK {
			t.Errorf("%s = %d %s", c.what, rec.Code, rec.Body)
			continue
		}
		if got := lastUserText(t, received); got != c.want {
			t.Errorf("%s: 업스트림이 받은 글 %q, 기대 %q", c.what, got, c.want)
		}
	}
	for _, body := range received.all() {
		if _, leaked := body["guardrails"]; leaked {
			t.Fatal("요청의 guardrails 가 업스트림에 전달됐다")
		}
	}
}

// 키 검증 결과는 캐시된다. 팀의 가드레일 설정을 바꾸면 이미 캐시된 키에도 바로 적용돼야 한다
func TestGuardrail_TeamSettingChangeReachesAlreadyCachedKeys(t *testing.T) {
	upstream, received := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	api.ok("POST", "/guardrails", contentFilterBody("mask-mail", map[string]any{"patterns": []any{prebuiltRule("email", "MASK")}}))
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	key := api.ok("POST", "/key/generate", map[string]any{"team_id": "t1"})["key"].(string)

	chatText(api, key, "a@b.com", nil) // 이 요청이 키를 캐시에 올린다
	if got := lastUserText(t, received); got != "a@b.com" {
		t.Fatalf("붙이기 전 = %q", got)
	}
	api.ok("POST", "/team/update", map[string]any{"team_id": "t1", "metadata": map[string]any{"guardrails": []string{"mask-mail"}}})
	chatText(api, key, "a@b.com", nil)
	if got := lastUserText(t, received); got != "[EMAIL_REDACTED]" {
		t.Errorf("팀에 붙인 뒤 = %q", got)
	}
}

func TestGuardrail_OnlyAdminsCanSwitchOffGlobalGuardrails(t *testing.T) {
	upstream, received := fakeOllama(t)
	api := newAPI(t)
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	api.ok("POST", "/guardrails", contentFilterBody("always", map[string]any{"default_on": true, "patterns": []any{prebuiltRule("email", "MASK")}}))
	api.ok("POST", "/guardrails", contentFilterBody("other", map[string]any{"default_on": true, "patterns": []any{prebuiltRule("ipv4", "MASK")}}))
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)

	// 요청 본문으로는 전사 정책을 끌 수 없다
	chatText(api, key, "a@b.com", map[string]any{"disable_global_guardrails": true, "metadata": map[string]any{"opted_out_global_guardrails": []string{"always"}}})
	if got := lastUserText(t, received); got != "[EMAIL_REDACTED]" {
		t.Errorf("본문의 끄는 설정이 먹혔다: %q", got)
	}

	// 관리자는 키에 정할 수 있다: 전부 끄기, 또는 이름 하나만 끄기
	off := api.ok("POST", "/key/generate", map[string]any{"disable_global_guardrails": true})["key"].(string)
	chatText(api, off, "a@b.com 10.0.0.1", nil)
	if got := lastUserText(t, received); got != "a@b.com 10.0.0.1" {
		t.Errorf("disable_global_guardrails 키 = %q", got)
	}
	optOut := api.ok("POST", "/key/generate", map[string]any{"metadata": map[string]any{"opted_out_global_guardrails": []string{"always"}}})["key"].(string)
	chatText(api, optOut, "a@b.com 10.0.0.1", nil)
	if got := lastUserText(t, received); got != "a@b.com [IPV4_REDACTED]" {
		t.Errorf("이름 하나만 끈 키 = %q", got)
	}

	// 일반 사용자는 자기 키를 만들거나 고칠 때 끄는 설정을 정할 수 없다
	// (거절 문구가 이 규칙의 것인지까지 본다. 일반 사용자는 다른 이유로도 막히므로 문구가 다르면 시험이 틀린 것이다)
	const denied = "disable or opt out of global guardrails"
	api.ok("POST", "/user/new", map[string]any{"user_id": "member", "user_role": "internal_user"})
	userKey := api.ok("POST", "/key/generate", map[string]any{"user_id": "member"})["key"].(string)
	// 개인 키는 만든 사람이 자기 명의로 만든 것만 고칠 수 있다(litellm)
	_, own := callAs(api, userKey, "POST", "/key/generate", map[string]any{"user_id": "member"})
	ownKey, _ := own.(map[string]any)["key"].(string)
	if ownKey == "" {
		t.Fatalf("일반 사용자가 자기 키를 만들지 못했다: %v", own)
	}
	for what, body := range map[string]map[string]any{
		"맨 위 disable":          {"disable_global_guardrails": true},
		"metadata 의 disable":   {"metadata": map[string]any{"disable_global_guardrails": true}},
		"metadata 의 opted_out": {"metadata": map[string]any{"opted_out_global_guardrails": []string{"always"}}},
	} {
		if status, resp := callAs(api, userKey, "POST", "/key/generate", merge(body, map[string]any{"user_id": "member"})); status != http.StatusForbidden || !strings.Contains(fmt.Sprint(resp), denied) {
			t.Errorf("생성: %s = %d %v", what, status, resp)
		}
		if status, resp := callAs(api, ownKey, "POST", "/key/update", merge(body, map[string]any{"key": ownKey})); status != http.StatusForbidden || !strings.Contains(fmt.Sprint(resp), denied) {
			t.Errorf("수정: %s = %d %v", what, status, resp)
		}
	}
	// 가드레일을 더하는 것과 다른 metadata 를 고치는 것은 된다
	if status, resp := callAs(api, ownKey, "POST", "/key/update", map[string]any{"key": ownKey, "guardrails": []string{"always"}, "metadata": map[string]any{"note": "x"}}); status != http.StatusOK {
		t.Errorf("가드레일 더하기 = %d %v", status, resp)
	}
	chatText(api, ownKey, "a@b.com", nil)
	if got := lastUserText(t, received); got != "[EMAIL_REDACTED]" {
		t.Errorf("일반 사용자 키 = %q", got)
	}
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func TestGuardrail_TeamAdminCannotOptTheTeamOutOfGlobalGuardrails(t *testing.T) {
	w := newAuthzWorld(t)
	w.api.ok("PATCH", "/update/ui_settings", map[string]any{"team_admin_editable_team_fields": []string{"metadata"}})
	for what, metadata := range map[string]string{
		"disable_global_guardrails":   `{"disable_global_guardrails":true}`,
		"opted_out_global_guardrails": `{"opted_out_global_guardrails":["pii"]}`,
	} {
		rec := w.as("boss", "POST", "/team/update", `{"team_id":"t1","metadata":`+metadata+`}`)
		if rec.Code != 403 || !strings.Contains(rec.Body.String(), "Only proxy admins") {
			t.Errorf("팀 admin 의 %s: %d %s", what, rec.Code, rec.Body)
		}
	}
	w.expect(w.as("boss", "POST", "/team/update", `{"team_id":"t1","metadata":{"guardrails":["pii"],"note":"x"}}`), 200, "가드레일을 더하는 metadata 는 된다")
	// 관리자는 정할 수 있고, 그 값을 팀 admin 이 다른 metadata 와 함께 그대로 다시 보내는 것은 바뀐 게 아니다
	w.api.ok("POST", "/team/update", map[string]any{"team_id": "t1", "metadata": map[string]any{"opted_out_global_guardrails": []string{"pii"}}})
	w.expect(w.as("boss", "POST", "/team/update", `{"team_id":"t1","metadata":{"opted_out_global_guardrails":["pii"],"note":"y"}}`), 200, "그대로 다시 보냄")
	w.expect(w.as("boss", "POST", "/team/update", `{"team_id":"t1","metadata":{"note":"z"}}`), 200, "끄는 설정을 없애는 것은 정책을 조이는 일")
}

func TestGuardrail_PostCallChecksTheModelAnswerButNotStreams(t *testing.T) {
	upstream, _ := replyingUpstream(t, "문의는 admin@corp.local 로")
	h, _, pool := testGateway(t)
	api := apiClient{t: t, h: h}
	api.ok("POST", "/model/new", map[string]any{"model_name": "m1", "litellm_params": map[string]any{"model": "ollama/x", "api_base": upstream.URL}})
	key := api.ok("POST", "/key/generate", map[string]any{})["key"].(string)
	mask := api.ok("POST", "/guardrails", contentFilterBody("answer-mask", map[string]any{"default_on": true, "mode": "post_call",
		"patterns": []any{prebuiltRule("email", "MASK")}}))["guardrail_id"].(string)

	rec := chatText(api, key, "누구에게 문의하나요", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "[EMAIL_REDACTED]") || strings.Contains(rec.Body.String(), "admin@corp.local") {
		t.Errorf("응답 가리기 = %d %s", rec.Code, rec.Body)
	}
	if rec := chatText(api, key, "a@b.com 를 알려 줘", nil); rec.Code != http.StatusOK {
		t.Errorf("post_call 가드레일이 질문을 막았다: %d", rec.Code)
	}

	api.ok("PATCH", "/guardrails/"+mask, map[string]any{"litellm_params": map[string]any{"patterns": []any{prebuiltRule("email", "BLOCK")}}})
	rec = chatText(api, key, "누구에게 문의하나요", nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"guardrail_mode":"post_call"`) || strings.Contains(rec.Body.String(), "admin@corp.local") {
		t.Errorf("응답 차단 = %d %s", rec.Code, rec.Body)
	}
	// 막은 응답의 토큰은 이미 썼으므로 사용량에는 남는다(오류 코드로 구분한다)
	var tokens int
	for deadline := time.Now().Add(3 * time.Second); ; {
		_ = pool.QueryRow(context.Background(), "SELECT COALESCE(sum(completion_tokens), 0) FROM request_log WHERE error_code = 'guardrail_blocked'").Scan(&tokens)
		if tokens > 0 || time.Now().After(deadline) {
			break
		}
		<-time.After(20 * time.Millisecond)
	}
	if tokens != 3 {
		t.Errorf("막은 응답의 사용량 = %d 토큰, 3", tokens)
	}

	// 스트림은 검사하지 않는다(문서에 적은 한계). 스트림이 모델의 글을 그대로 흘린다
	rec = chatText(api, key, "누구에게 문의하나요", map[string]any{"stream": true})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "admin@corp.local") {
		t.Errorf("스트림 = %d %s", rec.Code, rec.Body)
	}
}

func TestGuardrail_ChangesAreAuditedWithTheValueBefore(t *testing.T) {
	api := newAPI(t)
	id := api.ok("POST", "/guardrails", contentFilterBody("pii", map[string]any{"patterns": []any{prebuiltRule("email", "MASK")}}))["guardrail_id"].(string)
	if e := auditEntryFor(t, api, "table_name=LiteLLM_GuardrailsTable&object_id="+id+"&action=created"); e["before_value"] != nil {
		t.Errorf("만들기에 이전 값이 있다: %v", e["before_value"])
	}
	api.ok("PATCH", "/guardrails/"+id, map[string]any{"guardrail_name": "pii-2"})
	update := auditEntryFor(t, api, "table_name=LiteLLM_GuardrailsTable&object_id="+id+"&action=updated")
	if before, _ := update["before_value"].(map[string]any); before["guardrail_name"] != "pii" {
		t.Errorf("수정 기록의 이전 값 = %v", update["before_value"])
	}
	api.ok("DELETE", "/guardrails/"+id, nil)
	deleted := auditEntryFor(t, api, "table_name=LiteLLM_GuardrailsTable&object_id="+id+"&action=deleted")
	if before, _ := deleted["before_value"].(map[string]any); before["guardrail_name"] != "pii-2" {
		t.Errorf("삭제 기록의 이전 값 = %v", deleted["before_value"])
	}
}
