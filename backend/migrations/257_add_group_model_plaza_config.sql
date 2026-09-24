-- Model plaza curation is presentation-only and must not affect model access.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS model_plaza_config JSONB NOT NULL
        DEFAULT '{"mode":"all"}'::jsonb;

UPDATE groups
SET model_plaza_config = '{"mode":"all"}'::jsonb
WHERE model_plaza_config IS NULL
   OR jsonb_typeof(model_plaza_config) <> 'object';
