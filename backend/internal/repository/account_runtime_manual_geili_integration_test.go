//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestManualAccountBalanceGeiliIntegrationStopsAndClearsWithoutActivating(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	parent := runtimeIntegrationAccountGeili(t, repo)
	child := runtimeIntegrationAccountGeili(t, repo)
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET parent_account_id=$2,quota_dimension='spark' WHERE id=$1`, child.ID, parent.ID)
	require.NoError(t, err)
	ids := []int64{parent.ID, child.ID}
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM scheduler_outbox WHERE account_id=ANY($1)`, pq.Array(ids))
		require.NoError(t, err)
	})
	require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, parent.ID, true))
	state, err := repo.GetAccountRuntimeGeili(ctx, parent.ID)
	require.NoError(t, err)
	require.True(t, state.BalanceBlocked)
	require.Equal(t, "manual_insufficient_balance", state.BalanceReason)
	markedVersion := state.Version
	for _, id := range ids {
		var status string
		var schedulable bool
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status,schedulable FROM accounts WHERE id=$1`, id).Scan(&status, &schedulable))
		require.Equal(t, "inactive", status)
		require.False(t, schedulable)
		var events int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1 AND event_type=$2`, id, service.SchedulerOutboxEventAccountChanged).Scan(&events))
		require.Positive(t, events)
	}
	// Ordinary legacy enable operations remain guarded while explicitly unpaid.
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET status='active',schedulable=TRUE WHERE id=$1`, parent.ID)
	require.NoError(t, err)
	var active bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status='active' OR schedulable FROM accounts WHERE id=$1`, parent.ID).Scan(&active))
	require.False(t, active)
	require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, parent.ID, false))
	state, err = repo.GetAccountRuntimeGeili(ctx, parent.ID)
	require.NoError(t, err)
	require.False(t, state.BalanceBlocked)
	require.Empty(t, state.BalanceReason)
	require.Nil(t, state.BalanceBlockedAt)
	require.Equal(t, "manual", state.StopReason)
	require.Greater(t, state.Version, markedVersion)
	for _, id := range ids {
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status='active' OR schedulable FROM accounts WHERE id=$1`, id).Scan(&active))
		require.False(t, active)
	}
	require.ErrorIs(t, repo.SetManualAccountBalanceStopGeili(ctx, child.ID, true), service.ErrAccountManualBalanceShadowGeili)
	var probeCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_probe_logs_geili WHERE account_id=ANY($1)`, pq.Array(ids)).Scan(&probeCount))
	require.Zero(t, probeCount, "manual controls perform no paid probe")
}

func TestManualAccountBalanceGeiliIntegrationPreservesHistoricalChildDebt(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	parent := runtimeIntegrationAccountGeili(t, repo)
	child := runtimeIntegrationAccountGeili(t, repo)
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET parent_account_id=$2,quota_dimension='spark' WHERE id=$1`, child.ID, parent.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM scheduler_outbox WHERE account_id=ANY($1)`, pq.Array([]int64{parent.ID, child.ID}))
		require.NoError(t, err)
	})
	require.NoError(t, repo.MarkAccountBalanceBlockedGeili(ctx, child.ID, "historical_unpaid"))
	require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, parent.ID, true))
	require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, parent.ID, false))
	state, err := repo.GetAccountRuntimeGeili(ctx, child.ID)
	require.NoError(t, err)
	require.True(t, state.BalanceBlocked)
	require.Equal(t, "historical_unpaid", state.BalanceReason)
	// The rare historical shadow marker is manually clearable without releasing
	// the credential parent's separate balance stop or enabling the shadow.
	require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, parent.ID, true))
	require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, child.ID, false))
	state, err = repo.GetAccountRuntimeGeili(ctx, child.ID)
	require.NoError(t, err)
	require.False(t, state.BalanceBlocked)
	parentState, err := repo.GetAccountRuntimeGeili(ctx, parent.ID)
	require.NoError(t, err)
	require.True(t, parentState.BalanceBlocked)
	var stopped bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status='inactive' AND NOT schedulable FROM accounts WHERE id=$1`, child.ID).Scan(&stopped))
	require.True(t, stopped)
}
