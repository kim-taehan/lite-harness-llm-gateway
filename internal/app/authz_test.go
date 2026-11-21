package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// 팀·사용자 라우트의 핸들러 단계 권한(H 1장). 라우트 단계는 "인증만 되면 통과"라서 일반 사용자가 남의 정보를 보거나 팀을 고칠 수 있었다

type authzWorld struct {
	t   *testing.T
	api apiClient
	b   browser
}

func newAuthzWorld(t *testing.T) *authzWorld {
	api := newAPI(t)
	w := &authzWorld{t: t, api: api, b: browser{t: t, h: api.h}}
	for _, u := range []string{"boss", "mate", "outsider"} {
		api.ok("POST", "/user/new", map[string]any{"user_id": u, "user_role": "internal_user"})
	}
	api.ok("POST", "/user/new", map[string]any{"user_id": "watcher", "user_role": "internal_user_viewer"})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t1", "team_alias": "팀1", "max_budget": 10})
	api.ok("POST", "/team/new", map[string]any{"team_id": "t2"})
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "t1", "member": map[string]any{"user_id": "boss", "role": "admin"}})
	api.ok("POST", "/team/member_add", map[string]any{"team_id": "t1", "member": map[string]any{"user_id": "mate", "role": "user"}})
	return w
}

// as 는 그 사용자 명의의 새 키로 부른 결과다
func (w *authzWorld) as(user, method, path, body string) *httptest.ResponseRecorder {
	w.t.Helper()
	header := map[string]string{"Authorization": "Bearer " + w.keyOf(user)}
	if body == "" {
		return w.b.do(method, path, nil, header)
	}
	return w.b.doJSON(method, path, body, header)
}

func (w *authzWorld) keyOf(user string) string {
	return w.api.ok("POST", "/key/generate", map[string]any{"user_id": user})["key"].(string)
}

func (w *authzWorld) expect(rec *httptest.ResponseRecorder, status int, what string) {
	w.t.Helper()
	if rec.Code != status {
		w.t.Errorf("%s: %d(기대 %d) %s", what, rec.Code, status, rec.Body.String())
	}
}

func TestAuthz_UsersSeeOnlyThemselves(t *testing.T) {
	w := newAuthzWorld(t)
	w.expect(w.as("outsider", "GET", "/user/list", ""), 403, "일반 사용자의 사용자 목록")
	w.expect(w.as("watcher", "GET", "/user/list", ""), 403, "viewer 의 사용자 목록")
	w.expect(w.as("outsider", "GET", "/v2/user/info?user_id=boss", ""), 404, "남의 v2 정보(없는 것처럼)")
	w.expect(w.as("outsider", "GET", "/user/info?user_id=boss", ""), 403, "남의 v1 정보")
	w.expect(w.as("outsider", "GET", "/v2/user/info", ""), 200, "본인 v2 정보")
	w.expect(w.as("outsider", "GET", "/v2/user/info?user_id=outsider", ""), 200, "본인 v2 정보(id 지정)")
	// 비관리자가 user_id 없이 /user/info 를 불러도 모든 팀이 나오지 않는다(본인 정보다)
	rec := w.as("outsider", "GET", "/user/info", "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"team_id":"t1"`) {
		t.Errorf("비관리자의 /user/info 가 팀 전체를 준다: %d %s", rec.Code, rec.Body.String())
	}
	// 팀 admin 은 자기 팀 사용자를 본다
	w.expect(w.as("boss", "GET", "/v2/user/info?user_id=mate", ""), 200, "팀 admin 이 팀원 정보")
	w.expect(w.as("mate", "GET", "/v2/user/info?user_id=boss", ""), 404, "팀 일반 멤버가 다른 팀원 정보")
	w.expect(w.as("outsider", "GET", "/v2/user/info?user_id=mate", ""), 404, "다른 팀 사용자 정보")
}

func TestAuthz_TeamReadsAreScoped(t *testing.T) {
	w := newAuthzWorld(t)
	w.expect(w.as("mate", "GET", "/team/info?team_id=t1", ""), 200, "멤버의 팀 정보")
	w.expect(w.as("outsider", "GET", "/team/info?team_id=t1", ""), 403, "비멤버의 팀 정보")
	w.expect(w.as("watcher", "GET", "/team/info?team_id=t1", ""), 403, "viewer 의 팀 정보")

	mine := w.as("mate", "GET", "/team/list", "")
	if mine.Code != 200 || !strings.Contains(mine.Body.String(), `"team_id":"t1"`) || strings.Contains(mine.Body.String(), `"team_id":"t2"`) {
		t.Errorf("v1 팀 목록은 내 팀만 나와야 한다: %d %s", mine.Code, mine.Body.String())
	}
	w.expect(w.as("mate", "GET", "/team/list?user_id=boss", ""), 401, "남의 팀 목록")
	// v2 는 user_id 를 호출자 id 로 채운다(설계 9장)
	v2 := w.as("outsider", "GET", "/v2/team/list", "")
	if v2.Code != 200 || !strings.Contains(v2.Body.String(), `"total":0`) {
		t.Errorf("v2 팀 목록은 내 팀(없음)만: %d %s", v2.Code, v2.Body.String())
	}
	w.expect(w.as("outsider", "GET", "/v2/team/list?user_id=mate", ""), 401, "v2 남의 팀 목록")
	if all := w.api.ok("GET", "/v2/team/list", nil); all["total"].(float64) < 2 {
		t.Errorf("관리자는 모든 팀: %v", all["total"])
	}
}

func TestAuthz_TeamWritesNeedTeamAdmin(t *testing.T) {
	w := newAuthzWorld(t)
	add := `{"team_id":"t1","member":{"user_id":"outsider","role":"user"}}`
	w.expect(w.as("mate", "POST", "/team/member_add", add), 403, "일반 멤버의 멤버 추가")
	w.expect(w.as("outsider", "POST", "/team/member_add", `{"team_id":"t1","member":{"user_id":"outsider","role":"admin"}}`), 403, "비멤버가 스스로 admin 으로 가입")
	w.expect(w.as("boss", "POST", "/team/member_add", `{"team_id":"t2","member":{"user_id":"outsider","role":"user"}}`), 403, "남의 팀에 멤버 추가")
	w.expect(w.as("boss", "POST", "/team/member_add", `{"team_id":"nope","member":{"user_id":"outsider"}}`), 404, "없는 팀")
	w.expect(w.as("boss", "POST", "/team/member_add", `{"team_id":"t1","member":{"user_id":"ghost-user"}}`), 403, "팀 admin 이 없는 user_id 추가")
	w.expect(w.as("boss", "POST", "/team/member_add", add), 200, "팀 admin 의 멤버 추가")

	w.expect(w.as("mate", "POST", "/team/member_update", `{"team_id":"t1","user_id":"mate","role":"admin"}`), 403, "일반 멤버가 스스로 승격")
	w.expect(w.as("boss", "POST", "/team/member_update", `{"team_id":"t1","user_id":"mate","role":"admin"}`), 200, "팀 admin 의 역할 변경")
	w.expect(w.as("outsider", "POST", "/team/member_delete", `{"team_id":"t1","user_id":"boss"}`), 403, "일반 멤버가 admin 삭제")
	// litellm 은 member_delete 에서 팀이 없으면 권한 검사보다 먼저 400 이다
	w.expect(w.as("outsider", "POST", "/team/member_delete", `{"team_id":"nope","user_id":"boss"}`), 400, "없는 팀")
}

func TestAuthz_TeamUpdateIsLimitedForTeamAdmins(t *testing.T) {
	w := newAuthzWorld(t)
	w.expect(w.as("mate", "POST", "/team/update", `{"team_id":"t1","team_alias":"해킹"}`), 403, "일반 멤버의 팀 수정")
	w.expect(w.as("outsider", "POST", "/team/update", `{"team_id":"nope"}`), 403, "없는 팀도 같은 403")
	// 기본 설정에서는 팀 admin 도 팀 설정을 못 바꾼다
	rec := w.as("boss", "POST", "/team/update", `{"team_id":"t1","team_alias":"새 이름"}`)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "cannot edit team settings") {
		t.Errorf("기본 설정의 팀 admin: %d %s", rec.Code, rec.Body.String())
	}
	// 관리자가 tpm_limit 만 열어 주면 그것만 된다. 그대로 다시 보낸 값은 바뀐 것으로 치지 않는다
	w.api.ok("PATCH", "/update/ui_settings", map[string]any{"team_admin_editable_team_fields": []string{"tpm_limit", "max_budget"}})
	w.expect(w.as("boss", "POST", "/team/update", `{"team_id":"t1","tpm_limit":500,"team_alias":"팀1"}`), 200, "열어 준 필드만 바꿈(별명은 그대로)")
	rec = w.as("boss", "POST", "/team/update", `{"team_id":"t1","team_alias":"새 이름"}`)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "'team_alias'") {
		t.Errorf("열지 않은 필드: %d %s", rec.Code, rec.Body.String())
	}
	w.expect(w.as("boss", "POST", "/team/update", `{"team_id":"t1","max_budget":5}`), 200, "예산 낮추기")
	rec = w.as("boss", "POST", "/team/update", `{"team_id":"t1","max_budget":50}`)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "raise_max_budget") {
		t.Errorf("예산 올리기는 raise_max_budget 이 있어야 한다: %d %s", rec.Code, rec.Body.String())
	}
	w.expect(w.as("boss", "POST", "/team/update", `{"team_id":"t1","blocked":true}`), 403, "차단은 열지 않았다")
}
