//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func runtimeIntegrationAccountGeili(t *testing.T, repo *accountRepository) *service.Account {
	t.Helper()
	account := &service.Account{Name: "runtime-geili", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test-fixture"}}
	require.NoError(t, repo.Create(context.Background(), account))
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(t, err)
	})
	loaded, err := repo.GetByID(context.Background(), account.ID)
	require.NoError(t, err)
	return loaded
}

func TestAccountRuntimeGeiliIntegrationDebtCannotBeClearedByLegacyMutations(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := runtimeIntegrationAccountGeili(t, repo)
	require.NoError(t, repo.MarkAccountBalanceBlockedGeili(ctx, account.ID, "supplier_insufficient_balance"))
	require.NoError(t, repo.ClearError(ctx, account.ID))
	require.NoError(t, repo.SetSchedulable(ctx, account.ID, true))
	_, err := integrationDB.Exec(`UPDATE accounts SET status='active',schedulable=TRUE,extra='{"ordinary_edit":true}'::jsonb WHERE id=$1`, account.ID)
	require.NoError(t, err)
	loaded, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusError, loaded.Status)
	require.False(t, loaded.Schedulable)
	require.True(t, loaded.RuntimeState.BalanceBlocked)
	require.Equal(t, true, loaded.Extra["ordinary_edit"])
	gate, err := repo.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "claude-opus-5")
	require.NoError(t, err)
	require.False(t, gate.Allowed)
	loaded.RuntimeState, err = repo.GetAccountRuntimeGeili(ctx, account.ID)
	require.NoError(t, err)
	snapshot := service.MakeAccountProbeSnapshotGeili(loaded)
	require.NoError(t, repo.RecordAccountProbeGeili(ctx, service.AccountProbeResultGeili{Snapshot: snapshot, Model: "claude-opus-5", Source: "manual", Success: true, Complete: true, CheckedAt: time.Now()}))
	_, err = repo.ActivateAccountAfterProbeGeili(ctx, service.AccountProbeActivationGeili{Snapshot: snapshot, Model: "claude-opus-5"})
	require.ErrorIs(t, err, service.ErrAccountBalanceBlockedGeili)
}

func TestAccountRuntimeGeiliIntegrationLateGrantCannotOverwriteCredentialChange(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := runtimeIntegrationAccountGeili(t, repo)
	require.NoError(t, repo.RecordModelFailureGeili(ctx, account.ID, "", "transport_failure", time.Time{}))
	account, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	snapshot := service.MakeAccountProbeSnapshotGeili(account)
	require.NoError(t, repo.RecordAccountProbeGeili(ctx, service.AccountProbeResultGeili{Snapshot: snapshot, Model: "claude-opus-5", Source: "manual", Success: true, Complete: true, CheckedAt: time.Now()}))
	_, err = integrationDB.Exec(`UPDATE accounts SET credentials='{"api_key":"changed"}'::jsonb,updated_at=NOW() WHERE id=$1`, account.ID)
	require.NoError(t, err)
	_, err = repo.ActivateAccountAfterProbeGeili(ctx, service.AccountProbeActivationGeili{Snapshot: snapshot, Model: "claude-opus-5"})
	require.ErrorIs(t, err, service.ErrAccountRuntimeConflictGeili)
}

func TestAccountRuntimeGeiliIntegrationHealthyProbeAtomicallyActivates(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := runtimeIntegrationAccountGeili(t, repo)
	require.NoError(t, repo.RecordModelFailureGeili(ctx, account.ID, "", "transport_failure", time.Time{}))
	account, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	snapshot := service.MakeAccountProbeSnapshotGeili(account)
	require.NoError(t, repo.RecordAccountProbeGeili(ctx, service.AccountProbeResultGeili{Snapshot: snapshot, Model: "claude-opus-5", Source: "manual", Success: true, Complete: true, CheckedAt: time.Now()}))
	state, err := repo.ActivateAccountAfterProbeGeili(ctx, service.AccountProbeActivationGeili{Snapshot: snapshot, Model: "claude-opus-5"})
	require.NoError(t, err)
	require.Greater(t, state.Version, snapshot.RuntimeVersion)
	loaded, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, loaded.Schedulable)
	require.Equal(t, service.StatusActive, loaded.Status)
	gate, err := repo.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "claude-opus-5")
	require.NoError(t, err)
	require.True(t, gate.Allowed)
}

func TestAccountRuntimeGeiliIntegrationDistributedLeaseBudget(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	var accounts []*service.Account
	var leases []*service.AccountProbeLeaseGeili
	for i := 0; i < 5; i++ {
		accounts = append(accounts, runtimeIntegrationAccountGeili(t, repo))
	}
	for i := 0; i < 4; i++ {
		lease, err := repo.AcquireProbeLeaseGeili(ctx, accounts[i].ID, "claude-opus-5", 35*time.Second)
		require.NoError(t, err)
		require.NotNil(t, lease)
		leases = append(leases, lease)
	}
	t.Cleanup(func() {
		for _, lease := range leases {
			require.NoError(t, repo.ReleaseProbeLeaseGeili(ctx, lease))
		}
	})
	lease, err := repo.AcquireProbeLeaseGeili(ctx, accounts[4].ID, "claude-opus-5", 35*time.Second)
	require.NoError(t, err)
	require.Nil(t, lease)
	lease, err = repo.AcquireProbeLeaseGeili(ctx, accounts[0].ID, "other-model", 35*time.Second)
	require.NoError(t, err)
	require.Nil(t, lease)
	require.NoError(t, repo.ReleaseProbeLeaseGeili(ctx, leases[0]))
	lease, err = repo.AcquireProbeLeaseGeili(ctx, accounts[4].ID, "claude-opus-5", 35*time.Second)
	require.NoError(t, err)
	require.NotNil(t, lease)
	leases = append(leases, lease)
}

func TestAccountRuntimeGeiliIntegrationStartupReclamationAndPagination(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	first := runtimeIntegrationAccountGeili(t, repo)
	second := runtimeIntegrationAccountGeili(t, repo)
	echo := runtimeIntegrationAccountGeili(t, repo)
	for _, account := range []*service.Account{first, second, echo} {
		message := "credit balance is too low"
		body := ""
		if account == echo {
			message = ""
			body = `{"request":{"prompt":"insufficient balance"}}`
		}
		_, err := integrationDB.Exec(`INSERT INTO ops_error_logs(account_id,request_id,error_phase,error_type,status_code,upstream_error_message,error_body) VALUES($1,$2,'upstream','upstream_error',400,$3,$4)`, account.ID, fmt.Sprintf("runtime-startup-%d", account.ID), message, body)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := integrationDB.Exec(`DELETE FROM ops_error_logs WHERE account_id=$1`, account.ID)
			require.NoError(t, err)
		})
	}
	count, err := repo.InitializeAccountBalanceBlocksGeili(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = repo.InitializeAccountBalanceBlocksGeili(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = repo.InitializeAccountBalanceBlocksGeili(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, count)
	state, err := repo.GetAccountRuntimeGeili(ctx, echo.ID)
	require.NoError(t, err)
	require.False(t, state.BalanceBlocked)
	ids, more, err := repo.ListAccountBalanceBlockedGeili(ctx, 0, 1)
	require.NoError(t, err)
	require.Equal(t, []int64{first.ID}, ids)
	require.True(t, more)
	ids, more, err = repo.ListAccountBalanceBlockedGeili(ctx, ids[0], 1)
	require.NoError(t, err)
	require.Equal(t, []int64{second.ID}, ids)
	require.False(t, more)
}

func TestAccountRuntimeGeiliIntegrationModelBlockDoesNotExpireOrClearOtherModels(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := runtimeIntegrationAccountGeili(t, repo)
	for _, model := range []string{"claude-opus-5", "claude-sonnet-5"} {
		for i := 0; i < 3; i++ {
			require.NoError(t, repo.RecordModelFailureGeili(ctx, account.ID, model, "invalid_tool_arguments", time.Now().Add(-time.Minute)))
		}
	}
	gate, err := repo.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "claude-opus-5")
	require.NoError(t, err)
	require.False(t, gate.Allowed)
	gate, err = repo.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "claude-haiku-5")
	require.NoError(t, err)
	require.True(t, gate.Allowed)
	account, err = repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	snapshot := service.MakeAccountProbeSnapshotGeili(account)
	require.NoError(t, repo.RecordAccountProbeGeili(ctx, service.AccountProbeResultGeili{Snapshot: snapshot, Model: "claude-opus-5", Source: "manual", Success: true, Complete: true, CheckedAt: time.Now()}))
	_, err = repo.ActivateAccountAfterProbeGeili(ctx, service.AccountProbeActivationGeili{Snapshot: snapshot, Model: "claude-opus-5"})
	require.NoError(t, err)
	gate, err = repo.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "claude-opus-5")
	require.NoError(t, err)
	require.True(t, gate.Allowed)
	gate, err = repo.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "claude-sonnet-5")
	require.NoError(t, err)
	require.False(t, gate.Allowed)
}

func TestAccountRuntimeGeiliIntegrationLateGroupChangeInvalidatesProbe(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := runtimeIntegrationAccountGeili(t, repo)
	require.NoError(t, repo.RecordModelFailureGeili(ctx, account.ID, "", "transport_failure", time.Time{}))
	account, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	snapshot := service.MakeAccountProbeSnapshotGeili(account)
	require.NoError(t, repo.RecordAccountProbeGeili(ctx, service.AccountProbeResultGeili{Snapshot: snapshot, Model: "claude-opus-5", Source: "manual", Success: true, Complete: true, CheckedAt: time.Now()}))
	group := mustCreateGroup(t, integrationEntClient, &service.Group{Name: "runtime-group", Platform: service.PlatformAnthropic})
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM groups WHERE id=$1`, group.ID)
		require.NoError(t, err)
	})
	require.NoError(t, repo.AddToGroup(ctx, account.ID, group.ID, 1))
	_, err = repo.ActivateAccountAfterProbeGeili(ctx, service.AccountProbeActivationGeili{Snapshot: snapshot, Model: "claude-opus-5"})
	require.ErrorIs(t, err, service.ErrAccountRuntimeConflictGeili)
}

func TestAccountRuntimeGeiliIntegrationProbeBackoffPersistsWithoutInvalidatingCAS(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := runtimeIntegrationAccountGeili(t, repo)
	checked := time.Now()
	snapshot := service.MakeAccountProbeSnapshotGeili(account)
	for i := 1; i <= 4; i++ {
		require.NoError(t, repo.RecordAccountProbeGeili(ctx, service.AccountProbeResultGeili{Snapshot: snapshot, Model: "claude-opus-5", Source: "reload", CheckedAt: checked, Reason: "probe_failed"}))
		state, err := repo.GetAccountRuntimeGeili(ctx, account.ID)
		require.NoError(t, err)
		require.Equal(t, snapshot.RuntimeVersion, state.Version)
		health := state.Models["claude-opus-5"]
		expectedCount := i
		if expectedCount > 3 {
			expectedCount = 3
		}
		require.Equal(t, expectedCount, health.ProbeFailureCount)
		require.WithinDuration(t, checked.Add([]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}[expectedCount-1]), *health.NextCheckAt, time.Millisecond)
		require.Nil(t, state.NextCheckAt)
	}
}

func TestAccountRuntimeGeiliIntegrationProbeClearsOnlyGenericTemporaryStop(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	for _, tc := range []struct {
		name, reason string
		clear        bool
	}{
		{"transport", "upstream connection timeout", true},
		{"provider-window", `{"status_code":429,"scope":"account","error_message":"provider rate limit"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := runtimeIntegrationAccountGeili(t, repo)
			future := time.Now().Add(time.Hour)
			_, err := integrationDB.Exec(`UPDATE accounts SET temp_unschedulable_until=$2,temp_unschedulable_reason=$3 WHERE id=$1`, account.ID, future, tc.reason)
			require.NoError(t, err)
			account, err = repo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.False(t, account.IsSchedulable())
			snapshot := service.MakeAccountProbeSnapshotGeili(account)
			require.NoError(t, repo.RecordAccountProbeGeili(ctx, service.AccountProbeResultGeili{Snapshot: snapshot, Model: "claude-opus-5", Source: "manual", Success: true, Complete: true, CheckedAt: time.Now()}))
			_, err = repo.ActivateAccountAfterProbeGeili(ctx, service.AccountProbeActivationGeili{Snapshot: snapshot, Model: "claude-opus-5"})
			require.NoError(t, err)
			loaded, err := repo.GetByID(ctx, account.ID)
			require.NoError(t, err)
			require.Equal(t, tc.clear, loaded.IsSchedulable())
			if tc.clear {
				require.Nil(t, loaded.TempUnschedulableUntil)
				require.Empty(t, loaded.TempUnschedulableReason)
			} else {
				require.NotNil(t, loaded.TempUnschedulableUntil)
				require.Equal(t, tc.reason, loaded.TempUnschedulableReason)
			}
		})
	}
}

func TestAccountRuntimeGeiliIntegrationParentDebtBlocksAllShadows(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	parent := runtimeIntegrationAccountGeili(t, repo)
	shadow := runtimeIntegrationAccountGeili(t, repo)
	_, err := integrationDB.Exec(`UPDATE accounts SET parent_account_id=$2,quota_dimension='spark' WHERE id=$1`, shadow.ID, parent.ID)
	require.NoError(t, err)
	// Shadows retain their own quota/scheduling dimension while sharing credentials.
	_, err = integrationDB.Exec(`UPDATE accounts SET schedulable=FALSE,rate_limit_reset_at=NOW()+INTERVAL '1 hour' WHERE id=$1`, parent.ID)
	require.NoError(t, err)
	gate, err := repo.CheckAccountRuntimeAdmissionGeili(ctx, shadow.ID, "claude-opus-5")
	require.NoError(t, err)
	require.True(t, gate.Allowed)
	require.Equal(t, parent.ID, gate.CredentialAccountID)
	require.NoError(t, repo.MarkAccountBalanceBlockedGeili(ctx, parent.ID, "supplier_insufficient_balance"))
	gate, err = repo.CheckAccountRuntimeAdmissionGeili(ctx, shadow.ID, "claude-opus-5")
	require.NoError(t, err)
	require.False(t, gate.Allowed)
	require.True(t, gate.BalanceBlocked)
	require.Equal(t, "balance", gate.Reason)
	loaded, err := repo.GetByID(ctx, shadow.ID)
	require.NoError(t, err)
	require.True(t, loaded.Schedulable)
	// A legacy toggle of the shadow cannot clear its credential owner's block.
	require.NoError(t, repo.SetSchedulable(ctx, shadow.ID, true))
	gate, err = repo.CheckAccountRuntimeAdmissionGeili(ctx, shadow.ID, "claude-opus-5")
	require.NoError(t, err)
	require.False(t, gate.Allowed)
}

func TestAccountRuntimeGeiliIntegrationProbeClearsGenericOverloadWithoutRateWindow(t *testing.T) {
	ctx := context.Background()
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := runtimeIntegrationAccountGeili(t, repo)
	_, err := integrationDB.Exec(`UPDATE accounts SET status='inactive',schedulable=FALSE,overload_until=NOW()+INTERVAL '1 hour',updated_at=NOW() WHERE id=$1`, account.ID)
	require.NoError(t, err)
	account, err = repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	snapshot := service.MakeAccountProbeSnapshotGeili(account)
	require.NoError(t, repo.RecordAccountProbeGeili(ctx, service.AccountProbeResultGeili{Snapshot: snapshot, Model: "claude-opus-5", Source: "admin", Success: true, Complete: true, CheckedAt: time.Now()}))
	_, err = repo.ActivateAccountAfterProbeGeili(ctx, service.AccountProbeActivationGeili{Snapshot: snapshot, Model: "claude-opus-5"})
	require.NoError(t, err)
	account, err = repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Nil(t, account.OverloadUntil)
	require.True(t, account.IsSchedulable())
}
