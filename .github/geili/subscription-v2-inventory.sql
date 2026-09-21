-- Run before/after rollout with psql -X -v ON_ERROR_STOP=1. Read-only;
-- identifiers and quota facts only, never credentials or request contents.
BEGIN READ ONLY;
SET LOCAL statement_timeout='30s';
SET LOCAL TIME ZONE 'Asia/Shanghai';
WITH active_subscriptions AS (
 SELECT s.* FROM user_subscriptions s
 WHERE s.deleted_at IS NULL AND s.status='active' AND s.starts_at<=now() AND s.expires_at>now()
), owners AS (
 SELECT user_id,count(*) AS active_pools FROM user_subscriptions WHERE deleted_at IS NULL AND status IN ('active','suspended') AND expires_at>now() GROUP BY user_id
), active_lots AS (
 SELECT e.* FROM user_subscription_entitlements e
 WHERE e.status='active' AND e.starts_at<=now() AND e.expires_at>now()
), lot_summary AS (
 SELECT user_subscription_id,count(*) AS active_units,min(expires_at) AS first_expiry,max(expires_at) AS last_expiry,
 sum(daily_limit_usd) AS finite_daily_quota,bool_or(coalesce(daily_limit_usd,0)=0) AS unlimited_daily,
 sum(CASE WHEN (daily_window_start AT TIME ZONE 'Asia/Shanghai')::date=(now() AT TIME ZONE 'Asia/Shanghai')::date THEN daily_usage_usd ELSE 0 END) AS active_lot_daily_used,
 min(daily_limit_usd) AS min_tier,max(daily_limit_usd) AS max_tier,min(plan_id) AS lot_plan_id
 FROM active_lots GROUP BY user_subscription_id
), keys AS (
 SELECT subscription_id,count(*) AS bound_keys FROM api_keys WHERE deleted_at IS NULL GROUP BY subscription_id
)
SELECT s.user_id,s.id AS subscription_id,s.plan_id,p.name,p.validity_days,l.active_units,l.min_tier,l.max_tier,
 CASE WHEN l.unlimited_daily THEN NULL ELSE l.finite_daily_quota END AS daily_quota,l.active_lot_daily_used,
 l.first_expiry,l.last_expiry,coalesce(k.bound_keys,0) AS bound_keys,
 CASE WHEN o.active_pools=1 AND l.active_units=1 AND NOT p.is_legacy_compat AND
 ((CASE WHEN p.validity_unit IN ('week','weeks') THEN p.validity_days*7 WHEN p.validity_unit IN ('month','months') THEN p.validity_days*30 ELSE p.validity_days END=7 AND l.min_tier IN (90,180)) OR (CASE WHEN p.validity_unit IN ('week','weeks') THEN p.validity_days*7 WHEN p.validity_unit IN ('month','months') THEN p.validity_days*30 ELSE p.validity_days END=30 AND l.min_tier IN (45,90,180)))
 THEN 'v2_candidate' ELSE 'legacy_daily_candidate' END AS expected_mode
FROM active_subscriptions s JOIN owners o ON o.user_id=s.user_id LEFT JOIN lot_summary l ON l.user_subscription_id=s.id
LEFT JOIN subscription_plans p ON p.id=coalesce(l.lot_plan_id,s.plan_id) LEFT JOIN keys k ON k.subscription_id=s.id ORDER BY s.user_id,s.id;
SELECT status,count(*),min(admitted_at) AS first_admitted_at,max(admitted_at) AS last_admitted_at FROM subscription_requests WHERE status='admitted' GROUP BY status;
SELECT status,count(*) FROM payment_orders WHERE order_type='subscription' GROUP BY status ORDER BY status;
COMMIT;
