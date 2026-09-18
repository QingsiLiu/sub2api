-- Async media completes after the creating HTTP request; retain its original rights.
CREATE TABLE IF NOT EXISTS subscription_media_tasks (
 task_id TEXT NOT NULL,
 api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
 user_id BIGINT NOT NULL REFERENCES users(id),
 subscription_id BIGINT NOT NULL REFERENCES user_subscriptions(id),
 admission_key VARCHAR NOT NULL REFERENCES subscription_requests(request_key),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(task_id,api_key_id)
);
CREATE INDEX IF NOT EXISTS subscription_media_tasks_subscription_id ON subscription_media_tasks(subscription_id);
