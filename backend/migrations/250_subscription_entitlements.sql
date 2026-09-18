-- Subscription entitlements are immutable purchase lots.  The existing
-- user_subscriptions row remains the aggregate/compatibility identity.
CREATE TABLE IF NOT EXISTS user_subscription_entitlements (
    id BIGSERIAL PRIMARY KEY,
    user_subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE CASCADE,
    plan_id BIGINT NULL REFERENCES subscription_plans(id) ON DELETE RESTRICT,
    source_order_id BIGINT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    lot_index INTEGER NOT NULL DEFAULT 0,
    purchase_mode VARCHAR(20) NOT NULL DEFAULT 'renew',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    starts_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    daily_limit_usd DECIMAL(20,8),
    weekly_limit_usd DECIMAL(20,8),
    monthly_limit_usd DECIMAL(20,8),
    daily_window_start TIMESTAMPTZ,
    weekly_window_start TIMESTAMPTZ,
    monthly_window_start TIMESTAMPTZ,
    daily_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0,
    weekly_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0,
    monthly_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0,
    lifetime_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0,
    refunded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS subscription_mode VARCHAR(20) NOT NULL DEFAULT 'renew';
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS subscription_quantity INTEGER NOT NULL DEFAULT 1;

CREATE INDEX IF NOT EXISTS idx_user_subscription_entitlements_subscription_expiry
    ON user_subscription_entitlements(user_subscription_id, expires_at);
CREATE INDEX IF NOT EXISTS idx_user_subscription_entitlements_status_expiry
    ON user_subscription_entitlements(status, expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS user_subscription_entitlements_order_lot_unique
    ON user_subscription_entitlements(source_order_id, lot_index)
    WHERE source_order_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS subscription_entitlement_orders (
    id BIGSERIAL PRIMARY KEY,
    entitlement_id BIGINT NOT NULL REFERENCES user_subscription_entitlements(id) ON DELETE CASCADE,
    order_id BIGINT NOT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    lot_index INTEGER NOT NULL DEFAULT 0,
    operation VARCHAR(20) NOT NULL,
    days_added INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS subscription_entitlement_orders_order_lot_unique
    ON subscription_entitlement_orders(order_id, lot_index);
CREATE INDEX IF NOT EXISTS idx_subscription_entitlement_orders_entitlement
    ON subscription_entitlement_orders(entitlement_id);

-- Every historical aggregate subscription starts with one compatibility lot.
-- Preserve its existing usage and its plan/group quota snapshot.
INSERT INTO user_subscription_entitlements (
    user_subscription_id, plan_id, lot_index, purchase_mode, status,
    starts_at, expires_at, daily_limit_usd, weekly_limit_usd, monthly_limit_usd,
    daily_window_start, weekly_window_start, monthly_window_start,
    daily_usage_usd, weekly_usage_usd, monthly_usage_usd, lifetime_usage_usd,
    created_at, updated_at
)
SELECT us.id,
       us.plan_id,
       0,
       'renew',
       CASE WHEN us.deleted_at IS NOT NULL THEN 'revoked'
            WHEN us.expires_at <= NOW() THEN 'expired'
            ELSE us.status END,
       us.starts_at,
       us.expires_at,
       CASE WHEN p.id IS NOT NULL THEN p.daily_limit_usd ELSE g.daily_limit_usd END,
       CASE WHEN p.id IS NOT NULL THEN p.weekly_limit_usd ELSE g.weekly_limit_usd END,
       CASE WHEN p.id IS NOT NULL THEN p.monthly_limit_usd ELSE g.monthly_limit_usd END,
       us.daily_window_start,
       us.weekly_window_start,
       us.monthly_window_start,
       us.daily_usage_usd,
       us.weekly_usage_usd,
       us.monthly_usage_usd,
       GREATEST(us.daily_usage_usd, us.weekly_usage_usd, us.monthly_usage_usd),
       us.created_at,
       us.updated_at
FROM user_subscriptions us
LEFT JOIN subscription_plans p ON p.id = us.plan_id
LEFT JOIN groups g ON g.id = us.group_id
WHERE NOT EXISTS (
    SELECT 1 FROM user_subscription_entitlements e
    WHERE e.user_subscription_id = us.id AND e.source_order_id IS NULL
);
