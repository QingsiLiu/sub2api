-- psql -v group_id=4 -f gpt-transport-diagnostics.sql
-- Bounded read-only observation. Attempts and final requests are different units.
\if :{?group_id}
\else
\set group_id 4
\endif
BEGIN READ ONLY;
SET LOCAL statement_timeout = '15s';
WITH recent AS (
  SELECT * FROM ops_error_logs
  WHERE group_id = :'group_id'::bigint AND created_at >= now() - interval '15 minutes'
)
SELECT :'group_id'::bigint AS group_id,
       count(*) FILTER (WHERE status_code >= 400) AS final_http_error_rows,
       count(*) FILTER (WHERE status_code < 400 AND error_message LIKE 'Recovered upstream error:%') AS recovered_request_rows,
       count(*) FILTER (WHERE status_code < 400 AND error_message NOT LIKE 'Recovered upstream error:%') AS other_error_rows_requires_terminal_review
FROM recent;

WITH attempts AS (
  SELECT (attempt->>'account_id')::bigint AS account_id,
         count(*) AS failed_upstream_attempts,
         count(*) FILTER (WHERE attempt->>'kind' = 'request_error') AS transport_attempts
  FROM ops_error_logs o
  CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(o.upstream_errors::jsonb) = 'array'
    THEN o.upstream_errors::jsonb ELSE '[]'::jsonb END) attempt
  WHERE o.group_id = :'group_id'::bigint AND o.created_at >= now() - interval '15 minutes'
  GROUP BY (attempt->>'account_id')::bigint
), usage AS (
  SELECT account_id, count(*) AS usage_rows
  FROM usage_logs WHERE group_id = :'group_id'::bigint AND created_at >= now() - interval '15 minutes'
  GROUP BY account_id
)
SELECT a.id, a.type, a.priority, a.status, a.schedulable,
       a.temp_unschedulable_until,
       coalesce(attempts.failed_upstream_attempts, 0) AS failed_upstream_attempts,
       coalesce(attempts.transport_attempts, 0) AS transport_attempts,
       coalesce(usage.usage_rows, 0) AS usage_rows_not_client_delivery_proof
FROM accounts a JOIN account_groups ag ON ag.account_id = a.id AND ag.group_id = :'group_id'::bigint
LEFT JOIN attempts ON attempts.account_id = a.id LEFT JOIN usage ON usage.account_id = a.id
WHERE a.deleted_at IS NULL
ORDER BY transport_attempts DESC, a.priority, a.id;
COMMIT;
