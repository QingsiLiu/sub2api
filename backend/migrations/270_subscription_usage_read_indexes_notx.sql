-- Geili: keep subscription quota reads index-only when the ledger has a tail.
-- The read path joins recent requests to allocations by request_key and then
-- resolves the immutable contract term. These indexes are additive and safe
-- to build concurrently on production-sized tables.
CREATE INDEX CONCURRENTLY IF NOT EXISTS subscription_requests_subscription_admitted_request_key_geili
 ON subscription_requests(subscription_id, admitted_at, request_key);

CREATE INDEX CONCURRENTLY IF NOT EXISTS subscription_request_contracts_request_key_term_geili
 ON subscription_request_contracts(request_key, term_id);
