-- Independent observation only. No billing/receipt/usage rows are modified.
SET LOCAL lock_timeout = '5s';
CREATE TABLE gateway_response_audits (
 id BIGSERIAL PRIMARY KEY,
 audit_request_id UUID NOT NULL,
 turn INTEGER NOT NULL CHECK (turn >= 0),
 user_id BIGINT NOT NULL,
 api_key_id BIGINT NOT NULL,
 account_id BIGINT NOT NULL DEFAULT 0,
 model VARCHAR(100) NOT NULL DEFAULT '',
 endpoint VARCHAR(128) NOT NULL,
 status VARCHAR(20) NOT NULL CHECK (status IN ('success','partial_failure','empty','failed','unknown')),
 request_id VARCHAR(64) NOT NULL DEFAULT '',
 client_request_id VARCHAR(64) NOT NULL DEFAULT '',
 usage_request_id VARCHAR(128) NOT NULL DEFAULT '',
 started_at TIMESTAMPTZ NOT NULL,
 finished_at TIMESTAMPTZ NOT NULL,
 evidence JSONB NOT NULL,
 UNIQUE (audit_request_id, turn)
);
CREATE INDEX gateway_response_audits_time_geili ON gateway_response_audits (finished_at DESC,id DESC);
CREATE INDEX gateway_response_audits_user_time_geili ON gateway_response_audits (user_id,finished_at DESC);
CREATE INDEX gateway_response_audits_model_time_geili ON gateway_response_audits (model,finished_at DESC);
CREATE INDEX gateway_response_audits_usage_geili ON gateway_response_audits (api_key_id,usage_request_id) WHERE usage_request_id <> '';

CREATE INDEX gateway_response_audits_started_geili ON gateway_response_audits (started_at);
