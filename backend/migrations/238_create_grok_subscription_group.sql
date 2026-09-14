-- Separate subscription-priced Grok group from the normal 0.15x balance group.
-- The normal Grok Heavy group remains available to balance users at its own rate.
DO $$
DECLARE
    source_id BIGINT;
    target_id BIGINT;
    col RECORD;
BEGIN
    SELECT id INTO source_id FROM groups
      WHERE id = 88 OR lower(name) = lower('Grok Heavy')
      ORDER BY (id = 88) DESC LIMIT 1;
    IF source_id IS NULL THEN
        RAISE NOTICE 'Grok Heavy source group not present; skipping subscription group creation';
        RETURN;
    END IF;

    SELECT id INTO target_id FROM groups WHERE lower(name) = lower('Grok Heavy-订阅') AND deleted_at IS NULL LIMIT 1;
    IF target_id IS NULL THEN
        INSERT INTO groups (name, description, platform, subscription_type, rate_multiplier, is_exclusive, status)
        SELECT 'Grok Heavy-订阅', description, platform, 'subscription', 1.0, is_exclusive, 'active'
        FROM groups WHERE id = source_id
        RETURNING id INTO target_id;

        -- Copy optional routing/pricing columns only when present in this
        -- installation; production versions have historically diverged.
        FOR col IN SELECT c.column_name FROM information_schema.columns c
                   WHERE c.table_schema = 'public' AND c.table_name = 'groups'
                     AND c.column_name IN ('default_validity_days','model_routing','model_routing_enabled','fallback_group_id','allow_image_generation','allow_batch_image_generation','image_rate_independent','image_rate_multiplier','image_price_1k','image_price_2k','image_price_4k','video_rate_independent','video_rate_multiplier','video_price_480p','video_price_720p','video_price_1080p','video_model_prices','audio_realtime_price_per_min','audio_tts_price_per_million_chars','audio_stt_price_per_hour','search_price_per_1k','web_search_price_per_call','supported_model_scopes','long_context_pricing_enabled','model_pricing','allow_live','allow_messages_dispatch','default_mapped_model')
        LOOP
            EXECUTE format('UPDATE groups target SET %1$I = source.%1$I FROM groups source WHERE target.id = $1 AND source.id = $2', col.column_name) USING target_id, source_id;
        END LOOP;
    ELSE
        UPDATE groups SET subscription_type = 'subscription', rate_multiplier = 1.0, status = 'active', updated_at = NOW()
          WHERE id = target_id;
    END IF;

    -- Move only subscription entitlements/plans; ordinary balance routing stays on source_id.
    UPDATE user_subscription_groups SET group_id = target_id
      WHERE group_id = source_id AND user_subscription_id IS NOT NULL;
    UPDATE subscription_plan_groups SET group_id = target_id
      WHERE group_id = source_id AND subscription_plan_id IS NOT NULL;
    UPDATE subscription_plans SET group_id = target_id
      WHERE group_id = source_id AND lower(name) LIKE '%grok%';
END $$;
