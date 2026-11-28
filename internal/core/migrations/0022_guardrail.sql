-- 가드레일(litellm GuardrailsTable). 요청·응답의 내용을 검사해 막거나 가리는 규칙 묶음이다. litellm_params 는 대시보드가 보낸 설정을 그대로 저장한다
CREATE TABLE guardrail (
    guardrail_id   text        PRIMARY KEY,
    guardrail_name text        NOT NULL UNIQUE,
    litellm_params jsonb       NOT NULL,
    guardrail_info jsonb       NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
