-- 업스트림 키(inference 소유). AES-256-GCM 으로 봉한 값(nonce || 암호문)만 넣고 평문은 저장하지 않는다. 키를 쓰지 않는 업스트림(Ollama 등)은 NULL
ALTER TABLE model_deployment ADD COLUMN api_key_encrypted bytea;
