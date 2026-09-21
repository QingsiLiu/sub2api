package subscription

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func v2Plan(id int64, days int, daily float64, price string) Plan {
	return Plan{ID: id, Name: "test", Kind: RecognizePlan(days, daily), PeriodDays: days, DailyUSD: daily, Price: decimal.RequireFromString(price)}
}
func v2Contract(now time.Time, p Plan, units int, remaining time.Duration) *Contract {
	return &Contract{SubscriptionID: 11, UserID: 22, TermID: "original", Revision: 8, Mode: ContractModeV2, Kind: p.Kind, PlanID: p.ID, PlanName: p.Name, UnitDailyUSD: p.DailyUSD, Quantity: units, PeriodDays: p.PeriodDays, StartsAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(remaining), Status: "active"}
}
func TestContractV2FiveProducts(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	for _, p := range []Plan{v2Plan(1, 7, 90, "7.89"), v2Plan(2, 7, 180, "19.77"), v2Plan(3, 30, 45, "29.90"), v2Plan(4, 30, 90, "59.90"), v2Plan(5, 30, 180, "119.90")} {
		got, err := PreviewContract(nil, Plan{}, p, "purchase", 2, 0, now)
		require.NoError(t, err)
		require.Equal(t, 2, got.After.Quantity)
		require.True(t, p.Price.Mul(decimal.NewFromInt(2)).Equal(got.Amount))
		require.Equal(t, now.Add(time.Duration(p.PeriodDays)*24*time.Hour), got.After.ExpiresAt)
	}
}
func TestContractV2ProratesWholeRemainingTerm(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	p := v2Plan(1, 7, 90, "10.01")
	for _, tc := range []struct {
		remaining time.Duration
		days      int
		amount    string
	}{{3 * 24 * time.Hour, 3, "4.29"}, {time.Nanosecond, 1, "1.43"}, {24*time.Hour + time.Nanosecond, 2, "2.86"}, {90 * 24 * time.Hour, 90, "128.70"}} {
		original := v2Contract(now, p, 2, tc.remaining)
		got, err := PreviewContract(original, p, p, "stack", 1, 0, now)
		require.NoError(t, err)
		require.Equal(t, tc.days, got.BillableDays)
		require.Equal(t, tc.amount, got.Amount.StringFixed(2))
		require.Equal(t, 3, got.After.Quantity)
		require.Equal(t, original.ExpiresAt, got.After.ExpiresAt)
		require.Equal(t, original.TermID, got.After.TermID)
		require.Equal(t, 2, original.Quantity)
	}
}
func TestContractV2RenewAndWholeUpgrade(t *testing.T) {
	now := time.Now()
	p := v2Plan(1, 30, 45, "20.00")
	target := v2Plan(3, 30, 180, "100.00")
	current := v2Contract(now, p, 2, 45*24*time.Hour)
	renew, err := PreviewContract(current, p, p, "renew", 0, 3, now)
	require.NoError(t, err)
	require.Equal(t, "120.00", renew.Amount.StringFixed(2))
	require.Equal(t, current.ExpiresAt.Add(90*24*time.Hour), renew.After.ExpiresAt)
	require.Equal(t, 2, renew.After.Quantity)
	upgrade, err := PreviewContract(current, p, target, "upgrade", 0, 0, now)
	require.NoError(t, err)
	require.Equal(t, "240.00", upgrade.Amount.StringFixed(2))
	require.Equal(t, current.ExpiresAt, upgrade.After.ExpiresAt)
	require.Equal(t, 2, upgrade.After.Quantity)
	require.Equal(t, 180.0, upgrade.After.UnitDailyUSD)
}
func TestContractV2RejectsCrossTypeAndChangedTier(t *testing.T) {
	now := time.Now()
	p := v2Plan(1, 7, 90, "9.00")
	current := v2Contract(now, p, 2, 3*24*time.Hour)
	_, err := PreviewContract(current, p, v2Plan(2, 30, 180, "90"), "upgrade", 0, 0, now)
	require.ErrorIs(t, err, ErrContractType)
	_, err = PreviewContract(current, p, v2Plan(2, 7, 180, "19"), "stack", 1, 0, now)
	require.ErrorIs(t, err, ErrContractTier)
	_, err = PreviewContract(current, p, p, "upgrade", 0, 0, now)
	require.ErrorIs(t, err, ErrContractTier)
	_, err = PreviewContract(current, p, p, "renew", 1, 1, now)
	require.ErrorIs(t, err, ErrQuantity)
	_, err = PreviewContract(current, p, p, "purchase", 1, 0, now)
	require.ErrorIs(t, err, ErrStateConflict)
	current.Mode = ContractModeLegacy
	_, err = PreviewContract(current, p, p, "renew", 0, 1, now)
	require.ErrorIs(t, err, ErrContractCompatibility)
	current.ExpiresAt = now
	_, err = PreviewContract(current, p, v2Plan(2, 30, 45, "29"), "purchase", 1, 0, now)
	require.NoError(t, err)
}
func TestContractV2BeijingAndProduction429(t *testing.T) {
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, beijing)
	day := DayStart(now)
	daily, weekly := 45.0, 315.0
	lots := []Lot{{ID: 1, Status: "active", StartsAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(3 * 24 * time.Hour), DailyLimitUSD: &daily, WeeklyLimitUSD: &weekly, DailyUsageUSD: 41.92257312, WeeklyUsageUSD: 315, DailyWindowStart: &day}, {ID: 2, Status: "active", StartsAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(5 * 24 * time.Hour), DailyLimitUSD: &daily, WeeklyLimitUSD: &weekly, DailyUsageUSD: 45.126, WeeklyUsageUSD: 136.224, DailyWindowStart: &day}}
	c := &Contract{Mode: ContractModeLegacy}
	summary := ContractSummary(c, lots, 87.04857312, now)
	require.Equal(t, 90.0, *summary.DailyLimitUSD)
	require.Equal(t, 2.95142688, *summary.RemainingUSD)
	require.Nil(t, summary.WeeklyLimitUSD)
	require.Nil(t, summary.MonthlyLimitUSD)
	require.Equal(t, time.Date(2026, 9, 21, 16, 0, 0, 0, time.UTC), summary.DailyResetAt.UTC())
	require.Equal(t, time.Date(2026, 9, 21, 0, 0, 0, 0, beijing), DayStart(time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)))
}
func TestContractV2LegacyExpiryPreservesLiveSiblingQuota(t *testing.T) {
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, beijing)
	day := DayStart(now)
	daily := 45.0
	lots := []Lot{{Status: "active", StartsAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Minute), DailyLimitUSD: &daily, DailyUsageUSD: 40, DailyWindowStart: &day}, {Status: "active", StartsAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(time.Hour), DailyLimitUSD: &daily, DailyUsageUSD: 10, DailyWindowStart: &day}}
	summary := ContractSummary(&Contract{Mode: ContractModeLegacy}, lots, 50, now)
	require.Equal(t, 45.0, *summary.DailyLimitUSD)
	require.Equal(t, 10.0, summary.DailyUsageUSD)
	require.Equal(t, 35.0, *summary.RemainingUSD)
}
func TestContractV2ShortStackGetsMidnightReset(t *testing.T) {
	now := time.Date(2026, 9, 21, 23, 59, 0, 0, beijing)
	p := v2Plan(1, 7, 90, "10")
	current := v2Contract(now, p, 2, 2*time.Minute)
	summary := ContractSummary(current, nil, 100, now)
	require.Equal(t, 80.0, *summary.RemainingUSD)
	midnight := DayStart(now).Add(24 * time.Hour)
	require.Equal(t, midnight, *summary.DailyResetAt)
	summary = ContractSummary(current, nil, 0, midnight)
	require.Equal(t, 180.0, *summary.RemainingUSD)
}
