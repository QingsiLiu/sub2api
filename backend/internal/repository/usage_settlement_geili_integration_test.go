//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func settlementFixture(t *testing.T) (*usageBillingRepository, *service.UsageBillingCommand, *service.UsageLog) {
	t.Helper()
	c := testEntClient(t)
	user := mustCreateUser(t, c, &service.User{Email: "settlement-" + uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 100})
	key := mustCreateApiKey(t, c, &service.APIKey{UserID: user.ID, Key: "sk-" + uuid.NewString(), Name: "settlement"})
	account := mustCreateAccount(t, c, &service.Account{Name: "settlement-" + uuid.NewString(), Type: service.AccountTypeAPIKey})
	cmd := &service.UsageBillingCommand{RequestID: "financial-" + uuid.NewString(), APIKeyID: key.ID, UserID: user.ID, AccountID: account.ID, AccountType: service.AccountTypeAPIKey, BalanceCost: 1.25, APIKeyQuotaCost: 1.25, Model: "test-model"}
	log := &service.UsageLog{RequestID: "usage-" + uuid.NewString(), APIKeyID: key.ID, UserID: user.ID, AccountID: account.ID, Model: "test-model", RequestedModel: "test-model", ActualCost: 1.25, TotalCost: 2.5, InputTokens: 20, OutputTokens: 5, RateMultiplier: .5, CreatedAt: time.Now()}
	registerBillingFixtureCleanup(t, user.ID, account.ID)
	return &usageBillingRepository{db: integrationDB}, cmd, log
}

func TestSettlementPreparedSurvivesRestartAndDeliversOnce(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.UserID).Scan(&balance))
	require.Equal(t, 100.0, balance)
	// New repository simulates a restart: only persisted command/detail survive.
	restarted := &usageBillingRepository{db: integrationDB}
	applied, err := restarted.ProcessPendingSettlements(ctx, 128)
	require.NoError(t, err)
	found := false
	for _, a := range applied {
		if a.Command.RequestID == cmd.RequestID {
			found = true
		}
	}
	require.True(t, found)
	_, err = restarted.DeliverSettledUsage(ctx, 128)
	require.NoError(t, err)
	_, err = restarted.Apply(ctx, cmd)
	require.NoError(t, err)
	_, err = restarted.DeliverSettledUsage(ctx, 128)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.UserID).Scan(&balance))
	require.Equal(t, 98.75, balance)
	var n int
	var amount float64
	var tokens int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*),MAX(actual_cost),MAX(input_tokens) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, log.RequestID, cmd.APIKeyID).Scan(&n, &amount, &tokens))
	require.Equal(t, 1, n)
	require.Equal(t, 1.25, amount)
	require.Equal(t, 20, tokens)
	var usageID int64
	var delivered sql.NullTime
	require.NoError(t, integrationDB.QueryRow(`SELECT usage_log_id,delivered_at FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&usageID, &delivered))
	require.Positive(t, usageID)
	require.True(t, delivered.Valid)
}

func TestSettlementReceiptConflictChecksMoneyEvenWithSameFingerprint(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	cmd.RequestFingerprint = "caller-supplied"
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	changed := *cmd
	changed.BalanceCost = 2
	require.ErrorIs(t, r.PrepareSettlement(ctx, &changed, log), service.ErrUsageBillingRequestConflict)
	changed = *cmd
	changed.UserID++
	require.ErrorIs(t, r.PrepareSettlement(ctx, &changed, nil), service.ErrUsageBillingRequestConflict)
	_, err := r.Apply(ctx, cmd)
	require.NoError(t, err)
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&n))
	require.Equal(t, 1, n)
}

func TestSettlementLogLockDoesNotBlockDebitAndZeroPlaceholderRepaired(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	empty := *log
	empty.ActualCost = 0
	empty.InputTokens = 0
	_, err := (&usageLogRepository{sql: integrationDB}).CreateDirect(ctx, &empty)
	require.NoError(t, err)
	lock, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = lock.Exec(`SELECT id FROM usage_logs WHERE id=$1 FOR UPDATE`, empty.ID)
	require.NoError(t, err)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	fast, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	result, err := r.Apply(fast, cmd)
	require.NoError(t, err)
	require.True(t, result.Applied)
	var id int64
	require.NoError(t, integrationDB.QueryRow(`UPDATE usage_settlement_receipts SET delivery_lease_token='test' WHERE request_id=$1 AND api_key_id=$2 RETURNING id`, cmd.RequestID, cmd.APIKeyID).Scan(&id))
	blocked, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	err = r.deliverSettlementUsage(blocked, id, "test")
	stop()
	require.Error(t, err)
	require.NoError(t, lock.Rollback())
	require.NoError(t, r.deliverSettlementUsage(ctx, id, "test"))
	var amount float64
	var tokens int
	require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost,input_tokens FROM usage_logs WHERE id=$1`, empty.ID).Scan(&amount, &tokens))
	require.Equal(t, 1.25, amount)
	require.Equal(t, 20, tokens)
}

func TestSettlementDeliveryRefusesWrongNonzeroAmount(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	bad := *log
	bad.ActualCost = 99
	_, err := (&usageLogRepository{sql: integrationDB}).CreateDirect(ctx, &bad)
	require.NoError(t, err)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	_, err = r.Apply(ctx, cmd)
	require.NoError(t, err)
	var id int64
	require.NoError(t, integrationDB.QueryRow(`UPDATE usage_settlement_receipts SET delivery_lease_token='conflict' WHERE request_id=$1 AND api_key_id=$2 RETURNING id`, cmd.RequestID, cmd.APIKeyID).Scan(&id))
	require.ErrorIs(t, r.deliverSettlementUsage(ctx, id, "conflict"), service.ErrUsageBillingRequestConflict)
	var amount float64
	require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost FROM usage_logs WHERE id=$1`, bad.ID).Scan(&amount))
	require.Equal(t, 99.0, amount)
}

func TestSettlementConcurrentApplyAndDeliveryNoDuplicateDebit(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); copy := *cmd; _, err := r.Apply(ctx, &copy); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.UserID).Scan(&balance))
	require.Equal(t, 98.75, balance)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = r.DeliverSettledUsage(ctx, 128) }()
	}
	wg.Wait()
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, log.RequestID, cmd.APIKeyID).Scan(&n))
	require.Equal(t, 1, n)
}

func TestSettlementTransactionFailureLeavesPendingWithoutDebit(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	// A zero statement timeout holds the original user while the settlement
	// deadline expires. Debit, dedup, and settled marker must all roll back.
	lock, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = lock.Exec(`SELECT id FROM users WHERE id=$1 FOR UPDATE`, cmd.UserID)
	require.NoError(t, err)
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = r.Apply(short, cmd)
	cancel()
	require.Error(t, err)
	require.NoError(t, lock.Rollback())
	var state string
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT state FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&state))
	require.Equal(t, "pending", state)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.UserID).Scan(&balance))
	require.Equal(t, 100.0, balance)
	_, err = r.Apply(ctx, cmd)
	require.NoError(t, err)
}

func TestSettlementHoldCaptureReleaseMutuallyExclusive(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	batch := uuid.NewString()
	holdID := "auapi_image_hold:" + batch
	hold := &service.BatchImageBalanceHoldCommand{RequestID: holdID, HoldRequestID: holdID, APIKeyID: cmd.APIKeyID, UserID: cmd.UserID, BatchID: batch, HoldAmount: 10}
	_, err := r.ReserveBatchImageBalance(ctx, hold)
	require.NoError(t, err)
	capture := *hold
	capture.RequestID = "auapi_image_capture:" + batch
	capture.RequestFingerprint = ""
	capture.ActualAmount = 1.25
	capture.UsageDetail = log
	_, err = r.CaptureBatchImageBalance(ctx, &capture)
	require.NoError(t, err)
	release := *hold
	release.RequestID = "auapi_image_release:" + batch
	release.RequestFingerprint = ""
	_, err = r.ReleaseBatchImageBalance(ctx, &release)
	require.Error(t, err)
	_, err = r.CaptureBatchImageBalance(ctx, &capture)
	require.NoError(t, err)
	var balance, frozen float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, cmd.UserID).Scan(&balance, &frozen))
	require.Equal(t, 98.75, balance)
	require.Zero(t, frozen)
	t.Log(fmt.Sprintf("hold %s captured once with durable detail", batch))
}

func TestSettlementLeaseReclaimAndHealthThresholds(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	_, err := integrationDB.Exec(`UPDATE usage_settlement_receipts SET settlement_lease_token='dead-worker',settlement_lease_until=NOW()+INTERVAL '1 minute',created_at=NOW()-INTERVAL '3 minutes' WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID)
	require.NoError(t, err)
	applied, err := r.ProcessPendingSettlements(ctx, 128)
	require.NoError(t, err)
	for _, a := range applied {
		require.NotEqual(t, cmd.RequestID, a.Command.RequestID)
	}
	h, err := r.SettlementHealth(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, h.PendingSettlements, int64(1))
	require.GreaterOrEqual(t, h.Warning30Seconds, int64(1))
	require.GreaterOrEqual(t, h.Critical120Seconds, int64(1))
	_, err = integrationDB.Exec(`UPDATE usage_settlement_receipts SET settlement_lease_until=NOW()-INTERVAL '1 second' WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID)
	require.NoError(t, err)
	applied, err = r.ProcessPendingSettlements(ctx, 128)
	require.NoError(t, err)
	found := false
	for _, a := range applied {
		if a.Command.RequestID == cmd.RequestID {
			found = true
		}
	}
	require.True(t, found)
	_, err = r.DeliverSettledUsage(ctx, 128)
	require.NoError(t, err)
}

func TestSettlementPlatformQuotaIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	_, err := integrationDB.Exec(`INSERT INTO user_platform_quotas(user_id,platform,daily_limit_usd,weekly_limit_usd,monthly_limit_usd,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,created_at,updated_at)VALUES($1,'openai',50,100,500,0,0,0,NOW(),NOW())`, cmd.UserID)
	require.NoError(t, err)
	cmd.Platform = "openai"
	cmd.PlatformQuotaCost = 1.25
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	_, err = r.Apply(ctx, cmd)
	require.NoError(t, err)
	_, err = r.Apply(ctx, cmd)
	require.NoError(t, err)
	var day, week, month float64
	require.NoError(t, integrationDB.QueryRow(`SELECT daily_usage_usd,weekly_usage_usd,monthly_usage_usd FROM user_platform_quotas WHERE user_id=$1 AND platform='openai'`, cmd.UserID).Scan(&day, &week, &month))
	require.Equal(t, 1.25, day)
	require.Equal(t, day, week)
	require.Equal(t, day, month)
}

func TestSettlementSubscriptionAccountingDateIsAdmissionDate(t *testing.T) {
	ctx := context.Background()
	r, cmd, _ := settlementFixture(t)
	c := testEntClient(t)
	group := mustCreateGroup(t, c, &service.Group{Name: "settlement-sub-" + uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription})
	sub := mustCreateSubscription(t, c, &service.UserSubscription{UserID: cmd.UserID, GroupID: group.ID})
	t.Cleanup(func() { cleanupBillingFixture(t, cmd.UserID, []int64{cmd.AccountID}, []int64{group.ID}) })
	admitted := time.Date(2026, 9, 24, 15, 59, 0, 0, time.UTC)
	key := uuid.NewString()
	_, err := integrationDB.Exec(`INSERT INTO subscription_requests(request_key,subscription_id,api_key_id,status,lots,admitted_at)VALUES($1,$2,$3,'admitted','[]',$4)`, key, sub.ID, cmd.APIKeyID, admitted)
	require.NoError(t, err)
	cmd.SubscriptionID = &sub.ID
	cmd.SubscriptionAdmissionKey = key
	cmd.SubscriptionCost = cmd.BalanceCost
	cmd.BalanceCost = 0
	cmd.CompletedAt = admitted.Add(2 * time.Minute)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, nil))
	var day string
	require.NoError(t, integrationDB.QueryRow(`SELECT accounting_date::text FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&day))
	require.Equal(t, "2026-09-24", day)
	// Marking settled after midnight must retain the earlier admitted day.
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	var id int64
	require.NoError(t, tx.QueryRow(`SELECT id FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&id))
	require.NoError(t, markSettlementSettledTx(ctx, tx, id))
	require.NoError(t, tx.Commit())
	require.NoError(t, integrationDB.QueryRow(`SELECT accounting_date::text FROM usage_settlement_receipts WHERE id=$1`, id).Scan(&day))
	require.Equal(t, "2026-09-24", day)
}

func TestSettlementReceiptRemainsIdempotencyAuthorityAfterDedupArchiveLoss(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	_, err := r.Apply(ctx, cmd)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`DELETE FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID)
	require.NoError(t, err)
	result, err := r.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result.Applied)
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.UserID).Scan(&balance))
	require.Equal(t, 98.75, balance)
}

func TestSettlementRejectsTwoFinancialIdentitiesForOneUsage(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	other := *cmd
	other.RequestID = uuid.NewString()
	require.Error(t, r.PrepareSettlement(ctx, &other, log))
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.UserID).Scan(&balance))
	require.Equal(t, 100.0, balance)
}

func TestSettlementFreeCaptureWithoutHoldStillDeliversZeroLog(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	log.ActualCost = 0
	log.TotalCost = 0
	capture := &service.BatchImageBalanceHoldCommand{RequestID: uuid.NewString(), BatchID: uuid.NewString(), UserID: cmd.UserID, APIKeyID: cmd.APIKeyID, UsageDetail: log}
	result, err := r.CaptureBatchImageBalance(ctx, capture)
	require.NoError(t, err)
	require.True(t, result.Applied)
	_, err = r.DeliverSettledUsage(ctx, 128)
	require.NoError(t, err)
	var amount float64
	require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, log.RequestID, cmd.APIKeyID).Scan(&amount))
	require.Zero(t, amount)
}

func TestSettlementTerminalFailureHasEvidenceWithoutUndeliverableDetail(t *testing.T) {
	ctx := context.Background()
	r, cmd, _ := settlementFixture(t)
	cmd.BalanceCost = 0
	cmd.APIKeyQuotaCost = 0
	cmd.TerminalFailure = true
	_, err := r.Apply(ctx, cmd)
	require.NoError(t, err)
	var amount float64
	var delivered sql.NullTime
	var usageID sql.NullInt64
	require.NoError(t, integrationDB.QueryRow(`SELECT charged_amount,delivered_at,usage_log_id FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&amount, &delivered, &usageID))
	require.Zero(t, amount)
	require.True(t, delivered.Valid)
	require.False(t, usageID.Valid)
	changed := *cmd
	changed.BalanceCost = 1
	require.Error(t, r.PrepareSettlement(ctx, &changed, nil))
}

func TestSettlementLegacyAuapiReleaseFailsClosed(t *testing.T) {
	ctx := context.Background()
	r, cmd, _ := settlementFixture(t)
	batch := uuid.NewString()
	holdID := "auapi_image_hold:" + batch
	for _, id := range []string{holdID, "auapi_image_release:" + batch} {
		_, err := integrationDB.Exec(`INSERT INTO usage_billing_dedup(request_id,api_key_id,request_fingerprint)VALUES($1,$2,'old')`, id, cmd.APIKeyID)
		require.NoError(t, err)
	}
	_, err := r.ReleaseBatchImageBalance(ctx, &service.BatchImageBalanceHoldCommand{RequestID: "auapi_image_release:" + batch, HoldRequestID: holdID, UserID: cmd.UserID, APIKeyID: cmd.APIKeyID, BatchID: batch, HoldAmount: 1, RequestFingerprint: "old"})
	require.ErrorContains(t, err, "requires financial reconciliation")
}

func TestSettlementFreeReceiptRestoresFailedZeroMetadata(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	cmd.BalanceCost = 0
	cmd.APIKeyQuotaCost = 0
	log.ActualCost = 0
	empty := *log
	empty.InputTokens = 0
	empty.OutputTokens = 0
	_, err := (&usageLogRepository{sql: integrationDB}).CreateDirect(ctx, &empty)
	require.NoError(t, err)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	_, err = r.Apply(ctx, cmd)
	require.NoError(t, err)
	_, err = r.DeliverSettledUsage(ctx, 128)
	require.NoError(t, err)
	var tokens int
	var amount float64
	require.NoError(t, integrationDB.QueryRow(`SELECT input_tokens,actual_cost FROM usage_logs WHERE id=$1`, empty.ID).Scan(&tokens, &amount))
	require.Equal(t, 20, tokens)
	require.Zero(t, amount)
}

func TestSettlementLegacyFingerprintAloneCannotManufactureAmount(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	cmd.Normalize()
	_, err := integrationDB.Exec(`INSERT INTO usage_billing_dedup(request_id,api_key_id,request_fingerprint,created_at)VALUES($1,$2,$3,NOW()-INTERVAL '1 day')`, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
	require.NoError(t, err)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	_, err = r.Apply(ctx, cmd)
	require.ErrorContains(t, err, "no independently verified")
	var state string
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT state FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&state))
	require.Equal(t, "pending", state)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, cmd.UserID).Scan(&balance))
	require.Equal(t, 100.0, balance)
}

func TestSettlementLegacyRetryPreservesOriginalMonetaryEvidence(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	cmd.Normalize()
	original := time.Date(2026, 9, 23, 17, 0, 0, 0, time.UTC)
	log.CreatedAt = original
	_, err := integrationDB.Exec(`INSERT INTO usage_billing_dedup(request_id,api_key_id,request_fingerprint,created_at)VALUES($1,$2,$3,$4)`, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint, original)
	require.NoError(t, err)
	_, err = (&usageLogRepository{sql: integrationDB}).CreateDirect(ctx, log)
	require.NoError(t, err)
	retry := *log
	retry.InputTokens = 999
	retry.CreatedAt = time.Now()
	require.NoError(t, r.PrepareSettlement(ctx, cmd, &retry))
	result, err := r.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result.Applied)
	var day string
	var tokens int
	require.NoError(t, integrationDB.QueryRow(`SELECT accounting_date::text,(detail->>'input_tokens')::int FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&day, &tokens))
	require.Equal(t, "2026-09-24", day)
	require.Equal(t, 20, tokens)
}

func TestSettlementSimpleWindowOnlyPreservesStandardPriceWithoutChargingMoney(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	cmd.BalanceCost = 0
	cmd.SubscriptionCost = 0
	cmd.APIKeyQuotaCost = 0
	cmd.AccountQuotaCost = 0
	cmd.PlatformQuotaCost = 0
	cmd.APIKeyRateLimitCost = 1.25
	log.ActualCost = 1.25
	log.TotalCost = 2.5
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	_, err := r.Apply(ctx, cmd)
	require.NoError(t, err)
	_, err = r.Apply(ctx, cmd)
	require.NoError(t, err)
	_, err = r.DeliverSettledUsage(ctx, 4)
	require.NoError(t, err)
	var balance, quota, w5, w1, w7 float64
	require.NoError(t, integrationDB.QueryRow(`SELECT u.balance,k.quota_used,k.usage_5h,k.usage_1d,k.usage_7d FROM users u JOIN api_keys k ON k.user_id=u.id WHERE k.id=$1`, cmd.APIKeyID).Scan(&balance, &quota, &w5, &w1, &w7))
	require.Equal(t, 100.0, balance)
	require.Zero(t, quota)
	require.Equal(t, 1.25, w5)
	require.Equal(t, w5, w1)
	require.Equal(t, w5, w7)
	var charged, standard, detailMoney, windowAmount float64
	require.NoError(t, integrationDB.QueryRow(`SELECT charged_amount,(detail->>'total_cost')::numeric,(detail->>'actual_cost')::numeric,(command->>'APIKeyRateLimitCost')::numeric FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&charged, &standard, &detailMoney, &windowAmount))
	require.Zero(t, charged)
	require.Zero(t, detailMoney)
	require.Equal(t, 2.5, standard)
	require.Equal(t, 1.25, windowAmount)
	var rawMoney, rawStandard float64
	require.NoError(t, integrationDB.QueryRow(`SELECT actual_cost,total_cost FROM usage_logs WHERE request_id=$1 AND api_key_id=$2`, log.RequestID, cmd.APIKeyID).Scan(&rawMoney, &rawStandard))
	require.Zero(t, rawMoney)
	require.Equal(t, 2.5, rawStandard)
}

func TestSettlementRetryDifferentAdmissionUsesFirstPersistedDay(t *testing.T) {
	f, contract := v2StandardFixture(t)
	ctx := context.Background()
	// Both calendar days belong to this paid term; only the request admission
	// differs, not which entitlement can be charged.
	_, err := integrationDB.Exec(`UPDATE user_subscriptions SET starts_at=NOW()-INTERVAL '3 days' WHERE id=$1`, f.sub.ID)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE user_subscription_entitlements SET starts_at=NOW()-INTERVAL '3 days' WHERE user_subscription_id=$1`, f.sub.ID)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE subscription_contracts SET starts_at=NOW()-INTERVAL '3 days' WHERE subscription_id=$1`, f.sub.ID)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE subscription_contract_terms SET starts_at=NOW()-INTERVAL '3 days' WHERE subscription_id=$1`, f.sub.ID)
	require.NoError(t, err)
	a, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	b, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	first := v2TestCommand(f, a, f.key.ID, 1.25)
	yesterday := time.Now().Add(-24 * time.Hour).Truncate(time.Microsecond)
	_, err = integrationDB.Exec(`UPDATE subscription_requests SET admitted_at=$2 WHERE request_key=$1`, a.AdmissionKey, yesterday)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE subscription_request_contracts SET usage_date=($2::timestamptz AT TIME ZONE 'Asia/Shanghai')::date WHERE request_key=$1`, a.AdmissionKey, yesterday)
	require.NoError(t, err)
	repo := &usageBillingRepository{db: integrationDB}
	require.NoError(t, repo.PrepareSettlement(ctx, first, nil))
	retry := *first
	retry.SubscriptionAdmissionKey = b.AdmissionKey
	result, err := repo.Apply(ctx, &retry)
	require.NoError(t, err)
	require.True(t, result.Applied)
	var stateA, stateB, date string
	var costA, costB float64
	require.NoError(t, integrationDB.QueryRow(`SELECT status,cost_usd FROM subscription_requests WHERE request_key=$1`, a.AdmissionKey).Scan(&stateA, &costA))
	require.NoError(t, integrationDB.QueryRow(`SELECT status,cost_usd FROM subscription_requests WHERE request_key=$1`, b.AdmissionKey).Scan(&stateB, &costB))
	require.Equal(t, "settled", stateA)
	require.Equal(t, 1.25, costA)
	require.Equal(t, "deduplicated", stateB)
	require.Zero(t, costB)
	require.NoError(t, integrationDB.QueryRow(`SELECT accounting_date::text FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, first.RequestID, first.APIKeyID).Scan(&date))
	require.Equal(t, yesterday.In(time.FixedZone("Beijing", 8*3600)).Format("2006-01-02"), date)
	var original, other float64
	require.NoError(t, integrationDB.QueryRow(`SELECT COALESCE(SUM(used_usd) FILTER(WHERE usage_date=$3::date),0),COALESCE(SUM(used_usd) FILTER(WHERE usage_date<>$3::date),0) FROM subscription_daily_usage WHERE subscription_id=$1 AND term_id=$2`, f.sub.ID, contract.TermID, date).Scan(&original, &other))
	require.Equal(t, 1.25, original)
	require.Zero(t, other)
	_, err = repo.Apply(ctx, first)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM usage_settlement_receipts WHERE user_id=$1`, f.user.ID)
		require.NoError(t, err)
	})
}
