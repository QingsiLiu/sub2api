-- Geili: incrementally materialized financial statistics rows for a bounded
-- recent window. 269's canonical view rebuilds every receipt/log/contract row
-- and decodes receipt JSON on each read; dashboard/usage reads over one busy
-- day took 10-16s and 7 days exceeded 2 minutes. Readers now aggregate these
-- exact copies and resolve still-queued source changes in their own snapshot.
-- No persisted financial value, legacy migration or canonical view changes.
SET LOCAL lock_timeout='5s';

-- One statistics row, its stable source identity and its rollup bucket date.
-- Receipt rows are keyed by receipt ID; legacy rows (possibly several contract
-- rows for one log) by usage log ID.
CREATE VIEW usage_financial_fact_source AS
SELECT
 (CASE WHEN financial_time_source='receipt' THEN 1 ELSE 2 END)::smallint AS fact_kind,
 CASE WHEN financial_time_source='receipt' THEN financial_receipt_id ELSE id END AS source_id,
 id, user_id, api_key_id, account_id, group_id, subscription_id,
 model, requested_model, upstream_model, upstream_model_mismatch,
 request_type, stream, openai_ws_mode, native_compaction_v2, billing_type, billing_mode, image_count,
 input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
 total_cost, actual_cost, account_stats_cost, account_rate_multiplier, duration_ms,
 inbound_endpoint, upstream_endpoint,
 accounting_date, created_at, admitted_at, completed_at, settled_at, financial_time_source, record_completeness, detail_pending,
 ((CASE WHEN financial_time_source='receipt' THEN COALESCE(completed_at,settled_at) ELSE created_at END) AT TIME ZONE 'Asia/Shanghai')::date AS bucket_date
FROM usage_financial_statistics;

-- Holds every source row whose GREATEST(accounting_date,bucket_date) is on or
-- after coverage_start. Only the financial rollup consumer writes it.
CREATE TABLE usage_financial_facts AS SELECT * FROM usage_financial_fact_source WITH NO DATA;
ALTER TABLE usage_financial_facts ALTER COLUMN fact_kind SET NOT NULL, ALTER COLUMN source_id SET NOT NULL;
ALTER TABLE usage_financial_facts SET (autovacuum_vacuum_scale_factor=0.02, autovacuum_analyze_scale_factor=0.02);
CREATE INDEX usage_financial_facts_source_geili ON usage_financial_facts(fact_kind, source_id);
CREATE INDEX usage_financial_facts_accounting_geili ON usage_financial_facts(accounting_date);
CREATE INDEX usage_financial_facts_user_geili ON usage_financial_facts(user_id, accounting_date);
CREATE INDEX usage_financial_facts_bucket_geili ON usage_financial_facts(bucket_date);

CREATE TABLE usage_financial_fact_state (
 id SMALLINT PRIMARY KEY CHECK(id=1), coverage_start DATE
);
INSERT INTO usage_financial_fact_state(id) VALUES(1);

-- Every fact identity a queued source event can change, resolved in the
-- consumer/reader snapshot. Deliberately a superset: receipt state and user
-- matching are re-evaluated by the canonical view on recomputation.
CREATE FUNCTION usage_financial_fact_keys(kind TEXT,old_value JSONB,new_value JSONB)
RETURNS TABLE(fact_kind SMALLINT, source_id BIGINT) LANGUAGE SQL STABLE ROWS 4 AS $$
 WITH identities AS MATERIALIZED (
  SELECT v FROM (VALUES(old_value),(new_value)) AS facts(v) WHERE v IS NOT NULL AND kind IN ('receipt','log')
 )
 SELECT 1::smallint,(v->>'id')::bigint FROM identities WHERE kind='receipt'
 UNION
 SELECT 2::smallint,(v->>'id')::bigint FROM identities WHERE kind='log'
 UNION
 SELECT 2::smallint,u.id FROM identities i JOIN usage_logs u ON kind='receipt' AND u.id=(i.v->>'log')::bigint
 UNION
 SELECT 2::smallint,u.id FROM identities i JOIN usage_logs u ON kind='receipt' AND u.request_id=i.v->>'request' AND u.api_key_id=(i.v->>'key')::bigint
 UNION
 SELECT 1::smallint,r.id FROM identities i JOIN usage_settlement_receipts r ON kind='log' AND r.usage_log_id=(i.v->>'id')::bigint
 UNION
 SELECT 1::smallint,r.id FROM identities i JOIN usage_settlement_receipts r ON kind='log' AND r.usage_request_id=i.v->>'request' AND r.api_key_id=(i.v->>'key')::bigint
$$;

-- Same trigger as 265; accounting_date and admitted_at are projected by the
-- statistics view, so changing only them must invalidate materialized rows too.
CREATE OR REPLACE FUNCTION enqueue_usage_financial_rollup_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE old_value JSONB; new_value JSONB; kind TEXT;
BEGIN
 kind := CASE WHEN TG_TABLE_NAME='usage_settlement_receipts' THEN 'receipt' ELSE 'log' END;
 IF kind='receipt' THEN
  -- Retry leases and error strings cannot change the financial projection.
  IF TG_OP='UPDATE' AND ROW(OLD.user_id,OLD.api_key_id,OLD.usage_request_id,OLD.usage_log_id,OLD.state,OLD.charged_amount,OLD.detail,OLD.command->>'TerminalFailure',OLD.account_id,OLD.group_id,OLD.subscription_id,OLD.billing_type,OLD.completed_at,OLD.settled_at,OLD.record_source,OLD.record_completeness,OLD.delivered_at,OLD.accounting_date,OLD.admitted_at)
   IS NOT DISTINCT FROM ROW(NEW.user_id,NEW.api_key_id,NEW.usage_request_id,NEW.usage_log_id,NEW.state,NEW.charged_amount,NEW.detail,NEW.command->>'TerminalFailure',NEW.account_id,NEW.group_id,NEW.subscription_id,NEW.billing_type,NEW.completed_at,NEW.settled_at,NEW.record_source,NEW.record_completeness,NEW.delivered_at,NEW.accounting_date,NEW.admitted_at) THEN RETURN NEW; END IF;
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

-- Per completion-day receipt minus linked-log cost by group, only for days
-- before the rollup cursor. Rewritten with the financial daily rollup of the
-- same day and transaction; a day without a marker is read from source rows.
CREATE TABLE usage_financial_group_daily_deltas (
 bucket_date DATE NOT NULL, group_id BIGINT NOT NULL, amount NUMERIC NOT NULL,
 PRIMARY KEY(bucket_date, group_id)
);
CREATE TABLE usage_financial_group_delta_days (bucket_date DATE PRIMARY KEY);

-- Legacy subscription rows also read request status/times and contract dates.
-- Those changes enqueue the matching usage log so facts and rollups recompute it.
CREATE FUNCTION enqueue_usage_financial_subscription_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='subscription_requests' THEN
  IF TG_OP='UPDATE' AND ROW(OLD.subscription_id,OLD.api_key_id,OLD.billing_request_id,OLD.status,OLD.settled_at,OLD.admitted_at,OLD.request_key)
   IS NOT DISTINCT FROM ROW(NEW.subscription_id,NEW.api_key_id,NEW.billing_request_id,NEW.status,NEW.settled_at,NEW.admitted_at,NEW.request_key) THEN RETURN NEW; END IF;
  INSERT INTO usage_financial_rollup_events(source,new_identity)
  SELECT DISTINCT 'log',jsonb_build_object('id',u.id,'user',u.user_id,'key',u.api_key_id,'request',u.request_id,'at',u.created_at)
  FROM (VALUES(OLD.subscription_id,OLD.api_key_id,OLD.billing_request_id),(NEW.subscription_id,NEW.api_key_id,NEW.billing_request_id)) x(s,k,b)
  JOIN usage_logs u ON x.b<>'' AND u.request_id=x.b AND u.api_key_id=x.k AND u.subscription_id=x.s AND u.billing_type=1;
 ELSE
  IF TG_OP='UPDATE' AND ROW(OLD.request_key,OLD.usage_date) IS NOT DISTINCT FROM ROW(NEW.request_key,NEW.usage_date) THEN RETURN NEW; END IF;
  INSERT INTO usage_financial_rollup_events(source,new_identity)
  SELECT DISTINCT 'log',jsonb_build_object('id',u.id,'user',u.user_id,'key',u.api_key_id,'request',u.request_id,'at',u.created_at)
  FROM (VALUES(OLD.request_key),(NEW.request_key)) x(r)
  JOIN subscription_requests sr ON sr.request_key=x.r
  JOIN usage_logs u ON sr.billing_request_id<>'' AND u.request_id=sr.billing_request_id AND u.api_key_id=sr.api_key_id AND u.subscription_id=sr.subscription_id AND u.billing_type=1;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER subscription_requests_financial_rollup_event AFTER INSERT OR UPDATE OR DELETE ON subscription_requests FOR EACH ROW EXECUTE FUNCTION enqueue_usage_financial_subscription_event();
CREATE TRIGGER subscription_contracts_financial_rollup_event AFTER INSERT OR UPDATE OR DELETE ON subscription_request_contracts FOR EACH ROW EXECUTE FUNCTION enqueue_usage_financial_subscription_event();
