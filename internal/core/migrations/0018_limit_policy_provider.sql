-- 프로바이더 예산(litellm provider_budget_config): 대상 id 는 배포의 provider 이름이다. 예산을 넘으면 그 프로바이더의 배포를 후보에서 뺀다
ALTER TABLE limit_policy DROP CONSTRAINT limit_policy_scope_check;
ALTER TABLE limit_policy ADD CONSTRAINT limit_policy_scope_check CHECK (scope IN ('key', 'team', 'user', 'team_member', 'end_user', 'tag', 'provider'));
