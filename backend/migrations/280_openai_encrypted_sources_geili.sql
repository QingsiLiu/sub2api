-- Routing provenance only: no ciphertext, prompts, credentials or billing data.
CREATE TABLE IF NOT EXISTS openai_encrypted_sources_geili (
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 cipher_digest CHAR(64) NOT NULL CHECK (cipher_digest ~ '^[0-9a-f]{64}$'),
 group_id BIGINT NOT NULL,
 account_id BIGINT NOT NULL,
 model TEXT NOT NULL,
 backend_fingerprint CHAR(64) NOT NULL,
 conflict BOOLEAN NOT NULL DEFAULT FALSE,
 generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,cipher_digest)
);
CREATE INDEX IF NOT EXISTS openai_encrypted_sources_expiry_geili ON openai_encrypted_sources_geili(last_accessed_at);
