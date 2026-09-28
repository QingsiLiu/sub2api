-- Geili 0.2.8-geili.20: dashboards and usage records are back on the official
-- usage_logs + usage_dashboard_hourly/daily path. The financial rollup consumer
-- is gone, so stop the per-row triggers that fed it and free the derived data.
-- Receipts, settlement and the usage_financial_* views are untouched; table
-- definitions stay so older images fail soft instead of on a missing relation.

DROP TRIGGER IF EXISTS usage_logs_financial_rollup_event ON usage_logs;
DROP TRIGGER IF EXISTS usage_receipts_financial_rollup_event ON usage_settlement_receipts;
DROP TRIGGER IF EXISTS subscription_requests_financial_rollup_event ON subscription_requests;
DROP TRIGGER IF EXISTS subscription_contracts_financial_rollup_event ON subscription_request_contracts;

DROP FUNCTION IF EXISTS enqueue_usage_financial_rollup_event();
DROP FUNCTION IF EXISTS enqueue_usage_financial_subscription_event();

-- Only the large derived tables; the tiny singleton state rows stay as-is.
TRUNCATE usage_financial_rollup_events, usage_financial_facts;
