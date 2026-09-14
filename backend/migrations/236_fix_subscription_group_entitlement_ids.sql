-- Ent-generated queries require a conventional single-column id on entitlement tables.
-- Repair installations created by migration 234/235 without changing those immutable files.
ALTER TABLE user_subscription_groups ADD COLUMN IF NOT EXISTS id BIGINT;
CREATE SEQUENCE IF NOT EXISTS user_subscription_groups_id_seq;
ALTER TABLE user_subscription_groups ALTER COLUMN id SET DEFAULT nextval('user_subscription_groups_id_seq');
UPDATE user_subscription_groups SET id = nextval('user_subscription_groups_id_seq') WHERE id IS NULL;
SELECT setval('user_subscription_groups_id_seq', COALESCE((SELECT MAX(id) FROM user_subscription_groups), 1), true);
ALTER TABLE user_subscription_groups ALTER COLUMN id SET NOT NULL;
ALTER TABLE user_subscription_groups DROP CONSTRAINT IF EXISTS user_subscription_groups_pkey;
ALTER TABLE user_subscription_groups ADD CONSTRAINT user_subscription_groups_pkey PRIMARY KEY (id);
CREATE UNIQUE INDEX IF NOT EXISTS user_subscription_groups_subscription_group_unique ON user_subscription_groups(user_subscription_id, group_id);

ALTER TABLE subscription_plan_groups ADD COLUMN IF NOT EXISTS id BIGINT;
CREATE SEQUENCE IF NOT EXISTS subscription_plan_groups_id_seq;
ALTER TABLE subscription_plan_groups ALTER COLUMN id SET DEFAULT nextval('subscription_plan_groups_id_seq');
UPDATE subscription_plan_groups SET id = nextval('subscription_plan_groups_id_seq') WHERE id IS NULL;
SELECT setval('subscription_plan_groups_id_seq', COALESCE((SELECT MAX(id) FROM subscription_plan_groups), 1), true);
ALTER TABLE subscription_plan_groups ALTER COLUMN id SET NOT NULL;
ALTER TABLE subscription_plan_groups DROP CONSTRAINT IF EXISTS subscription_plan_groups_pkey;
ALTER TABLE subscription_plan_groups ADD CONSTRAINT subscription_plan_groups_pkey PRIMARY KEY (id);
CREATE UNIQUE INDEX IF NOT EXISTS subscription_plan_groups_plan_group_unique ON subscription_plan_groups(subscription_plan_id, group_id);
