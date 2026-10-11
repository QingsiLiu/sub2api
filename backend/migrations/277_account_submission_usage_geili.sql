-- The original invitation becomes a revocable, read-only usage capability after submission.
SET LOCAL lock_timeout = '5s';
ALTER TABLE account_submission_invites_geili ADD COLUMN view_revoked_at TIMESTAMPTZ;

-- Keep one external credential identity per account, including bulk/re-auth/internal updates.
CREATE FUNCTION protect_external_account_identity_geili() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.credentials->'api_key' IS DISTINCT FROM NEW.credentials->'api_key'
        OR OLD.platform IS DISTINCT FROM NEW.platform OR OLD.type IS DISTINCT FROM NEW.type)
       AND EXISTS (SELECT 1 FROM account_submission_invites_geili WHERE account_id=OLD.id) THEN
        RAISE EXCEPTION 'Externally submitted credentials cannot be replaced; create a new invitation'
            USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER protect_external_account_identity_geili
BEFORE UPDATE OF credentials, platform, type ON accounts
FOR EACH ROW EXECUTE FUNCTION protect_external_account_identity_geili();
