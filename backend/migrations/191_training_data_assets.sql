-- 可授权训练数据资产：权利账本、原始报文索引、版本化数据集与删除传播。
-- 完整请求/响应正文不进入 PostgreSQL，只保存于隔离的私有对象存储。

CREATE TABLE IF NOT EXISTS training_rights (
    rights_id UUID PRIMARY KEY,
    scope_type TEXT NOT NULL CHECK (scope_type IN ('user','api_key')),
    scope_ref TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('unknown','eligible','excluded','withdrawn','expired','legal_hold')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    consent_or_contract_id TEXT NOT NULL,
    allowed_purposes TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    allowed_dataset_types TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    allowed_recipients TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    region TEXT NOT NULL DEFAULT '',
    effective_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    evidence_uri TEXT NOT NULL DEFAULT '',
    evidence_sha256 TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (scope_type, scope_ref)
);

CREATE INDEX IF NOT EXISTS idx_training_rights_status_effective
    ON training_rights (status, effective_at, expires_at);

CREATE TABLE IF NOT EXISTS training_rights_events (
    event_id BIGSERIAL PRIMARY KEY,
    rights_id UUID NOT NULL REFERENCES training_rights(rights_id),
    version BIGINT NOT NULL CHECK (version > 0),
    event_type TEXT NOT NULL CHECK (event_type IN ('created','updated','withdrawn','expired','excluded','legal_hold','restored')),
    snapshot JSONB NOT NULL,
    actor_ref TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (rights_id, version)
);

CREATE OR REPLACE FUNCTION reject_training_rights_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'training_rights_events is append-only';
END;
$$;

DROP TRIGGER IF EXISTS trg_training_rights_events_append_only ON training_rights_events;
CREATE TRIGGER trg_training_rights_events_append_only
BEFORE UPDATE OR DELETE ON training_rights_events
FOR EACH ROW EXECUTE FUNCTION reject_training_rights_event_mutation();

CREATE TABLE IF NOT EXISTS training_captures (
    capture_id UUID PRIMARY KEY,
    request_id TEXT NOT NULL DEFAULT '',
    client_request_id TEXT NOT NULL DEFAULT '',
    user_subject_ref TEXT NOT NULL,
    api_key_subject_ref TEXT NOT NULL,
    rights_id UUID,
    rights_version BIGINT,
    rights_status TEXT NOT NULL DEFAULT 'unknown',
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    route TEXT NOT NULL,
    method TEXT NOT NULL,
    protocol TEXT NOT NULL DEFAULT '',
    client_model TEXT NOT NULL DEFAULT '',
    upstream_models TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    stream BOOLEAN NOT NULL DEFAULT FALSE,
    http_status INTEGER NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    capture_complete BOOLEAN NOT NULL DEFAULT FALSE,
    capture_status TEXT NOT NULL CHECK (capture_status IN ('complete','incomplete','deleted','legal_hold')),
    incomplete_reasons TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    request_bytes BIGINT NOT NULL DEFAULT 0,
    upstream_request_bytes BIGINT NOT NULL DEFAULT 0,
    upstream_response_bytes BIGINT NOT NULL DEFAULT 0,
    client_response_bytes BIGINT NOT NULL DEFAULT 0,
    raw_object_prefix TEXT NOT NULL,
    raw_manifest_key TEXT NOT NULL,
    redaction_version TEXT NOT NULL,
    legal_hold BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_training_captures_user_time
    ON training_captures (user_subject_ref, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_training_captures_api_key_time
    ON training_captures (api_key_subject_ref, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_training_captures_rights_time
    ON training_captures (rights_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_training_captures_status_time
    ON training_captures (capture_status, started_at DESC);

CREATE TABLE IF NOT EXISTS training_dataset_releases (
    release_id TEXT PRIMARY KEY,
    release_kind TEXT NOT NULL CHECK (release_kind IN ('curated','buyer')),
    parent_release_id TEXT REFERENCES training_dataset_releases(release_id),
    buyer_id TEXT NOT NULL DEFAULT '',
    contract_id TEXT NOT NULL DEFAULT '',
    dataset_type TEXT NOT NULL CHECK (dataset_type IN ('chat','code','eval')),
    status TEXT NOT NULL CHECK (status IN ('draft','review','finalized','delivered','revoked','expired')),
    rules_version TEXT NOT NULL,
    source_started_at TIMESTAMPTZ NOT NULL,
    source_finished_at TIMESTAMPTZ NOT NULL,
    source_high_watermark UUID,
    object_prefix TEXT NOT NULL,
    manifest_sha256 TEXT NOT NULL DEFAULT '',
    sample_count BIGINT NOT NULL DEFAULT 0,
    allowed_purpose TEXT NOT NULL DEFAULT 'model_training',
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS training_dataset_samples (
    release_id TEXT NOT NULL REFERENCES training_dataset_releases(release_id) ON DELETE CASCADE,
    sample_id TEXT NOT NULL,
    capture_id UUID NOT NULL REFERENCES training_captures(capture_id),
    rights_id UUID,
    rights_version BIGINT,
    split TEXT NOT NULL CHECK (split IN ('train','validation','test')),
    dataset_type TEXT NOT NULL CHECK (dataset_type IN ('chat','code','eval')),
    shard_key TEXT NOT NULL,
    row_number BIGINT NOT NULL,
    content_sha256 TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (release_id, sample_id)
);

CREATE INDEX IF NOT EXISTS idx_training_dataset_samples_capture
    ON training_dataset_samples (capture_id, release_id);

CREATE TABLE IF NOT EXISTS training_deletion_requests (
    deletion_id UUID PRIMARY KEY,
    scope_type TEXT NOT NULL CHECK (scope_type IN ('user','api_key')),
    scope_ref TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    scope TEXT NOT NULL DEFAULT 'all',
    idempotency_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('pending','processing','completed','partially_completed','failed','legal_hold')),
    reason TEXT NOT NULL DEFAULT '',
    due_at TIMESTAMPTZ,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    result JSONB NOT NULL DEFAULT '{}'::JSONB
);

CREATE INDEX IF NOT EXISTS idx_training_deletion_requests_scope
    ON training_deletion_requests (scope_type, scope_ref, requested_at DESC);

CREATE TABLE IF NOT EXISTS training_deletion_targets (
    deletion_id UUID NOT NULL REFERENCES training_deletion_requests(deletion_id) ON DELETE CASCADE,
    target_type TEXT NOT NULL CHECK (target_type IN ('raw','spool','prompt_audit','curated_release','buyer_release','buyer_notice')),
    target_ref TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('pending','processing','completed','failed','legal_hold')),
    cursor JSONB NOT NULL DEFAULT '{}'::JSONB,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    evidence JSONB NOT NULL DEFAULT '{}'::JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (deletion_id, target_type, target_ref)
);

CREATE INDEX IF NOT EXISTS idx_training_deletion_targets_claim
    ON training_deletion_targets (status, lease_expires_at, updated_at);

CREATE TABLE IF NOT EXISTS training_delivery_audits (
    id BIGSERIAL PRIMARY KEY,
    release_id TEXT NOT NULL REFERENCES training_dataset_releases(release_id),
    action TEXT NOT NULL,
    actor_ref TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_training_delivery_audits_release
    ON training_delivery_audits (release_id, created_at DESC);
