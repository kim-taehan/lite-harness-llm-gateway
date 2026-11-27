-- 대상(키·팀·사용자·팀원) × 모델 하나에 거는 예산·RPM·TPM(litellm model_max_budget). limits 소유라 target_id 에 외래 키를 걸지 않는다
CREATE TABLE limit_model_policy (
    scope           text        NOT NULL CHECK (scope IN ('key', 'team', 'user', 'team_member')),
    target_id       text        NOT NULL,
    model           text        NOT NULL,
    max_budget      numeric,
    spend           numeric     NOT NULL DEFAULT 0,
    budget_duration text,
    budget_reset_at timestamptz,
    tpm_limit       bigint,
    rpm_limit       bigint,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, target_id, model)
);
