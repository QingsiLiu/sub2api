ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS subscription_id BIGINT REFERENCES user_subscriptions(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS route_preferences JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS api_keys_subscription_id_idx ON api_keys(subscription_id);

ALTER TABLE composite_model_routes
    ADD COLUMN IF NOT EXISTS target_group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS composite_model_routes_target_group_id_idx
    ON composite_model_routes(target_group_id);
