-- Keep one immutable provenance row per external account, including on older application rollback.
-- Only roots have a usable public capability; batch members get random, undisclosed token hashes.
ALTER TABLE account_submission_invites_geili
    ADD COLUMN parent_invite_id BIGINT REFERENCES account_submission_invites_geili(id) ON DELETE CASCADE,
    ADD COLUMN account_ref UUID NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX account_submission_account_ref_geili ON account_submission_invites_geili(account_ref);
CREATE INDEX account_submission_parent_geili ON account_submission_invites_geili(parent_invite_id) WHERE parent_invite_id IS NOT NULL;
ALTER TABLE account_submission_invites_geili ADD CONSTRAINT account_submission_parent_not_self_geili CHECK (parent_invite_id IS NULL OR parent_invite_id <> id);
