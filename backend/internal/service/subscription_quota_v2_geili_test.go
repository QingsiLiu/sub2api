package service

import (
	"errors"
	"testing"
	"time"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestContractDailyQuotaV2Reported429Regression(t *testing.T) {
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	day := geilisub.DayStart(now)
	daily, weekly := 45.0, 315.0
	lots := []SubscriptionEntitlement{
		{ID: 1, Status: "active", StartsAt: now.AddDate(0, 0, -6), ExpiresAt: now.AddDate(0, 0, 24), DailyLimitUSD: &daily, WeeklyLimitUSD: &weekly, DailyWindowStart: &day, DailyUsageUSD: 41.92257312, WeeklyUsageUSD: 315},
		{ID: 2, Status: "active", StartsAt: now.AddDate(0, 0, -2), ExpiresAt: now.AddDate(0, 0, 28), DailyLimitUSD: &daily, WeeklyLimitUSD: &weekly, DailyWindowStart: &day, DailyUsageUSD: 45.126, WeeklyUsageUSD: 136.224},
	}
	sub := &UserSubscription{ID: 382, Status: "active", StartsAt: lots[0].StartsAt, ExpiresAt: lots[1].ExpiresAt, Entitlements: lots, DailyUsageUSD: 87.04857312, QuotaUsageDate: day, Contract: &geilisub.Contract{Mode: geilisub.ContractModeLegacy, Status: "active", StartsAt: lots[0].StartsAt, ExpiresAt: lots[1].ExpiresAt}}
	s := &SubscriptionService{now: func() time.Time { return now }}
	_, err := s.ValidateAndCheckLimits(sub, nil)
	require.NoError(t, err)
	a := sub.QuotaSummaryAt(now)
	require.InDelta(t, 2.95142688, a.AvailableUSD, 1e-10)
	require.Nil(t, a.WeeklyLimitUSD)
	require.Nil(t, a.MonthlyLimitUSD)
	p := entitlementSubscriptionProgress(sub, nil, now)
	require.InDelta(t, 2.95142688, p.Daily.RemainingUSD, 1e-10)
	require.Nil(t, p.Weekly)
	require.Nil(t, p.Monthly)
	require.Equal(t, 315.0, sub.Entitlements[0].WeeklyUsageUSD, "read projection retains original audit evidence")
}

func TestContractDailyQuotaV2MidnightAndMutation(t *testing.T) {
	now := time.Date(2026, 9, 21, 15, 59, 0, 0, time.UTC)
	day := geilisub.DayStart(now)
	c := &geilisub.Contract{Mode: geilisub.ContractModeV2, Status: "active", Kind: "month", UnitDailyUSD: 45, Quantity: 2, StartsAt: now.AddDate(0, 0, -5), ExpiresAt: now.AddDate(0, 0, 25)}
	sub := &UserSubscription{Status: "active", StartsAt: c.StartsAt, ExpiresAt: c.ExpiresAt, Contract: c, DailyUsageUSD: 90, QuotaUsageDate: day}
	svc := &SubscriptionService{now: func() time.Time { return now }}
	_, err := svc.ValidateAndCheckLimits(sub, nil)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)
	var app *infraerrors.ApplicationError
	require.True(t, errors.As(err, &app))
	require.Equal(t, "2026-09-22T00:00:00+08:00", app.Metadata["window_resets_at"])
	require.Equal(t, "0", app.Metadata["remaining_usd"])
	next := now.Add(2 * time.Minute)
	require.Zero(t, sub.QuotaSummaryAt(next).DailyUsageUSD)
	require.Equal(t, 90.0, sub.QuotaSummaryAt(next).AvailableUSD)
	require.Equal(t, 90.0, sub.DailyUsageUSD, "crossing midnight never erases the prior bucket")
	c.Quantity = 3
	require.Equal(t, 45.0, sub.QuotaSummaryAt(now).AvailableUSD, "stack adds shared quota without clearing usage")
	c.UnitDailyUSD = 90
	require.Equal(t, 180.0, sub.QuotaSummaryAt(now).AvailableUSD, "upgrade applies to every unit")
	c.ExpiresAt = c.ExpiresAt.AddDate(0, 0, 30)
	require.Equal(t, 180.0, sub.QuotaSummaryAt(now).AvailableUSD, "renewal leaves today's spending intact")
}

func TestContractDailyQuotaV2ExpiredLegacyProjectionIsIdempotent(t *testing.T) {
	now := time.Now()
	day := geilisub.DayStart(now)
	limit := 45.0
	rawUsage := 40.0
	sub := &UserSubscription{Status: "active", StartsAt: now.AddDate(0, 0, -4), ExpiresAt: now.AddDate(0, 0, 20), DailyUsageUSD: rawUsage, LedgerDailyUsageUSD: &rawUsage, QuotaUsageDate: day, Contract: &geilisub.Contract{Mode: geilisub.ContractModeLegacy, Status: "active"}, Entitlements: []SubscriptionEntitlement{
		{ID: 1, Status: "active", StartsAt: now.AddDate(0, 0, -31), ExpiresAt: now.Add(-time.Minute), DailyLimitUSD: &limit, DailyWindowStart: &day, DailyUsageUSD: 30},
		{ID: 2, Status: "active", StartsAt: now.AddDate(0, 0, -4), ExpiresAt: now.AddDate(0, 0, 20), DailyLimitUSD: &limit, DailyWindowStart: &day, DailyUsageUSD: 10},
	}}
	for i := 0; i < 3; i++ {
		effectiveSubscriptionSummary(sub, now)
		require.Equal(t, 10.0, sub.DailyUsageUSD)
		require.Equal(t, 35.0, *sub.AggregateQuotaSummary().RemainingUSD)
		require.Equal(t, 40.0, *sub.LedgerDailyUsageUSD)
	}
}
