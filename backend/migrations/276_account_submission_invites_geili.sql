-- Only invitation hashes/configuration are stored here; credentials stay in accounts.
SET LOCAL lock_timeout = '5s';
CREATE TABLE account_submission_invites_geili (
    id BIGSERIAL PRIMARY KEY,
    token_hash CHAR(64) NOT NULL UNIQUE,
    created_by BIGINT NOT NULL,
    config JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    submitted_at TIMESTAMPTZ,
    account_id BIGINT UNIQUE REFERENCES accounts(id) ON DELETE RESTRICT,
    CHECK ((submitted_at IS NULL) = (account_id IS NULL))
);
CREATE INDEX account_submission_invites_created_geili
    ON account_submission_invites_geili (created_at DESC, id DESC);
