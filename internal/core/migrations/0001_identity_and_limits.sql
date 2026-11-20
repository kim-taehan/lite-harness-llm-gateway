-- 신원(auth 소유): team, app_user, team_member
-- id 는 litellm 처럼 문자열이다. 호출자가 정할 수 있고(team_id, user_id), 없으면 서버가 uuid 문자열을 만든다

CREATE TABLE team (
    team_id     text PRIMARY KEY,
    team_alias  text,
    models      text[]      NOT NULL DEFAULT '{}',
    metadata    jsonb,
    blocked     boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
-- litellm 도 별칭은 unique 가 아니다(같은 별칭 팀을 여러 개 만들 수 있다)
CREATE INDEX team_alias_idx ON team (lower(team_alias));

CREATE TABLE app_user (
    user_id       text PRIMARY KEY,
    user_email    text,
    user_alias    text,
    -- null 을 허용한다. litellm /user/new 는 역할을 안 보내면 null 로 저장하고 인증 때 internal_user 로 본다
    user_role     text CHECK (user_role IN ('proxy_admin', 'proxy_admin_viewer', 'internal_user', 'internal_user_viewer')),
    models        text[]      NOT NULL DEFAULT '{}',
    metadata      jsonb,
    sso_user_id   text UNIQUE,
    password_hash text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- litellm 은 이메일 중복을 대소문자 무시로 막는다
CREATE UNIQUE INDEX app_user_email_idx ON app_user (lower(user_email));

-- 팀원은 이 테이블 한 곳에만 둔다. litellm 의 members_with_roles 와 사용자 teams 배열은 여기서 만든다(설계 9장)
CREATE TABLE team_member (
    team_id    text        NOT NULL REFERENCES team (team_id) ON DELETE CASCADE,
    user_id    text        NOT NULL REFERENCES app_user (user_id) ON DELETE CASCADE,
    role       text        NOT NULL CHECK (role IN ('admin', 'user')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_member_user_idx ON team_member (user_id);

-- 한도(limits 소유). target_id 는 다른 모듈의 id 라 외래 키를 걸지 않는다(설계 8.1)
CREATE TABLE limit_policy (
    scope                 text        NOT NULL CHECK (scope IN ('key', 'team', 'user')),
    target_id             text        NOT NULL,
    max_budget            numeric,
    soft_budget           numeric,
    spend                 numeric     NOT NULL DEFAULT 0,
    tpm_limit             bigint,
    rpm_limit             bigint,
    max_parallel_requests integer,
    budget_duration       text,
    budget_reset_at       timestamptz,
    updated_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, target_id)
);
