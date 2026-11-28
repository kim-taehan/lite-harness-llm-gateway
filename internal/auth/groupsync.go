package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"llm-gateway/internal/core"
)

// groupAdminMetadataKey 는 그룹 때문에 관리자가 된 사용자에게 남기는 표시다. 이 표시가 있어야 그룹을 떠날 때 역할을 되돌린다
// (관리자가 손으로 지정한 관리자는 그룹과 상관없이 유지하려고)
const groupAdminMetadataKey = "group_admin"

// groupSyncActor 는 그룹 동기화가 지우는 팀 키의 기록에 남는 행위자다
const groupSyncActor = "group_sync"

// syncGroups 는 SSO 로그인 때 Keycloak 이 알려 준 그룹으로 팀원 기록과 관리자 역할을 표에 맞춘다.
// 로그인 키는 요청마다 사용자·팀 행을 읽으므로, 여기서 맞춰 두면 그 키의 권한이 바로 달라진다.
// 표에 나오는 팀만 "그룹이 기준"이다: 그룹이 있으면 넣고, 없으면 뺀다. 표에 없는 팀의 수동 소속은 건드리지 않는다.
// 응답에 그룹 클레임이 아예 없으면(Keycloak 매퍼를 빼먹음) 아무것도 바꾸지 않는다. 그렇지 않으면 설정 실수 하나로 전원이 팀에서 빠진다.
// 한 번에 끝나는 트랜잭션은 아니다(팀마다 따로 처리하는 단계들이 모두 같은 결과로 수렴하고, 중간에 멈추면 다음 로그인이 이어서 맞춘다)
func (l *Login) syncGroups(r *http.Request, user User, claims oidcClaims) (User, error) {
	if !l.groups.Enabled() {
		return user, nil
	}
	tokenGroups, present := groupsIn(claims.Raw, l.groups.claim())
	if !present {
		l.logger.Warn("SSO 응답에 그룹 클레임이 없어 소속을 맞추지 않았다. Keycloak 그룹 매퍼의 'Add to userinfo' 와 GATEWAY_GROUPS_CLAIM 을 확인한다",
			"claim", l.groups.claim())
		return user, nil
	}
	desiredTeams, desiredAdmin := l.groups.granted(tokenGroups)
	added, removed, err := l.syncTeams(r.Context(), user.UserID, desiredTeams)
	if err != nil {
		return user, err
	}
	user, adminChange, err := l.syncAdminRole(r.Context(), user, desiredAdmin)
	if err != nil {
		return user, err
	}
	if len(added) > 0 || len(removed) > 0 || adminChange != "" {
		values := map[string]any{"method": "sso_groups", "matched_groups": l.groups.matched(tokenGroups)}
		if len(added) > 0 {
			values["teams_added"] = added
		}
		if len(removed) > 0 {
			values["teams_removed"] = removed
		}
		if adminChange != "" {
			values["admin"] = adminChange
		}
		l.record(r, "group_sync", user.UserID, values)
	}
	return user, nil
}

// syncTeams 는 표에 나오는 팀마다 소속을 그룹에 맞춘다. 표가 가리키는 팀이 아직 없으면 건너뛰고(표 오타로 로그인이 막히지 않게) 경고한다.
// 팀에서 빼면 그 사용자의 그 팀 키도 함께 지워진다(수동으로 팀원을 뺄 때와 같다)
func (l *Login) syncTeams(ctx context.Context, userID string, desired []string) (added, removed []string, err error) {
	for _, teamID := range l.groups.managedTeams() {
		_, member, err := l.teams.MembershipIn(ctx, teamID, userID)
		if err != nil {
			return nil, nil, err
		}
		switch want := slices.Contains(desired, teamID); {
		case want && !member:
			if _, err := l.teams.Find(ctx, teamID); errors.Is(err, ErrTeamNotFound) {
				l.logger.Warn("그룹 표의 팀이 없어 건너뛴다", "team_id", teamID)
				continue
			} else if err != nil {
				return nil, nil, err
			}
			if err := l.teams.addMember(ctx, teamID, userID, TeamRoleUser); err != nil {
				return nil, nil, err
			}
			added = append(added, teamID)
		case !want && member:
			if _, err := l.teams.RemoveMember(ctx, teamID, &userID, nil, groupSyncActor); err != nil {
				return nil, nil, err
			}
			removed = append(removed, teamID)
		}
	}
	return added, removed, nil
}

// syncAdminRole 은 관리자 그룹에 맞춰 역할을 올리고 내린다. 올릴 때 원래 역할과 표시를 남기고, 표시가 있는 사용자만 내린다.
// 돌려주는 문자열은 바뀐 내용이다("promoted"·"demoted", 바뀐 것이 없으면 빈 문자열)
func (l *Login) syncAdminRole(ctx context.Context, user User, desiredAdmin bool) (User, string, error) {
	isAdmin := user.EffectiveRole() == core.RoleProxyAdmin
	marker, flagged := user.Metadata[groupAdminMetadataKey].(map[string]any)
	switch {
	case desiredAdmin && !isAdmin:
		previous := ""
		if user.UserRole != nil {
			previous = string(*user.UserRole)
		}
		metadata := withMetadata(user.Metadata, groupAdminMetadataKey, map[string]any{"previous_role": previous})
		admin := core.RoleProxyAdmin
		updated, err := l.users.Update(ctx, user.UserID, UserPatch{UserRole: core.Change[core.Role]{Set: true, Value: &admin},
			Metadata: core.Change[map[string]any]{Set: true, Value: &metadata}})
		return updated, "promoted", err
	case !desiredAdmin && flagged:
		cleared := withMetadata(user.Metadata, groupAdminMetadataKey, nil)
		patch := UserPatch{Metadata: core.Change[map[string]any]{Set: true, Value: &cleared}}
		change := ""
		if isAdmin {
			// 그 사이 관리자가 역할을 손으로 바꿨으면(이미 관리자가 아니면) 표시만 지운다
			previous, _ := marker["previous_role"].(string)
			var role *core.Role
			if previous != "" {
				r := core.Role(previous)
				role = &r
			}
			patch.UserRole = core.Change[core.Role]{Set: true, Value: role}
			change = "demoted"
		}
		updated, err := l.users.Update(ctx, user.UserID, patch)
		return updated, change, err
	}
	return user, "", nil
}

// matched 는 토큰의 그룹 중 표에 있는 이름이다(감사 기록에 남길 값. 표에 없는 그룹 이름은 남기지 않는다)
func (m GroupMapping) matched(tokenGroups []string) []string {
	var names []string
	for name := range m.Groups {
		if slices.Contains(tokenGroups, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// withMetadata 는 metadata 를 복사해 key 를 바꾼다(value 가 nil 이면 지운다)
func withMetadata(metadata map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(metadata)+1)
	for k, v := range metadata {
		out[k] = v
	}
	if value == nil {
		delete(out, key)
	} else {
		out[key] = value
	}
	return out
}
