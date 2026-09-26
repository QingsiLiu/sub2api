-- Geili: exact, independently invalidated all-time financial sufficient statistics.
-- No source writer takes a rollup lock or references a mutable parent via FK.
CREATE TABLE usage_financial_daily_rollups (
 bucket_date DATE PRIMARY KEY,
 requests BIGINT NOT NULL, input_tokens NUMERIC NOT NULL, output_tokens NUMERIC NOT NULL,
 cache_creation_tokens NUMERIC NOT NULL, cache_read_tokens NUMERIC NOT NULL,
 total_cost NUMERIC NOT NULL, actual_cost NUMERIC NOT NULL, account_cost NUMERIC NOT NULL,
 duration_sum NUMERIC NOT NULL, duration_count BIGINT NOT NULL,
 balance_cost NUMERIC NOT NULL, subscription_cost NUMERIC NOT NULL,
 detail_pending BIGINT NOT NULL, unknown_amount BIGINT NOT NULL,
 incomplete_records BIGINT NOT NULL, unknown_standard BIGINT NOT NULL, unknown_tokens BIGINT NOT NULL,
 computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE usage_financial_rollup_state (
 id SMALLINT PRIMARY KEY CHECK(id=1), start_date DATE, closed_before DATE,
 CHECK ((start_date IS NULL) = (closed_before IS NULL))
);
INSERT INTO usage_financial_rollup_state(id) VALUES(1);
CREATE TABLE usage_financial_rollup_events (
 id BIGSERIAL PRIMARY KEY,
 source TEXT NOT NULL CHECK(source IN ('log','receipt','day')),
 old_identity JSONB, new_identity JSONB,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX usage_settlement_rollup_time_geili ON usage_settlement_receipts((COALESCE(completed_at,settled_at))) WHERE state='settled';

CREATE FUNCTION enqueue_usage_financial_rollup_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE old_value JSONB; new_value JSONB; kind TEXT;
BEGIN
 kind := CASE WHEN TG_TABLE_NAME='usage_settlement_receipts' THEN 'receipt' ELSE 'log' END;
 IF kind='receipt' THEN
  -- Retry leases and error strings cannot change the financial projection.
  IF TG_OP='UPDATE' AND ROW(OLD.user_id,OLD.api_key_id,OLD.usage_request_id,OLD.usage_log_id,OLD.state,OLD.charged_amount,OLD.detail,OLD.command->>'TerminalFailure',OLD.account_id,OLD.group_id,OLD.subscription_id,OLD.billing_type,OLD.completed_at,OLD.settled_at,OLD.record_source,OLD.record_completeness,OLD.delivered_at)
   IS NOT DISTINCT FROM ROW(NEW.user_id,NEW.api_key_id,NEW.usage_request_id,NEW.usage_log_id,NEW.state,NEW.charged_amount,NEW.detail,NEW.command->>'TerminalFailure',NEW.account_id,NEW.group_id,NEW.subscription_id,NEW.billing_type,NEW.completed_at,NEW.settled_at,NEW.record_source,NEW.record_completeness,NEW.delivered_at) THEN RETURN NEW; END IF;
  IF TG_OP<>'INSERT' THEN old_value := jsonb_build_object('id',OLD.id,'user',OLD.user_id,'key',OLD.api_key_id,'request',OLD.usage_request_id,'log',OLD.usage_log_id,'at',COALESCE(OLD.completed_at,OLD.settled_at,OLD.created_at)); END IF;
  IF TG_OP<>'DELETE' THEN new_value := jsonb_build_object('id',NEW.id,'user',NEW.user_id,'key',NEW.api_key_id,'request',NEW.usage_request_id,'log',NEW.usage_log_id,'at',COALESCE(NEW.completed_at,NEW.settled_at,NEW.created_at)); END IF;
 ELSE
  IF TG_OP<>'INSERT' THEN old_value := jsonb_build_object('id',OLD.id,'user',OLD.user_id,'key',OLD.api_key_id,'request',OLD.request_id,'at',OLD.created_at); END IF;
  IF TG_OP<>'DELETE' THEN new_value := jsonb_build_object('id',NEW.id,'user',NEW.user_id,'key',NEW.api_key_id,'request',NEW.request_id,'at',NEW.created_at); END IF;
 END IF;
 INSERT INTO usage_financial_rollup_events(source,old_identity,new_identity) VALUES(kind,old_value,new_value);
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER usage_logs_financial_rollup_event AFTER INSERT OR UPDATE OR DELETE ON usage_logs FOR EACH ROW EXECUTE FUNCTION enqueue_usage_financial_rollup_event();
CREATE TRIGGER usage_receipts_financial_rollup_event AFTER INSERT OR UPDATE OR DELETE ON usage_settlement_receipts FOR EACH ROW EXECUTE FUNCTION enqueue_usage_financial_rollup_event();

-- Resolve dependencies in the READER/CONSUMER snapshot, not a trigger's earlier
-- snapshot: concurrent receipt/log transactions may not see one another yet.
-- Typical event: two old/new own dates plus their single linked counterpart
-- dates. ROWS is a planner estimate, never a result limit; default 1000 causes
-- disproportionate JIT planning even for an empty recently drained queue.
CREATE FUNCTION usage_financial_event_days(kind TEXT,old_value JSONB,new_value JSONB)
RETURNS TABLE(bucket_date DATE) LANGUAGE SQL STABLE ROWS 4 AS $$
 WITH identities AS MATERIALIZED (
  SELECT v FROM (VALUES(old_value),(new_value)) AS facts(v) WHERE v IS NOT NULL
 ), affected AS (
  SELECT (v->>'at')::timestamptz AS at FROM identities
  UNION ALL
  SELECT COALESCE(r.completed_at,r.settled_at) FROM identities i
  JOIN usage_settlement_receipts r ON kind='log' AND r.state='settled'
   AND r.user_id=(i.v->>'user')::bigint AND r.api_key_id=(i.v->>'key')::bigint
   AND ((r.usage_request_id=i.v->>'request' AND (r.usage_log_id IS NULL OR r.usage_log_id=(i.v->>'id')::bigint))
    OR (r.usage_request_id IS NULL AND r.usage_log_id=(i.v->>'id')::bigint))
  UNION ALL
  SELECT u.created_at FROM identities i JOIN usage_logs u ON kind='receipt'
   AND u.user_id=(i.v->>'user')::bigint AND u.api_key_id=(i.v->>'key')::bigint
   AND (((i.v->>'log')::bigint=u.id AND (i.v->>'request' IS NULL OR i.v->>'request'=u.request_id))
    OR (i.v->>'log' IS NULL AND i.v->>'request' IS NOT NULL AND i.v->>'request'=u.request_id))
 ) SELECT DISTINCT (at AT TIME ZONE 'Asia/Shanghai')::date FROM affected WHERE at IS NOT NULL
$$;
COMMENT ON TABLE usage_financial_rollup_events IS 'Exact source change identities only; resolve counterpart dates in the same RR snapshot as recomputation, acknowledge exact IDs after ALL dependent days are published.';
