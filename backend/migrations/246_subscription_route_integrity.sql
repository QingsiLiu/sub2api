-- A provider/model may have several independently selectable account pools.
DROP INDEX IF EXISTS idx_composite_model_routes_unique_active;
CREATE UNIQUE INDEX IF NOT EXISTS idx_composite_model_routes_profile_unique_active
    ON composite_model_routes (group_id, endpoint, match_type, public_model, profile_key)
    WHERE deleted_at IS NULL;

-- Every fulfilment path (payment, redeem, manual assignment) grants the same
-- facade. The existing subscription remains the only owner of quota/expiry.
CREATE OR REPLACE FUNCTION geili_grant_unified_subscription() RETURNS trigger AS $$
BEGIN
    INSERT INTO user_subscription_groups (user_subscription_id, group_id)
    SELECT NEW.id, g.id FROM groups g
    WHERE g.name = '全模型订阅' AND g.platform = 'composite'
      AND g.subscription_type = 'subscription' AND g.deleted_at IS NULL
    ON CONFLICT (user_subscription_id, group_id) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS geili_unified_subscription_grant ON user_subscriptions;
CREATE TRIGGER geili_unified_subscription_grant AFTER INSERT ON user_subscriptions
    FOR EACH ROW EXECUTE FUNCTION geili_grant_unified_subscription();

CREATE OR REPLACE FUNCTION geili_grant_unified_plan() RETURNS trigger AS $$
BEGIN
    INSERT INTO subscription_plan_groups (subscription_plan_id, group_id)
    SELECT NEW.id, g.id FROM groups g
    WHERE g.name = '全模型订阅' AND g.platform = 'composite'
      AND g.subscription_type = 'subscription' AND g.deleted_at IS NULL
    ON CONFLICT (subscription_plan_id, group_id) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS geili_unified_plan_grant ON subscription_plans;
CREATE TRIGGER geili_unified_plan_grant AFTER INSERT ON subscription_plans
    FOR EACH ROW EXECUTE FUNCTION geili_grant_unified_plan();

-- Cover expired terms too, so renewing an old subscription restores its bundle.
INSERT INTO user_subscription_groups (user_subscription_id, group_id)
SELECT us.id, g.id FROM user_subscriptions us CROSS JOIN groups g
WHERE us.deleted_at IS NULL AND g.deleted_at IS NULL AND g.name = '全模型订阅'
  AND g.platform = 'composite' AND g.subscription_type = 'subscription'
ON CONFLICT (user_subscription_id, group_id) DO NOTHING;
