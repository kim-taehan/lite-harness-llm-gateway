-- 프로젝트(litellm LiteLLM_ProjectTable): 팀 안에서 키를 용도별로 묶고 그 묶음에 예산·허용 모델·차단을 거는 단위. 팀은 필수다.
-- 예산·한도는 limit_policy 의 project 종류(대상 id 는 project_id)다
CREATE TABLE project (
    project_id    text        PRIMARY KEY,
    project_alias text,
    description   text,
    -- 팀을 지우면 프로젝트도 함께 지운다(팀의 키도 같은 문장에서 지워지므로 키가 가리키는 프로젝트가 남지 않는다)
    team_id       text        NOT NULL REFERENCES team (team_id) ON DELETE CASCADE,
    models        text[]      NOT NULL DEFAULT '{}',
    metadata      jsonb       NOT NULL DEFAULT '{}',
    blocked       boolean     NOT NULL DEFAULT false,
    created_by    text,
    updated_by    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX project_team_idx ON project (team_id);

-- 키는 프로젝트를 고를 수 있다(선택). 키가 남아 있는 프로젝트는 지울 수 없다(앱이 먼저 막고, 이 외래 키가 한 번 더 막는다)
ALTER TABLE api_key ADD COLUMN project_id text REFERENCES project (project_id);
CREATE INDEX api_key_project_idx ON api_key (project_id);
ALTER TABLE deleted_api_key ADD COLUMN project_id text;

ALTER TABLE limit_policy DROP CONSTRAINT limit_policy_scope_check;
ALTER TABLE limit_policy ADD CONSTRAINT limit_policy_scope_check CHECK (scope IN ('key', 'team', 'user', 'team_member', 'end_user', 'tag', 'provider', 'project'));
ALTER TABLE limit_model_policy DROP CONSTRAINT limit_model_policy_scope_check;
ALTER TABLE limit_model_policy ADD CONSTRAINT limit_model_policy_scope_check CHECK (scope IN ('key', 'team', 'user', 'team_member', 'end_user', 'tag', 'project'));
