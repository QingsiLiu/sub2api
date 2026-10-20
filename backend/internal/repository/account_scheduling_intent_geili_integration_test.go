//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestAccountRuntimeGeiliIntegrationManualStopWinsLateProbeAndError(t *testing.T) {
	ctx := service.WithAccountRuntimePresentationGeili(context.Background())
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	a := runtimeIntegrationAccountGeili(t, repo)
	require.NoError(t, repo.SetError(ctx, a.ID, "temporary upstream failure"))
	a, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	snapshot := service.MakeAccountProbeSnapshotGeili(a)
	require.NoError(t, repo.RecordAccountProbeGeili(ctx, service.AccountProbeResultGeili{
		Snapshot: snapshot, Model: "fixture-model", Source: "automatic", Success: true, Complete: true, CheckedAt: time.Now(),
	}))
	// Already false due to the error: an explicit off must still invalidate CAS.
	require.NoError(t, repo.SetManualAccountSchedulingGeili(ctx, a.ID, false))
	_, err = repo.ActivateAccountAfterProbeGeili(ctx, service.AccountProbeActivationGeili{Snapshot: snapshot, Model: "fixture-model"})
	require.ErrorIs(t, err, service.ErrAccountRuntimeConflictGeili)
	require.NoError(t, repo.RecordModelFailureGeili(ctx, a.ID, "", "late transport error", time.Time{}))
	a, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, service.AccountManuallyStoppedGeili(a))
	require.Equal(t, "manual", a.RuntimeState.StopReason)
	require.NoError(t, repo.SetManualAccountBalanceStopGeili(ctx, a.ID, true))
	recovery := service.NewAccountRecoveryServiceGeili(repo, func(context.Context, *service.Account, string) (bool, error) { return true, nil }, nil)
	defer recovery.Stop()
	a, err = recovery.RecheckAccountGeili(ctx, a.ID, "fixture-model", false)
	require.NoError(t, err)
	require.False(t, a.Schedulable, "even an explicit health check preserves the manual switch")
	require.True(t, a.RuntimeState.BalanceBlocked)
	require.NoError(t, repo.SetManualAccountSchedulingGeili(ctx, a.ID, true))
	a, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, a.IsSchedulable())
	require.True(t, a.RuntimeState.BalanceBlocked, "enable does not clear the independent label")
}

func TestAccountRuntimeGeiliIntegrationBulkSwitchPreservesManualIntent(t *testing.T) {
	ctx := service.WithAccountRuntimePresentationGeili(context.Background())
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	a := runtimeIntegrationAccountGeili(t, repo)
	enabled := false
	_, err := repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Schedulable: &enabled})
	require.NoError(t, err)
	require.NoError(t, repo.SetError(ctx, a.ID, "late upstream error"))
	a, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, service.AccountManuallyStoppedGeili(a))
	enabled = true
	_, err = repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Schedulable: &enabled})
	require.NoError(t, err)
	a, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.False(t, service.AccountManuallyStoppedGeili(a))
	require.Equal(t, service.StatusError, a.Status, "enabling does not pretend the upstream error was verified")
}

func TestAccountRuntimeGeiliIntegrationEditedInactiveSurvivesLateError(t *testing.T) {
	ctx := service.WithAccountRuntimePresentationGeili(context.Background())
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	for _, bulk := range []bool{false, true} {
		a := runtimeIntegrationAccountGeili(t, repo)
		a.Status = "inactive"
		if bulk {
			_, err := repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Status: &a.Status})
			require.NoError(t, err)
		} else {
			require.NoError(t, repo.Update(ctx, a))
		}
		require.NoError(t, repo.SetError(ctx, a.ID, "late error after manual disable"))
		loaded, err := repo.GetByID(ctx, a.ID)
		require.NoError(t, err)
		require.True(t, service.AccountManuallyStoppedGeili(loaded))
	}
}

func TestAccountRuntimeGeiliIntegrationMigrationPreservesExistingStops(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	a := runtimeIntegrationAccountGeili(t, repo)
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET status='inactive',schedulable=FALSE WHERE id=$1`, a.ID)
	require.NoError(t, err)
	require.NoError(t, repo.MarkAccountBalanceBlockedGeili(ctx, a.ID, "historical debt"))
	source, err := migrations.FS.ReadFile("284_account_scheduling_intent_geili.sql")
	require.NoError(t, err)
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, string(source))
	require.NoError(t, err)
	var stopped, marked bool
	var reason string
	err = tx.QueryRowContext(ctx, `SELECT a.status='inactive' AND NOT a.schedulable,r.balance_blocked,r.stop_reason FROM accounts a JOIN account_runtime_geili r ON r.account_id=a.id WHERE a.id=$1`, a.ID).Scan(&stopped, &marked, &reason)
	require.NoError(t, err)
	require.True(t, stopped)
	require.True(t, marked)
	require.Equal(t, "manual", reason)
}
