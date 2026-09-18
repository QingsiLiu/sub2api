package subscription

import (
	"testing"
	"time"
)

func ptr(v float64) *float64 { return &v }
func TestAggregateAddsLimitsAndUnlimitedWins(t *testing.T) {
	now := time.Now()
	lots := []Lot{{Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), DailyLimitUSD: ptr(45), WeeklyLimitUSD: ptr(315), MonthlyLimitUSD: ptr(1350)}, {Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(48 * time.Hour), DailyLimitUSD: ptr(45), WeeklyLimitUSD: nil, MonthlyLimitUSD: ptr(1350)}}
	got := Aggregate(lots, now)
	if got.DailyLimitUSD == nil || *got.DailyLimitUSD != 90 {
		t.Fatalf("daily=%v", got.DailyLimitUSD)
	}
	if got.WeeklyLimitUSD != nil {
		t.Fatal("weekly should be unlimited")
	}
	if got.ActiveLotCount != 2 {
		t.Fatal("active lot count")
	}
	if got.NextExpiryAt == nil || !got.NextExpiryAt.Equal(lots[0].ExpiresAt) {
		t.Fatal("next expiry")
	}
}
func TestAllocateUsesEarliestExpiryAndSplits(t *testing.T) {
	now := time.Now()
	lots := []Lot{{ID: 1, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), DailyLimitUSD: ptr(5), WeeklyLimitUSD: ptr(5), MonthlyLimitUSD: ptr(5)}, {ID: 2, Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), DailyLimitUSD: ptr(5), WeeklyLimitUSD: ptr(5), MonthlyLimitUSD: ptr(5)}}
	got, err := Allocate(lots, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].DailyUsageUSD != 5 || got[1].DailyUsageUSD != 2 {
		t.Fatalf("usage=%v,%v", got[0].DailyUsageUSD, got[1].DailyUsageUSD)
	}
}
func TestAllocateRejectsNoActiveLot(t *testing.T) {
	now := time.Now()
	_, err := Allocate([]Lot{{Status: "expired", StartsAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}}, 1, now)
	if err == nil {
		t.Fatal("expected no entitlement error")
	}
}
