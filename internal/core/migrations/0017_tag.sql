-- 태그(litellm TagTable). 요청이 붙인 태그 중 등록한 것에만 예산·한도가 걸린다. 한도는 limit_policy 의 tag 종류(대상 id 는 태그 이름)다
CREATE TABLE tag (
    name        text        PRIMARY KEY,
    description text,
    models      text[]      NOT NULL DEFAULT '{}',
    created_by  text,
    updated_by  text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE limit_policy DROP CONSTRAINT limit_policy_scope_check;
ALTER TABLE limit_policy ADD CONSTRAINT limit_policy_scope_check CHECK (scope IN ('key', 'team', 'user', 'team_member', 'end_user', 'tag'));
ALTER TABLE limit_model_policy DROP CONSTRAINT limit_model_policy_scope_check;
ALTER TABLE limit_model_policy ADD CONSTRAINT limit_model_policy_scope_check CHECK (scope IN ('key', 'team', 'user', 'team_member', 'end_user', 'tag'));
