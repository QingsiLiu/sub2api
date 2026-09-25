-- Small identity/date indexes only. Do not rebuild/index the multi-GB usage_logs
-- table: financial reporting uses its existing request/key and created_at indexes.
CREATE INDEX CONCURRENTLY IF NOT EXISTS subscription_requests_billing_identity_geili
 ON subscription_requests(billing_request_id,api_key_id,subscription_id,id)
 WHERE status='settled' AND billing_request_id<>'';
CREATE INDEX CONCURRENTLY IF NOT EXISTS subscription_request_contracts_usage_date_geili
 ON subscription_request_contracts(usage_date,request_key);
CREATE INDEX CONCURRENTLY IF NOT EXISTS subscription_requests_settled_admitted_geili
 ON subscription_requests(admitted_at,id) WHERE status='settled';
CREATE INDEX CONCURRENTLY IF NOT EXISTS usage_settlement_accounting_date_geili
 ON usage_settlement_receipts(accounting_date,id) WHERE state='settled';
CREATE INDEX CONCURRENTLY IF NOT EXISTS usage_settlement_delivered_log_geili
 ON usage_settlement_receipts(usage_log_id) WHERE usage_log_id IS NOT NULL;
