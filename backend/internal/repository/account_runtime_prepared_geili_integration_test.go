//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRuntimePreparedGeiliIntegrationReadsRemainFresh(t *testing.T) {
	ctx := context.Background()
	plain := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := runtimeIntegrationAccountGeili(t, plain)
	prepared := NewAccountRepository(integrationEntClient, integrationDB, nil).(*accountRepository)
	for i := 0; i < 8; i++ {
		gate, err := prepared.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "fixture-model")
		require.NoError(t, err)
		require.True(t, gate.Allowed)
	}
	require.NoError(t, plain.RecordModelFailureGeili(ctx, account.ID, "fixture-model", "temporary_upstream_failure", time.Time{}))
	state, err := prepared.GetAccountRuntimeGeili(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, 1, state.Models["fixture-model"].FailureCount)
	require.NoError(t, prepared.ResetAccountModelSuccessGeili(ctx, account.ID, "fixture-model"))
	state, err = plain.GetAccountRuntimeGeili(ctx, account.ID)
	require.NoError(t, err)
	require.Zero(t, state.Models["fixture-model"].FailureCount)
	require.NoError(t, plain.MarkAccountBalanceBlockedGeili(ctx, account.ID, "supplier_insufficient_balance"))
	gate, err := prepared.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "fixture-model")
	require.NoError(t, err)
	require.True(t, gate.Allowed)
	require.True(t, gate.BalanceBlocked)
	state, err = prepared.GetAccountRuntimeGeili(ctx, account.ID)
	require.NoError(t, err)
	require.True(t, state.BalanceBlocked)
}

func BenchmarkAccountRuntimeReadPlanGeili(b *testing.B) {
	ctx := context.Background()
	plain := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	account := &service.Account{Name: "runtime-read-plan-benchmark", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "synthetic-benchmark"}}
	require.NoError(b, plain.Create(ctx, account))
	b.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(b, err)
	})
	prepared := NewAccountRepository(integrationEntClient, integrationDB, nil).(*accountRepository)
	for _, mode := range []struct {
		name string
		repo *accountRepository
	}{{"plain", plain}, {"prepared", prepared}} {
		b.Run(mode.name, func(b *testing.B) {
			for i := 0; i < 20; i++ {
				_, err := mode.repo.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "fixture-model")
				require.NoError(b, err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				gate, err := mode.repo.CheckAccountRuntimeAdmissionGeili(ctx, account.ID, "fixture-model")
				if err != nil || !gate.Allowed {
					b.Fatalf("fresh admission failed: %v", err)
				}
				if _, err := mode.repo.GetAccountRuntimeGeili(ctx, account.ID); err != nil {
					b.Fatalf("fresh runtime read failed: %v", err)
				}
			}
		})
	}
}
