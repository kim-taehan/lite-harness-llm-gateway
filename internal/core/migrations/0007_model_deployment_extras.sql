-- 대시보드 모델 편집(inference 소유). litellm 은 litellm_params·model_info 를 통째로 JSON 으로 저장하고 화면이 임의 키(guardrails,
-- access_groups, team_id, health_check_model 등)를 넣고 다시 읽는다. 우리는 쓰는 값은 열로 두고, 나머지 키는 여기 그대로 둔다
ALTER TABLE model_deployment
    ADD COLUMN extra_params jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN extra_info   jsonb NOT NULL DEFAULT '{}';
