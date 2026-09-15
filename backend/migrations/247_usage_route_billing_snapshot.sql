-- Keep the selected route and billing inputs even if groups/routes change later.
-- Old rows remain NULL; no historical route is guessed or reconstructed.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS route_billing_snapshot JSONB;
