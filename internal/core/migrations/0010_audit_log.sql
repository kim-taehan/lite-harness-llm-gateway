-- 관리 변경 기록(admin 소유). litellm LiteLLM_AuditLog 와 같은 모양이라 대시보드 감사 화면이 그대로 읽는다.
-- 비밀 값(키 원문·비밀번호)은 넣지 않는다. 다른 모듈의 id 를 담으므로 외래 키는 걸지 않는다(설계 8.1)

CREATE TABLE audit_log (
    id                 text PRIMARY KEY,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    changed_by         text        NOT NULL,
    changed_by_api_key text        NOT NULL DEFAULT '',
    action             text        NOT NULL,
    table_name         text        NOT NULL,
    object_id          text        NOT NULL,
    before_value       jsonb,
    updated_values     jsonb
);
CREATE INDEX audit_log_updated_idx ON audit_log (updated_at DESC);
CREATE INDEX audit_log_object_idx ON audit_log (object_id);
