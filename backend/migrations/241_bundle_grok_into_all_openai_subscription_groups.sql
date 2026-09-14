-- Production-aware bundle migration. OpenAI subscription groups are not a
-- single fixed ID: each plan tier has its own group and rate 1.0. Grant all
-- of them the one Grok subscription group, while leaving normal Grok Heavy
-- (0.15x) untouched.
DO $$
DECLARE
    grok_sub_id BIGINT;
BEGIN
    SELECT id INTO grok_sub_id
      FROM groups
     WHERE lower(name) = lower('Grok Heavy-订阅') AND deleted_at IS NULL
     LIMIT 1;

    IF grok_sub_id IS NULL THEN
        INSERT INTO groups (name, description, platform, subscription_type, rate_multiplier, is_exclusive, status)
        VALUES ('Grok Heavy-订阅', 'Grok Heavy subscription entitlement', 'grok', 'subscription', 1.0, false, 'active')
        RETURNING id INTO grok_sub_id;
    ELSE
        UPDATE groups SET subscription_type = 'subscription', rate_multiplier = 1.0, status = 'active', updated_at = NOW()
         WHERE id = grok_sub_id;
    END IF;

    -- Repair the earlier migration if it linked the normal 0.15x group.
    UPDATE user_subscription_groups
       SET group_id = grok_sub_id
     WHERE group_id = 88
       AND NOT EXISTS (
           SELECT 1 FROM user_subscription_groups existing
            WHERE existing.user_subscription_id = user_subscription_groups.user_subscription_id
              AND existing.group_id = grok_sub_id
       );
    DELETE FROM user_subscription_groups old
     WHERE old.group_id = 88
       AND EXISTS (SELECT 1 FROM user_subscription_groups keep
                    WHERE keep.user_subscription_id = old.user_subscription_id
                      AND keep.group_id = grok_sub_id);

    INSERT INTO user_subscription_groups (user_subscription_id, group_id)
    SELECT us.id, grok_sub_id
      FROM user_subscriptions us
      JOIN groups primary_group ON primary_group.id = us.group_id
     WHERE us.deleted_at IS NULL
       AND primary_group.deleted_at IS NULL
       AND primary_group.platform = 'openai'
       AND primary_group.subscription_type = 'subscription'
    ON CONFLICT (user_subscription_id, group_id) DO NOTHING;

    INSERT INTO subscription_plan_groups (subscription_plan_id, group_id)
    SELECT sp.id, grok_sub_id
      FROM subscription_plans sp
      JOIN groups primary_group ON primary_group.id = sp.group_id
     WHERE primary_group.deleted_at IS NULL
       AND primary_group.platform = 'openai'
       AND primary_group.subscription_type = 'subscription'
    ON CONFLICT (subscription_plan_id, group_id) DO NOTHING;
END $$;
