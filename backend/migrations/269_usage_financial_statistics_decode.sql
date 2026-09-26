-- Geili: statistics decode each receipt JSON once per row. Scalar casts,
-- presence/null behavior and receipt amounts match the canonical projection.
-- Dedicated views leave the ID-only pagination path free of function scans.
-- No persisted financial values or legacy migration checksums change.
SET LOCAL lock_timeout='5s';
-- Geili: keep the exact canonical financial projection and append its physical
-- receipt identity for late materialization. Page/sort only scalar identities,
-- then decode wide JSON details for the requested page, never every day's row.
-- Split the disjoint receipt joins so delivered details use the log primary key.
-- Existing migrations/checksums and all persisted financial values stay intact.
-- Receipt-backed financial reporting, without writing fabricated usage rows.
-- Separate branches expose ordinary indexed timestamps and contract dates to
-- the planner; callers add branch-specific timestamp bounds before aggregation.
CREATE OR REPLACE VIEW usage_financial_statistics AS
SELECT
 COALESCE(u.id,-r.id) AS id,
 r.user_id AS user_id,
 r.api_key_id AS api_key_id,
 COALESCE(r.account_id,u.account_id) AS account_id,
 COALESCE(r.usage_request_id,r.request_id)::character varying(64) AS request_id,
 CASE WHEN d.model IS NOT NULL THEN d.model WHEN r.detail ? 'model' THEN NULL ELSE u.model END AS model,
 CASE WHEN d.requested_model IS NOT NULL THEN d.requested_model WHEN r.detail ? 'requested_model' THEN NULL ELSE u.requested_model END AS requested_model,
 CASE WHEN d.upstream_model IS NOT NULL THEN d.upstream_model WHEN r.detail ? 'upstream_model' THEN NULL ELSE u.upstream_model END AS upstream_model,
 CASE WHEN d.upstream_response_model IS NOT NULL THEN d.upstream_response_model WHEN r.detail ? 'upstream_response_model' THEN NULL ELSE u.upstream_response_model END AS upstream_response_model,
 CASE WHEN d.upstream_model_mismatch IS NOT NULL THEN d.upstream_model_mismatch WHEN r.detail ? 'upstream_model_mismatch' THEN NULL ELSE u.upstream_model_mismatch END AS upstream_model_mismatch,
 COALESCE(r.group_id,u.group_id) AS group_id,
 r.subscription_id AS subscription_id,
 CASE WHEN d.input_tokens IS NOT NULL THEN d.input_tokens WHEN r.detail ? 'input_tokens' THEN NULL ELSE u.input_tokens END AS input_tokens,
 CASE WHEN d.output_tokens IS NOT NULL THEN d.output_tokens WHEN r.detail ? 'output_tokens' THEN NULL ELSE u.output_tokens END AS output_tokens,
 CASE WHEN d.cache_creation_tokens IS NOT NULL THEN d.cache_creation_tokens WHEN r.detail ? 'cache_creation_tokens' THEN NULL ELSE u.cache_creation_tokens END AS cache_creation_tokens,
 CASE WHEN d.cache_read_tokens IS NOT NULL THEN d.cache_read_tokens WHEN r.detail ? 'cache_read_tokens' THEN NULL ELSE u.cache_read_tokens END AS cache_read_tokens,
 CASE WHEN d.cache_creation_5m_tokens IS NOT NULL THEN d.cache_creation_5m_tokens WHEN r.detail ? 'cache_creation_5m_tokens' THEN NULL ELSE u.cache_creation_5m_tokens END AS cache_creation_5m_tokens,
 CASE WHEN d.cache_creation_1h_tokens IS NOT NULL THEN d.cache_creation_1h_tokens WHEN r.detail ? 'cache_creation_1h_tokens' THEN NULL ELSE u.cache_creation_1h_tokens END AS cache_creation_1h_tokens,
 CASE WHEN d.image_output_tokens IS NOT NULL THEN d.image_output_tokens WHEN r.detail ? 'image_output_tokens' THEN NULL ELSE u.image_output_tokens END AS image_output_tokens,
 CASE WHEN d.image_output_cost IS NOT NULL THEN d.image_output_cost WHEN r.detail ? 'image_output_cost' THEN NULL ELSE u.image_output_cost END AS image_output_cost,
 CASE WHEN d.image_input_tokens IS NOT NULL THEN d.image_input_tokens WHEN r.detail ? 'image_input_tokens' THEN NULL ELSE u.image_input_tokens END AS image_input_tokens,
 CASE WHEN d.image_input_cost IS NOT NULL THEN d.image_input_cost WHEN r.detail ? 'image_input_cost' THEN NULL ELSE u.image_input_cost END AS image_input_cost,
 CASE WHEN d.input_cost IS NOT NULL THEN d.input_cost WHEN r.detail ? 'input_cost' THEN NULL ELSE u.input_cost END AS input_cost,
 CASE WHEN d.output_cost IS NOT NULL THEN d.output_cost WHEN r.detail ? 'output_cost' THEN NULL ELSE u.output_cost END AS output_cost,
 CASE WHEN d.cache_creation_cost IS NOT NULL THEN d.cache_creation_cost WHEN r.detail ? 'cache_creation_cost' THEN NULL ELSE u.cache_creation_cost END AS cache_creation_cost,
 CASE WHEN d.cache_read_cost IS NOT NULL THEN d.cache_read_cost WHEN r.detail ? 'cache_read_cost' THEN NULL ELSE u.cache_read_cost END AS cache_read_cost,
 CASE WHEN d.total_cost IS NOT NULL THEN d.total_cost WHEN r.detail ? 'total_cost' THEN NULL ELSE u.total_cost END AS total_cost,
 r.charged_amount AS actual_cost,
 CASE WHEN d.rate_multiplier IS NOT NULL THEN d.rate_multiplier WHEN r.detail ? 'rate_multiplier' THEN NULL ELSE u.rate_multiplier END AS rate_multiplier,
 CASE WHEN d.account_rate_multiplier IS NOT NULL THEN d.account_rate_multiplier WHEN r.detail ? 'account_rate_multiplier' THEN NULL ELSE u.account_rate_multiplier END AS account_rate_multiplier,
 r.billing_type AS billing_type,
 CASE WHEN d.request_type IS NOT NULL THEN d.request_type WHEN r.detail ? 'request_type' THEN NULL ELSE u.request_type END AS request_type,
 CASE WHEN d.stream IS NOT NULL THEN d.stream WHEN r.detail ? 'stream' THEN NULL ELSE u.stream END AS stream,
 CASE WHEN d.openai_ws_mode IS NOT NULL THEN d.openai_ws_mode WHEN r.detail ? 'openai_ws_mode' THEN NULL ELSE u.openai_ws_mode END AS openai_ws_mode,
 CASE WHEN d.duration_ms IS NOT NULL THEN d.duration_ms WHEN r.detail ? 'duration_ms' THEN NULL ELSE u.duration_ms END AS duration_ms,
 CASE WHEN d.first_token_ms IS NOT NULL THEN d.first_token_ms WHEN r.detail ? 'first_token_ms' THEN NULL ELSE u.first_token_ms END AS first_token_ms,
 CASE WHEN d.user_agent IS NOT NULL THEN d.user_agent WHEN r.detail ? 'user_agent' THEN NULL ELSE u.user_agent END AS user_agent,
 CASE WHEN d.ip_address IS NOT NULL THEN d.ip_address WHEN r.detail ? 'ip_address' THEN NULL ELSE u.ip_address END AS ip_address,
 CASE WHEN d.image_count IS NOT NULL THEN d.image_count WHEN r.detail ? 'image_count' THEN NULL ELSE u.image_count END AS image_count,
 CASE WHEN d.image_size IS NOT NULL THEN d.image_size WHEN r.detail ? 'image_size' THEN NULL ELSE u.image_size END AS image_size,
 CASE WHEN d.image_input_size IS NOT NULL THEN d.image_input_size WHEN r.detail ? 'image_input_size' THEN NULL ELSE u.image_input_size END AS image_input_size,
 CASE WHEN d.image_output_size IS NOT NULL THEN d.image_output_size WHEN r.detail ? 'image_output_size' THEN NULL ELSE u.image_output_size END AS image_output_size,
 CASE WHEN d.image_size_source IS NOT NULL THEN d.image_size_source WHEN r.detail ? 'image_size_source' THEN NULL ELSE u.image_size_source END AS image_size_source,
 CASE WHEN r.detail ? 'image_size_breakdown' THEN r.detail->'image_size_breakdown' ELSE u.image_size_breakdown END AS image_size_breakdown,
 CASE WHEN d.video_count IS NOT NULL THEN d.video_count WHEN r.detail ? 'video_count' THEN NULL ELSE u.video_count END AS video_count,
 CASE WHEN d.video_resolution IS NOT NULL THEN d.video_resolution WHEN r.detail ? 'video_resolution' THEN NULL ELSE u.video_resolution END AS video_resolution,
 CASE WHEN d.video_duration_seconds IS NOT NULL THEN d.video_duration_seconds WHEN r.detail ? 'video_duration_seconds' THEN NULL ELSE u.video_duration_seconds END AS video_duration_seconds,
 CASE WHEN d.service_tier IS NOT NULL THEN d.service_tier WHEN r.detail ? 'service_tier' THEN NULL ELSE u.service_tier END AS service_tier,
 CASE WHEN d.reasoning_effort IS NOT NULL THEN d.reasoning_effort WHEN r.detail ? 'reasoning_effort' THEN NULL ELSE u.reasoning_effort END AS reasoning_effort,
 CASE WHEN d.requested_reasoning_effort IS NOT NULL THEN d.requested_reasoning_effort WHEN r.detail ? 'requested_reasoning_effort' THEN NULL ELSE u.requested_reasoning_effort END AS requested_reasoning_effort,
 CASE WHEN d.inbound_endpoint IS NOT NULL THEN d.inbound_endpoint WHEN r.detail ? 'inbound_endpoint' THEN NULL ELSE u.inbound_endpoint END AS inbound_endpoint,
 CASE WHEN d.upstream_endpoint IS NOT NULL THEN d.upstream_endpoint WHEN r.detail ? 'upstream_endpoint' THEN NULL ELSE u.upstream_endpoint END AS upstream_endpoint,
 CASE WHEN d.cache_ttl_overridden IS NOT NULL THEN d.cache_ttl_overridden WHEN r.detail ? 'cache_ttl_overridden' THEN NULL ELSE u.cache_ttl_overridden END AS cache_ttl_overridden,
 CASE WHEN d.long_context_billing_applied IS NOT NULL THEN d.long_context_billing_applied WHEN r.detail ? 'long_context_billing_applied' THEN NULL ELSE u.long_context_billing_applied END AS long_context_billing_applied,
 CASE WHEN d.channel_id IS NOT NULL THEN d.channel_id WHEN r.detail ? 'channel_id' THEN NULL ELSE u.channel_id END AS channel_id,
 CASE WHEN d.model_mapping_chain IS NOT NULL THEN d.model_mapping_chain WHEN r.detail ? 'model_mapping_chain' THEN NULL ELSE u.model_mapping_chain END AS model_mapping_chain,
 CASE WHEN d.billing_tier IS NOT NULL THEN d.billing_tier WHEN r.detail ? 'billing_tier' THEN NULL ELSE u.billing_tier END AS billing_tier,
 CASE WHEN d.billing_mode IS NOT NULL THEN d.billing_mode WHEN r.detail ? 'billing_mode' THEN NULL ELSE u.billing_mode END AS billing_mode,
 CASE WHEN d.account_stats_cost IS NOT NULL THEN d.account_stats_cost WHEN r.detail ? 'account_stats_cost' THEN NULL ELSE u.account_stats_cost END AS account_stats_cost,
 CASE WHEN r.detail ? 'route_billing_snapshot' THEN r.detail->'route_billing_snapshot' ELSE u.route_billing_snapshot END AS route_billing_snapshot,
 CASE WHEN d.upstream_request_id IS NOT NULL THEN d.upstream_request_id WHEN r.detail ? 'upstream_request_id' THEN NULL ELSE u.upstream_request_id END AS upstream_request_id,
 CASE WHEN d.session_id IS NOT NULL THEN d.session_id WHEN r.detail ? 'session_id' THEN NULL ELSE u.session_id END AS session_id,
 CASE WHEN d.native_compaction_v2 IS NOT NULL THEN d.native_compaction_v2 WHEN r.detail ? 'native_compaction_v2' THEN NULL ELSE u.native_compaction_v2 END AS native_compaction_v2,
 COALESCE(u.created_at,(r.detail->>'created_at')::timestamptz,r.completed_at) AS created_at,
 r.accounting_date,r.settled_at,r.completed_at,r.record_source,r.record_completeness,
 (r.record_source='live' AND r.record_completeness='complete' AND r.delivered_at IS NULL) AS detail_pending, r.admitted_at, 'receipt'::text AS financial_time_source, r.id AS financial_receipt_id
FROM usage_settlement_receipts r
LEFT JOIN LATERAL (
 SELECT linked.* FROM usage_logs linked
 WHERE r.usage_log_id=linked.id AND r.user_id=linked.user_id AND r.api_key_id=linked.api_key_id
 AND (r.usage_request_id IS NULL OR r.usage_request_id=linked.request_id)
 UNION ALL
 SELECT linked.* FROM usage_logs linked
 WHERE r.usage_log_id IS NULL AND r.usage_request_id=linked.request_id
 AND r.user_id=linked.user_id AND r.api_key_id=linked.api_key_id
 LIMIT 1
) u ON TRUE
CROSS JOIN LATERAL jsonb_to_record(r.detail) AS d(model character varying(100), requested_model character varying(100), upstream_model character varying(100), upstream_response_model character varying(200), upstream_model_mismatch boolean, input_tokens integer, output_tokens integer, cache_creation_tokens integer, cache_read_tokens integer, cache_creation_5m_tokens integer, cache_creation_1h_tokens integer, image_output_tokens integer, image_output_cost numeric(20,10), image_input_tokens integer, image_input_cost numeric(20,10), input_cost numeric(20,10), output_cost numeric(20,10), cache_creation_cost numeric(20,10), cache_read_cost numeric(20,10), total_cost numeric(20,10), rate_multiplier numeric(10,4), account_rate_multiplier numeric(10,4), request_type smallint, stream boolean, openai_ws_mode boolean, duration_ms integer, first_token_ms integer, user_agent character varying(512), ip_address character varying(45), image_count integer, image_size character varying(10), image_input_size character varying(32), image_output_size character varying(32), image_size_source character varying(16), image_size_breakdown jsonb, video_count integer, video_resolution character varying(10), video_duration_seconds integer, service_tier character varying(16), reasoning_effort character varying(20), requested_reasoning_effort character varying(20), inbound_endpoint character varying(128), upstream_endpoint character varying(128), cache_ttl_overridden boolean, long_context_billing_applied boolean, channel_id bigint, model_mapping_chain character varying(500), billing_tier character varying(50), billing_mode character varying(20), account_stats_cost numeric(20,10), route_billing_snapshot jsonb, upstream_request_id character varying(128), session_id character varying(255), native_compaction_v2 boolean)
WHERE r.state='settled' AND COALESCE(r.command->>'TerminalFailure','false')<>'true'
UNION ALL
SELECT u.id, u.user_id, u.api_key_id, u.account_id, u.request_id, u.model, u.requested_model, u.upstream_model, u.upstream_response_model, u.upstream_model_mismatch, u.group_id, u.subscription_id, u.input_tokens, u.output_tokens, u.cache_creation_tokens, u.cache_read_tokens, u.cache_creation_5m_tokens, u.cache_creation_1h_tokens, u.image_output_tokens, u.image_output_cost, u.image_input_tokens, u.image_input_cost, u.input_cost, u.output_cost, u.cache_creation_cost, u.cache_read_cost, u.total_cost, u.actual_cost, u.rate_multiplier, u.account_rate_multiplier, u.billing_type, u.request_type, u.stream, u.openai_ws_mode, u.duration_ms, u.first_token_ms, u.user_agent, u.ip_address, u.image_count, u.image_size, u.image_input_size, u.image_output_size, u.image_size_source, u.image_size_breakdown, u.video_count, u.video_resolution, u.video_duration_seconds, u.service_tier, u.reasoning_effort, u.requested_reasoning_effort, u.inbound_endpoint, u.upstream_endpoint, u.cache_ttl_overridden, u.long_context_billing_applied, u.channel_id, u.model_mapping_chain, u.billing_tier, u.billing_mode, u.account_stats_cost, u.route_billing_snapshot, u.upstream_request_id, u.session_id, u.native_compaction_v2, u.created_at,
 (u.created_at AT TIME ZONE 'Asia/Shanghai')::date AS accounting_date, u.created_at AS settled_at,u.created_at AS completed_at,
 'legacy_log'::text AS record_source,'complete'::text AS record_completeness,FALSE AS detail_pending, NULL::timestamptz AS admitted_at, 'log'::text AS financial_time_source, NULL::bigint AS financial_receipt_id
FROM usage_logs u
WHERE (u.billing_type<>1 OR u.subscription_id IS NULL) AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id AND r.usage_request_id=u.request_id
 AND (r.usage_log_id IS NULL OR r.usage_log_id=u.id))
AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.usage_request_id IS NULL AND r.usage_log_id=u.id AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id)
UNION ALL
SELECT u.id, u.user_id, u.api_key_id, u.account_id, u.request_id, u.model, u.requested_model, u.upstream_model, u.upstream_response_model, u.upstream_model_mismatch, u.group_id, u.subscription_id, u.input_tokens, u.output_tokens, u.cache_creation_tokens, u.cache_read_tokens, u.cache_creation_5m_tokens, u.cache_creation_1h_tokens, u.image_output_tokens, u.image_output_cost, u.image_input_tokens, u.image_input_cost, u.input_cost, u.output_cost, u.cache_creation_cost, u.cache_read_cost, u.total_cost, u.actual_cost, u.rate_multiplier, u.account_rate_multiplier, u.billing_type, u.request_type, u.stream, u.openai_ws_mode, u.duration_ms, u.first_token_ms, u.user_agent, u.ip_address, u.image_count, u.image_size, u.image_input_size, u.image_output_size, u.image_size_source, u.image_size_breakdown, u.video_count, u.video_resolution, u.video_duration_seconds, u.service_tier, u.reasoning_effort, u.requested_reasoning_effort, u.inbound_endpoint, u.upstream_endpoint, u.cache_ttl_overridden, u.long_context_billing_applied, u.channel_id, u.model_mapping_chain, u.billing_tier, u.billing_mode, u.account_stats_cost, u.route_billing_snapshot, u.upstream_request_id, u.session_id, u.native_compaction_v2, u.created_at,
 c.usage_date AS accounting_date, COALESCE(sr.settled_at,u.created_at) AS settled_at,u.created_at AS completed_at,
 'legacy_log'::text AS record_source,'complete'::text AS record_completeness,FALSE AS detail_pending, sr.admitted_at AS admitted_at, 'contract'::text AS financial_time_source, NULL::bigint AS financial_receipt_id
FROM subscription_request_contracts c JOIN subscription_requests sr ON sr.request_key=c.request_key JOIN usage_logs u ON sr.subscription_id=u.subscription_id AND sr.api_key_id=u.api_key_id AND sr.billing_request_id=u.request_id AND sr.status='settled'
WHERE u.billing_type=1 AND u.subscription_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM subscription_requests prior WHERE prior.subscription_id=sr.subscription_id AND prior.api_key_id=sr.api_key_id AND prior.billing_request_id=sr.billing_request_id AND prior.status='settled' AND prior.id<sr.id) AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id AND r.usage_request_id=u.request_id
 AND (r.usage_log_id IS NULL OR r.usage_log_id=u.id))
AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.usage_request_id IS NULL AND r.usage_log_id=u.id AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id)
UNION ALL
SELECT u.id, u.user_id, u.api_key_id, u.account_id, u.request_id, u.model, u.requested_model, u.upstream_model, u.upstream_response_model, u.upstream_model_mismatch, u.group_id, u.subscription_id, u.input_tokens, u.output_tokens, u.cache_creation_tokens, u.cache_read_tokens, u.cache_creation_5m_tokens, u.cache_creation_1h_tokens, u.image_output_tokens, u.image_output_cost, u.image_input_tokens, u.image_input_cost, u.input_cost, u.output_cost, u.cache_creation_cost, u.cache_read_cost, u.total_cost, u.actual_cost, u.rate_multiplier, u.account_rate_multiplier, u.billing_type, u.request_type, u.stream, u.openai_ws_mode, u.duration_ms, u.first_token_ms, u.user_agent, u.ip_address, u.image_count, u.image_size, u.image_input_size, u.image_output_size, u.image_size_source, u.image_size_breakdown, u.video_count, u.video_resolution, u.video_duration_seconds, u.service_tier, u.reasoning_effort, u.requested_reasoning_effort, u.inbound_endpoint, u.upstream_endpoint, u.cache_ttl_overridden, u.long_context_billing_applied, u.channel_id, u.model_mapping_chain, u.billing_tier, u.billing_mode, u.account_stats_cost, u.route_billing_snapshot, u.upstream_request_id, u.session_id, u.native_compaction_v2, u.created_at,
 (sr.admitted_at AT TIME ZONE 'Asia/Shanghai')::date AS accounting_date, COALESCE(sr.settled_at,u.created_at) AS settled_at,u.created_at AS completed_at,
 'legacy_log'::text AS record_source,'complete'::text AS record_completeness,FALSE AS detail_pending, sr.admitted_at AS admitted_at, 'admitted'::text AS financial_time_source, NULL::bigint AS financial_receipt_id
FROM subscription_requests sr JOIN usage_logs u ON sr.subscription_id=u.subscription_id AND sr.api_key_id=u.api_key_id AND sr.billing_request_id=u.request_id AND sr.status='settled'
WHERE u.billing_type=1 AND u.subscription_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM subscription_request_contracts c WHERE c.request_key=sr.request_key) AND NOT EXISTS(SELECT 1 FROM subscription_requests prior WHERE prior.subscription_id=sr.subscription_id AND prior.api_key_id=sr.api_key_id AND prior.billing_request_id=sr.billing_request_id AND prior.status='settled' AND prior.id<sr.id) AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id AND r.usage_request_id=u.request_id
 AND (r.usage_log_id IS NULL OR r.usage_log_id=u.id))
AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.usage_request_id IS NULL AND r.usage_log_id=u.id AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id)
UNION ALL
SELECT u.id, u.user_id, u.api_key_id, u.account_id, u.request_id, u.model, u.requested_model, u.upstream_model, u.upstream_response_model, u.upstream_model_mismatch, u.group_id, u.subscription_id, u.input_tokens, u.output_tokens, u.cache_creation_tokens, u.cache_read_tokens, u.cache_creation_5m_tokens, u.cache_creation_1h_tokens, u.image_output_tokens, u.image_output_cost, u.image_input_tokens, u.image_input_cost, u.input_cost, u.output_cost, u.cache_creation_cost, u.cache_read_cost, u.total_cost, u.actual_cost, u.rate_multiplier, u.account_rate_multiplier, u.billing_type, u.request_type, u.stream, u.openai_ws_mode, u.duration_ms, u.first_token_ms, u.user_agent, u.ip_address, u.image_count, u.image_size, u.image_input_size, u.image_output_size, u.image_size_source, u.image_size_breakdown, u.video_count, u.video_resolution, u.video_duration_seconds, u.service_tier, u.reasoning_effort, u.requested_reasoning_effort, u.inbound_endpoint, u.upstream_endpoint, u.cache_ttl_overridden, u.long_context_billing_applied, u.channel_id, u.model_mapping_chain, u.billing_tier, u.billing_mode, u.account_stats_cost, u.route_billing_snapshot, u.upstream_request_id, u.session_id, u.native_compaction_v2, u.created_at,
 (u.created_at AT TIME ZONE 'Asia/Shanghai')::date AS accounting_date, u.created_at AS settled_at,u.created_at AS completed_at,
 'legacy_log'::text AS record_source,'complete'::text AS record_completeness,FALSE AS detail_pending, NULL::timestamptz AS admitted_at, 'log'::text AS financial_time_source, NULL::bigint AS financial_receipt_id
FROM usage_logs u
WHERE u.billing_type=1 AND u.subscription_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM subscription_requests sr WHERE sr.subscription_id=u.subscription_id AND sr.api_key_id=u.api_key_id AND sr.billing_request_id=u.request_id AND sr.status='settled') AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id AND r.usage_request_id=u.request_id
 AND (r.usage_log_id IS NULL OR r.usage_log_id=u.id))
AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.usage_request_id IS NULL AND r.usage_log_id=u.id AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id);

-- Unbounded totals avoid per-row historical subscription-date lookups.
CREATE OR REPLACE VIEW usage_financial_total_statistics AS
SELECT
 COALESCE(u.id,-r.id) AS id,
 r.user_id AS user_id,
 r.api_key_id AS api_key_id,
 COALESCE(r.account_id,u.account_id) AS account_id,
 COALESCE(r.usage_request_id,r.request_id)::character varying(64) AS request_id,
 CASE WHEN d.model IS NOT NULL THEN d.model WHEN r.detail ? 'model' THEN NULL ELSE u.model END AS model,
 CASE WHEN d.requested_model IS NOT NULL THEN d.requested_model WHEN r.detail ? 'requested_model' THEN NULL ELSE u.requested_model END AS requested_model,
 CASE WHEN d.upstream_model IS NOT NULL THEN d.upstream_model WHEN r.detail ? 'upstream_model' THEN NULL ELSE u.upstream_model END AS upstream_model,
 CASE WHEN d.upstream_response_model IS NOT NULL THEN d.upstream_response_model WHEN r.detail ? 'upstream_response_model' THEN NULL ELSE u.upstream_response_model END AS upstream_response_model,
 CASE WHEN d.upstream_model_mismatch IS NOT NULL THEN d.upstream_model_mismatch WHEN r.detail ? 'upstream_model_mismatch' THEN NULL ELSE u.upstream_model_mismatch END AS upstream_model_mismatch,
 COALESCE(r.group_id,u.group_id) AS group_id,
 r.subscription_id AS subscription_id,
 CASE WHEN d.input_tokens IS NOT NULL THEN d.input_tokens WHEN r.detail ? 'input_tokens' THEN NULL ELSE u.input_tokens END AS input_tokens,
 CASE WHEN d.output_tokens IS NOT NULL THEN d.output_tokens WHEN r.detail ? 'output_tokens' THEN NULL ELSE u.output_tokens END AS output_tokens,
 CASE WHEN d.cache_creation_tokens IS NOT NULL THEN d.cache_creation_tokens WHEN r.detail ? 'cache_creation_tokens' THEN NULL ELSE u.cache_creation_tokens END AS cache_creation_tokens,
 CASE WHEN d.cache_read_tokens IS NOT NULL THEN d.cache_read_tokens WHEN r.detail ? 'cache_read_tokens' THEN NULL ELSE u.cache_read_tokens END AS cache_read_tokens,
 CASE WHEN d.cache_creation_5m_tokens IS NOT NULL THEN d.cache_creation_5m_tokens WHEN r.detail ? 'cache_creation_5m_tokens' THEN NULL ELSE u.cache_creation_5m_tokens END AS cache_creation_5m_tokens,
 CASE WHEN d.cache_creation_1h_tokens IS NOT NULL THEN d.cache_creation_1h_tokens WHEN r.detail ? 'cache_creation_1h_tokens' THEN NULL ELSE u.cache_creation_1h_tokens END AS cache_creation_1h_tokens,
 CASE WHEN d.image_output_tokens IS NOT NULL THEN d.image_output_tokens WHEN r.detail ? 'image_output_tokens' THEN NULL ELSE u.image_output_tokens END AS image_output_tokens,
 CASE WHEN d.image_output_cost IS NOT NULL THEN d.image_output_cost WHEN r.detail ? 'image_output_cost' THEN NULL ELSE u.image_output_cost END AS image_output_cost,
 CASE WHEN d.image_input_tokens IS NOT NULL THEN d.image_input_tokens WHEN r.detail ? 'image_input_tokens' THEN NULL ELSE u.image_input_tokens END AS image_input_tokens,
 CASE WHEN d.image_input_cost IS NOT NULL THEN d.image_input_cost WHEN r.detail ? 'image_input_cost' THEN NULL ELSE u.image_input_cost END AS image_input_cost,
 CASE WHEN d.input_cost IS NOT NULL THEN d.input_cost WHEN r.detail ? 'input_cost' THEN NULL ELSE u.input_cost END AS input_cost,
 CASE WHEN d.output_cost IS NOT NULL THEN d.output_cost WHEN r.detail ? 'output_cost' THEN NULL ELSE u.output_cost END AS output_cost,
 CASE WHEN d.cache_creation_cost IS NOT NULL THEN d.cache_creation_cost WHEN r.detail ? 'cache_creation_cost' THEN NULL ELSE u.cache_creation_cost END AS cache_creation_cost,
 CASE WHEN d.cache_read_cost IS NOT NULL THEN d.cache_read_cost WHEN r.detail ? 'cache_read_cost' THEN NULL ELSE u.cache_read_cost END AS cache_read_cost,
 CASE WHEN d.total_cost IS NOT NULL THEN d.total_cost WHEN r.detail ? 'total_cost' THEN NULL ELSE u.total_cost END AS total_cost,
 r.charged_amount AS actual_cost,
 CASE WHEN d.rate_multiplier IS NOT NULL THEN d.rate_multiplier WHEN r.detail ? 'rate_multiplier' THEN NULL ELSE u.rate_multiplier END AS rate_multiplier,
 CASE WHEN d.account_rate_multiplier IS NOT NULL THEN d.account_rate_multiplier WHEN r.detail ? 'account_rate_multiplier' THEN NULL ELSE u.account_rate_multiplier END AS account_rate_multiplier,
 r.billing_type AS billing_type,
 CASE WHEN d.request_type IS NOT NULL THEN d.request_type WHEN r.detail ? 'request_type' THEN NULL ELSE u.request_type END AS request_type,
 CASE WHEN d.stream IS NOT NULL THEN d.stream WHEN r.detail ? 'stream' THEN NULL ELSE u.stream END AS stream,
 CASE WHEN d.openai_ws_mode IS NOT NULL THEN d.openai_ws_mode WHEN r.detail ? 'openai_ws_mode' THEN NULL ELSE u.openai_ws_mode END AS openai_ws_mode,
 CASE WHEN d.duration_ms IS NOT NULL THEN d.duration_ms WHEN r.detail ? 'duration_ms' THEN NULL ELSE u.duration_ms END AS duration_ms,
 CASE WHEN d.first_token_ms IS NOT NULL THEN d.first_token_ms WHEN r.detail ? 'first_token_ms' THEN NULL ELSE u.first_token_ms END AS first_token_ms,
 CASE WHEN d.user_agent IS NOT NULL THEN d.user_agent WHEN r.detail ? 'user_agent' THEN NULL ELSE u.user_agent END AS user_agent,
 CASE WHEN d.ip_address IS NOT NULL THEN d.ip_address WHEN r.detail ? 'ip_address' THEN NULL ELSE u.ip_address END AS ip_address,
 CASE WHEN d.image_count IS NOT NULL THEN d.image_count WHEN r.detail ? 'image_count' THEN NULL ELSE u.image_count END AS image_count,
 CASE WHEN d.image_size IS NOT NULL THEN d.image_size WHEN r.detail ? 'image_size' THEN NULL ELSE u.image_size END AS image_size,
 CASE WHEN d.image_input_size IS NOT NULL THEN d.image_input_size WHEN r.detail ? 'image_input_size' THEN NULL ELSE u.image_input_size END AS image_input_size,
 CASE WHEN d.image_output_size IS NOT NULL THEN d.image_output_size WHEN r.detail ? 'image_output_size' THEN NULL ELSE u.image_output_size END AS image_output_size,
 CASE WHEN d.image_size_source IS NOT NULL THEN d.image_size_source WHEN r.detail ? 'image_size_source' THEN NULL ELSE u.image_size_source END AS image_size_source,
 CASE WHEN r.detail ? 'image_size_breakdown' THEN r.detail->'image_size_breakdown' ELSE u.image_size_breakdown END AS image_size_breakdown,
 CASE WHEN d.video_count IS NOT NULL THEN d.video_count WHEN r.detail ? 'video_count' THEN NULL ELSE u.video_count END AS video_count,
 CASE WHEN d.video_resolution IS NOT NULL THEN d.video_resolution WHEN r.detail ? 'video_resolution' THEN NULL ELSE u.video_resolution END AS video_resolution,
 CASE WHEN d.video_duration_seconds IS NOT NULL THEN d.video_duration_seconds WHEN r.detail ? 'video_duration_seconds' THEN NULL ELSE u.video_duration_seconds END AS video_duration_seconds,
 CASE WHEN d.service_tier IS NOT NULL THEN d.service_tier WHEN r.detail ? 'service_tier' THEN NULL ELSE u.service_tier END AS service_tier,
 CASE WHEN d.reasoning_effort IS NOT NULL THEN d.reasoning_effort WHEN r.detail ? 'reasoning_effort' THEN NULL ELSE u.reasoning_effort END AS reasoning_effort,
 CASE WHEN d.requested_reasoning_effort IS NOT NULL THEN d.requested_reasoning_effort WHEN r.detail ? 'requested_reasoning_effort' THEN NULL ELSE u.requested_reasoning_effort END AS requested_reasoning_effort,
 CASE WHEN d.inbound_endpoint IS NOT NULL THEN d.inbound_endpoint WHEN r.detail ? 'inbound_endpoint' THEN NULL ELSE u.inbound_endpoint END AS inbound_endpoint,
 CASE WHEN d.upstream_endpoint IS NOT NULL THEN d.upstream_endpoint WHEN r.detail ? 'upstream_endpoint' THEN NULL ELSE u.upstream_endpoint END AS upstream_endpoint,
 CASE WHEN d.cache_ttl_overridden IS NOT NULL THEN d.cache_ttl_overridden WHEN r.detail ? 'cache_ttl_overridden' THEN NULL ELSE u.cache_ttl_overridden END AS cache_ttl_overridden,
 CASE WHEN d.long_context_billing_applied IS NOT NULL THEN d.long_context_billing_applied WHEN r.detail ? 'long_context_billing_applied' THEN NULL ELSE u.long_context_billing_applied END AS long_context_billing_applied,
 CASE WHEN d.channel_id IS NOT NULL THEN d.channel_id WHEN r.detail ? 'channel_id' THEN NULL ELSE u.channel_id END AS channel_id,
 CASE WHEN d.model_mapping_chain IS NOT NULL THEN d.model_mapping_chain WHEN r.detail ? 'model_mapping_chain' THEN NULL ELSE u.model_mapping_chain END AS model_mapping_chain,
 CASE WHEN d.billing_tier IS NOT NULL THEN d.billing_tier WHEN r.detail ? 'billing_tier' THEN NULL ELSE u.billing_tier END AS billing_tier,
 CASE WHEN d.billing_mode IS NOT NULL THEN d.billing_mode WHEN r.detail ? 'billing_mode' THEN NULL ELSE u.billing_mode END AS billing_mode,
 CASE WHEN d.account_stats_cost IS NOT NULL THEN d.account_stats_cost WHEN r.detail ? 'account_stats_cost' THEN NULL ELSE u.account_stats_cost END AS account_stats_cost,
 CASE WHEN r.detail ? 'route_billing_snapshot' THEN r.detail->'route_billing_snapshot' ELSE u.route_billing_snapshot END AS route_billing_snapshot,
 CASE WHEN d.upstream_request_id IS NOT NULL THEN d.upstream_request_id WHEN r.detail ? 'upstream_request_id' THEN NULL ELSE u.upstream_request_id END AS upstream_request_id,
 CASE WHEN d.session_id IS NOT NULL THEN d.session_id WHEN r.detail ? 'session_id' THEN NULL ELSE u.session_id END AS session_id,
 CASE WHEN d.native_compaction_v2 IS NOT NULL THEN d.native_compaction_v2 WHEN r.detail ? 'native_compaction_v2' THEN NULL ELSE u.native_compaction_v2 END AS native_compaction_v2,
 COALESCE(u.created_at,(r.detail->>'created_at')::timestamptz,r.completed_at) AS created_at,
 r.accounting_date,r.settled_at,r.completed_at,r.record_source,r.record_completeness,
 (r.record_source='live' AND r.record_completeness='complete' AND r.delivered_at IS NULL) AS detail_pending, r.admitted_at, 'receipt'::text AS financial_time_source, r.id AS financial_receipt_id
FROM usage_settlement_receipts r
LEFT JOIN LATERAL (
 SELECT linked.* FROM usage_logs linked
 WHERE r.usage_log_id=linked.id AND r.user_id=linked.user_id AND r.api_key_id=linked.api_key_id
 AND (r.usage_request_id IS NULL OR r.usage_request_id=linked.request_id)
 UNION ALL
 SELECT linked.* FROM usage_logs linked
 WHERE r.usage_log_id IS NULL AND r.usage_request_id=linked.request_id
 AND r.user_id=linked.user_id AND r.api_key_id=linked.api_key_id
 LIMIT 1
) u ON TRUE
CROSS JOIN LATERAL jsonb_to_record(r.detail) AS d(model character varying(100), requested_model character varying(100), upstream_model character varying(100), upstream_response_model character varying(200), upstream_model_mismatch boolean, input_tokens integer, output_tokens integer, cache_creation_tokens integer, cache_read_tokens integer, cache_creation_5m_tokens integer, cache_creation_1h_tokens integer, image_output_tokens integer, image_output_cost numeric(20,10), image_input_tokens integer, image_input_cost numeric(20,10), input_cost numeric(20,10), output_cost numeric(20,10), cache_creation_cost numeric(20,10), cache_read_cost numeric(20,10), total_cost numeric(20,10), rate_multiplier numeric(10,4), account_rate_multiplier numeric(10,4), request_type smallint, stream boolean, openai_ws_mode boolean, duration_ms integer, first_token_ms integer, user_agent character varying(512), ip_address character varying(45), image_count integer, image_size character varying(10), image_input_size character varying(32), image_output_size character varying(32), image_size_source character varying(16), image_size_breakdown jsonb, video_count integer, video_resolution character varying(10), video_duration_seconds integer, service_tier character varying(16), reasoning_effort character varying(20), requested_reasoning_effort character varying(20), inbound_endpoint character varying(128), upstream_endpoint character varying(128), cache_ttl_overridden boolean, long_context_billing_applied boolean, channel_id bigint, model_mapping_chain character varying(500), billing_tier character varying(50), billing_mode character varying(20), account_stats_cost numeric(20,10), route_billing_snapshot jsonb, upstream_request_id character varying(128), session_id character varying(255), native_compaction_v2 boolean)
WHERE r.state='settled' AND COALESCE(r.command->>'TerminalFailure','false')<>'true'
UNION ALL
SELECT u.id, u.user_id, u.api_key_id, u.account_id, u.request_id, u.model, u.requested_model, u.upstream_model, u.upstream_response_model, u.upstream_model_mismatch, u.group_id, u.subscription_id, u.input_tokens, u.output_tokens, u.cache_creation_tokens, u.cache_read_tokens, u.cache_creation_5m_tokens, u.cache_creation_1h_tokens, u.image_output_tokens, u.image_output_cost, u.image_input_tokens, u.image_input_cost, u.input_cost, u.output_cost, u.cache_creation_cost, u.cache_read_cost, u.total_cost, u.actual_cost, u.rate_multiplier, u.account_rate_multiplier, u.billing_type, u.request_type, u.stream, u.openai_ws_mode, u.duration_ms, u.first_token_ms, u.user_agent, u.ip_address, u.image_count, u.image_size, u.image_input_size, u.image_output_size, u.image_size_source, u.image_size_breakdown, u.video_count, u.video_resolution, u.video_duration_seconds, u.service_tier, u.reasoning_effort, u.requested_reasoning_effort, u.inbound_endpoint, u.upstream_endpoint, u.cache_ttl_overridden, u.long_context_billing_applied, u.channel_id, u.model_mapping_chain, u.billing_tier, u.billing_mode, u.account_stats_cost, u.route_billing_snapshot, u.upstream_request_id, u.session_id, u.native_compaction_v2, u.created_at,
 NULL::date AS accounting_date, u.created_at AS settled_at,u.created_at AS completed_at,
 'legacy_log'::text AS record_source,'complete'::text AS record_completeness,FALSE AS detail_pending, NULL::timestamptz AS admitted_at, 'log'::text AS financial_time_source, NULL::bigint AS financial_receipt_id
FROM usage_logs u
WHERE TRUE AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id AND r.usage_request_id=u.request_id
 AND (r.usage_log_id IS NULL OR r.usage_log_id=u.id))
AND NOT EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.state='settled'
 AND r.usage_request_id IS NULL AND r.usage_log_id=u.id AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id);
