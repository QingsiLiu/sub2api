package handler

import (
	"testing"
	"time"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayUsageGeiliContractMidnightProjection(t *testing.T) {
	now := time.Date(2026, 11, 3, 15, 59, 0, 0, time.UTC)
	day := geilisub.DayStart(now)
	contract := &geilisub.Contract{Mode: geilisub.ContractModeV2, Status: "active", PlanName: "Stacked plan", UnitDailyUSD: 45, Quantity: 2, StartsAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(48 * time.Hour)}
	used := 90.0
	sub := &service.UserSubscription{ID: 10, Status: "active", Contract: contract, StartsAt: contract.StartsAt, ExpiresAt: contract.ExpiresAt, DailyUsageUSD: used, LedgerDailyUsageUSD: &used, QuotaUsageDate: day, WeeklyUsageUSD: 10000, MonthlyUsageUSD: 10000}
	key := &service.APIKey{BillingSource: service.BillingSourceSubscription, Group: &service.Group{Name: "Wrong route"}}
	before := *sub
	h := &GatewayHandler{}
	for _, tc := range []struct {
		now                  time.Time
		remaining, dailyUsed float64
	}{
		{now, 0, 90},
		{now.Add(2 * time.Minute), 90, 0},
		{now, 0, 90},
	} {
		response := h.subscriptionUsageResponseGeili(key, sub, tc.now)
		require.Equal(t, tc.remaining, response["remaining"])
		require.Equal(t, "Stacked plan", response["planName"])
		details := response["subscription"].(gin.H)
		require.Equal(t, tc.dailyUsed, details["daily_usage_usd"])
		require.Equal(t, 90.0, *details["daily_limit_usd"].(*float64))
		require.Nil(t, details["weekly_limit_usd"])
		require.Nil(t, details["monthly_limit_usd"])
		require.Nil(t, details["weekly_window_start"])
		require.Equal(t, before, *sub, "usage projection must not mutate shared subscription evidence")
	}
}

func TestGatewayUsageGeiliEntitlementRemainingIsAuthoritative(t *testing.T) {
	now := time.Now()
	day := geilisub.DayStart(now)
	daily, weekly := 10.0, 20.0
	// The available capacity is a sum of each lot's tightest window, rather
	// than the minimum of summed limits: min(1,20) + min(10,1) = 2, not 11.
	lots := []service.SubscriptionEntitlement{
		{ID: 1, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), DailyLimitUSD: &daily, WeeklyLimitUSD: &weekly, DailyWindowStart: &day, WeeklyWindowStart: &day, DailyUsageUSD: 9},
		{ID: 2, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(2 * time.Hour), DailyLimitUSD: &daily, WeeklyLimitUSD: &weekly, DailyWindowStart: &day, WeeklyWindowStart: &day, WeeklyUsageUSD: 19},
	}
	sub := &service.UserSubscription{Entitlements: lots}
	before := append([]service.SubscriptionEntitlement(nil), lots...)
	response := (&GatewayHandler{}).subscriptionUsageResponseGeili(&service.APIKey{BillingSource: service.BillingSourceSubscription}, sub, now)
	require.Equal(t, 2.0, response["remaining"])
	require.Equal(t, lots[1].ExpiresAt, response["subscription"].(gin.H)["expires_at"])
	require.Equal(t, before, sub.Entitlements)

	sub.Entitlements = []service.SubscriptionEntitlement{{Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}}
	response = (&GatewayHandler{}).subscriptionUsageResponseGeili(&service.APIKey{BillingSource: service.BillingSourceSubscription}, sub, now)
	require.Equal(t, -1.0, response["remaining"], "unlimited remains compatible with the existing usage API")
}
