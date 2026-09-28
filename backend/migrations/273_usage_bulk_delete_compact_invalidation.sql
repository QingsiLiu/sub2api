-- Geili: retention and admin cleanup delete usage_logs in 5k–10k row batches.
-- Per-row DELETE triggers turned each nightly retention run into ~34k queued
-- financial events plus as many group invalidations. Bulk deleters now set the
-- transaction-local geili.usage_bulk_delete and enqueue one compact event per
-- affected day (plus exact log events for logs with settled receipts) in the
-- same statement. Only usage_logs DELETE is skipped; every other write keeps
-- the per-row events of 260/271.

CREATE OR REPLACE FUNCTION enqueue_usage_financial_rollup_event() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE old_value JSONB; new_value JSONB; kind TEXT;
BEGIN
 kind := CASE WHEN TG_TABLE_NAME='usage_settlement_receipts' THEN 'receipt' ELSE 'log' END;
 IF kind='log' AND TG_OP='DELETE' AND current_setting('geili.usage_bulk_delete', true)='on' THEN RETURN OLD; END IF;
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

CREATE OR REPLACE FUNCTION enqueue_group_usage_rollup_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('geili.usage_bulk_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    IF TG_OP <> 'INSERT' AND OLD.group_id IS NOT NULL THEN
        INSERT INTO usage_group_rollup_invalidations (affected_at, group_id)
        VALUES (OLD.created_at, OLD.group_id);
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.group_id IS NOT NULL THEN
        INSERT INTO usage_group_rollup_invalidations (affected_at, group_id)
        VALUES (NEW.created_at, NEW.group_id);
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
