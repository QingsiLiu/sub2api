-- Reusable user benefit campaigns and idempotent claim ledger.
CREATE TABLE IF NOT EXISTS benefit_campaigns (
    id BIGSERIAL PRIMARY KEY,
    slug VARCHAR(120) NOT NULL UNIQUE,
    title VARCHAR(200) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'draft',
    starts_at TIMESTAMPTZ NOT NULL,
    claim_ends_at TIMESTAMPTZ NOT NULL,
    eligibility_starts_at TIMESTAMPTZ NOT NULL,
    eligibility_ends_at TIMESTAMPTZ NOT NULL,
    duration_days INTEGER NOT NULL DEFAULT 7,
    daily_limit_usd DECIMAL(20,8) NOT NULL DEFAULT 45,
    reset_mode VARCHAR(30) NOT NULL DEFAULT 'beijing_day',
    max_claims BIGINT,
    eligibility_snapshot_at TIMESTAMPTZ,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    updated_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (status IN ('draft','active','paused','closed')),
    CHECK (claim_ends_at > starts_at),
    CHECK (eligibility_ends_at > eligibility_starts_at),
    CHECK (duration_days > 0 AND duration_days <= 365),
    CHECK (daily_limit_usd >= 0),
    CHECK (reset_mode = 'beijing_day')
);

CREATE INDEX IF NOT EXISTS benefit_campaigns_active_window_idx
    ON benefit_campaigns(status, starts_at, claim_ends_at);

CREATE TABLE IF NOT EXISTS benefit_campaign_eligibility (
    campaign_id BIGINT NOT NULL REFERENCES benefit_campaigns(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason VARCHAR(80) NOT NULL DEFAULT 'settled_usage',
    snapshot_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (campaign_id, user_id)
);
CREATE INDEX IF NOT EXISTS benefit_campaign_eligibility_user_idx
    ON benefit_campaign_eligibility(user_id, campaign_id);

CREATE TABLE IF NOT EXISTS benefit_campaign_claims (
    id BIGSERIAL PRIMARY KEY,
    campaign_id BIGINT NOT NULL REFERENCES benefit_campaigns(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id) ON DELETE RESTRICT,
    entitlement_id BIGINT NOT NULL REFERENCES user_subscription_entitlements(id) ON DELETE RESTRICT,
    idempotency_key VARCHAR(200) NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (campaign_id, user_id),
    UNIQUE (idempotency_key)
);
CREATE INDEX IF NOT EXISTS benefit_campaign_claims_campaign_idx
    ON benefit_campaign_claims(campaign_id, claimed_at);
CREATE INDEX IF NOT EXISTS benefit_campaign_claims_user_idx
    ON benefit_campaign_claims(user_id, claimed_at);

CREATE UNIQUE INDEX IF NOT EXISTS benefit_campaign_one_active_slug_idx
    ON benefit_campaigns(slug) WHERE status IN ('active','paused');


-- Default 2026 double-festival campaign. Eligibility is frozen against the
-- fixed Beijing window so later claims cannot change the audience.
INSERT INTO benefit_campaigns(
    slug,title,status,starts_at,claim_ends_at,eligibility_starts_at,eligibility_ends_at,
    duration_days,daily_limit_usd,reset_mode,config
) VALUES(
    'double-festival',
    '中秋国庆双节赠礼',
    'active',
    '2026-09-25 00:00:00+08',
    '2026-10-09 00:00:00+08',
    '2026-09-11 00:00:00+08',
    '2026-09-25 00:00:00+08',
    7,45,'beijing_day',
    '{"source":"settled_usage","display":"activities"}'::jsonb
) ON CONFLICT (slug) DO NOTHING;

INSERT INTO benefit_campaign_eligibility(campaign_id,user_id,reason,snapshot_at)
SELECT c.id,u.id,'settled_usage','2026-09-25 00:00:00+08'
FROM benefit_campaigns c
JOIN users u ON u.deleted_at IS NULL AND u.status='active'
WHERE c.slug='double-festival'
  AND EXISTS (
      SELECT 1 FROM usage_logs l
      WHERE l.user_id=u.id
        AND l.created_at >= c.eligibility_starts_at
        AND l.created_at < c.eligibility_ends_at
  )
ON CONFLICT (campaign_id,user_id) DO NOTHING;

UPDATE benefit_campaigns
SET eligibility_snapshot_at='2026-09-25 00:00:00+08', updated_at=NOW()
WHERE slug='double-festival' AND eligibility_snapshot_at IS NULL;
