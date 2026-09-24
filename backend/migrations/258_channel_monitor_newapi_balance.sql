-- Migration: 258_channel_monitor_newapi_balance
-- Add a passive NewAPI account balance check mode.

ALTER TABLE channel_monitors
    DROP CONSTRAINT IF EXISTS channel_monitors_check_mode_check;

ALTER TABLE channel_monitors
    ADD CONSTRAINT channel_monitors_check_mode_check
    CHECK (check_mode IN ('probe', 'quota', 'quota_probe', 'newapi_balance'));

COMMENT ON COLUMN channel_monitors.check_mode IS
    'probe = LLM probe; quota = linked account usage; quota_probe = probe + linked account usage; newapi_balance = NewAPI account quota';
