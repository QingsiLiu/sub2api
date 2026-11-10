//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageWalletGateGeiliCancelledApplyRollsBackAndCanRetry(t *testing.T) {
	r, cmd, detail := settlementFixture(t)
	require.NoError(t, r.PrepareSettlement(context.Background(), cmd, detail))
	release, err := r.walletGeili.acquire(context.Background(), cmd.UserID)
	require.NoError(t, err)
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err = r.Apply(ctx, cmd)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	var balance float64
	var dedup int
	require.NoError(t, integrationDB.QueryRow("SELECT balance FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.Equal(t, 100.0, balance)
	require.NoError(t, integrationDB.QueryRow("SELECT count(*) FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2", cmd.RequestID, cmd.APIKeyID).Scan(&dedup))
	require.Zero(t, dedup, "cancelled wait must roll back its receipt claim and dedup insert")
	release()
	result, err := r.Apply(context.Background(), cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	result, err = r.Apply(context.Background(), cmd)
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.NoError(t, integrationDB.QueryRow("SELECT balance FROM users WHERE id=$1", cmd.UserID).Scan(&balance))
	require.Equal(t, 98.75, balance)
	require.Empty(t, r.walletGeili.keys)
}

func TestUsageWalletGateGeiliTwoInstancesKeepConcurrentBillingExactlyOnce(t *testing.T) {
	r, base, _ := settlementFixture(t)
	other := &usageBillingRepository{db: integrationDB}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var applied atomic.Int64
	var wg sync.WaitGroup
	errorsCh := make(chan error, 64)
	start := make(chan struct{})
	for i := range 32 {
		for _, repo := range []*usageBillingRepository{r, other} {
			wg.Add(1)
			go func(i int, repo *usageBillingRepository) {
				defer wg.Done()
				<-start
				cmd := *base
				cmd.RequestID = fmt.Sprintf("%s-%d", base.RequestID, i)
				result, err := repo.Apply(ctx, &cmd)
				if err != nil {
					errorsCh <- err
				} else if result == nil {
					errorsCh <- errors.New("missing billing result")
				} else if result.Applied {
					applied.Add(1)
				}
			}(i, repo)
		}
	}
	close(start)
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		require.NoError(t, err)
	}
	require.Equal(t, int64(32), applied.Load())
	var balance float64
	var settled int
	require.NoError(t, integrationDB.QueryRow("SELECT balance FROM users WHERE id=$1", base.UserID).Scan(&balance))
	require.Equal(t, 60.0, balance)
	require.NoError(t, integrationDB.QueryRow("SELECT count(*) FROM usage_settlement_receipts WHERE api_key_id=$1 AND state='settled'", base.APIKeyID).Scan(&settled))
	require.Equal(t, 32, settled)
	require.Empty(t, r.walletGeili.keys)
	require.Empty(t, other.walletGeili.keys)
}
