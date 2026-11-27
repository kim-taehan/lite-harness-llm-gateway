package limits

import (
	"context"
	"time"
)

// 프로바이더 예산(litellm provider_budget_config). 한도 값은 limit_policy 의 provider 종류(max_budget·budget_duration)이고
// 사용액은 응답 뒤 배포의 provider 로 더한다. 요청 단위 검사(Gate)가 아니라 배포를 고르는 단계가 ExhaustedProviders 를 본다

// ExhaustedProviders 는 예산을 다 쓴 프로바이더와 다음 리셋 시각이다(리셋 주기가 없으면 nil). 예산이 없는 프로바이더는 결과에 없다.
// 리셋은 예산 리셋 작업(1분 주기)이 하므로 리셋 시각이 지나도 그 작업이 돌 때까지 막혀 있을 수 있다
func (p *Policies) ExhaustedProviders(ctx context.Context) (map[string]*time.Time, error) {
	rows, err := p.db.Q(ctx).Query(ctx, `
		SELECT target_id, budget_reset_at FROM limit_policy
		WHERE scope = 'provider' AND max_budget IS NOT NULL AND spend >= max_budget`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]*time.Time{}
	for rows.Next() {
		var provider string
		var resetAt *time.Time
		if err := rows.Scan(&provider, &resetAt); err != nil {
			return nil, err
		}
		result[provider] = resetAt
	}
	return result, rows.Err()
}

// ExhaustedProviders 는 Gate 를 통해서도 부를 수 있게 해 추론 경로가 Policies 를 따로 받지 않게 한다
func (g *Gate) ExhaustedProviders(ctx context.Context) (map[string]*time.Time, error) {
	return g.policies.ExhaustedProviders(ctx)
}

// FindAll 은 종류 하나의 모든 대상과 한도다(관리 화면·조회용). 한도를 정하지 않은 대상은 결과에 없다
func (p *Policies) FindAll(ctx context.Context, scope Scope) (map[string]Policy, error) {
	rows, err := p.db.Q(ctx).Query(ctx, "SELECT target_id FROM limit_policy WHERE scope = $1", scope)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return p.Find(ctx, scope, ids)
}
