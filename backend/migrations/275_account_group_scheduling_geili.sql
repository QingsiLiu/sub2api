-- Opt-in only. Historical group priorities must not start influencing live requests.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS group_scheduling_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS group_scheduling_version bigint NOT NULL DEFAULT 0;
ALTER TABLE account_groups ADD COLUMN IF NOT EXISTS priority_mode varchar(16) NOT NULL DEFAULT 'inherit';
ALTER TABLE account_groups ADD CONSTRAINT account_groups_priority_mode_geili_check
    CHECK (priority_mode IN ('inherit', 'auto', 'fixed'));
CREATE TABLE IF NOT EXISTS geili_group_scheduling_audits (
    id bigserial PRIMARY KEY,
    group_id bigint NOT NULL,
    version bigint NOT NULL,
    actor varchar(200) NOT NULL,
    source varchar(20) NOT NULL,
    before_state jsonb NOT NULL,
    after_state jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS geili_group_scheduling_audits_group_idx
    ON geili_group_scheduling_audits(group_id, id DESC);
