package admin

import (
	"context"
	"encoding/json"
	"errors"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/inference"
	"llm-gateway/internal/limits"
)

// 이전 값(before_value): 바꾸는 요청을 처리하기 전에 대상의 모습을 읽어 두었다가 감사 기록에 함께 남긴다.
// "무엇으로 바꿨나"(updated_values)만으로는 "무엇에서 바꿨나"를 알 수 없어서다. 응답에 쓰는 행과 같은 모양이다.
// 만드는 요청은 이전 값이 없고, 읽지 못하거나 없는 대상은 이전 값 없이 기록한다(변경 자체를 막지 않는다)

// maxBeforeSnapshots 는 한 요청에서 이전 값을 읽는 대상 수의 상한이다
const maxBeforeSnapshots = 50

// beforeValue 는 id 로 대상을 읽는다. 없는 대상은 (nil, nil) 이다
type beforeValue func(ctx context.Context, id string) (any, error)

// valuesBefore 는 요청이 바꿀 대상들의 이전 모습이다(id → 값). 만들기이거나 이전 값을 읽는 방법이 없는 테이블이면 비어 있다.
// 비밀 값은 응답 행에도 없지만 한 번 더 뺀다(감사 기록에는 어떤 경우에도 비밀이 없어야 한다)
func (h *Handlers) valuesBefore(ctx context.Context, action, table string, ids []string) map[string]any {
	read := h.beforeReader(table)
	// 대상이 너무 많으면(일괄 삭제 등) 이전 값을 읽지 않는다. 권한 검사 전에 읽으므로 호출자가 요청 하나로 DB 읽기를 부풀리지 못하게 한다
	if action == "created" || read == nil || len(ids) == 0 || len(ids) > maxBeforeSnapshots {
		return nil
	}
	before := make(map[string]any, len(ids))
	for _, id := range ids {
		value, err := read(ctx, id)
		if err != nil {
			h.logger.Warn("감사 기록의 이전 값을 읽지 못했다", "table", table, "object_id", id, "err", err)
			continue
		}
		if value != nil {
			before[id] = scrubbedJSON(value)
		}
	}
	return before
}

// scrubbedJSON 은 값을 JSON 으로 바꿨다가 읽어 비밀 필드를 모든 깊이에서 뺀 일반 값이다
func scrubbedJSON(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var generic any
	if json.Unmarshal(data, &generic) != nil {
		return nil
	}
	return scrubValue(generic)
}

// beforeReader 는 감사 테이블 이름별로 이전 값을 읽는 함수다. 구성원·초대처럼 읽을 수 있는 한 줄이 없는 테이블은 nil 이다
func (h *Handlers) beforeReader(table string) beforeValue {
	switch table {
	case auditKeyTable:
		return func(ctx context.Context, id string) (any, error) {
			key, err := h.keys.Find(ctx, id) // id 가 해시가 아닌 별명이면 못 찾는다. 이전 값 없이 둔다
			if errors.Is(err, auth.ErrKeyNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return h.rows.keyRowOf(ctx, key)
		}
	case auditTeamTable:
		return func(ctx context.Context, id string) (any, error) {
			team, err := h.teams.Find(ctx, id)
			if errors.Is(err, auth.ErrTeamNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return h.rows.teamRowOf(ctx, team)
		}
	case auditUserTable:
		return func(ctx context.Context, id string) (any, error) {
			user, err := h.users.Find(ctx, id)
			if errors.Is(err, auth.ErrUserNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			rows, err := h.rows.userRows(ctx, []auth.User{user})
			if err != nil {
				return nil, err
			}
			return rows[0], nil
		}
	case auditModelTable:
		return func(ctx context.Context, id string) (any, error) {
			d, err := h.deployments.Find(ctx, id)
			if errors.Is(err, inference.ErrDeploymentNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return deploymentRow(d), nil
		}
	case auditOrganizationTable:
		return func(ctx context.Context, id string) (any, error) {
			org, err := h.organizations.Find(ctx, id)
			if errors.Is(err, auth.ErrOrganizationNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return firstRow(h.organizationRows(ctx, []auth.Organization{org}))
		}
	case auditProjectTable:
		return func(ctx context.Context, id string) (any, error) {
			project, err := h.projects.Find(ctx, id)
			if errors.Is(err, auth.ErrProjectNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return firstRow(h.projectRows(ctx, []auth.Project{project}))
		}
	case auditTagTable:
		return func(ctx context.Context, id string) (any, error) {
			found, err := h.tags.Find(ctx, []string{id})
			if err != nil || len(found) == 0 {
				return nil, err
			}
			rows, err := h.tagRows(ctx, []limits.Tag{found[id]})
			if err != nil {
				return nil, err
			}
			return rows[id], nil
		}
	case auditCustomerTable:
		return func(ctx context.Context, id string) (any, error) {
			customer, err := h.customers.Find(ctx, id)
			if errors.Is(err, limits.ErrCustomerNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return firstRow(h.customerRows(ctx, []limits.Customer{customer}))
		}
	case auditConfigTable:
		return h.configBefore
	}
	return nil
}

// firstRow 는 한 대상의 행 목록에서 첫 행이다
func firstRow(rows []row, err error) (any, error) {
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// configBefore 는 설정 성격의 변경(auditConfigTable)의 이전 값이다. 대상 id 가 설정 이름(화면 설정 4가지), IP 허용 목록(general_settings),
// 프로바이더 예산(프로바이더 이름)이다. 모델 허브처럼 따로 읽을 값이 없는 것은 이전 값 없이 둔다
func (h *Handlers) configBefore(ctx context.Context, id string) (any, error) {
	switch id {
	case uiSettingsForm.name, uiThemeForm.name, defaultTeamForm.name, internalUserForm.name:
		return h.settings.Get(ctx, id)
	case "general_settings":
		list, err := h.ipAllow.List(ctx)
		if err != nil {
			return nil, err
		}
		return row{"allowed_ips": list}, nil
	}
	policies, err := h.limits.Find(ctx, limits.ScopeProvider, []string{id})
	if err != nil {
		return nil, err
	}
	if policy, ok := policies[id]; ok {
		return providerBudgetRow(policy), nil
	}
	return nil, nil
}
