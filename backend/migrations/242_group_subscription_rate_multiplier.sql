-- Keep balance and subscription pricing multipliers independent.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS subscription_rate_multiplier DECIMAL(10,4);

UPDATE groups
   SET subscription_rate_multiplier = rate_multiplier
 WHERE subscription_rate_multiplier IS NULL;

ALTER TABLE groups
    ALTER COLUMN subscription_rate_multiplier SET DEFAULT 1.0,
    ALTER COLUMN subscription_rate_multiplier SET NOT NULL;

COMMENT ON COLUMN groups.subscription_rate_multiplier IS
    'Multiplier used for subscription requests; balance requests use rate_multiplier.';
