//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestManualAccountBalanceGeiliIntegrationIndependentOfScheduling(t *testing.T) {
	ctx := service.WithAccountRuntimePresentationGeili(context.Background())
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	for _, status := range []string{service.StatusActive, "inactive", service.StatusError} {
		t.Run(status, func(t *testing.T) {
			parent := runtimeIntegrationAccountGeili(t, repo)
			child := runtimeIntegrationAccountGeili(t, repo)
			_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET parent_account_id=$2,quota_dimension='spark' WHERE id=$1`, child.ID, parent.ID)
			require.NoError(t, err)
			_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET status=$2,schedulable=$3,error_message='preserved error' WHERE id=$1`, parent.ID, status, status == service.StatusActive)
			require.NoError(t, err)
			before, err := repo.GetByID(ctx, parent.ID)
			require.NoError(t, err)
			for _, marked := range []bool{true, false} {
				require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, parent.ID, marked))
				after, err := repo.GetByID(ctx, parent.ID)
				require.NoError(t, err)
				require.Equal(t, before.Status, after.Status)
				require.Equal(t, before.Schedulable, after.Schedulable)
				require.Equal(t, before.ErrorMessage, after.ErrorMessage)
				require.Equal(t, marked, after.RuntimeState.BalanceBlocked)
				shadow, err := repo.GetByID(ctx, child.ID)
				require.NoError(t, err)
				require.True(t, shadow.Schedulable)
				require.Equal(t, service.StatusActive, shadow.Status)
			}
			// Marked accounts can still be explicitly enabled without clearing debt.
			require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, parent.ID, true))
			require.NoError(t, repo.SetManualAccountSchedulingGeili(ctx, parent.ID, true))
			after, err := repo.GetByID(ctx, parent.ID)
			require.NoError(t, err)
			require.True(t, after.Schedulable)
			require.True(t, after.RuntimeState.BalanceBlocked)
			require.ErrorIs(t, repo.SetManualAccountBalanceStopGeili(ctx, child.ID, true), service.ErrAccountManualBalanceShadowGeili)
		})
	}
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
	// A historical shadow label is clearable without changing the parent label
	// or the scheduling state of either account.
	require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, parent.ID, true))
	require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, child.ID, false))
	state, err = repo.GetAccountRuntimeGeili(ctx, child.ID)
	require.NoError(t, err)
	require.False(t, state.BalanceBlocked)
	parentState, err := repo.GetAccountRuntimeGeili(ctx, parent.ID)
	require.NoError(t, err)
	require.True(t, parentState.BalanceBlocked)
	var stopped bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status='active' AND schedulable FROM accounts WHERE id=$1`, child.ID).Scan(&stopped))
	require.True(t, stopped)
}
