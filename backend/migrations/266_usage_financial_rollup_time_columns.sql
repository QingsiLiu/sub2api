-- Geili: scalar-column statistics keep the empty/current financial tail cheap.
-- A COALESCE expression partial index remains usable for range lookup, but PG18
-- estimates one third of settled receipts even beyond its maximum timestamp.
-- The resulting false cost triggers hundreds of milliseconds of JIT compilation.
-- These disjoint indexes match the equivalent completed/fallback predicates.
CREATE INDEX usage_settlement_rollup_completed_geili
 ON usage_settlement_receipts(completed_at)
 WHERE state='settled' AND completed_at IS NOT NULL;
CREATE INDEX usage_settlement_rollup_settled_fallback_geili
 ON usage_settlement_receipts(settled_at)
 WHERE state='settled' AND completed_at IS NULL;
