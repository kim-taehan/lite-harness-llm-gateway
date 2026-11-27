-- 조직(litellm LiteLLM_OrganizationTable): 팀들을 묶고 팀 위에서 예산·허용 모델의 상한을 정한다. 조직은 전체 관리자만 관리한다(조직 관리자 역할 없음).
-- 예산·한도는 limit_policy 의 organization 종류(대상 id 는 organization_id)다
CREATE TABLE organization (
    organization_id    text        PRIMARY KEY,
    organization_alias text        NOT NULL,
    models             text[]      NOT NULL DEFAULT '{}',
    metadata           jsonb       NOT NULL DEFAULT '{}',
    created_by         text,
    updated_by         text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- 구성원 목록. 역할 문자열(user_role)은 화면에 보이는 정보일 뿐이고 권한에는 쓰지 않는다
CREATE TABLE organization_member (
    organization_id text        NOT NULL REFERENCES organization (organization_id) ON DELETE CASCADE,
    user_id         text        NOT NULL REFERENCES app_user (user_id) ON DELETE CASCADE,
    user_role       text        NOT NULL DEFAULT 'internal_user',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);
CREATE INDEX organization_member_user_idx ON organization_member (user_id);

-- 팀은 조직에 속할 수 있다(선택). 팀이 남은 조직은 지울 수 없다(앱이 먼저 막고 이 외래 키가 한 번 더 막는다)
ALTER TABLE team ADD COLUMN organization_id text REFERENCES organization (organization_id);
CREATE INDEX team_organization_idx ON team (organization_id);

ALTER TABLE limit_policy DROP CONSTRAINT limit_policy_scope_check;
ALTER TABLE limit_policy ADD CONSTRAINT limit_policy_scope_check
    CHECK (scope IN ('key', 'team', 'user', 'team_member', 'end_user', 'tag', 'provider', 'project', 'organization'));
ALTER TABLE limit_model_policy DROP CONSTRAINT limit_model_policy_scope_check;
ALTER TABLE limit_model_policy ADD CONSTRAINT limit_model_policy_scope_check
    CHECK (scope IN ('key', 'team', 'user', 'team_member', 'end_user', 'tag', 'project', 'organization'));
