-- Additive repair: never replay or alter migration 250's aggregate backfill.
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS subscription_snapshot JSONB;
ALTER TABLE user_subscription_entitlements ADD COLUMN IF NOT EXISTS source_type VARCHAR NOT NULL DEFAULT 'legacy';
ALTER TABLE user_subscription_entitlements ADD COLUMN IF NOT EXISTS source_reference VARCHAR NOT NULL DEFAULT '';
UPDATE user_subscription_entitlements SET source_type='payment',source_reference=source_order_id::text WHERE source_order_id IS NOT NULL AND source_type='legacy';
ALTER TABLE subscription_entitlement_orders ADD COLUMN IF NOT EXISTS before_expires_at TIMESTAMPTZ;
ALTER TABLE subscription_entitlement_orders ADD COLUMN IF NOT EXISTS after_expires_at TIMESTAMPTZ;
ALTER TABLE subscription_entitlement_orders ADD COLUMN IF NOT EXISTS reversed_at TIMESTAMPTZ;
CREATE TABLE IF NOT EXISTS subscription_requests (
 id BIGSERIAL PRIMARY KEY, request_key VARCHAR NOT NULL UNIQUE,
 subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id), api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
 status VARCHAR NOT NULL DEFAULT 'admitted', lots JSONB NOT NULL, admitted_at TIMESTAMPTZ NOT NULL,
 settled_at TIMESTAMPTZ, billing_request_id VARCHAR NOT NULL DEFAULT '', cost_usd DECIMAL(20,10) NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS subscription_requests_subscription_id_status ON subscription_requests(subscription_id,status);
CREATE TABLE IF NOT EXISTS subscription_refunds (
 id BIGSERIAL PRIMARY KEY, order_id BIGINT NOT NULL UNIQUE REFERENCES payment_orders(id),
 subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id), status VARCHAR NOT NULL DEFAULT 'pending',
 snapshot JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS subscription_refunds_subscription_id_status ON subscription_refunds(subscription_id,status);
CREATE TABLE IF NOT EXISTS subscription_operations (
 id BIGSERIAL PRIMARY KEY,subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id),
 entitlement_id BIGINT NOT NULL REFERENCES user_subscription_entitlements(id),operation VARCHAR NOT NULL,
 source_type VARCHAR NOT NULL,source_reference VARCHAR NOT NULL DEFAULT '',actor_id BIGINT NOT NULL DEFAULT 0,
 detail JSONB,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS subscription_operations_subscription_id_created_at ON subscription_operations(subscription_id,created_at);
CREATE TABLE IF NOT EXISTS subscription_usage_allocations (
 id BIGSERIAL PRIMARY KEY,request_key VARCHAR NOT NULL REFERENCES subscription_requests(request_key),
 entitlement_id BIGINT NOT NULL REFERENCES user_subscription_entitlements(id),cost_usd DECIMAL(20,10) NOT NULL,
 daily_window_start TIMESTAMPTZ,weekly_window_start TIMESTAMPTZ,monthly_window_start TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),UNIQUE(request_key,entitlement_id)
);
-- Restore lifecycle gating to the parent: old suspended/revoked rows remain gated
-- by parent status/deleted_at. Naturally expired and refunded lots stay terminal.
UPDATE user_subscription_entitlements e SET status='active' FROM user_subscriptions s
WHERE s.id=e.user_subscription_id AND e.source_type='legacy' AND e.status IN ('suspended','revoked') AND e.expires_at>NOW();
-- Only wholly unrepresented parents are eligible. Existing paid lots are not duplicated.
INSERT INTO user_subscription_entitlements(user_subscription_id,plan_id,status,starts_at,expires_at,daily_limit_usd,weekly_limit_usd,monthly_limit_usd,daily_window_start,weekly_window_start,monthly_window_start,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,lifetime_usage_usd,source_type,created_at,updated_at)
SELECT s.id,s.plan_id,CASE WHEN s.expires_at>NOW() THEN 'active' ELSE 'expired' END,s.starts_at,s.expires_at,
 CASE WHEN p.id IS NOT NULL THEN p.daily_limit_usd ELSE g.daily_limit_usd END,
 CASE WHEN p.id IS NOT NULL THEN p.weekly_limit_usd ELSE g.weekly_limit_usd END,
 CASE WHEN p.id IS NOT NULL THEN p.monthly_limit_usd ELSE g.monthly_limit_usd END,
 s.daily_window_start,s.weekly_window_start,s.monthly_window_start,s.daily_usage_usd,s.weekly_usage_usd,s.monthly_usage_usd,
 GREATEST(s.daily_usage_usd,s.weekly_usage_usd,s.monthly_usage_usd),'legacy',s.created_at,s.updated_at
FROM user_subscriptions s LEFT JOIN subscription_plans p ON p.id=s.plan_id LEFT JOIN groups g ON g.id=s.group_id
WHERE (p.id IS NOT NULL OR g.id IS NOT NULL) AND NOT EXISTS(SELECT 1 FROM user_subscription_entitlements e WHERE e.user_subscription_id=s.id);
-- Cache invalidation survives process termination and temporary Redis failure.
CREATE TABLE IF NOT EXISTS subscription_cache_outbox (
 subscription_id BIGINT PRIMARY KEY,user_id BIGINT NOT NULL,group_id BIGINT,version BIGINT NOT NULL DEFAULT 1
);
CREATE OR REPLACE FUNCTION geili_subscription_cache_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO subscription_cache_outbox(subscription_id,user_id,group_id) VALUES(NEW.id,NEW.user_id,NEW.group_id)
 ON CONFLICT(subscription_id) DO UPDATE SET user_id=EXCLUDED.user_id,group_id=EXCLUDED.group_id,version=subscription_cache_outbox.version+1;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS geili_subscription_cache_changed ON user_subscriptions;
CREATE TRIGGER geili_subscription_cache_changed AFTER INSERT OR UPDATE ON user_subscriptions FOR EACH ROW EXECUTE FUNCTION geili_subscription_cache_changed();

CREATE INDEX IF NOT EXISTS subscription_usage_allocations_entitlement_id_id ON subscription_usage_allocations(entitlement_id,id);
CREATE OR REPLACE FUNCTION geili_subscription_operation_cache_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO subscription_cache_outbox(subscription_id,user_id,group_id)
 SELECT id,user_id,group_id FROM user_subscriptions WHERE id=NEW.subscription_id
 ON CONFLICT(subscription_id) DO UPDATE SET version=subscription_cache_outbox.version+1;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS geili_subscription_operation_cache_changed ON subscription_operations;
CREATE TRIGGER geili_subscription_operation_cache_changed AFTER INSERT ON subscription_operations FOR EACH ROW EXECUTE FUNCTION geili_subscription_operation_cache_changed();
