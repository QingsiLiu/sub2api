//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func auapiBillingFixture(t *testing.T, balance float64) (*auapiImageTaskRepository, *service.AUAPIImageTaskRecord) {
	t.Helper()
	ctx := context.Background()
	c := testEntClient(t)
	suffix := uuid.NewString()
	u := mustCreateUser(t, c, &service.User{Email: "auapi-bill-" + suffix + "@example.invalid", Balance: balance})
	k := mustCreateApiKey(t, c, &service.APIKey{UserID: u.ID, Key: "sk-" + suffix, Name: "AUAPI test", BillingSource: "balance"})
	now := time.Now().UTC().Truncate(time.Microsecond)
	task := &service.AUAPIImageTaskRecord{TaskID: "auimgtask_" + suffix, UserID: u.ID, APIKeyID: k.ID, AccountID: 1, IdempotencyKey: "idem-" + suffix, RequestHash: "hash-" + suffix, Phase: "estimate", Status: "processing", HoldAmount: 2, CreatedAt: now, NextPollAt: now, ExpiresAt: now.Add(time.Hour)}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM auapi_image_tasks WHERE user_id=$1`, `DELETE FROM usage_balance_holds WHERE user_id=$1`, `DELETE FROM usage_settlement_receipts WHERE user_id=$1`, `DELETE FROM usage_logs WHERE user_id=$1`, `DELETE FROM usage_billing_dedup WHERE api_key_id IN (SELECT id FROM api_keys WHERE user_id=$1)`, `DELETE FROM api_keys WHERE user_id=$1`, `DELETE FROM users WHERE id=$1`} {
			_, err := integrationDB.ExecContext(ctx, q, u.ID)
			require.NoError(t, err)
		}
	})
	return &auapiImageTaskRepository{db: integrationDB}, task
}
func assertAUAPIBalances(t *testing.T, id int64, balance, frozen float64) {
	t.Helper()
	var b, f float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, id).Scan(&b, &f))
	require.InDelta(t, balance, b, 1e-8)
	require.InDelta(t, frozen, f, 1e-8)
}

func TestAUAPIConcurrentIdempotencyOwnsExactlyOneHold(t *testing.T) {
	repo, seed := auapiBillingFixture(t, 20)
	const n = 16
	type result struct {
		task    *service.AUAPIImageTaskRecord
		created bool
		err     error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			candidate := *seed
			candidate.TaskID = fmt.Sprintf("%s-%d", seed.TaskID, i)
			<-start
			task, created, err := repo.Create(context.Background(), &candidate)
			results <- result{task, created, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	winner := ""
	created := 0
	for r := range results {
		require.NoError(t, r.err)
		if r.created {
			created++
		}
		if winner == "" {
			winner = r.task.TaskID
		}
		require.Equal(t, winner, r.task.TaskID)
	}
	require.Equal(t, 1, created)
	assertAUAPIBalances(t, seed.UserID, 18, 2)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_balance_holds WHERE user_id=$1`, seed.UserID).Scan(&count))
	require.Equal(t, 1, count)
	changed := *seed
	changed.TaskID += "-changed"
	changed.RequestHash = "different"
	_, _, err := repo.Create(context.Background(), &changed)
	require.ErrorIs(t, err, service.ErrAUAPIImageConflict)
	assertAUAPIBalances(t, seed.UserID, 18, 2)
}
func TestAUAPIInsufficientBalanceRollsBackAcceptedTask(t *testing.T) {
	repo, task := auapiBillingFixture(t, 1)
	_, _, err := repo.Create(context.Background(), task)
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM auapi_image_tasks WHERE user_id=$1`, task.UserID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_balance_holds WHERE user_id=$1`, task.UserID).Scan(&count))
	require.Zero(t, count)
	assertAUAPIBalances(t, task.UserID, 1, 0)
}
func TestAUAPIFailedTaskReleaseUsesOriginalNamespaceAndDoesNotReleaseOtherTask(t *testing.T) {
	ctx := context.Background()
	repo, task := auapiBillingFixture(t, 20)
	_, _, err := repo.Create(ctx, task)
	require.NoError(t, err)
	other := *task
	other.TaskID += "-other"
	other.IdempotencyKey += "-other"
	_, _, err = repo.Create(ctx, &other)
	require.NoError(t, err)
	bill := NewUsageBillingRepository(nil, integrationDB)
	cmd := &service.BatchImageBalanceHoldCommand{RequestID: "auapi_image_release:" + task.TaskID, HoldRequestID: "auapi_image_hold:" + task.TaskID, APIKeyID: task.APIKeyID, UserID: task.UserID, BatchID: task.TaskID, HoldAmount: 2, RequestFingerprint: task.RequestHash}
	for i := 0; i < 2; i++ {
		_, err = bill.ReleaseBatchImageBalance(ctx, cmd)
		require.NoError(t, err)
	}
	assertAUAPIBalances(t, task.UserID, 18, 2)
	capture := *cmd
	capture.RequestID = "auapi_image_capture:" + task.TaskID
	capture.ActualAmount = 1
	_, err = bill.CaptureBatchImageBalance(ctx, &capture)
	require.Error(t, err)
	assertAUAPIBalances(t, task.UserID, 18, 2)
}
func TestAUAPILeaseSaveCannotUnlockOrOverwriteNewWorker(t *testing.T) {
	ctx := context.Background()
	repo, task := auapiBillingFixture(t, 20)
	_, _, err := repo.Create(ctx, task)
	require.NoError(t, err)
	first, err := repo.Claim(ctx, task.TaskID, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, first)
	first.Phase = "poll"
	require.NoError(t, repo.Save(ctx, first))
	second, err := repo.Claim(ctx, task.TaskID, time.Minute)
	require.NoError(t, err)
	require.Nil(t, second, "save must not release its active lease")
	_, err = integrationDB.ExecContext(ctx, `UPDATE auapi_image_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE task_id=$1`, task.TaskID)
	require.NoError(t, err)
	second, err = repo.Claim(ctx, task.TaskID, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.NotEqual(t, first.LeaseToken, second.LeaseToken)
	first.Phase = "fail"
	require.ErrorIs(t, repo.Save(ctx, first), service.ErrAUAPIImageLease)
	require.NoError(t, repo.ReleaseLease(ctx, first))
	third, err := repo.Claim(ctx, task.TaskID, time.Minute)
	require.NoError(t, err)
	require.Nil(t, third, "old release cannot clear new lease")
	second.Phase = "done"
	require.NoError(t, repo.Save(ctx, second))
	require.NoError(t, repo.ReleaseLease(ctx, second))
	got, err := repo.Get(ctx, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "done", got.Phase)
}
