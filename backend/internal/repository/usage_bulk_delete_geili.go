package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// usageBulkDeleteNowGeili is the clock for the exact-event window (tests).
var usageBulkDeleteNowGeili = time.Now

// deleteUsageLogsCompactGeili deletes one usage_logs batch and publishes its
// rollup invalidations in the same statement (migration 273). Per-row DELETE
// triggers are skipped via the transaction-local geili.usage_bulk_delete:
//   - group rollups get one invalidation per affected day;
//   - financial rollups get exact 'log' events only for rows that can touch
//     materialized receipt facts (log or linked receipt day inside the widest
//     possible fact window, one day of margin), and one 'day' event per other
//     affected log/settled-receipt day. 'day' events rebuild kind=2 facts and
//     daily rollups exactly, so retention of old days queues a handful of rows
//     instead of one per deleted log.
//
// prefix declares the CTEs that pick the batch; deleteSQL is the DELETE that
// consumes them, without RETURNING. Placeholders after len(args) are reserved.
func deleteUsageLogsCompactGeili(ctx context.Context, tx *sql.Tx, prefix, deleteSQL string, args []any) (int64, error) {
	if _, err := tx.ExecContext(ctx, `SELECT set_config('geili.usage_bulk_delete','on',true)`); err != nil {
		return 0, err
	}
	n := len(args)
	query := fmt.Sprintf(`WITH %s,
deleted AS (%s RETURNING id,user_id,api_key_id,request_id,group_id,created_at),
exact AS (
 SELECT u.* FROM deleted u
 WHERE (u.created_at AT TIME ZONE 'Asia/Shanghai')::date >= $%[3]d::date
 OR EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.usage_log_id=u.id
  AND GREATEST(r.accounting_date,(COALESCE(r.completed_at,r.settled_at) AT TIME ZONE 'Asia/Shanghai')::date) >= $%[3]d::date)
 OR EXISTS(SELECT 1 FROM usage_settlement_receipts r WHERE r.usage_request_id=u.request_id AND r.api_key_id=u.api_key_id
  AND GREATEST(r.accounting_date,(COALESCE(r.completed_at,r.settled_at) AT TIME ZONE 'Asia/Shanghai')::date) >= $%[3]d::date)
),
compact AS (SELECT u.* FROM deleted u WHERE NOT EXISTS(SELECT 1 FROM exact e WHERE e.id=u.id)),
compact_days AS (
 SELECT (created_at AT TIME ZONE 'Asia/Shanghai')::date AS day FROM compact
 UNION
 SELECT (COALESCE(r.completed_at,r.settled_at) AT TIME ZONE 'Asia/Shanghai')::date FROM compact u
 JOIN usage_settlement_receipts r ON r.state='settled' AND r.user_id=u.user_id AND r.api_key_id=u.api_key_id
  AND ((r.usage_request_id=u.request_id AND (r.usage_log_id IS NULL OR r.usage_log_id=u.id)) OR (r.usage_request_id IS NULL AND r.usage_log_id=u.id))
 WHERE COALESCE(r.completed_at,r.settled_at) IS NOT NULL
),
financial AS (
 INSERT INTO usage_financial_rollup_events(source,old_identity,new_identity)
 SELECT 'log',jsonb_build_object('id',id,'user',user_id,'key',api_key_id,'request',request_id,'at',created_at),NULL::jsonb FROM exact
 UNION ALL
 SELECT 'day',NULL::jsonb,jsonb_build_object('at',day::timestamp AT TIME ZONE 'Asia/Shanghai') FROM compact_days
),
grp AS (
 INSERT INTO usage_group_rollup_invalidations(affected_at)
 SELECT DISTINCT ((created_at AT TIME ZONE $%[4]d::text)::date)::timestamp AT TIME ZONE $%[4]d::text FROM deleted WHERE group_id IS NOT NULL
)
SELECT COUNT(*) FROM deleted`, prefix, deleteSQL, n+1, n+2)
	floor := financialRollupDay(usageBulkDeleteNowGeili().In(financialBeijing)).AddDate(0, 0, -financialFactDays())
	params := append(append([]any{}, args...), financialFactDate(floor), service.GroupUsageTimezoneName())
	var deleted int64
	if err := tx.QueryRowContext(ctx, query, params...).Scan(&deleted); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('geili.usage_bulk_delete','off',true)`); err != nil {
		return 0, err
	}
	return deleted, nil
}
