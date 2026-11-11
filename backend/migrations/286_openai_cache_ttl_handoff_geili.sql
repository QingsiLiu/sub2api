-- Old blue/green owners do not know the new scalar TTL fields. Preserve only
-- those two metadata columns from the immutable, identity/amount-matched receipt
-- when such an owner inserts or restores a log. No financial column is changed.
CREATE OR REPLACE FUNCTION geili_restore_openai_cache_ttl_from_receipt()
RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
DECLARE
    frozen jsonb;
    short_text text;
    long_text text;
    short_tokens bigint;
    long_tokens bigint;
BEGIN
    SELECT r.detail INTO frozen
    FROM public.usage_settlement_receipts r
    WHERE r.usage_request_id IS NOT NULL
      AND r.usage_request_id = NEW.request_id
      AND r.api_key_id = NEW.api_key_id
      AND r.user_id = NEW.user_id
      AND r.account_id = NEW.account_id
      AND r.subscription_id IS NOT DISTINCT FROM NEW.subscription_id
      AND r.state = 'settled' AND r.record_source = 'live'
      AND r.record_completeness = 'complete'
      AND r.charged_amount = NEW.actual_cost;

    short_text := frozen->>'cache_creation_30m_tokens';
    long_text := frozen->>'cache_creation_12h_tokens';
    IF jsonb_typeof(frozen->'cache_creation_30m_tokens') = 'number'
       AND jsonb_typeof(frozen->'cache_creation_12h_tokens') = 'number'
       AND short_text ~ '^[0-9]{1,10}$' AND long_text ~ '^[0-9]{1,10}$' THEN
        short_tokens := short_text::bigint;
        long_tokens := long_text::bigint;
        IF short_tokens + long_tokens = NEW.cache_creation_tokens THEN
            -- The positive INTEGER aggregate also bounds each nonnegative part.
            NEW.cache_creation_30m_tokens := short_tokens::integer;
            NEW.cache_creation_12h_tokens := long_tokens::integer;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER geili_usage_log_openai_cache_ttl_handoff
BEFORE INSERT OR UPDATE OF cache_creation_tokens, actual_cost ON usage_logs
FOR EACH ROW
WHEN (NEW.cache_creation_tokens > 0
      AND NEW.cache_creation_30m_tokens = 0
      AND NEW.cache_creation_12h_tokens = 0)
EXECUTE FUNCTION geili_restore_openai_cache_ttl_from_receipt();
