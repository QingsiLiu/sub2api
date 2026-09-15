ALTER TABLE composite_model_routes
    ADD COLUMN IF NOT EXISTS profile_key VARCHAR(50) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS composite_model_routes_group_profile_idx
    ON composite_model_routes(group_id, profile_key);
