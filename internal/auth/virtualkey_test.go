package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llm-gateway/internal/core"
	"llm-gateway/internal/core/dbtest"
)

// 가상 키 검증(J 5장): 형식 → 키 있음 → 차단 → 만료 → 팀 차단 순서와 litellm 문구, 역할은 소유자 사용자 행에서

type keyWorld struct {
	t     *testing.T
	now   time.Time
	keys  *Keys
	users *Users
	teams *Teams
	auth  *Authenticator
}

func newKeyWorld(t *testing.T) *keyWorld {
	db, _ := dbtest.Open(t)
	w := &keyWorld{t: t, now: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)}
	clock := func() time.Time { return w.now }
	cache := NewKeyCache(time.Minute, clock)
	w.users = NewUsers(db, cache)
	w.teams = NewTeams(db, w.users, cache)
	w.keys = NewKeys(db, cache)
	master, _ := NewMasterKey(testMasterKey)
	w.auth = NewAuthenticator(master, w.keys, w.users, w.teams, cache, clock)
	return w
}

func (w *keyWorld) generate(input NewKey) (string, Key) {
	w.t.Helper()
	plain, key, err := w.keys.Generate(w.t.Context(), input)
	if err != nil {
		w.t.Fatal(err)
	}
	return plain, key
}

// call 은 이 키로 route 를 부른 결과다
func (w *keyWorld) call(plain string, access core.Access) (int, map[string]any) {
	w.t.Helper()
	handler := w.auth.Guard(core.Route{Method: http.MethodGet, Path: "/x", Access: access, Handler: func(rw http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		core.WriteJSON(rw, http.StatusOK, map[string]any{"user_id": p.UserID, "role": p.Role})
	}})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if e, ok := body["error"].(map[string]any); ok {
		return rec.Code, e
	}
	return rec.Code, body
}

func TestGeneratedKey_HasLitellmShape(t *testing.T) {
	w := newKeyWorld(t)
	plain, key := w.generate(NewKey{CreatedBy: "default_user_id"})

	if !strings.HasPrefix(plain, "sk-") || len(plain) != 25 {
		t.Errorf("원문 = %q, sk- + 22자여야 한다", plain)
	}
	if key.Token != hashKey(plain) || *key.KeyName != "sk-..."+plain[len(plain)-4:] {
		t.Errorf("token·key_name = %s, %s", key.Token, *key.KeyName)
	}
	if key.Blocked != nil || key.KeyType != "default" {
		t.Errorf("blocked 는 null, key_type 은 default 여야 한다: %+v", key)
	}
}

func TestVirtualKey_RoleComesFromOwnerUserRow(t *testing.T) {
	w := newKeyWorld(t)
	ctx := t.Context()
	viewer := core.RoleInternalUserViewer
	_, _, _ = w.users.Create(ctx, NewUser{UserID: ptr("v1"), UserRole: &viewer})
	_, _, _ = w.users.Create(ctx, NewUser{UserID: ptr("n1")}) // 역할 null → internal_user

	viewerKey, _ := w.generate(NewKey{UserID: ptr("v1")})
	nullRoleKey, _ := w.generate(NewKey{UserID: ptr("n1")})
	orphanKey, _ := w.generate(NewKey{UserID: ptr("ghost")})

	internalOnly := core.Roles(core.RoleInternalUser)
	if code, _ := w.call(nullRoleKey, internalOnly); code != 200 {
		t.Errorf("역할 null 사용자는 internal_user 로 본다: %d", code)
	}
	if code, body := w.call(viewerKey, internalOnly); code != 401 || !strings.Contains(body["message"].(string), "Your role=internal_user_viewer") {
		t.Errorf("internal_user_viewer 는 401: %d %v", code, body)
	}
	if code, body := w.call(orphanKey, internalOnly); code != 401 || !strings.Contains(body["message"].(string), "Your role=unknown") {
		t.Errorf("사용자 행 없는 키는 역할이 없다: %d %v", code, body)
	}
	if code, _ := w.call(orphanKey, core.Authenticated()); code != 200 {
		t.Errorf("핸들러가 판정하는 라우트는 역할 없이도 통과한다: %d", code)
	}

	// 역할을 바꾸면 캐시가 지워져 바로 반영된다
	_, _ = w.users.Update(ctx, "v1", UserPatch{UserRole: core.To(core.RoleInternalUser)})
	if code, _ := w.call(viewerKey, internalOnly); code != 200 {
		t.Errorf("역할 변경이 반영되지 않았다: %d", code)
	}
}

func TestVirtualKey_BlockedExpiredAndTeamBlockedLikeLitellm(t *testing.T) {
	w := newKeyWorld(t)
	ctx := t.Context()
	_, _ = w.teams.Create(ctx, NewTeam{TeamID: ptr("t1")}, "")
	expires := w.now.Add(time.Hour)
	plain, key := w.generate(NewKey{TeamID: ptr("t1"), Expires: &expires})
	any := core.Authenticated()

	if code, _ := w.call(plain, any); code != 200 {
		t.Fatalf("정상 키: %d", code)
	}

	_, _ = w.teams.Update(ctx, "t1", TeamPatch{Blocked: core.To(true)})
	if code, body := w.call(plain, any); code != 401 || body["message"] != "Authentication Error, Team=t1 is blocked. Update via `/team/unblock` if you're an admin." {
		t.Errorf("팀 차단(캐시도 지워져야 한다): %d %v", code, body)
	}
	_, _ = w.teams.Update(ctx, "t1", TeamPatch{Blocked: core.To(false)})

	_, _ = w.keys.SetBlocked(ctx, key.Token, true, "")
	if code, body := w.call(plain, any); code != 401 || body["message"] != "Authentication Error, Key is blocked. Update via `/key/unblock` if you're an admin." {
		t.Errorf("키 차단: %d %v", code, body)
	}
	_, _ = w.keys.SetBlocked(ctx, key.Token, false, "")

	w.now = w.now.Add(2 * time.Hour)
	code, body := w.call(plain, any)
	// 대시보드는 이 앞부분을 보고 로그아웃한다
	if code != 401 || body["type"] != "expired_key" ||
		body["message"] != "Authentication Error - Expired Key. Key Expiry time 2026-11-21 10:00:00+00:00 and current time 2026-11-21 11:00:00+00:00" {
		t.Errorf("만료: %d %v", code, body)
	}
}

func TestDeletedKey_StopsWorkingAndKeepsCopy(t *testing.T) {
	w := newKeyWorld(t)
	ctx := t.Context()
	plain, key := w.generate(NewKey{KeyAlias: ptr("ci")})
	if code, _ := w.call(plain, core.Authenticated()); code != 200 {
		t.Fatal("정상 키가 거부됐다")
	}
	if _, err := w.keys.Delete(ctx, []string{key.Token}, "admin"); err != nil {
		t.Fatal(err)
	}
	if code, body := w.call(plain, core.Authenticated()); code != 401 || body["type"] != "token_not_found_in_db" {
		t.Errorf("지운 키(캐시도 지워져야 한다): %d %v", code, body)
	}
	if d, err := w.keys.FindDeleted(ctx, key.Token); err != nil || *d.DeletedBy != "admin" {
		t.Errorf("사본: %+v %v", d, err)
	}
}

func TestKeyAlias_IsGloballyUnique(t *testing.T) {
	w := newKeyWorld(t)
	w.generate(NewKey{KeyAlias: ptr("shared")})
	if _, _, err := w.keys.Generate(t.Context(), NewKey{KeyAlias: ptr("shared")}); err != ErrKeyAliasTaken {
		t.Errorf("err = %v", err)
	}
}

func TestTeamDelete_ArchivesTeamKeys(t *testing.T) {
	w := newKeyWorld(t)
	ctx := t.Context()
	_, _ = w.teams.Create(ctx, NewTeam{TeamID: ptr("t1")}, "")
	plain, key := w.generate(NewKey{TeamID: ptr("t1")})
	if err := w.teams.Delete(ctx, []string{"t1"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if code, _ := w.call(plain, core.Authenticated()); code != 401 {
		t.Errorf("지운 팀의 키가 동작한다: %d", code)
	}
	if _, err := w.keys.FindDeleted(ctx, key.Token); err != nil {
		t.Errorf("팀 키 사본이 없다: %v", err)
	}
}

func TestLoginKey_ValidForSevenDays(t *testing.T) {
	w := newKeyWorld(t)
	_, _, _ = w.users.Create(t.Context(), NewUser{UserID: ptr("kim")})
	master, _ := NewMasterKey(testMasterKey)
	key, err := newLoginKeys(master).issue(loginClaims{UserID: "kim", Expires: w.now.Add(loginKeyTTL)})
	if err != nil {
		t.Fatal(err)
	}
	w.now = w.now.Add(7*24*time.Hour - time.Minute)
	if code, body := w.call(key, core.Authenticated()); code != 200 {
		t.Errorf("7일 안: %d %v", code, body)
	}
	w.now = w.now.Add(2 * time.Minute)
	if code, body := w.call(key, core.Authenticated()); code != 401 || body["type"] != "expired_key" {
		t.Errorf("7일 뒤: %d %v", code, body)
	}
}
