package subscription

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func adminGrantFixture() ([]Lot, time.Time) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, beijing)
	gift, grant := 45.0, 360.0
	day := DayStart(now)
	return []Lot{
		{ID: 10, SourceType: "campaign", PurchaseMode: "campaign", Status: "active", StartsAt: now.Add(-3 * 24 * time.Hour), ExpiresAt: now.Add(4 * 24 * time.Hour), DailyLimitUSD: &gift, DailyWindowStart: &day, DailyUsageUSD: 30},
		{ID: 11, SourceType: SourceAdminGrant, PurchaseMode: SourceAdminGrant, Status: "active", StartsAt: now, ExpiresAt: now.AddDate(0, 0, 30), DailyLimitUSD: &grant, DailyWindowStart: &day},
	}, now
}

func TestGrantTimelineStacksThenFallsBack(t *testing.T) {
	lots, now := adminGrantFixture()
	segments := GrantTimeline(lots, now)
	require.Len(t, segments, 2)
	require.Equal(t, now, segments[0].StartsAt)
	require.Equal(t, lots[0].ExpiresAt, segments[0].EndsAt)
	require.Equal(t, 405.0, *segments[0].DailyLimitUSD)
	require.Equal(t, 2, segments[0].LotCount)
	require.Equal(t, lots[0].ExpiresAt, segments[1].StartsAt)
	require.Equal(t, lots[1].ExpiresAt, segments[1].EndsAt)
	require.Equal(t, 360.0, *segments[1].DailyLimitUSD)
}

func TestGrantTimelineIgnoresDeadLots(t *testing.T) {
	lots, now := adminGrantFixture()
	paid := 90.0
	lots = append(lots,
		Lot{ID: 12, SourceType: "payment", Status: "active", StartsAt: now.Add(-10 * 24 * time.Hour), ExpiresAt: now.Add(-time.Hour), DailyLimitUSD: &paid},
		Lot{ID: 13, SourceType: "payment", Status: "revoked", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(10 * 24 * time.Hour), DailyLimitUSD: &paid},
		Lot{ID: 14, SourceType: "payment", Status: "refunded", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(10 * 24 * time.Hour), DailyLimitUSD: &paid},
	)
	segments := GrantTimeline(lots, now)
	require.Len(t, segments, 2)
	require.Equal(t, 405.0, *segments[0].DailyLimitUSD)
	require.Equal(t, 360.0, *segments[1].DailyLimitUSD)
	require.Empty(t, GrantTimeline(nil, now))
	require.Empty(t, GrantTimeline(lots[2:], now))
}

func TestGrantTimelineMergesEqualIntervalsAndKeepsGaps(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, beijing)
	a := 100.0
	lots := []Lot{
		{ID: 1, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), DailyLimitUSD: &a},
		{ID: 2, Status: "active", StartsAt: now.Add(48 * time.Hour), ExpiresAt: now.Add(72 * time.Hour), DailyLimitUSD: &a},
	}
	segments := GrantTimeline(lots, now)
	require.Len(t, segments, 2, "the uncovered day is omitted")
	require.Equal(t, now.Add(24*time.Hour), segments[0].EndsAt)
	require.Equal(t, now.Add(48*time.Hour), segments[1].StartsAt)
	unlimited := []Lot{{ID: 3, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}}
	require.Nil(t, GrantTimeline(unlimited, now)[0].DailyLimitUSD)
}

// The quota the router actually enforces must agree with the preview timeline.
func TestAdminGrantLegacyQuotaStacksAndRetiresGiftUsage(t *testing.T) {
	lots, now := adminGrantFixture()
	c := &Contract{SubscriptionID: 1, Mode: ContractModeLegacy, Status: "active", StartsAt: lots[0].StartsAt, ExpiresAt: lots[1].ExpiresAt}
	summary := ContractSummary(c, lots, 30, now)
	require.Equal(t, 405.0, *summary.DailyLimitUSD)
	require.Equal(t, 375.0, *summary.RemainingUSD)

	// The gift expires part way through a day; its share of that day's usage
	// leaves with it, so the grant alone is fully available.
	expiry := DayStart(now).Add(20 * time.Hour)
	lots[0].ExpiresAt = expiry
	later := expiry.Add(time.Hour)
	summary = ContractSummary(c, lots, 30, later)
	require.Equal(t, 360.0, *summary.DailyLimitUSD)
	require.Equal(t, 360.0, *summary.RemainingUSD)
}

func TestValidateGrant(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, beijing)
	daily, reason, err := ValidateGrant(360.004, 30, "  offline order  ", now)
	require.NoError(t, err)
	require.Equal(t, 360.0, daily)
	require.Equal(t, "offline order", reason)
	for _, bad := range []float64{0, -1, 10000.01, math.NaN(), math.Inf(1), 0.001} {
		_, _, err = ValidateGrant(bad, 30, "x", now)
		require.ErrorIs(t, err, ErrAdminGrantDaily, "daily %v", bad)
	}
	for _, bad := range []int{0, -1, 3651} {
		_, _, err = ValidateGrant(100, bad, "x", now)
		require.ErrorIs(t, err, ErrAdminGrantDays, "days %d", bad)
	}
	_, _, err = ValidateGrant(100, 30, " \t", now)
	require.ErrorIs(t, err, ErrAdminGrantReason)
	_, _, err = ValidateGrant(100, 30, strings.Repeat("赠", 501), now)
	require.ErrorIs(t, err, ErrAdminGrantReason)
	_, _, err = ValidateGrant(100, 3650, strings.Repeat("赠", 500), now)
	require.NoError(t, err)
	_, _, err = ValidateGrant(100, 3650, "x", time.Date(2095, 1, 1, 0, 0, 0, 0, time.UTC))
	require.ErrorIs(t, err, ErrAdminGrantDays)
	require.False(t, ValidGrantKey("short"))
	require.True(t, ValidGrantKey("grant-0001"))
}
