-- 감사 기록 변조 방지: 기록마다 순번(seq), 앞 기록의 해시(prev_hash), 자기 해시(row_hash)를 둔다. 해시는 마스터 키에서 파생한 키로 만든
-- HMAC 이라 DB 만 고칠 수 있는 사람은 고친 뒤 해시를 다시 맞출 수 없다. 이 마이그레이션 전의 기록은 해시가 없다(검증에서 따로 센다)
ALTER TABLE audit_log ADD COLUMN seq bigserial;
ALTER TABLE audit_log ADD COLUMN prev_hash text;
ALTER TABLE audit_log ADD COLUMN row_hash text;
CREATE UNIQUE INDEX audit_log_seq_idx ON audit_log (seq);
