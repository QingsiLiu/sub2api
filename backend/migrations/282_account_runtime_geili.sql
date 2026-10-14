-- Durable channel recovery state is independent of administrator-editable extra.
CREATE TABLE account_runtime_geili (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    balance_blocked BOOLEAN NOT NULL DEFAULT FALSE,
    balance_reason TEXT NOT NULL DEFAULT '',
    balance_blocked_at TIMESTAMPTZ,
    stop_reason TEXT NOT NULL DEFAULT '',
    protection_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    model_states JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(model_states) = 'object'),
    last_probe JSONB,
    next_check_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE account_probe_logs_geili (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model TEXT NOT NULL DEFAULT '',
    runtime_version BIGINT NOT NULL,
    fingerprint TEXT NOT NULL,
    source TEXT NOT NULL,
    success BOOLEAN NOT NULL,
    complete BOOLEAN NOT NULL,
    balance_verified BOOLEAN NOT NULL DEFAULT FALSE,
    checked_at TIMESTAMPTZ NOT NULL,
    result JSONB NOT NULL CHECK (jsonb_typeof(result) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX account_probe_logs_account_geili ON account_probe_logs_geili(account_id, created_at DESC, id DESC);

CREATE TABLE account_probe_leases_geili (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model TEXT NOT NULL,
    token TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(account_id)
);
CREATE INDEX account_probe_leases_expiry_geili ON account_probe_leases_geili(expires_at);

-- Preserve already-confirmed legacy billing stops before new manual recovery
-- controls become available. Recent request errors are reclaimed separately by
-- a bounded startup job, never by scanning an operational log in a migration.
INSERT INTO account_runtime_geili(account_id,version,balance_blocked,balance_reason,balance_blocked_at,stop_reason)
SELECT id,1,TRUE,'supplier_insufficient_balance',NOW(),'balance'
FROM accounts WHERE deleted_at IS NULL AND status='error'
AND (error_message LIKE 'Credit balance exhausted (400):%' OR error_message LIKE 'Payment required (402):%');

-- Group edits share the account row lock and invalidate an in-flight grant,
-- including newly inserted memberships that an existing-row lock cannot see.
CREATE FUNCTION stamp_account_group_runtime_geili() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        UPDATE accounts SET updated_at=clock_timestamp() WHERE id=OLD.account_id;
    END IF;
    IF TG_OP <> 'DELETE' AND (TG_OP='INSERT' OR NEW.account_id IS DISTINCT FROM OLD.account_id) THEN
        UPDATE accounts SET updated_at=clock_timestamp() WHERE id=NEW.account_id;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER account_groups_runtime_stamp_geili AFTER INSERT OR UPDATE OR DELETE ON account_groups
FOR EACH ROW EXECUTE FUNCTION stamp_account_group_runtime_geili();

-- Old clear-error, toggle, bulk edit and rollback application paths cannot turn
-- a balance-blocked account into an eligible channel. Writers lock accounts
-- before runtime state so this guard shares the same serialization point.
CREATE FUNCTION guard_account_balance_runtime_geili() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM account_runtime_geili WHERE account_id = NEW.id AND balance_blocked) THEN
        NEW.schedulable := FALSE;
        IF NEW.status = 'active' THEN NEW.status := 'error'; END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER accounts_balance_runtime_guard_geili
BEFORE UPDATE OF status, schedulable ON accounts
FOR EACH ROW EXECUTE FUNCTION guard_account_balance_runtime_geili();
