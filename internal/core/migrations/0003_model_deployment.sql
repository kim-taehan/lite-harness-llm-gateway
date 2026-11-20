-- 모델 배포(inference 소유). model_name 은 클라이언트가 부르는 이름(그룹)이고, 같은 이름의 배포가 여럿이면 weight 로 나눈다(설계 4장 5단계)

CREATE TABLE model_deployment (
    id                    text PRIMARY KEY,
    model_name            text        NOT NULL,
    -- litellm 의 litellm_params.model 앞부분(ollama, openai 등). 1차는 OpenAI 호환 /v1 로 보낸다(설계 5장)
    provider              text        NOT NULL,
    base_url              text        NOT NULL,
    upstream_model        text        NOT NULL,
    weight                integer     NOT NULL DEFAULT 1 CHECK (weight > 0),
    max_input_tokens      integer,
    max_output_tokens     integer,
    supports_reasoning    boolean     NOT NULL DEFAULT false,
    input_cost_per_token  numeric,
    output_cost_per_token numeric,
    active                boolean     NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX model_deployment_name_idx ON model_deployment (model_name) WHERE active;
