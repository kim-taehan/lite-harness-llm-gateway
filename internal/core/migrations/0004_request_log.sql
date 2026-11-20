-- 사용량(limits 소유). 기록 시점에 key·user·team id 를 함께 남겨 집계를 이 테이블만으로 한다(설계 3장).
-- 다른 모듈의 id 라 외래 키를 걸지 않는다(설계 8.1). 요청·응답 본문은 남기지 않는다(설계 10장)

CREATE TABLE request_log (
    request_id        text PRIMARY KEY,
    created_at        timestamptz NOT NULL,
    key_token         text,
    user_id           text,
    team_id           text,
    model             text        NOT NULL,
    deployment_id     text,
    prompt_tokens     integer     NOT NULL DEFAULT 0,
    completion_tokens integer     NOT NULL DEFAULT 0,
    usage_source      text        NOT NULL CHECK (usage_source IN ('provider', 'estimate')),
    cost              numeric     NOT NULL DEFAULT 0,
    status            text        NOT NULL CHECK (status IN ('success', 'error', 'cancelled')),
    http_status       integer     NOT NULL,
    error_code        text,
    latency_ms        integer     NOT NULL,
    ttft_ms           integer
);
CREATE INDEX request_log_created_idx ON request_log (created_at);
CREATE INDEX request_log_key_idx ON request_log (key_token, created_at);
CREATE INDEX request_log_team_idx ON request_log (team_id, created_at);
CREATE INDEX request_log_user_idx ON request_log (user_id, created_at);
