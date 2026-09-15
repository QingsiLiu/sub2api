DO $$
DECLARE
    unified_group_id BIGINT;
BEGIN
    SELECT id INTO unified_group_id
      FROM groups
     WHERE lower(name) = lower('全模型订阅')
       AND deleted_at IS NULL
     ORDER BY id
     LIMIT 1;

    IF unified_group_id IS NULL THEN
        INSERT INTO groups (
            name, description, platform, subscription_type,
            rate_multiplier, subscription_rate_multiplier, status, is_exclusive
        ) VALUES (
            '全模型订阅', 'Unified multi-provider subscription facade', 'composite',
            'subscription', 1.0, 1.0, 'active', false
        ) RETURNING id INTO unified_group_id;
    ELSE
        UPDATE groups
           SET platform = 'composite', subscription_type = 'subscription',
               status = 'active', updated_at = NOW()
         WHERE id = unified_group_id;
    END IF;

    INSERT INTO subscription_plan_groups (subscription_plan_id, group_id)
    SELECT id, unified_group_id
      FROM subscription_plans
    ON CONFLICT (subscription_plan_id, group_id) DO NOTHING;

    INSERT INTO user_subscription_groups (user_subscription_id, group_id)
    SELECT id, unified_group_id
      FROM user_subscriptions
     WHERE deleted_at IS NULL
       AND status = 'active'
       AND expires_at > NOW()
    ON CONFLICT (user_subscription_id, group_id) DO NOTHING;
END $$;
