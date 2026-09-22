-- geili hook: snapshot configured routes separately from the actual selected group.
-- NULL on historical rows means the routing snapshot was not recorded.
ALTER TABLE ops_error_logs ADD COLUMN IF NOT EXISTS requested_group_ids BIGINT[];
