-- Preserve every historical subscription/key. New subscriptions are owned by
-- plans and new keys choose settlement independently of their routing groups.
DROP TRIGGER IF EXISTS geili_unified_subscription_grant ON user_subscriptions;
DROP TRIGGER IF EXISTS geili_unified_plan_grant ON subscription_plans;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'groups' AND column_name = 'subscription_enabled') THEN
        ALTER TABLE groups ADD COLUMN subscription_enabled BOOLEAN NOT NULL DEFAULT false;
        UPDATE groups SET subscription_enabled = true
        WHERE subscription_type = 'subscription'
           OR id IN (SELECT r.target_group_id FROM composite_model_routes r JOIN groups g ON g.id = r.group_id
                     WHERE r.deleted_at IS NULL AND g.subscription_type = 'subscription');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'subscription_plans' AND column_name = 'daily_limit_usd') THEN
        ALTER TABLE subscription_plans ADD COLUMN daily_limit_usd DECIMAL(20,8),
            ADD COLUMN weekly_limit_usd DECIMAL(20,8), ADD COLUMN monthly_limit_usd DECIMAL(20,8);
        UPDATE subscription_plans p SET daily_limit_usd = g.daily_limit_usd,
            weekly_limit_usd = g.weekly_limit_usd, monthly_limit_usd = g.monthly_limit_usd
        FROM groups g WHERE p.group_id = g.id;
    END IF;
END $$;

ALTER TABLE subscription_plans ALTER COLUMN group_id DROP NOT NULL;
ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS is_legacy_compat BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;
ALTER TABLE user_subscriptions ALTER COLUMN group_id DROP NOT NULL;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS plan_id BIGINT REFERENCES subscription_plans(id) ON DELETE RESTRICT;
ALTER TABLE redeem_codes ADD COLUMN IF NOT EXISTS plan_id BIGINT REFERENCES subscription_plans(id) ON DELETE RESTRICT;

-- An unambiguous existing plan can own its former group's subscriptions.
UPDATE user_subscriptions us SET plan_id = p.id
FROM (SELECT group_id, MIN(id) AS id FROM subscription_plans
      WHERE group_id IS NOT NULL AND NOT is_legacy_compat GROUP BY group_id HAVING COUNT(*) = 1) p
WHERE us.plan_id IS NULL AND us.group_id = p.group_id;

-- Ambiguous or unmarketed legacy groups get one non-sale compatibility plan.
-- Do not guess an old customer's product from one of several sale plans.
INSERT INTO subscription_plans (group_id, name, description, price, validity_days, validity_unit,
    features, product_name, for_sale, sort_order, is_legacy_compat,
    daily_limit_usd, weekly_limit_usd, monthly_limit_usd)
SELECT g.id, LEFT(g.name || ' · 历史订阅', 100), 'Migrated legacy subscription quota', 0, 30, 'day',
    '', '', false, 0, true, g.daily_limit_usd, g.weekly_limit_usd, g.monthly_limit_usd
FROM groups g
WHERE EXISTS (SELECT 1 FROM user_subscriptions us WHERE us.group_id = g.id AND us.plan_id IS NULL)
  AND NOT EXISTS (SELECT 1 FROM subscription_plans p WHERE p.group_id = g.id AND p.is_legacy_compat);
UPDATE user_subscriptions us SET plan_id = p.id
FROM subscription_plans p
WHERE us.plan_id IS NULL AND p.is_legacy_compat AND us.group_id = p.group_id;

CREATE UNIQUE INDEX IF NOT EXISTS subscription_plans_legacy_compat_group_unique
    ON subscription_plans(group_id) WHERE is_legacy_compat;
CREATE UNIQUE INDEX IF NOT EXISTS user_subscriptions_user_plan_unique_active
    ON user_subscriptions(user_id, plan_id) WHERE deleted_at IS NULL AND plan_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_plan ON user_subscriptions(plan_id);

-- Empty billing_source keeps the existing Key's legacy behavior. New API callers
-- explicitly supply balance/subscription and single/composite routing.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS billing_source VARCHAR(20) NOT NULL DEFAULT '';
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS routing_mode VARCHAR(20) NOT NULL DEFAULT 'single';
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS group_ids JSONB;
CREATE INDEX IF NOT EXISTS idx_api_keys_explicit_groups ON api_keys USING GIN(group_ids);
