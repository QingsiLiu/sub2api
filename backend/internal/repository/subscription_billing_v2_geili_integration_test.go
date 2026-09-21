//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func ensureBillingV2Contract(t *testing.T, f entitlementFixture, now time.Time) *geilisub.Contract {
	t.Helper()
	ctx := context.Background()
	tx, err := f.c.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	tc := dbent.NewTxContext(ctx, tx)
	_, err = geilisub.LockParent(tc, tx.Client(), f.sub.ID)
	require.NoError(t, err)
	contract, err := geilisub.EnsureContract(tc, tx.Client(), f.sub.ID, now)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return contract
}

func TestSubscriptionBillingV2RegressionAndSharedKeys(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	now := time.Now()
	day := geilisub.DayStart(now)
	lot := f.sub.Entitlements[0]
	require.NoError(t, f.c.UserSubscriptionEntitlement.UpdateOneID(lot.ID).SetStartsAt(now.AddDate(0, 0, -6)).SetDailyLimitUsd(45).SetWeeklyLimitUsd(315).SetDailyWindowStart(day).SetWeeklyWindowStart(now.AddDate(0, 0, -6)).SetDailyUsageUsd(41.92257312).SetWeeklyUsageUsd(315).Exec(ctx))
	require.NoError(t, f.c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(f.sub.ID).SetPlanID(f.plan.ID).SetStartsAt(now.AddDate(0, 0, -2)).SetExpiresAt(now.AddDate(0, 0, 28)).SetDailyLimitUsd(45).SetWeeklyLimitUsd(315).SetDailyWindowStart(day).SetWeeklyWindowStart(now.AddDate(0, 0, -2)).SetDailyUsageUsd(45.126).SetWeeklyUsageUsd(136.224).Exec(ctx))
	contract := ensureBillingV2Contract(t, f, now)
	require.Equal(t, "legacy_daily", contract.Mode)
	fresh, err := NewUserSubscriptionRepository(f.c).GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	require.InDelta(t, 2.95142688, *fresh.QuotaSummary.RemainingUSD, 1e-10)
	require.Nil(t, fresh.QuotaSummary.WeeklyLimitUSD)
	secondKey := mustCreateApiKey(t, f.c, &service.APIKey{UserID: f.user.ID, GroupID: f.key.GroupID, SubscriptionID: &f.sub.ID, BillingSource: "subscription", Key: "synthetic-v2-" + uuid.NewString(), Name: "second"})
	for _, keyID := range []int64{f.key.ID, secondKey.ID, f.key.ID} {
		admitted, err := f.svc.AdmitConsumption(ctx, fresh, keyID)
		require.NoError(t, err)
		cmd := &service.UsageBillingCommand{RequestID: uuid.NewString(), APIKeyID: keyID, UserID: f.user.ID, AccountID: f.account.ID, AccountType: "apikey", SubscriptionID: &f.sub.ID, SubscriptionAdmissionKey: admitted.AdmissionKey, SubscriptionCost: 1}
		result, err := NewUsageBillingRepository(f.c, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		require.True(t, result.Applied)
		result, err = NewUsageBillingRepository(f.c, integrationDB).Apply(ctx, cmd)
		require.NoError(t, err)
		require.False(t, result.Applied)
	}
	_, err = f.svc.AdmitConsumption(ctx, fresh, secondKey.ID)
	require.ErrorIs(t, err, service.ErrDailyLimitExceeded)
	used, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, now)
	require.NoError(t, err)
	require.InDelta(t, 90.04857312, used, 1e-10)
	used, err = geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, day.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Zero(t, used)
}

func TestSubscriptionBillingV2LateLegacyAllocationReadAndReplay(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	now := time.Now()
	contract := ensureBillingV2Contract(t, f, now)
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	lots[0].Normalize(now, true)
	raw, err := json.Marshal(lots)
	require.NoError(t, err)
	key := uuid.NewString()
	// An old process can admit without a contract binding after migration.
	require.NoError(t, f.c.SubscriptionRequest.Create().SetRequestKey(key).SetSubscriptionID(f.sub.ID).SetAPIKeyID(f.key.ID).SetLots(raw).SetAdmittedAt(now).Exec(ctx))
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO subscription_usage_allocations(request_key,entitlement_id,cost_usd,daily_window_start) VALUES($1,$2,4,$3)`, key, lots[0].ID, geilisub.DayStart(now))
	require.NoError(t, err)
	fresh, err := NewUserSubscriptionRepository(f.c).GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 4.0, fresh.DailyUsageUSD, "read projection includes old process's unbridged allocation")
	for i := 0; i < 2; i++ {
		tx, err := integrationDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, geilisub.LockSubscription(ctx, tx, f.sub.ID))
		require.NoError(t, geilisub.SyncDailyLedger(ctx, tx, f.sub.ID, now))
		require.NoError(t, tx.Commit())
	}
	used, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, contract.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 4.0, used)
	require.ErrorIs(t, NewUserSubscriptionRepository(f.c).IncrementUsage(ctx, f.sub.ID, 2), geilisub.ErrAdmissionRequired, "billing without original admission cannot guess a term")
}

func TestSubscriptionBillingV2LateSettlementCannotChargeNewTerm(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, f.c.SubscriptionPlan.UpdateOneID(f.plan.ID).SetDailyLimitUsd(90).Exec(ctx))
	require.NoError(t, f.c.UserSubscriptionEntitlement.UpdateOneID(f.sub.Entitlements[0].ID).SetDailyLimitUsd(90).Exec(ctx))
	old := ensureBillingV2Contract(t, f, now)
	require.Equal(t, "v2", old.Mode)
	admitted, err := f.svc.AdmitConsumption(ctx, f.sub, f.key.ID)
	require.NoError(t, err)
	// Re-purchase after natural expiry creates a new identity on the same sub ID.
	future := old.ExpiresAt.Add(time.Hour)
	plan := geilisub.Plan{ID: f.plan.ID, Name: f.plan.Name, Kind: "month", DailyUSD: 90, PeriodDays: 30, Price: decimal.NewFromInt(10)}
	change, err := geilisub.PreviewContract(nil, geilisub.Plan{}, plan, "purchase", 1, 0, future)
	require.NoError(t, err)
	change.After.SubscriptionID = f.sub.ID
	change.After.UserID = f.user.ID
	tx, err := f.c.Tx(ctx)
	require.NoError(t, err)
	tc := dbent.NewTxContext(ctx, tx)
	next, err := geilisub.ApplyContractChange(tc, tx.Client(), change, 0, "admin", "synthetic-new-term", 0, future)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.NotEqual(t, old.TermID, next.TermID)
	sqlTx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, geilisub.SettleAdmission(ctx, sqlTx, admitted.AdmissionKey, "late-"+uuid.NewString(), f.sub.ID, f.key.ID, 3, future.Add(time.Hour)))
	require.NoError(t, sqlTx.Commit())
	oldUsage, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, old.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 3.0, oldUsage)
	newUsage, err := geilisub.ReadDailyUsage(ctx, f.c, f.sub.ID, next.TermID, future)
	require.NoError(t, err)
	require.Zero(t, newUsage)
	require.Equal(t, f.sub.ID, next.SubscriptionID)
}

func TestSubscriptionBillingV2AdminWholeExpiryAndReset(t *testing.T) {
	f := newEntitlementFixture(t)
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, f.c.SubscriptionPlan.UpdateOneID(f.plan.ID).SetDailyLimitUsd(90).Exec(ctx))
	require.NoError(t, f.c.UserSubscriptionEntitlement.UpdateOneID(f.sub.Entitlements[0].ID).SetDailyLimitUsd(90).Exec(ctx))
	contract := ensureBillingV2Contract(t, f, now)
	plan := geilisub.Plan{ID: f.plan.ID, Name: f.plan.Name, Kind: "month", DailyUSD: 90, PeriodDays: 30, Price: decimal.NewFromInt(10)}
	change, err := geilisub.PreviewContract(contract, plan, plan, "stack", 1, 0, now)
	require.NoError(t, err)
	tx, err := f.c.Tx(ctx)
	require.NoError(t, err)
	tc := dbent.NewTxContext(ctx, tx)
	contract, err = geilisub.ApplyContractChange(tc, tx.Client(), change, 0, "admin", "synthetic-stack", 0, now)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	repo := NewUserSubscriptionRepository(f.c).(*userSubscriptionRepository)
	lots, err := geilisub.ReadLots(ctx, f.c, f.sub.ID)
	require.NoError(t, err)
	require.ErrorIs(t, repo.AdjustEntitlements(ctx, f.sub.ID, []int64{lots[0].ID}, 7, 1), geilisub.ErrSelection)
	require.NoError(t, repo.AdjustEntitlements(ctx, f.sub.ID, nil, 7, 1))
	adjusted, err := repo.GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, contract.Revision+1, adjusted.Contract.Revision)
	require.True(t, contract.ExpiresAt.AddDate(0, 0, 7).Equal(adjusted.Contract.ExpiresAt))
	for _, lot := range adjusted.Entitlements {
		require.True(t, lot.ExpiresAt.Equal(adjusted.Contract.ExpiresAt))
	}
	admitted, err := f.svc.AdmitConsumption(ctx, adjusted, f.key.ID)
	require.NoError(t, err)
	sqlTx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, geilisub.SettleAdmission(ctx, sqlTx, admitted.AdmissionKey, uuid.NewString(), f.sub.ID, f.key.ID, 3, now))
	require.NoError(t, sqlTx.Commit())
	late, err := f.svc.AdmitConsumption(ctx, adjusted, f.key.ID)
	require.NoError(t, err)
	require.NoError(t, repo.ResetUsageWindows(ctx, f.sub.ID, true, false, false, now, now))
	sqlTx, err = integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, geilisub.SettleAdmission(ctx, sqlTx, late.AdmissionKey, uuid.NewString(), f.sub.ID, f.key.ID, 2, now))
	require.NoError(t, sqlTx.Commit())
	final, err := repo.GetByID(ctx, f.sub.ID)
	require.NoError(t, err)
	require.Equal(t, 2.0, final.DailyUsageUSD, "an admitted request settling after admin reset is still billed")
	total := 0.0
	for _, lot := range final.Entitlements {
		total += lot.LifetimeUsageUSD
	}
	require.Equal(t, 5.0, total, "admin resets retain financial evidence")
	require.Greater(t, final.Contract.Revision, adjusted.Contract.Revision)
}
