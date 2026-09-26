package repository

import (
	"context"
	"database/sql"
)

// financialStatsRead keeps planner settings transaction-local. A single report
// observes one snapshot and restores the pooled connection on commit/rollback.
func financialStatsRead[T any](ctx context.Context, r *usageLogRepository, read func(*usageLogRepository) (T, error)) (T, error) {
	var zero T
	db, ok := r.sql.(*sql.DB)
	if !ok {
		return read(r)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SET LOCAL jit=off"); err != nil {
		return zero, err
	}
	value, err := read(&usageLogRepository{client: r.client, sql: tx})
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return value, nil
}
