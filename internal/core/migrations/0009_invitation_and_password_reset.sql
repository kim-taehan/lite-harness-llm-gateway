-- 대시보드 초대·비밀번호(auth 소유, L 6장).
-- 관리자가 비밀번호를 정하면 사용자가 다음 로그인 때 바꾸게 한다(litellm password_reset_required)
ALTER TABLE app_user ADD COLUMN password_reset_required boolean NOT NULL DEFAULT false;

-- 초대 링크. 링크로 들어온 사용자가 자기 비밀번호를 정한다. 7일 뒤 만료되고 한 번만 쓴다(litellm LiteLLM_InvitationLink)
CREATE TABLE invitation (
    id          text PRIMARY KEY,
    user_id     text        NOT NULL REFERENCES app_user (user_id) ON DELETE CASCADE,
    is_accepted boolean     NOT NULL DEFAULT false,
    accepted_at timestamptz,
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    created_by  text        NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    updated_by  text        NOT NULL
);
