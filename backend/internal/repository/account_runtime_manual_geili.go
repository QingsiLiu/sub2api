package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.AccountManualBalanceRepositoryGeili = (*accountRepository)(nil)

// The historical API name is retained for clients. Debt is only a label:
// neither marking nor clearing it changes scheduling, errors or probe timers.
func (r *accountRepository) SetManualAccountBalanceStopGeili(ctx context.Context, id int64, blocked bool) error {
	return r.withAccountRuntimeGeili(ctx, id, func(view *accountRepository, state *service.AccountRuntimeStateGeili) error {
		var parentID sql.NullInt64
		if err := accountRuntimeQueryRowGeili(ctx, view.sql, `SELECT parent_account_id FROM accounts WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&parentID); err != nil {
			return err
		}
		if parentID.Valid && blocked {
			return service.ErrAccountManualBalanceShadowGeili
		}
		state.BalanceBlocked = blocked
		state.BalanceReason, state.BalanceBlockedAt = "", nil
		if blocked {
			now := time.Now().UTC()
			state.BalanceReason, state.BalanceBlockedAt = "manual_insufficient_balance", &now
		}
		return persistAccountRuntimeGeili(ctx, view, state, true)
	})
}

// The account row lock and version bump invalidate probes even when scheduling
// was already off. Errors cannot erase this administrator intent.
func (r *accountRepository) SetManualAccountSchedulingGeili(ctx context.Context, id int64, enabled bool) error {
	return r.withAccountRuntimeGeili(ctx, id, func(view *accountRepository, state *service.AccountRuntimeStateGeili) error {
		if !enabled {
			state.StopReason = "manual"
		} else if state.StopReason == "manual" || state.StopReason == "balance" {
			state.StopReason = ""
		}
		if err := persistAccountRuntimeGeili(ctx, view, state, true); err != nil {
			return err
		}
		_, err := view.sql.ExecContext(ctx, `UPDATE accounts SET schedulable=$2,status=CASE WHEN $2 AND status='inactive' THEN 'active' ELSE status END,updated_at=clock_timestamp() WHERE id=$1 AND deleted_at IS NULL`, id, enabled)
		return err
	})
}

// Called after BulkUpdate locks the account rows, in the same transaction.
func setBulkManualSchedulingGeili(ctx context.Context, exec sqlExecutor, ids []int64, enabled bool) error {
	_, err := exec.ExecContext(ctx, `INSERT INTO account_runtime_geili(account_id,version,stop_reason)
SELECT id,1,CASE WHEN $2 THEN '' ELSE 'manual' END FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL
ON CONFLICT(account_id) DO UPDATE SET version=account_runtime_geili.version+1,
stop_reason=CASE WHEN NOT $2 THEN 'manual' WHEN account_runtime_geili.stop_reason IN ('manual','balance') THEN '' ELSE account_runtime_geili.stop_reason END,updated_at=clock_timestamp()`, pq.Array(ids), enabled)
	return err
}
