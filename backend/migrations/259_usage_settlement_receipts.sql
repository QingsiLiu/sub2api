-- Geili: durable financial evidence and retryable usage delivery are deliberately
-- independent of usage_logs and mutable parent rows (no foreign-key lock chain).
CREATE TABLE IF NOT EXISTS usage_settlement_receipts (
 id BIGSERIAL PRIMARY KEY,
 request_id TEXT NOT NULL, api_key_id BIGINT NOT NULL,
 usage_request_id TEXT,
 request_fingerprint TEXT NOT NULL,
 user_id BIGINT NOT NULL, account_id BIGINT, group_id BIGINT, subscription_id BIGINT,
 billing_type SMALLINT NOT NULL DEFAULT 0,
 charged_amount NUMERIC(20,10),
 accounting_date DATE NOT NULL,
 admitted_at TIMESTAMPTZ, completed_at TIMESTAMPTZ, settled_at TIMESTAMPTZ,
 command JSONB, detail JSONB,
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','settled')),
 record_source TEXT NOT NULL DEFAULT 'live' CHECK (record_source IN ('live','historical_recovery')),
 record_completeness TEXT NOT NULL DEFAULT 'partial' CHECK (record_completeness IN ('complete','partial','amount_unknown')),
 settlement_attempts INTEGER NOT NULL DEFAULT 0, delivery_attempts INTEGER NOT NULL DEFAULT 0,
 next_settlement_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), next_delivery_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 settlement_lease_token TEXT, settlement_lease_until TIMESTAMPTZ,
 delivery_lease_token TEXT, delivery_lease_until TIMESTAMPTZ,
 last_settlement_error TEXT, last_delivery_error TEXT,
 delivered_at TIMESTAMPTZ, usage_log_id BIGINT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(request_id,api_key_id),
 CHECK (charged_amount IS NULL OR charged_amount >= 0),
 CHECK (state <> 'settled' OR settled_at IS NOT NULL),
 CHECK (record_completeness <> 'amount_unknown' OR charged_amount IS NULL)
);
CREATE INDEX IF NOT EXISTS usage_settlement_pending ON usage_settlement_receipts(next_settlement_at,id) WHERE state='pending';
CREATE INDEX IF NOT EXISTS usage_settlement_delivery ON usage_settlement_receipts(next_delivery_at,id) WHERE state='settled' AND delivered_at IS NULL AND record_source='live' AND record_completeness='complete';
CREATE INDEX IF NOT EXISTS usage_settlement_unresolved_health ON usage_settlement_receipts(created_at) WHERE state='pending' OR (state='settled' AND delivered_at IS NULL AND record_source='live');
CREATE INDEX IF NOT EXISTS usage_settlement_user_date ON usage_settlement_receipts(user_id,accounting_date,id);
CREATE UNIQUE INDEX IF NOT EXISTS usage_settlement_log_identity ON usage_settlement_receipts(usage_request_id,api_key_id) WHERE usage_request_id IS NOT NULL;
COMMENT ON TABLE usage_settlement_receipts IS 'Financial evidence; retain at least 365 days and never purge pending/undelivered evidence. Does not store request bodies, credentials, prompts, or auth headers.';

-- Per-hold terminal state prevents a capture and release from independently
-- consuming the same frozen funds, even though they use distinct request IDs.
CREATE TABLE IF NOT EXISTS usage_balance_holds (
 hold_request_id TEXT NOT NULL, api_key_id BIGINT NOT NULL,
 user_id BIGINT NOT NULL, batch_id TEXT NOT NULL,
 held_amount NUMERIC(20,8) NOT NULL CHECK(held_amount>=0),
 captured_amount NUMERIC(20,8),
 state TEXT NOT NULL CHECK(state IN ('reserved','captured','released')),
 terminal_request_id TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(hold_request_id,api_key_id)
);
