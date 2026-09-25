//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSettlementReconciliationExactMismatchMissingAndBoundedSampling(t *testing.T) {
	ctx := context.Background()
	r, base, prototype := settlementFixture(t)
	before := time.Now().UTC().Truncate(time.Microsecond)
	cases := []struct {
		name   string
		amount float64
	}{{"good", 1}, {"amount", 1}, {"missing", 1}, {"identity", 1}, {"free", 0}, {"dedup", 1}, {"too-old", 1}}
	ids := map[string]int64{}
	logIDs := map[string]int64{}
	for _, tc := range cases {
		cmd := *base
		cmd.RequestID = "reconcile-" + tc.name + "-" + uuid.NewString()
		cmd.BalanceCost = tc.amount
		cmd.APIKeyQuotaCost = tc.amount
		log := *prototype
		log.ID = 0
		log.RequestID = "usage-" + uuid.NewString()
		log.ActualCost = tc.amount
		cmd.UsageDetail = &log
		_, err := r.Apply(ctx, &cmd)
		require.NoError(t, err)
		var id int64
		require.NoError(t, integrationDB.QueryRow(`SELECT id FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&id))
		ids[tc.name] = id
	}
	// Drain only four per pass as the production repository intentionally clamps.
	for i := 0; i < 3; i++ {
		_, err := r.DeliverSettledUsage(ctx, 100)
		require.NoError(t, err)
	}
	for name, id := range ids {
		var logID int64
		require.NoError(t, integrationDB.QueryRow(`SELECT usage_log_id FROM usage_settlement_receipts WHERE id=$1`, id).Scan(&logID))
		logIDs[name] = logID
	}
	_, err := integrationDB.Exec(`UPDATE usage_logs SET actual_cost=actual_cost+0.0000000001 WHERE id=$1`, logIDs["amount"])
	require.NoError(t, err)
	_, err = integrationDB.Exec(`DELETE FROM usage_logs WHERE id=$1`, logIDs["missing"])
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE usage_logs SET request_id='tampered-'||request_id WHERE id=$1`, logIDs["identity"])
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE usage_billing_dedup SET request_fingerprint='tampered' WHERE (request_id,api_key_id)=(SELECT request_id,api_key_id FROM usage_settlement_receipts WHERE id=$1)`, ids["dedup"])
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE usage_settlement_receipts SET delivered_at=NOW()-INTERVAL '10 minutes' WHERE id=$1`, ids["too-old"])
	require.NoError(t, err)
	// Exact window excludes any legitimate fixtures delivered before this test.
	result, err := reconcileRecentSettlements(ctx, integrationDB, 7, before.Add(5*time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(7), result.ScannedCount)
	require.Equal(t, int64(6), result.CheckedCount)
	require.Equal(t, int64(1), result.AmountMismatchCount)
	require.Equal(t, int64(1), result.DetailMissingCount)
	require.Equal(t, int64(1), result.IdentityMismatchCount)
	require.Equal(t, int64(1), result.DedupMismatchCount)
	amount, err := decimal.NewFromString(result.NetDiscrepancyUSD)
	require.NoError(t, err)
	require.True(t, amount.Equal(decimal.RequireFromString("0.0000000001")), result.NetDiscrepancyUSD)
	bounded, err := reconcileRecentSettlements(ctx, integrationDB, 2, before.Add(5*time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(2), bounded.ScannedCount)
	require.True(t, bounded.HasMore)
	require.Equal(t, ids["dedup"], bounded.LastID)
}

func TestSettlementReconciliationSubscriptionAllocationAndLegacyCoverage(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	c := testEntClient(t)
	group := mustCreateGroup(t, c, &service.Group{Name: "reconcile-sub-" + uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription})
	sub := mustCreateSubscription(t, c, &service.UserSubscription{UserID: cmd.UserID, GroupID: group.ID})
	t.Cleanup(func() { cleanupBillingFixture(t, cmd.UserID, []int64{cmd.AccountID}, []int64{group.ID}) })
	now := time.Now().UTC().Truncate(time.Microsecond)
	var lot int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO user_subscription_entitlements(user_subscription_id,status,starts_at,expires_at,source_type)VALUES($1,'active',$2,$3,'legacy')RETURNING id`, sub.ID, now.Add(-time.Hour), now.Add(time.Hour)).Scan(&lot))
	admission := "reconcile-admit-" + uuid.NewString()
	financial := "reconcile-fin-" + uuid.NewString()
	_, err := integrationDB.Exec(`INSERT INTO subscription_requests(request_key,subscription_id,api_key_id,status,lots,admitted_at,settled_at,billing_request_id,cost_usd)VALUES($1,$2,$3,'settled','[]',$4,$4,$5,1)`, admission, sub.ID, cmd.APIKeyID, now, financial)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`INSERT INTO subscription_usage_allocations(request_key,entitlement_id,cost_usd)VALUES($1,$2,1)`, admission, lot)
	require.NoError(t, err)
	// Read-audit fixture: synthetic source rows are inserted without changing real
	// money. This deliberately isolates independently mismatched stored evidence.
	log.SubscriptionID = &sub.ID
	log.BillingType = 1
	log.ActualCost = 1
	_, err = (&usageLogRepository{sql: integrationDB}).createSingle(ctx, integrationDB, log)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`INSERT INTO usage_billing_dedup(request_id,api_key_id,request_fingerprint)VALUES($1,$2,'sub-audit')`, financial, cmd.APIKeyID)
	require.NoError(t, err)
	var receipt int64
	raw := fmt.Sprintf(`{"SubscriptionAdmissionKey":%q}`, admission)
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO usage_settlement_receipts(request_id,usage_request_id,usage_log_id,request_fingerprint,user_id,api_key_id,account_id,subscription_id,billing_type,charged_amount,accounting_date,settled_at,delivered_at,state,record_source,record_completeness,command)VALUES($1,$2,$3,'sub-audit',$4,$5,$6,$7,1,1,($8::timestamptz AT TIME ZONE 'Asia/Shanghai')::date,$8,$8,'settled','live','complete',$9::jsonb)RETURNING id`, financial, log.RequestID, log.ID, cmd.UserID, cmd.APIKeyID, cmd.AccountID, sub.ID, now, raw).Scan(&receipt))
	good, err := r.ReconcileRecentSettlements(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), good.SubscriptionCheckedCount)
	require.Zero(t, good.SubscriptionMismatchCount)
	_, err = integrationDB.Exec(`UPDATE subscription_usage_allocations SET cost_usd=.75 WHERE request_key=$1`, admission)
	require.NoError(t, err)
	bad, err := r.ReconcileRecentSettlements(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), bad.SubscriptionMismatchCount)
	require.True(t, decimal.RequireFromString(bad.SubscriptionNetDiscrepancyUSD).Equal(decimal.RequireFromString("-.25")))
	// Legacy direct subscription counters have no admission identity: report them
	// as uncovered, not as an allocation mismatch (including genuine free usage).
	_, err = integrationDB.Exec(`UPDATE usage_settlement_receipts SET command='{}' WHERE id=$1`, receipt)
	require.NoError(t, err)
	legacy, err := r.ReconcileRecentSettlements(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, legacy.SubscriptionCheckedCount)
	require.Equal(t, int64(1), legacy.SubscriptionNotCheckedCount)
	require.Zero(t, legacy.SubscriptionMismatchCount)
}
