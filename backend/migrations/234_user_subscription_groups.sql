-- Shared subscription entitlements for Geili multi-group subscriptions.
-- Idempotent by design; the legacy user_subscriptions.group_id remains the
-- primary/compatibility group while this table contains additional entitlements.
CREATE TABLE IF NOT EXISTS user_subscription_groups (
    user_subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_subscription_id, group_id)
);

CREATE INDEX IF NOT EXISTS user_subscription_groups_group_id_idx
    ON user_subscription_groups (group_id);

-- Every legacy subscription keeps access to its original group. Re-running is safe.
INSERT INTO user_subscription_groups (user_subscription_id, group_id)
SELECT id, group_id FROM user_subscriptions
WHERE group_id IS NOT NULL
ON CONFLICT (user_subscription_id, group_id) DO NOTHING;
