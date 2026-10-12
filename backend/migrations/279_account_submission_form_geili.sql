SET LOCAL lock_timeout = '5s';
-- Full-form invitations retain the public-egress requirement even after ordinary
-- editing. Token refresh is allowed, but an existing supplier identity cannot be
-- replaced through metadata or bulk-edit operations.
CREATE FUNCTION protect_external_form_identity_geili() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE k TEXT;
BEGIN
    IF OLD.extra->>'external_submission_form_geili' = 'true'
       AND EXISTS (SELECT 1 FROM account_submission_invites_geili WHERE account_id=OLD.id) THEN
        IF NEW.extra->>'external_submission_form_geili' IS DISTINCT FROM 'true' THEN
            RAISE EXCEPTION 'External account egress policy is immutable' USING ERRCODE='23514';
        END IF;
        FOREACH k IN ARRAY ARRAY['chatgpt_account_id','chatgpt_user_id','account_uuid','org_uuid','aws_access_key_id'] LOOP
            IF OLD.credentials->k IS NOT NULL AND OLD.credentials->k IS DISTINCT FROM NEW.credentials->k THEN
                RAISE EXCEPTION 'External supplier identity is immutable' USING ERRCODE='23514';
            END IF;
        END LOOP;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER protect_external_form_identity_geili
BEFORE UPDATE OF credentials,extra ON accounts
FOR EACH ROW EXECUTE FUNCTION protect_external_form_identity_geili();
