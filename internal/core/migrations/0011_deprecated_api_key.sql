-- 재발급한 키의 유예 기간(auth 소유, litellm LiteLLM_DeprecatedVerificationToken). 옛 해시는 revoke_at 까지
-- 새 해시(active_token)의 키로 인증된다. 키를 또 재발급하면 active_token 을 새 해시로 옮긴다.
-- token·active_token 은 재발급으로 바뀌는 값이라 외래 키를 걸지 않는다

CREATE TABLE deprecated_api_key (
    token        text PRIMARY KEY,
    active_token text        NOT NULL,
    revoke_at    timestamptz NOT NULL
);
CREATE INDEX deprecated_api_key_active_idx ON deprecated_api_key (active_token);
