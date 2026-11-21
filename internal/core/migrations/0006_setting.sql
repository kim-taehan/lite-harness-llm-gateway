-- 대시보드에서 바꾸는 설정(core 소유). litellm 은 config 테이블의 litellm_settings 아래에 둔다.
-- 이름 하나에 JSON 객체 하나다(ui_settings, ui_theme_config, default_team_params, default_internal_user_params, model_hub). 바꾼 값만 저장하고 기본값은 코드에 있다
CREATE TABLE setting (
    name       text PRIMARY KEY,
    value      jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
