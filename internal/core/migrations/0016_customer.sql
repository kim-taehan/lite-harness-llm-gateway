-- 고객(end user): 요청의 user 값으로 식별하는 최종 사용자. 한도·예산은 limit_policy 의 end_user 종류로 둔다
CREATE TABLE customer (
    user_id    text        PRIMARY KEY,
    alias      text,
    blocked    boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE limit_policy DROP CONSTRAINT limit_policy_scope_check;
ALTER TABLE limit_policy ADD CONSTRAINT limit_policy_scope_check CHECK (scope IN ('key', 'team', 'user', 'team_member', 'end_user'));
ALTER TABLE limit_model_policy DROP CONSTRAINT limit_model_policy_scope_check;
ALTER TABLE limit_model_policy ADD CONSTRAINT limit_model_policy_scope_check CHECK (scope IN ('key', 'team', 'user', 'team_member', 'end_user'));
