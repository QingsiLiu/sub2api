-- Supplier ownership is kept outside accounts so the official account schema stays unchanged.
-- One account has at most one supplier owner; ordinary admin-created accounts have no row.
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS account_supplier_owners_geili (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS account_supplier_owners_owner_geili
    ON account_supplier_owners_geili (owner_user_id, account_id);
