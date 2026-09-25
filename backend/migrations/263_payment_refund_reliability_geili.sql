-- Refund intent and local debit commit together before any provider request.
-- One immutable operation per payment order; unknown outcomes require querying
-- the same provider identity, never issuing a fresh refund request.
CREATE TABLE IF NOT EXISTS payment_refund_journals (
    order_id BIGINT PRIMARY KEY REFERENCES payment_orders(id) ON DELETE RESTRICT,
    request_key TEXT NOT NULL UNIQUE,
    user_id BIGINT NOT NULL,
    refund_amount NUMERIC(20,8) NOT NULL CHECK (refund_amount > 0),
    gateway_amount NUMERIC(20,8) NOT NULL CHECK (gateway_amount > 0),
    deducted_balance NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK (deducted_balance >= 0),
    payload JSONB NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending_provider','succeeded','failed','manual_review')),
    provider_refund_id TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS payment_refund_journals_pending_idx
    ON payment_refund_journals (state, updated_at);
