package subscription

// The probe only scans this subscription's own lots through
// (entitlement_id,id), so other subscriptions' traffic never pushes an idle
// heavy subscription onto the unbounded fallback.
const dailyUsageReadQuery = `WITH pending_probe AS MATERIALIZED (
 SELECT request_key,cost_usd FROM subscription_usage_allocations
 WHERE entitlement_id IN (SELECT e.id FROM user_subscription_entitlements e WHERE e.user_subscription_id=$1)
 AND id>(SELECT allocation_watermark FROM subscription_ledger_state WHERE subscription_id=$1)
 LIMIT 1025
)
SELECT COALESCE((SELECT used_usd FROM subscription_daily_usage WHERE subscription_id=$1 AND term_id=$2 AND usage_date=$3),0)
+COALESCE(CASE WHEN (SELECT COUNT(*) FROM pending_probe)<1025 THEN (
 SELECT SUM(a.cost_usd) FROM pending_probe a
 WHERE (SELECT r.subscription_id=$1 AND r.admitted_at>=$4 AND r.admitted_at<$5
 AND COALESCE((SELECT b.term_id FROM subscription_request_contracts b WHERE b.request_key=r.request_key),
 (SELECT t.term_id FROM subscription_contract_terms t WHERE t.subscription_id=r.subscription_id AND t.starts_at<=r.admitted_at AND t.expires_at>r.admitted_at ORDER BY t.starts_at DESC LIMIT 1),
 (SELECT t.term_id FROM subscription_contract_terms t WHERE t.subscription_id=r.subscription_id ORDER BY t.starts_at LIMIT 1))=$2
 FROM subscription_requests r WHERE r.request_key=a.request_key)
) ELSE (SELECT SUM(a.cost_usd) FROM subscription_usage_allocations a JOIN subscription_requests r ON r.request_key=a.request_key LEFT JOIN subscription_request_contracts b ON b.request_key=r.request_key JOIN subscription_ledger_state st ON st.subscription_id=r.subscription_id WHERE r.subscription_id=$1 AND a.id>st.allocation_watermark AND r.admitted_at>=$4 AND r.admitted_at<$5 AND COALESCE(b.term_id,(SELECT t.term_id FROM subscription_contract_terms t WHERE t.subscription_id=r.subscription_id AND t.starts_at<=r.admitted_at AND t.expires_at>r.admitted_at ORDER BY t.starts_at DESC LIMIT 1),(SELECT t.term_id FROM subscription_contract_terms t WHERE t.subscription_id=r.subscription_id ORDER BY t.starts_at LIMIT 1))=$2) END,0)`
