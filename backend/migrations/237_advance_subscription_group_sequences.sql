-- Keep repaired entitlement sequences ahead of existing rows.
SELECT setval('user_subscription_groups_id_seq', COALESCE((SELECT MAX(id) FROM user_subscription_groups), 1), true);
SELECT setval('subscription_plan_groups_id_seq', COALESCE((SELECT MAX(id) FROM subscription_plan_groups), 1), true);
