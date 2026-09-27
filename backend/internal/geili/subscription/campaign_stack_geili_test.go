package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestCampaignStackEligibilityAndPrice(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	daily := 45.0
	lot := Lot{ID: 1, SourceType: "campaign", Status: "active", StartsAt: now.Add(-4 * 24 * time.Hour), ExpiresAt: now.Add(3 * 24 * time.Hour), DailyLimitUSD: &daily}
	current := &Contract{SubscriptionID: 1, UserID: 1, Mode: ContractModeLegacy, Status: "active", StartsAt: lot.StartsAt, ExpiresAt: lot.ExpiresAt, TermID: "gift", Revision: 2}
	plan := Plan{ID: 1, Kind: ContractKindWeek, DailyUSD: 90, PeriodDays: 7, Price: decimal.RequireFromString("46.99")}
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		units     int
		amount    string
		days      int
	}{
		{"three-days", 72 * time.Hour, 1, "20.14", 3},
		{"two-units", 72 * time.Hour, 2, "40.28", 3},
		{"partial-day", time.Second, 1, "6.71", 1},
		{"partial-fourth-day", 72*time.Hour + time.Second, 1, "26.85", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gift := lot
			gift.ExpiresAt = now.Add(tc.remaining)
			gift.StartsAt = gift.ExpiresAt.Add(-7 * 24 * time.Hour)
			candidate := CampaignStackCandidate(current, []Lot{gift}, now)
			require.NotNil(t, candidate)
			change, err := PreviewCampaignStack(current, candidate, plan, tc.units, 0, now)
			require.NoError(t, err)
			require.Equal(t, tc.amount, change.Amount.StringFixed(2))
			require.Equal(t, tc.days, change.BillableDays)
			require.Equal(t, tc.units, change.After.Quantity)
			require.True(t, gift.ExpiresAt.Equal(change.After.ExpiresAt))
			require.Equal(t, current.TermID, change.After.TermID)
		})
	}
	for _, change := range []func(*Lot){
		func(l *Lot) { l.Status = "suspended" }, func(l *Lot) { l.Status = "refund_pending" },
		func(l *Lot) { l.SourceType = "payment" }, func(l *Lot) { l.StartsAt = now.Add(time.Hour); l.ExpiresAt = l.StartsAt.Add(7 * 24 * time.Hour) },
		func(l *Lot) { l.StartsAt = l.StartsAt.Add(-time.Hour) },
	} {
		other := lot
		change(&other)
		require.Nil(t, CampaignStackCandidate(current, []Lot{lot, other}, now))
	}
	expired := lot
	expired.SourceType = "payment"
	expired.ExpiresAt = now.Add(-time.Second)
	require.NotNil(t, CampaignStackCandidate(current, []Lot{expired, lot}, now))
	paid := *current
	paid.Mode = ContractModeV2
	require.Nil(t, CampaignStackCandidate(&paid, []Lot{lot}, now))
	paid.ExpiresAt = now.Add(-time.Second)
	require.NotNil(t, CampaignStackCandidate(&paid, []Lot{expired, lot}, now))
	later := lot
	later.ID = 2
	later.StartsAt = later.StartsAt.Add(time.Hour)
	later.ExpiresAt = later.ExpiresAt.Add(time.Hour)
	candidate := CampaignStackCandidate(current, []Lot{later, lot}, now)
	require.Equal(t, 90.0, candidate.GiftDailyUSD)
	require.True(t, later.ExpiresAt.Equal(candidate.ExpiresAt))
	lot.DailyUsageUSD = 44
	lot.LifetimeUsageUSD = 400
	require.True(t, SameCampaignStack(candidate, CampaignStackCandidate(current, []Lot{lot, later}, now)))
	month := plan
	month.Kind = ContractKindMonth
	month.PeriodDays = 30
	_, err := PreviewCampaignStack(current, candidate, month, 1, 0, now)
	require.ErrorIs(t, err, ErrContractType)
}

func TestCampaignStackStoreKeepsLedgerAndGift(t *testing.T) {
	c, db := v2Store(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	parent, plan := v2Parent(t, c, 90, 7, now)
	require.NoError(t, c.UserSubscription.UpdateOneID(parent.ID).ClearPlanID().Exec(ctx))
	expiry := now.Add(3 * 24 * time.Hour)
	lot, err := c.UserSubscriptionEntitlement.Create().SetUserSubscriptionID(parent.ID).SetSourceType("campaign").SetStatus("active").SetStartsAt(expiry.Add(-7 * 24 * time.Hour)).SetExpiresAt(expiry).SetDailyLimitUsd(45).SetDailyWindowStart(DayStart(now)).SetDailyUsageUsd(20).SetLifetimeUsageUsd(20).Save(ctx)
	require.NoError(t, err)
	current, err := EnsureContract(ctx, c, parent.ID, now)
	require.NoError(t, err)
	lots, err := ReadLots(ctx, c, parent.ID)
	require.NoError(t, err)
	change, err := PreviewCampaignStack(current, CampaignStackCandidate(current, lots, now), PlanFromEntity(plan), 1, 0, now)
	require.NoError(t, err)
	// An in-flight admission stays attached to the old term after conversion.
	require.NoError(t, c.SubscriptionRequest.Create().SetRequestKey("campaign-late").SetSubscriptionID(parent.ID).SetAPIKeyID(1).SetLots([]byte("[]")).SetAdmittedAt(now).Exec(ctx))
	order := v2Order(t, c, parent)
	after, err := ApplyContractChange(ctx, c, change, order.ID, "payment", PurchaseReference(order.ID), 0, now)
	require.NoError(t, err)
	require.Equal(t, current.TermID, after.TermID)
	require.Equal(t, parent.ID, after.SubscriptionID)
	_, err = db.Exec(`INSERT INTO subscription_usage_allocations(request_key,entitlement_id,cost_usd,daily_window_start) VALUES(?,?,?,?)`, "campaign-late", lot.ID, 5, DayStart(now))
	require.NoError(t, err)
	require.NoError(t, SyncDailyLedger(ctx, c, parent.ID, now))
	used, err := ReadDailyUsage(ctx, c, parent.ID, after.TermID, now)
	require.NoError(t, err)
	require.Equal(t, 25.0, used)
	again, err := ApplyContractChange(ctx, c, change, order.ID, "payment", PurchaseReference(order.ID), 0, now)
	require.NoError(t, err)
	require.Equal(t, after, again)
	lots, err = ReadLots(ctx, c, parent.ID)
	require.NoError(t, err)
	require.Len(t, lots, 2)
	summary := ContractSummary(after, lots, used, now)
	require.Equal(t, 135.0, *summary.DailyLimitUSD)
	require.Equal(t, 110.0, *summary.RemainingUSD)
	_, err = ValidateContractRefund(ctx, c, order.ID, now)
	require.ErrorIs(t, err, ErrContractRefund)
	renew, err := PreviewContract(after, PlanFromEntity(plan), PlanFromEntity(plan), "renew", 0, 1, now)
	require.NoError(t, err)
	renewed, err := ApplyContractChange(ctx, c, renew, 0, "admin", "renew", 0, now)
	require.NoError(t, err)
	unchanged, err := c.UserSubscriptionEntitlement.Get(ctx, lot.ID)
	require.NoError(t, err)
	require.True(t, lot.ExpiresAt.Equal(unchanged.ExpiresAt))
	require.Equal(t, lot.DailyUsageUsd, unchanged.DailyUsageUsd)
	lots, err = ReadLots(ctx, c, parent.ID)
	require.NoError(t, err)
	summary = ContractSummary(renewed, lots, 0, expiry.Add(time.Second))
	require.Equal(t, 90.0, *summary.DailyLimitUSD)
	nextDay, err := ReadDailyUsage(ctx, c, parent.ID, after.TermID, DayStart(now).Add(24*time.Hour))
	require.NoError(t, err)
	require.Zero(t, nextDay)
}
