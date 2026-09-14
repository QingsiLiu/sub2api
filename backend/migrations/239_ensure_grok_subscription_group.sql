-- Ensure a subscription-priced Grok group exists even on a fresh DB where
-- operational balance groups are seeded later.
INSERT INTO groups (name, platform, subscription_type, rate_multiplier, is_exclusive, status)
SELECT 'Grok Heavy-订阅', 'xai', 'subscription', 1.0, false, 'active'
WHERE NOT EXISTS (
  SELECT 1 FROM groups WHERE lower(name) = lower('Grok Heavy-订阅') AND deleted_at IS NULL
);
