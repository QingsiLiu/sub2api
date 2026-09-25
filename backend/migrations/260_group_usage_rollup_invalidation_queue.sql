-- geili: usage writers must never wait for the dashboard rollup singleton.
-- Events are immutable, transactionally committed with the source change and
-- deliberately have no bucket uniqueness constraint or source-table FK.
CREATE TABLE IF NOT EXISTS usage_group_rollup_invalidations (
    id BIGSERIAL PRIMARY KEY,
    affected_at TIMESTAMPTZ NOT NULL,
    group_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_usage_group_rollup_invalidations_affected
    ON usage_group_rollup_invalidations (affected_at, id);
COMMENT ON TABLE usage_group_rollup_invalidations IS
    'Durable group rollup invalidations; consume exact visible IDs in the same snapshot as recomputation, never a maximum-ID watermark.';
COMMENT ON COLUMN usage_group_rollup_invalidations.affected_at IS
    'Original source timestamp, interpreted in the current rollup timezone; not the session timezone at insertion.';

CREATE OR REPLACE FUNCTION enqueue_group_usage_rollup_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP <> 'INSERT' AND OLD.group_id IS NOT NULL THEN
        INSERT INTO usage_group_rollup_invalidations (affected_at, group_id)
        VALUES (OLD.created_at, OLD.group_id);
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.group_id IS NOT NULL THEN
        INSERT INTO usage_group_rollup_invalidations (affected_at, group_id)
        VALUES (NEW.created_at, NEW.group_id);
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS usage_logs_group_rollup_invalidate_insert ON usage_logs;
CREATE TRIGGER usage_logs_group_rollup_invalidate_insert
AFTER INSERT ON usage_logs
FOR EACH ROW
WHEN (NEW.group_id IS NOT NULL)
EXECUTE FUNCTION enqueue_group_usage_rollup_invalidation();

DROP TRIGGER IF EXISTS usage_logs_group_rollup_invalidate_delete ON usage_logs;
CREATE TRIGGER usage_logs_group_rollup_invalidate_delete
AFTER DELETE ON usage_logs
FOR EACH ROW
WHEN (OLD.group_id IS NOT NULL)
EXECUTE FUNCTION enqueue_group_usage_rollup_invalidation();

DROP TRIGGER IF EXISTS usage_logs_group_rollup_invalidate_update ON usage_logs;
CREATE TRIGGER usage_logs_group_rollup_invalidate_update
AFTER UPDATE OF created_at, group_id, actual_cost ON usage_logs
FOR EACH ROW
WHEN (
    (OLD.created_at IS DISTINCT FROM NEW.created_at
     OR OLD.group_id IS DISTINCT FROM NEW.group_id
     OR OLD.actual_cost IS DISTINCT FROM NEW.actual_cost)
    AND (OLD.group_id IS NOT NULL OR NEW.group_id IS NOT NULL)
)
EXECUTE FUNCTION enqueue_group_usage_rollup_invalidation();

-- Remove the obsolete locking functions so no accidental direct caller can
-- reinstate the former usage-insert -> singleton -> subscription lock chain.
DROP FUNCTION IF EXISTS invalidate_group_usage_rollup_state();
DROP FUNCTION IF EXISTS invalidate_group_usage_rollup_state_after_insert();
