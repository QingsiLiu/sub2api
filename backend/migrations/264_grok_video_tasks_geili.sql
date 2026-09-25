-- Geili: durable asynchronous-video ownership/pricing and completion work.
-- No parent/log FK: accepted tasks survive expiry/deletion without hot-row locks.
CREATE TABLE grok_video_tasks_geili (
 task_id TEXT NOT NULL,api_key_id BIGINT NOT NULL,user_id BIGINT NOT NULL,account_id BIGINT NOT NULL,
 financial_request_id TEXT NOT NULL,snapshot JSONB NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','prepared','failed')),
 observed JSONB,attempts INTEGER NOT NULL DEFAULT 0,next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_token TEXT,lease_until TIMESTAMPTZ,last_error TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(task_id,api_key_id),UNIQUE(financial_request_id,api_key_id)
);
CREATE INDEX grok_video_tasks_geili_pending ON grok_video_tasks_geili(next_poll_at,task_id) WHERE state='pending';
COMMENT ON TABLE grok_video_tasks_geili IS 'No prompts/credentials/URLs. Keep pending tasks until verified terminal observation and retain financial linkage for at least 365 days.';
