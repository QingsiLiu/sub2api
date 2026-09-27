-- Geili: usage record pages sort the materialized window by created_at. Without
-- these, an admin/user page over 7 days top-N sorts every fact row. Additive
-- and built concurrently. Only the financial rollup consumer writes the table.
CREATE INDEX CONCURRENTLY IF NOT EXISTS usage_financial_facts_created_geili
 ON usage_financial_facts(created_at DESC NULLS LAST, id DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS usage_financial_facts_user_created_geili
 ON usage_financial_facts(user_id, created_at DESC NULLS LAST, id DESC);
