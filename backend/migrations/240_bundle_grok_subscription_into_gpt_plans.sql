-- Existing GPT subscription plans must grant the dedicated subscription-priced
-- Grok group as well. This is intentionally separate from normal 0.15x Grok.
INSERT INTO subscription_plan_groups (subscription_plan_id, group_id)
SELECT sp.id, grok.id
FROM subscription_plans sp
JOIN groups primary_group ON primary_group.id = sp.group_id
JOIN groups grok ON lower(grok.name) = lower('Grok Heavy-订阅') AND grok.deleted_at IS NULL
WHERE sp.group_id = 4 OR lower(primary_group.name) = lower('GPT稳定')
ON CONFLICT (subscription_plan_id, group_id) DO NOTHING;
