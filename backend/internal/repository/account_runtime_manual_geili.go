package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.AccountManualBalanceRepositoryGeili = (*accountRepository)(nil)

// Both actions stop scheduling. Clearing the manual debt flag deliberately
// requires a separate enable or successful recovery probe before serving users.
func (r *accountRepository) SetManualAccountBalanceStopGeili(ctx context.Context, id int64, blocked bool) error {
	var children []int64
	err := r.withAccountRuntimeGeili(ctx, id, func(view *accountRepository, state *service.AccountRuntimeStateGeili) error {
		var parentID sql.NullInt64
		if err := accountRuntimeQueryRowGeili(ctx, view.sql, `SELECT parent_account_id FROM accounts WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&parentID); err != nil {
			return err
		}
		if parentID.Valid && blocked {
			return service.ErrAccountManualBalanceShadowGeili
		}
		rows, err := view.sql.QueryContext(ctx, `SELECT id FROM accounts WHERE parent_account_id=$1 AND deleted_at IS NULL ORDER BY id FOR UPDATE`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var childID int64
			if err := rows.Scan(&childID); err != nil {
				rows.Close()
				return err
			}
			children = append(children, childID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		state.BalanceBlocked = blocked
		state.BalanceReason, state.BalanceBlockedAt, state.StopReason = "", nil, "manual"
		state.NextCheckAt = nil
		if blocked {
			now := time.Now().UTC()
			state.BalanceReason, state.BalanceBlockedAt, state.StopReason = "manual_insufficient_balance", &now, "balance"
		}
		if err := persistAccountRuntimeGeili(ctx, view, state, true); err != nil {
			return err
		}
		ids := append([]int64{id}, children...)
		if _, err := view.sql.ExecContext(ctx, `UPDATE accounts SET status='inactive',schedulable=FALSE,error_message=$2,updated_at=clock_timestamp() WHERE id=ANY($1) AND deleted_at IS NULL`, pq.Array(ids), state.BalanceReason); err != nil {
			return err
		}
		for _, childID := range children {
			if err := enqueueSchedulerOutbox(ctx, view.sql, service.SchedulerOutboxEventAccountChanged, &childID, nil, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		r.syncSchedulerAccountSnapshots(ctx, children)
	}
	return err
}
