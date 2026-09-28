package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// deleteUsageLogsCompactGeili deletes one usage_logs batch and publishes its
// group rollup invalidations in the same statement: one per affected day
// instead of one per deleted row (migration 273). The per-row DELETE trigger
// is skipped via the transaction-local geili.usage_bulk_delete.
//
// prefix declares the CTEs that pick the batch; deleteSQL is the DELETE that
// consumes them, without RETURNING. Placeholders after len(args) are reserved.
func deleteUsageLogsCompactGeili(ctx context.Context, tx *sql.Tx, prefix, deleteSQL string, args []any) (int64, error) {
	if _, err := tx.ExecContext(ctx, `SELECT set_config('geili.usage_bulk_delete','on',true)`); err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`WITH %s,
deleted AS (%s RETURNING group_id,created_at),
grp AS (
 INSERT INTO usage_group_rollup_invalidations(affected_at)
 SELECT DISTINCT ((created_at AT TIME ZONE $%[3]d::text)::date)::timestamp AT TIME ZONE $%[3]d::text FROM deleted WHERE group_id IS NOT NULL
)
SELECT COUNT(*) FROM deleted`, prefix, deleteSQL, len(args)+1)
	params := append(append([]any{}, args...), service.GroupUsageTimezoneName())
	var deleted int64
	if err := tx.QueryRowContext(ctx, query, params...).Scan(&deleted); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('geili.usage_bulk_delete','off',true)`); err != nil {
		return 0, err
	}
	return deleted, nil
}
