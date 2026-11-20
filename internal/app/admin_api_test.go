package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// M1 종료 기준(설계 12장)을 HTTP 로 확인한다: 마스터 키로 팀·사용자를 만들고 팀에 사용자를 넣고 조회한다.
// 응답·오류 모양은 litellm 계약(docs/litellm-inventory/H·I)과 같아야 한다.

type apiClient struct {
	t *testing.T
	h http.Handler
}

func newAPI(t *testing.T) apiClient {
	h, _ := testHandler(t)
	return apiClient{t: t, h: h}
}

func (c apiClient) do(method, path string, body any) (int, any) {
	c.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+testMasterKey)
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	var decoded any
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		c.t.Fatalf("%s %s: 응답이 JSON 이 아니다: %q", method, path, rec.Body.String())
	}
	return rec.Code, decoded
}

func (c apiClient) ok(method, path string, body any) map[string]any {
	c.t.Helper()
	status, decoded := c.do(method, path, body)
	if status != http.StatusOK {
		c.t.Fatalf("%s %s: %d %v", method, path, status, decoded)
	}
	obj, _ := decoded.(map[string]any)
	return obj
}

func TestM1_TeamAndUserLifecycleWithMasterKey(t *testing.T) {
	api := newAPI(t)

	team := api.ok("POST", "/team/new", map[string]any{"team_id": "research", "team_alias": "Research", "models": []string{"llama3"}, "max_budget": 100, "tpm_limit": 5000})
	if team["team_id"] != "research" || team["max_budget"] != 100.0 || team["tpm_limit"] != 5000.0 {
		t.Errorf("팀 생성 응답 = %v", team)
	}
	// litellm: 마스터 키 호출자(default_user_id)가 팀 admin 으로 들어간다
	if members := team["members_with_roles"].([]any); len(members) != 1 || members[0].(map[string]any)["user_id"] != "default_user_id" {
		t.Errorf("members_with_roles = %v", team["members_with_roles"])
	}

	user := api.ok("POST", "/user/new", map[string]any{"user_id": nil, "user_email": "kim@corp.local", "user_role": "internal_user", "auto_create_key": false})
	userID := user["user_id"].(string)
	if user["key"] != "" || user["user_role"] != "internal_user" {
		t.Errorf("사용자 생성 응답 = %v", user)
	}

	added := api.ok("POST", "/team/member_add", map[string]any{"team_id": "research", "member": map[string]any{"user_id": userID, "role": "user"}})
	if len(added["updated_users"].([]any)) != 1 || len(added["members_with_roles"].([]any)) != 2 {
		t.Errorf("member_add 응답 = %v", added)
	}

	info := api.ok("GET", "/team/info?team_id=research", nil)
	teamInfo := info["team_info"].(map[string]any)
	if len(teamInfo["members_with_roles"].([]any)) != 2 || len(info["keys"].([]any)) != 0 {
		t.Errorf("team/info = %v", info)
	}

	list := api.ok("GET", "/v2/team/list?page_size=50&search_team_id_match=prefix&search=res", nil)
	if list["total"] != 1.0 || list["total_pages"] != 1.0 {
		t.Errorf("v2/team/list = %v", list)
	}
	row := list["teams"].([]any)[0].(map[string]any)
	if row["members_count"] != 2.0 || row["keys_count"] != 0.0 {
		t.Errorf("v2 팀 행 = %v", row)
	}

	v2user := api.ok("GET", "/v2/user/info?user_id="+userID, nil)
	if teams := v2user["teams"].([]any); len(teams) != 1 || teams[0] != "research" {
		t.Errorf("v2/user/info teams = %v", v2user["teams"])
	}

	users := api.ok("GET", "/user/list?user_email=KIM", nil)
	if users["total"] != 1.0 || users["page_size"] != 25.0 {
		t.Errorf("user/list = %v", users)
	}

	// 팀을 지우면 팀원 관계도 사라진다
	if deleted := api.ok("POST", "/team/delete", map[string]any{"team_ids": []string{"research"}}); deleted["deleted_teams"].([]any)[0] != "research" {
		t.Errorf("team/delete = %v", deleted)
	}
	if after := api.ok("GET", "/v2/user/info?user_id="+userID, nil); len(after["teams"].([]any)) != 0 {
		t.Errorf("지운 팀이 사용자에게 남았다: %v", after["teams"])
	}
	status, count := api.do("POST", "/user/delete", map[string]any{"user_ids": []string{userID}})
	if status != 200 || count != 1.0 {
		t.Errorf("user/delete 는 지운 수(정수)를 돌려준다: %d %v", status, count)
	}
}

func TestM1_ErrorShapesMatchLitellm(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		status int
		check  func(any) bool
	}{
		{"중복 팀 id: (A) repr 메시지", "POST", "/team/new", map[string]any{"team_id": "t1"}, 400, func(b any) bool {
			return errField(b, "message") == "{'error': 'Team id = t1 already exists. Please use a different team id.'}" && errField(b, "code") == "400"
		}},
		{"soft >= max: (A)", "POST", "/team/new", map[string]any{"max_budget": 5, "soft_budget": 5}, 400, func(b any) bool {
			return errField(b, "message") == "{'error': 'soft_budget (5) must be strictly lower than max_budget (5)'}"
		}},
		{"team_id 없음: 422 pydantic", "POST", "/team/update", map[string]any{"team_alias": "x"}, 422, func(b any) bool {
			d := b.(map[string]any)["detail"].([]any)[0].(map[string]any)
			return d["type"] == "missing" && d["msg"] == "Field required"
		}},
		{"형식 오류: 422", "POST", "/team/new", map[string]any{"tpm_limit": "many"}, 422, func(b any) bool {
			return b.(map[string]any)["detail"].([]any)[0].(map[string]any)["type"] == "int_type"
		}},
		{"없는 팀 수정: (A) 404", "POST", "/team/update", map[string]any{"team_id": "nope"}, 404, func(b any) bool {
			return errField(b, "message") == "{'error': 'Team not found, passed team_id=nope'}"
		}},
		{"없는 팀 삭제: (B) 404", "POST", "/team/delete", map[string]any{"team_ids": []string{"nope"}}, 404, func(b any) bool {
			return detailError(b) == "Team not found, passed team_id=nope"
		}},
		{"없는 팀 member_delete: (B) 400", "POST", "/team/member_delete", map[string]any{"team_id": "nope", "user_id": "u"}, 400, func(b any) bool {
			return detailError(b) == "Team id=nope does not exist in db"
		}},
		{"team/info 없는 팀: auth_error", "GET", "/team/info?team_id=nope", nil, 404, func(b any) bool {
			return errField(b, "type") == "auth_error" && errField(b, "param") == "None"
		}},
		{"v2 목록 page_size 범위: 422", "GET", "/v2/team/list?page_size=101", nil, 422, func(b any) bool {
			return b.(map[string]any)["detail"].([]any)[0].(map[string]any)["type"] == "less_than_equal"
		}},
		{"없는 사용자 수정은 만들지 않고 404(설계 9장)", "POST", "/user/update", map[string]any{"user_id": "ghost", "user_alias": "x"}, 404, func(b any) bool {
			return errField(b, "type") == "auth_error"
		}},
		{"잘못된 역할: 422", "POST", "/user/new", map[string]any{"user_role": "god"}, 422, func(b any) bool {
			return b.(map[string]any)["detail"].([]any)[0].(map[string]any)["type"] == "enum"
		}},
		{"user/list 잘못된 정렬: (B) 400", "GET", "/user/list?sort_by=nope", nil, 400, func(b any) bool {
			return detailError(b) == "Invalid sort column. Must be one of: user_id, user_email, created_at, spend, user_alias, user_role"
		}},
	}
	for _, c := range cases {
		status, body := api.do(c.method, c.path, c.body)
		if status != c.status || !c.check(body) {
			t.Errorf("%s: %d %v", c.name, status, body)
		}
	}
}

func TestM1_DuplicateMemberUsesLitellmSpecialShape(t *testing.T) {
	api := newAPI(t)
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1"})
	member := map[string]any{"team_id": "t1", "member": map[string]any{"user_email": "a@corp.local", "role": "user"}}
	api.ok("POST", "/team/member_add", member)

	status, body := api.do("POST", "/team/member_add", member)
	if status != 400 || errField(body, "type") != "team_member_already_in_team" || errField(body, "param") != "member" {
		t.Errorf("%d %v", status, body)
	}
}

func TestM1_NoKeyIs401(t *testing.T) {
	h, _ := testHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v2/team/list", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("키 없이 관리 API: %d", rec.Code)
	}
}

func errField(body any, field string) any {
	obj, _ := body.(map[string]any)
	e, _ := obj["error"].(map[string]any)
	return e[field]
}

func detailError(body any) any {
	obj, _ := body.(map[string]any)
	d, _ := obj["detail"].(map[string]any)
	return d["error"]
}
