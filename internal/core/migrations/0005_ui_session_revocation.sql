-- 대시보드 세션 폐기(auth 소유). 세션 키는 DB 에 두지 않는 자기 완결 로그인 키라서(설계 9장) 지울 행이 없다.
-- 이 시각보다 먼저 발급된 그 사용자의 대시보드 세션 키는 거부한다(로그아웃·비밀번호 변경). 데스크탑 로그인 키는 건드리지 않는다
ALTER TABLE app_user ADD COLUMN ui_sessions_revoked_at timestamptz;
