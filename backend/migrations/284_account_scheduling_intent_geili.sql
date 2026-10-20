-- Debt is an administrative label, independent from scheduling. Keep the
-- trigger as a no-op for rollback compatibility with the previous application.
CREATE OR REPLACE FUNCTION guard_account_balance_runtime_geili() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RETURN NEW;
END;
$$;

-- Preserve every existing stop. Removing the former debt label must not
-- silently enable accounts that were intentionally stopped by that action.
INSERT INTO account_runtime_geili(account_id,version,stop_reason)
SELECT id,1,'manual' FROM accounts
WHERE deleted_at IS NULL AND (status='inactive' OR (status='active' AND NOT schedulable))
ON CONFLICT(account_id) DO UPDATE
SET stop_reason='manual',version=account_runtime_geili.version+1,updated_at=NOW();

UPDATE account_runtime_geili r SET stop_reason='manual',version=r.version+1,updated_at=NOW()
FROM accounts a WHERE a.id=r.account_id AND a.deleted_at IS NULL
AND r.stop_reason='balance' AND (a.status<>'active' OR NOT a.schedulable);
