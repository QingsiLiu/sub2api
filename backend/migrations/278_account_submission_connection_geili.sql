SET LOCAL lock_timeout = '5s';
CREATE TABLE account_submission_connections_geili (
 invite_id BIGINT PRIMARY KEY REFERENCES account_submission_invites_geili(id) ON DELETE RESTRICT,
 proxy_id BIGINT UNIQUE REFERENCES proxies(id) ON DELETE RESTRICT,
 version BIGINT NOT NULL DEFAULT 0,
 request_id UUID,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Proxy.URL carries the public-only policy without changing upstream Ent schemas.
-- Administrative renaming must never remove that policy from an external proxy.
CREATE FUNCTION protect_external_proxy_policy_geili() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.name LIKE '[external] %' AND NEW.name NOT LIKE '[external] %' THEN
  RAISE EXCEPTION 'External proxy policy cannot be removed' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER protect_external_proxy_policy_geili BEFORE UPDATE OF name ON proxies
FOR EACH ROW EXECUTE FUNCTION protect_external_proxy_policy_geili();
