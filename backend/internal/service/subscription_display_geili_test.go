package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestEntitlementDisplayGeiliIndependentWindows(t *testing.T) {
	now := time.Date(2026, 9, 19, 18, 0, 0, 0, timezone.Location())
	today := timezone.StartOfDay(now)
	yesterday := today.AddDate(0, 0, -1)
	weekly := now.AddDate(0, 0, -8)
	monthly := now.AddDate(0, 0, -31)
	fresh := now.Add(-time.Hour)
	limits := 90.0
	sub := &UserSubscription{Status: "active", StartsAt: now.AddDate(0, 0, -40), ExpiresAt: now.AddDate(0, 0, 30), DailyUsageUSD: 999, WeeklyUsageUSD: 999, MonthlyUsageUSD: 999, Entitlements: []SubscriptionEntitlement{
		{ID: 1, Status: "active", StartsAt: now.AddDate(0, 0, -40), ExpiresAt: now.AddDate(0, 0, 10), DailyLimitUSD: &limits, WeeklyLimitUSD: &limits, MonthlyLimitUSD: &limits, DailyWindowStart: &yesterday, WeeklyWindowStart: &weekly, MonthlyWindowStart: &monthly, DailyUsageUSD: 90, WeeklyUsageUSD: 90, MonthlyUsageUSD: 90},
		{ID: 2, Status: "active", StartsAt: fresh, ExpiresAt: now.AddDate(0, 0, 30), DailyLimitUSD: &limits, WeeklyLimitUSD: &limits, MonthlyLimitUSD: &limits, DailyWindowStart: &today, WeeklyWindowStart: &fresh, MonthlyWindowStart: &fresh, DailyUsageUSD: 10, WeeklyUsageUSD: 20, MonthlyUsageUSD: 30},
		{ID: 3, Status: "active", StartsAt: now.AddDate(0, 0, -40), ExpiresAt: now.Add(-time.Minute), DailyLimitUSD: &limits, DailyUsageUSD: 90},
	}}
	progress := entitlementSubscriptionProgress(sub, nil, now)
	for i, w := range []*UsageWindowProgress{progress.Daily, progress.Weekly, progress.Monthly} {
		require.NotNil(t, w)
		require.Equal(t, 180.0, w.LimitUSD)
		require.Equal(t, float64((i+1)*10), w.UsedUSD)
		require.Positive(t, w.ResetsInSeconds)
	}
	require.True(t, today.AddDate(0, 0, 1).Equal(progress.Daily.ResetsAt))
	require.True(t, weekly.AddDate(0, 0, 14).Equal(progress.Weekly.ResetsAt))
	require.True(t, sub.Entitlements[0].ExpiresAt.Equal(progress.Monthly.ResetsAt))
	require.Equal(t, 90.0, sub.Entitlements[0].DailyUsageUSD, "projection must leave stored lot data untouched")
	require.Equal(t, 999.0, sub.DailyUsageUSD)
}

func TestEntitlementDisplayGeiliOneTimeAndUnused(t *testing.T) {
	now := time.Now()
	start := now.Add(-time.Hour)
	limit := 45.0
	expiry := start.AddDate(0, 0, 1)
	sub := &UserSubscription{Status: "active", Entitlements: []SubscriptionEntitlement{{Status: "active", StartsAt: start, ExpiresAt: expiry, DailyLimitUSD: &limit}}}
	require.Nil(t, entitlementSubscriptionProgress(sub, nil, now).Daily, "unused lots have no invented window")
	sub.Entitlements[0].DailyWindowStart = &start
	sub.Entitlements[0].DailyUsageUSD = 46
	progress := entitlementSubscriptionProgress(sub, nil, now)
	require.NotNil(t, progress.Daily)
	require.Equal(t, expiry, progress.Daily.ResetsAt)
	require.Equal(t, 100.0, progress.Daily.Percentage)
	require.Zero(t, progress.Daily.RemainingUSD)
	sub.Entitlements[0].Status = "refunded"
	require.Nil(t, entitlementSubscriptionProgress(sub, nil, now).Daily)
}
