package admin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/limits"
)

// litellm 가상 키 관리 API(docs/litellm-inventory/J_key_contract.md 3장).
// regenerate 의 유예 기간(grace_period, 옛 키를 잠시 더 받기)은 별도 표가 필요해 뒤 단계로 미룬다(설계 9장)

func teamMemberPermissionError(route string, m auth.Membership) core.ProxyError {
	return core.ProxyError{Status: 401, Type: "team_member_permission_error", Param: route,
		Message: fmt.Sprintf("Team member does not have permissions for endpoint: %s. You only have access to the following endpoints: %s for team %s. "+
			"To create keys for this team, ask a team admin to grant you access.", route, pyList(memberKeyRoutes(m.MemberPermissions)), m.TeamID)}
}

func pyList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = pyString(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// plainProxyError 는 detail 이 문자열인 HTTPException 을 litellm 이 감싼 모양이다(message 에 repr 이 없다)
func plainProxyError(status int, message string) core.ProxyError {
	return core.ProxyError{Status: status, Message: message, Type: "internal_server_error"}
}

func aliasTakenError(alias string) core.ProxyError {
	return core.ProxyError{Status: 400, Type: "bad_request_error", Param: "key_alias",
		Message: "Key with alias '" + alias + "' already exists. Unique key aliases across all keys are required."}
}

type keyFields struct {
	KeyAlias      core.Change[string]         `json:"key_alias"`
	Duration      core.Change[string]         `json:"duration"`
	Models        core.Change[[]string]       `json:"models"`
	UserID        core.Change[string]         `json:"user_id"`
	TeamID        core.Change[string]         `json:"team_id"`
	Metadata      core.Change[map[string]any] `json:"metadata"`
	Blocked       core.Change[bool]           `json:"blocked"`
	KeyType       *string                     `json:"key_type"`
	AllowedRoutes []string                    `json:"allowed_routes"`
	limitFields
}

func (h *Handlers) generateKey(w http.ResponseWriter, r *http.Request) {
	h.issueKey(w, r, "/key/generate", false)
}

// generateServiceAccountKey 는 팀 소유이고 사용자가 없는 키다(J 3.2). litellm 처럼 user_id 를 비우고 metadata.service_account_id 를 채운다
func (h *Handlers) generateServiceAccountKey(w http.ResponseWriter, r *http.Request) {
	h.issueKey(w, r, "/key/service-account/generate", true)
}

func (h *Handlers) issueKey(w http.ResponseWriter, r *http.Request, route string, serviceAccount bool) {
	var req struct {
		Key *string `json:"key"`
		keyFields
	}
	if _, ok := requestBody(w, r, &req); !ok {
		return
	}
	ctx := r.Context()
	p := principal(ctx)
	teamID := nonEmpty(req.TeamID.Value) // litellm: team_id "" 는 null
	userID := req.UserID.Value
	if serviceAccount && teamID == nil {
		detailError(400, "team_id is required for service account keys").Write(w)
		return
	}
	if !p.IsAdmin() {
		if denial, denied := h.checkKeyGeneration(ctx, p, req.keyFields, teamID, &userID, route); denied {
			denial.Write(w)
			return
		}
	}
	input, err := h.newKeyInput(req.Key, req.keyFields, p.UserID, teamID, userID, serviceAccount)
	if err != nil {
		plainProxyError(500, err.Error()).Write(w)
		return
	}

	var plain string
	var key auth.Key
	err = h.db.InTx(ctx, func(ctx context.Context) error {
		var err error
		if plain, key, err = h.keys.Generate(ctx, input); err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeKey, key.Token, req.patch())
		return err
	})
	var invalid *limits.InvalidError
	var badKey *auth.InvalidCustomKeyError
	switch {
	case err == nil:
		kr, err := h.rows.keyRowOf(ctx, key)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		// 원문은 이 응답에서만 한 번 나간다. token·token_id 는 해시다(litellm 과 같다)
		kr["key"] = plain
		kr["token_id"] = key.Token
		core.WriteJSON(w, http.StatusOK, kr)
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	case errors.As(err, &badKey):
		proxyError(400, badKey.Message).Write(w)
	case errors.Is(err, auth.ErrKeyAliasTaken):
		aliasTakenError(*input.KeyAlias).Write(w)
	case errors.Is(err, auth.ErrTeamNotFound):
		plainProxyError(400, "Unable to find team object in database. Team ID: "+derefOr(teamID, "")).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// newKeyInput 은 /key/generate 본문을 저장소 입력으로 바꾼다. 서비스 계정 키는 사용자가 없고 metadata.service_account_id 를 채운다.
// 만료 기간이 틀리면 오류다
func (h *Handlers) newKeyInput(key *string, req keyFields, createdBy string, teamID, userID *string, serviceAccount bool) (auth.NewKey, error) {
	metadata := req.Metadata.Or(nil)
	if serviceAccount {
		userID = nil
		if metadata == nil {
			metadata = map[string]any{}
		}
		if _, ok := metadata["service_account_id"]; !ok {
			metadata["service_account_id"] = cmp.Or(derefOr(nonEmpty(req.KeyAlias.Value), ""), core.NewUUID())
		}
	}
	input := auth.NewKey{Key: key, KeyAlias: nonEmpty(req.KeyAlias.Value), UserID: userID, TeamID: teamID,
		Models: req.Models.Or(nil), Metadata: metadata, KeyType: derefOr(req.KeyType, "default"),
		AllowedRoutes: req.AllowedRoutes, Blocked: req.Blocked.Value, CreatedBy: createdBy}
	if d := req.Duration.Value; d != nil && *d != "" {
		expires, err := expiresAfter(*d, h.now().UTC())
		if err != nil {
			return auth.NewKey{}, err
		}
		input.Expires = &expires
	}
	return input, nil
}

// keyCheckFailed 는 권한 검사 중 DB 오류다. 원인은 로그에만 남기고 응답에는 내부 내용을 싣지 않는다
func (h *Handlers) keyCheckFailed(err error) core.ProxyError {
	h.logger.Error("키 권한 검사 실패", "err", err)
	return plainProxyError(500, "Internal Server Error")
}

// checkKeyGeneration 은 비관리자의 키 생성 권한이다(J 3.1). userID 를 기본값(호출자)으로 채운다
func (h *Handlers) checkKeyGeneration(ctx context.Context, p auth.Principal, req keyFields, teamID *string, userID **string, route string) (core.ProxyError, bool) {
	if len(req.AllowedRoutes) > 0 || (req.KeyType != nil && *req.KeyType == "management") {
		return plainProxyError(403, pyDict("error", "Only proxy admins can set `allowed_routes` on a key. Use `key_type` to pick a preset route bucket instead.")), true
	}
	if *userID == nil {
		self := p.UserID
		*userID = &self
	}
	if teamID == nil {
		if **userID != p.UserID {
			return plainProxyError(403, fmt.Sprintf("User can only create keys for themselves. Got user_id=%s, Your ID=%s", **userID, p.UserID)), true
		}
		return core.ProxyError{}, false
	}
	m, member, err := h.membership(ctx, *teamID, p.UserID)
	if err != nil {
		return h.keyCheckFailed(err), true
	}
	if !member {
		// 팀이 없는 것과 팀원이 아닌 것은 문구가 다르다
		if _, err := h.teams.Find(ctx, *teamID); errors.Is(err, auth.ErrTeamNotFound) {
			return plainProxyError(400, "Team not found for team_id="+*teamID+". Non-admin users cannot create keys for non-existent teams."), true
		} else if err != nil {
			return h.keyCheckFailed(err), true
		}
		return plainProxyError(400, "User="+p.UserID+" not assigned to team="+*teamID), true
	}
	if !mayCall(m, route) {
		return teamMemberPermissionError(route, m), true
	}
	if **userID != p.UserID {
		_, isMember, err := h.membership(ctx, *teamID, **userID)
		if err != nil {
			return h.keyCheckFailed(err), true
		}
		if !isMember {
			return plainProxyError(400, "User="+**userID+" not assigned to team="+*teamID), true
		}
	}
	return core.ProxyError{}, false
}

func (h *Handlers) updateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key *string `json:"key"`
		keyFields
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.Key == nil && req.KeyAlias.Value == nil {
		validationError{{Type: "value_error", Loc: []any{"body"}, Msg: "Value error, Either 'key' or 'key_alias' must be provided", Input: raw}}.write(w)
		return
	}
	// /key/update 는 litellm 에서 auth_error·"None" 으로 감싼다(KM:4069-4088)
	ctx := r.Context()
	p := principal(ctx)
	key, err := h.keyTarget(ctx, req.Key, req.KeyAlias.Value)
	if errors.Is(err, auth.ErrKeyNotFound) {
		keyNotFound(req.Key, req.KeyAlias.Value).Write(w)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !p.IsAdmin() {
		if denial, denied := h.checkKeyUpdate(ctx, p, key, req.keyFields); denied {
			denial.Write(w)
			return
		}
	}
	patch, err := h.keyPatch(req.keyFields, req.Key == nil, p.UserID)
	if err != nil {
		authStyle(400, "Authentication Error, "+err.Error()).Write(w)
		return
	}

	var updated auth.Key
	err = h.db.InTx(ctx, func(ctx context.Context) error {
		var err error
		if updated, err = h.keys.Update(ctx, key.Token, patch); err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeKey, key.Token, req.patch())
		return err
	})
	var invalid *limits.InvalidError
	switch {
	case err == nil:
		kr, err := h.rows.keyRowOf(ctx, updated)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		kr["key"] = derefOr(req.Key, key.Token)
		core.WriteJSON(w, http.StatusOK, kr)
	case errors.As(err, &invalid):
		authStyle(400, pyDict("error", invalid.Message)).Write(w)
	case errors.Is(err, auth.ErrKeyAliasTaken):
		aliasTakenError(*req.KeyAlias.Value).Write(w)
	case errors.Is(err, auth.ErrTeamNotFound):
		authStyle(400, "Team not found for team_id="+derefOr(req.TeamID.Value, "")+". Non-admin users cannot set keys to non-existent teams.").Write(w)
	default:
		h.internalError(w, r, err)
	}
}

func keyNotFound(key, alias *string) core.ProxyError {
	message, param := "Key not found.", "key"
	if key == nil {
		message, param = "Key not found. No key with key_alias='"+*alias+"'.", "key_alias"
	}
	return core.ProxyError{Status: 404, Message: message, Type: "not_found_error", Param: param}
}

// keyPatch 는 /key/update 본문을 저장소가 받는 바꿀 값으로 바꾼다. 만료 기간이 틀리면 오류다
func (h *Handlers) keyPatch(req keyFields, foundByAlias bool, updatedBy string) (auth.KeyPatch, error) {
	patch := auth.KeyPatch{KeyAlias: req.KeyAlias, UserID: req.UserID, Models: req.Models, Metadata: req.Metadata, Blocked: req.Blocked, UpdatedBy: updatedBy}
	if foundByAlias {
		patch.KeyAlias = core.Change[string]{} // alias 로 찾았으면 그 alias 는 바꾸는 값이 아니다
	}
	// litellm: team_id "" 는 보내지 않은 것으로 본다
	if req.TeamID.Set && (req.TeamID.Value == nil || *req.TeamID.Value != "") {
		patch.TeamID = req.TeamID
	}
	// litellm: duration null 이나 "-1" 은 만료 해제, "" 는 무시, 값이 있으면 지금부터
	if req.Duration.Set {
		switch d := req.Duration.Value; {
		case d == nil || *d == "-1":
			patch.Expires = core.Change[time.Time]{Set: true}
		case *d != "":
			expires, err := expiresAfter(*d, h.now().UTC())
			if err != nil {
				return auth.KeyPatch{}, err
			}
			patch.Expires = core.To(expires)
		}
	}
	return patch, nil
}

func (h *Handlers) keyTarget(ctx context.Context, key, alias *string) (auth.Key, error) {
	if key != nil {
		return h.keys.Find(ctx, auth.TokenOf(*key))
	}
	return h.keys.FindByAlias(ctx, *alias)
}

// checkKeyUpdate 는 비관리자의 키 수정 권한이다(J 3.3): 팀 키는 팀 admin, 개인 키는 작성자이자 소유자만. 예산 변경은 admin 만
func (h *Handlers) checkKeyUpdate(ctx context.Context, p auth.Principal, key auth.Key, req keyFields) (core.ProxyError, bool) {
	if req.UserID.Set {
		switch v := req.UserID.Value; {
		case v == nil:
			return authStyle(403, "Non-admin users cannot remove the user_id from a key."), true
		case key.UserID == nil || *v != *key.UserID:
			return authStyle(403, fmt.Sprintf("Non-admin caller is not allowed to rebind the key from user=%s to user=%s", derefOr(key.UserID, "None"), *v)), true
		}
	}
	adminDenied := authStyle(403, pyDict("error", fmt.Sprintf("Only proxy admins, team admins, or org admins can call /key/update. user_role=%s, user_id=%s", p.Role, p.UserID)))
	if key.TeamID != nil {
		m, member, err := h.membership(ctx, *key.TeamID, p.UserID)
		if err != nil {
			return h.keyCheckFailed(err), true
		}
		if !member {
			return core.ProxyError{Status: 401, Type: "team_member_permission_error", Param: "/key/update",
				Message: fmt.Sprintf("User %s does not belong to team %s. Team-scoped key management endpoints can only be used for keys in your own team.", p.UserID, *key.TeamID)}, true
		}
		if !mayCall(m, "/key/update") {
			return teamMemberPermissionError("/key/update", m), true
		}
		return core.ProxyError{}, false
	}
	creator := key.CreatedBy != nil && key.UserID != nil && *key.CreatedBy == p.UserID && *key.UserID == p.UserID
	budgetChange := req.MaxBudget.Set || req.SoftBudget.Set
	if !creator || budgetChange {
		return adminDenied, true
	}
	return core.ProxyError{}, false
}

func (h *Handlers) deleteKeys(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keys       []string `json:"keys"`
		KeyAliases []string `json:"key_aliases"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if len(req.Keys) == 0 && len(req.KeyAliases) == 0 {
		validationError{{Type: "value_error", Loc: []any{"body"}, Msg: "Value error, At least one of 'keys' or 'key_aliases' must be provided.", Input: raw}}.write(w)
		return
	}
	ctx := r.Context()
	p := principal(ctx)
	found, err := h.deleteTargets(ctx, req.Keys, req.KeyAliases)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if len(found) == 0 {
		proxyError(404, "No keys found").Write(w)
		return
	}
	for _, k := range found {
		allowed, err := h.canModifyKey(ctx, p, k, "/key/delete")
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		if !allowed {
			proxyError(403, "You are not authorized to delete this key").Write(w)
			return
		}
	}
	tokens := make([]string, len(found))
	for i, k := range found {
		tokens[i] = k.Token
	}
	err = h.db.InTx(ctx, func(ctx context.Context) error {
		if _, err := h.keys.Delete(ctx, tokens, p.UserID); err != nil {
			return err
		}
		return h.limits.Delete(ctx, limits.ScopeKey, tokens)
	})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	// litellm 은 일부만 찾았어도 요청 목록을 그대로 돌려준다(J 3.4)
	deleted := req.Keys
	if len(deleted) == 0 {
		deleted = req.KeyAliases
	}
	core.WriteJSON(w, http.StatusOK, row{"deleted_keys": deleted})
}

func (h *Handlers) deleteTargets(ctx context.Context, keys, aliases []string) ([]auth.Key, error) {
	tokens := make([]string, len(keys))
	for i, k := range keys {
		tokens[i] = auth.TokenOf(k)
	}
	found, err := h.keys.FindAll(ctx, tokens)
	if err != nil {
		return nil, err
	}
	for _, alias := range aliases {
		k, err := h.keys.FindByAlias(ctx, alias)
		if errors.Is(err, auth.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		found = append(found, k)
	}
	return found, nil
}

// canModifyKey 는 litellm can_modify_verification_token 이다: proxy_admin, 팀 키면 팀 admin 이나 소유자, 개인 키면 소유자
func (h *Handlers) canModifyKey(ctx context.Context, p auth.Principal, k auth.Key, route string) (bool, error) {
	if p.IsAdmin() {
		return true, nil
	}
	if k.UserID != nil && *k.UserID == p.UserID && p.UserID != "" {
		return true, nil
	}
	if k.TeamID != nil {
		return h.teamMemberMay(ctx, p, *k.TeamID, route)
	}
	return false, nil
}

// regenerateKey 는 POST /key/{key}/regenerate 다(J 3.9). 본문의 key 가 경로보다 우선한다.
// 바꾸는 값(alias·기간·한도)은 새 키에 함께 적용한다. grace_period(옛 키를 잠시 더 받기)는 아직 없다(설계 9장)
func (h *Handlers) regenerateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key      *string             `json:"key"`
		NewKey   *string             `json:"new_key"`
		KeyAlias core.Change[string] `json:"key_alias"`
		Duration *string             `json:"duration"`
		limitFields
	}
	if _, ok := requestBody(w, r, &req); !ok {
		return
	}
	input := cmp.Or(derefOr(req.Key, ""), r.PathValue("key"))
	if input == "" {
		proxyError(400, "No key passed in.").Write(w)
		return
	}
	ctx := r.Context()
	p := principal(ctx)
	key, err := h.keys.Find(ctx, auth.TokenOf(input))
	if errors.Is(err, auth.ErrKeyNotFound) {
		proxyError(404, "Key "+input+" not found.").Write(w)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	allowed, err := h.canModifyKey(ctx, p, key, "/key/regenerate")
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !allowed {
		proxyError(403, "You are not authorized to regenerate this key").Write(w)
		return
	}
	patch := auth.KeyPatch{KeyAlias: req.KeyAlias, UpdatedBy: p.UserID}
	if req.Duration != nil && *req.Duration != "" { // 대시보드는 빈 문자열을 보낼 수 있고 litellm 은 무시한다
		expires, err := expiresAfter(*req.Duration, h.now().UTC())
		if err != nil {
			plainProxyError(500, err.Error()).Write(w)
			return
		}
		patch.Expires = core.To(expires)
	}
	plain, regenerated, err := h.rotateKey(ctx, key.Token, req.NewKey, p.UserID, patch, req.patch())
	var invalid *limits.InvalidError
	var badKey *auth.InvalidCustomKeyError
	switch {
	case err == nil:
		kr, err := h.rows.keyRowOf(ctx, regenerated)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		// litellm: token 은 빼고(null) token_id 에 새 해시, key 에 새 원문(이 응답에서만)
		kr["key"], kr["token_id"], kr["token"] = plain, regenerated.Token, nil
		core.WriteJSON(w, http.StatusOK, kr)
	case errors.As(err, &badKey):
		proxyError(400, badKey.Message).Write(w)
	case errors.Is(err, auth.ErrKeyAliasTaken):
		aliasTakenError(derefOr(req.KeyAlias.Value, "")).Write(w)
	case errors.As(err, &invalid):
		proxyError(400, invalid.Message).Write(w)
	default:
		h.internalError(w, r, err)
	}
}

// rotateKey 는 새 해시로 키를 바꾸고, 그 키의 한도를 새 해시로 옮기고, 바꾸는 값을 적용한다. 한 트랜잭션이다
func (h *Handlers) rotateKey(ctx context.Context, oldToken string, newKey *string, by string, patch auth.KeyPatch, limitPatch limits.Patch) (string, auth.Key, error) {
	var plain string
	var rotated auth.Key
	err := h.db.InTx(ctx, func(ctx context.Context) error {
		var err error
		if plain, rotated, err = h.keys.Regenerate(ctx, oldToken, newKey, by); err != nil {
			return err
		}
		if err := h.limits.Retarget(ctx, limits.ScopeKey, oldToken, rotated.Token); err != nil {
			return err
		}
		if rotated, err = h.keys.Update(ctx, rotated.Token, patch); err != nil {
			return err
		}
		_, err = h.limits.Apply(ctx, limits.ScopeKey, rotated.Token, limitPatch)
		return err
	})
	return plain, rotated, err
}

func (h *Handlers) blockKey(w http.ResponseWriter, r *http.Request)   { h.setKeyBlocked(w, r, true) }
func (h *Handlers) unblockKey(w http.ResponseWriter, r *http.Request) { h.setKeyBlocked(w, r, false) }

// setKeyBlocked 는 litellm 처럼 권한을 존재 확인보다 먼저 본다(J 3.8). 개인 키는 소유자도 막을 수 없다
func (h *Handlers) setKeyBlocked(w http.ResponseWriter, r *http.Request, blocked bool) {
	var req struct {
		Key *string `json:"key"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.Key == nil {
		validationError{missing(raw, "key")}.write(w)
		return
	}
	if !validKeyFormat(*req.Key) {
		core.ProxyError{Status: 400, Message: "Invalid key format.", Type: "bad_request_error", Param: "key"}.Write(w)
		return
	}
	ctx := r.Context()
	p := principal(ctx)
	token := auth.TokenOf(*req.Key)
	route := "/key/block"
	if !blocked {
		route = "/key/unblock"
	}
	key, err := h.keys.Find(ctx, token)
	if errors.Is(err, auth.ErrKeyNotFound) {
		if p.IsAdmin() {
			core.ProxyError{Status: 404, Message: "Key not found.", Type: "not_found_error", Param: "key"}.Write(w)
		} else {
			detailError(404, "Key not found: "+token).Write(w)
		}
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	allowed := p.IsAdmin()
	if !allowed && key.TeamID != nil {
		if allowed, err = h.teamMemberMay(ctx, p, *key.TeamID, route); err != nil {
			h.internalError(w, r, err)
			return
		}
	}
	if !allowed {
		detailError(403, fmt.Sprintf("Only proxy admins, team admins, or org admins can call %s. user_role=%s, user_id=%s", route, p.Role, p.UserID)).Write(w)
		return
	}
	updated, err := h.keys.SetBlocked(ctx, token, blocked, p.UserID)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	kr, err := h.rows.keyRowOf(ctx, updated)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, kr)
}

// resetKeySpend 는 litellm 처럼 값 검증을 권한 검사보다 먼저 한다. 오류는 (B) 모양이다(J 3.10)
func (h *Handlers) resetKeySpend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResetTo *float64 `json:"reset_to"`
	}
	raw, ok := requestBody(w, r, &req)
	if !ok {
		return
	}
	if req.ResetTo == nil {
		validationError{missing(raw, "reset_to")}.write(w)
		return
	}
	ctx := r.Context()
	p := principal(ctx)
	input := r.PathValue("key")
	key, err := h.keys.Find(ctx, auth.TokenOf(input))
	if errors.Is(err, auth.ErrKeyNotFound) {
		detailError(404, "Key "+input+" not found.").Write(w)
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	policies, err := h.limits.Find(ctx, limits.ScopeKey, []string{key.Token})
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	policy := policies[key.Token]
	resetTo := *req.ResetTo
	switch {
	case resetTo < 0:
		detailError(400, "reset_to must be >= 0").Write(w)
		return
	case resetTo > policy.Spend:
		detailError(400, fmt.Sprintf("reset_to (%v) must be <= current spend (%v)", resetTo, policy.Spend)).Write(w)
		return
	case policy.MaxBudget != nil && resetTo > *policy.MaxBudget:
		detailError(400, fmt.Sprintf("reset_to (%v) must be <= budget (%v)", resetTo, *policy.MaxBudget)).Write(w)
		return
	}
	allowed := p.IsAdmin()
	if !allowed && key.TeamID != nil {
		if allowed, err = h.teamMemberMay(ctx, p, *key.TeamID, "/key/{key_id}/reset_spend"); err != nil {
			h.internalError(w, r, err)
			return
		}
	}
	if !allowed {
		detailError(403, "You must be a proxy admin or team admin to reset key spend").Write(w)
		return
	}
	previous, err := h.limits.SetSpend(ctx, limits.ScopeKey, key.Token, resetTo)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	core.WriteJSON(w, http.StatusOK, row{"key_hash": key.Token, "spend": resetTo, "previous_spend": previous,
		"max_budget": policy.MaxBudget, "budget_reset_at": utc(policy.BudgetResetAt)})
}

func nonEmpty(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}
