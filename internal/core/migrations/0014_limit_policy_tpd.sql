-- 하루(UTC) 토큰 한도(litellm tpd_limit). 비어 있으면 한도 없음
ALTER TABLE limit_policy ADD COLUMN tpd_limit bigint;
