-- 가상 키(auth 소유). 원문은 저장하지 않고 sha256 hex 만 둔다(litellm 의 token 컬럼과 같은 값)

CREATE TABLE api_key (
    token          text PRIMARY KEY,
    key_name       text,
    key_alias      text,
    -- litellm 처럼 없는 사용자에게도 키를 만들 수 있어서 외래 키를 걸지 않는다
    user_id        text,
    -- 팀을 지우면 팀 키도 지운다(지우기 전에 deleted_api_key 로 옮긴다)
    team_id        text REFERENCES team (team_id) ON DELETE CASCADE,
    models         text[]      NOT NULL DEFAULT '{}',
    metadata       jsonb       NOT NULL DEFAULT '{}',
    key_type       text        NOT NULL DEFAULT 'default',
    allowed_routes text[]      NOT NULL DEFAULT '{}',
    -- litellm 은 요청에 없으면 null 로 둔다(false 가 아니다)
    blocked        boolean,
    expires        timestamptz,
    created_by     text,
    updated_by     text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
-- litellm 은 alias 를 앱에서 전역 고유로 검사한다. 동시에 두 요청이 와도 지켜지게 DB 에도 건다
CREATE UNIQUE INDEX api_key_alias_idx ON api_key (key_alias) WHERE key_alias IS NOT NULL;
CREATE INDEX api_key_user_idx ON api_key (user_id);
CREATE INDEX api_key_team_idx ON api_key (team_id);

-- 지운 키의 사본. /key/info 와 /key/list?status=deleted 가 읽는다(litellm 의 LiteLLM_DeletedVerificationToken)
CREATE TABLE deleted_api_key (
    id             text PRIMARY KEY,
    token          text        NOT NULL,
    key_name       text,
    key_alias      text,
    user_id        text,
    team_id        text,
    models         text[]      NOT NULL DEFAULT '{}',
    metadata       jsonb       NOT NULL DEFAULT '{}',
    key_type       text        NOT NULL DEFAULT 'default',
    allowed_routes text[]      NOT NULL DEFAULT '{}',
    blocked        boolean,
    expires        timestamptz,
    created_by     text,
    updated_by     text,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    deleted_at     timestamptz NOT NULL DEFAULT now(),
    deleted_by     text
);
CREATE INDEX deleted_api_key_token_idx ON deleted_api_key (token);
