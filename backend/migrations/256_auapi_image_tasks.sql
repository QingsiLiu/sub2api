-- Geili native asynchronous image tasks. No credentials or image bytes are stored here.
CREATE TABLE auapi_image_tasks (
 task_id TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL,
 api_key_id BIGINT NOT NULL,
 account_id BIGINT NOT NULL,
 idempotency_key TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 phase TEXT NOT NULL,
 snapshot JSONB NOT NULL,
 next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_until TIMESTAMPTZ,
 lease_token TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(user_id,api_key_id,idempotency_key)
);
CREATE INDEX auapi_image_tasks_due ON auapi_image_tasks(next_poll_at) WHERE phase <> 'done';
