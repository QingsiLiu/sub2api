-- Cover the complete settled-identity predicate used by financial reporting.
-- The existing nonempty-only index is not usable unless every query repeats
-- billing_request_id <> ''. Preserve legacy empty identities instead of dropping
-- them to force index selection. No usage_logs rebuild or financial data update.
CREATE INDEX CONCURRENTLY IF NOT EXISTS subscription_requests_settled_identity_lookup_geili
 ON subscription_requests(billing_request_id,api_key_id,subscription_id,id)
 WHERE status='settled';
