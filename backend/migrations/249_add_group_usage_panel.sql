-- usage_panel is the user-facing live-routing category. It is independent of
-- groups.platform, which only describes the upstream protocol.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS usage_panel VARCHAR(20) NOT NULL DEFAULT '';

ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_usage_panel_check;
ALTER TABLE groups ADD CONSTRAINT groups_usage_panel_check
    CHECK (usage_panel IN ('', 'gpt', 'grok', 'claude', 'national', 'gemini'));
