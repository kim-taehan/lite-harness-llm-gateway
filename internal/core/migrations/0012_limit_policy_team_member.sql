-- 팀원별 한도(limits 소유, litellm LiteLLM_TeamMembership 의 예산). 대상 id 는 "팀id:사용자id" 다(litellm 의 team_member descriptor 와 같다)
ALTER TABLE limit_policy DROP CONSTRAINT limit_policy_scope_check;
ALTER TABLE limit_policy ADD CONSTRAINT limit_policy_scope_check CHECK (scope IN ('key', 'team', 'user', 'team_member'));
