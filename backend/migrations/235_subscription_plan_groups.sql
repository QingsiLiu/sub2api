-- Optional group bundle for subscription plans; legacy group_id remains primary.
CREATE TABLE IF NOT EXISTS subscription_plan_groups (
    subscription_plan_id BIGINT NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (subscription_plan_id, group_id)
);
CREATE INDEX IF NOT EXISTS subscription_plan_groups_group_id_idx
    ON subscription_plan_groups (group_id);
INSERT INTO subscription_plan_groups (subscription_plan_id, group_id)
SELECT id, group_id FROM subscription_plans
ON CONFLICT (subscription_plan_id, group_id) DO NOTHING;
