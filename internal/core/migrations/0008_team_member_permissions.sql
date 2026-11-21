-- 팀 일반 멤버가 부를 수 있는 키 라우트(auth 소유, litellm team_member_permissions). null 이면 litellm 기본(/key/info, /key/health)이다
ALTER TABLE team ADD COLUMN member_permissions text[];
