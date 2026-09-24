package subscription

import (
	"testing"
	"time"
)

func TestContractSummaryAddsCampaignLotToV2Pool(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, beijing)
	paidExpiry := now.Add(24 * time.Hour)
	giftExpiry := now.Add(7 * 24 * time.Hour)
	paid := 90.0
	gift := 45.0
	got := ContractSummary(&Contract{Mode: ContractModeV2, Status: "active", UnitDailyUSD: 90, Quantity: 1, StartsAt: now.Add(-24 * time.Hour), ExpiresAt: paidExpiry}, []Lot{
		{SourceType: "payment", Status: "active", StartsAt: now.Add(-24 * time.Hour), ExpiresAt: paidExpiry, DailyLimitUSD: &paid},
		{SourceType: "campaign", Status: "active", StartsAt: now, ExpiresAt: giftExpiry, DailyLimitUSD: &gift},
	}, 10, now)
	if got.ActiveLotCount != 2 || got.DailyLimitUSD == nil || *got.DailyLimitUSD != 135 || got.ExpiresAt == nil || !got.ExpiresAt.Equal(giftExpiry) {
		t.Fatalf("unexpected campaign summary: %+v", got)
	}
	if got.RemainingUSD == nil || *got.RemainingUSD != 125 {
		t.Fatalf("remaining = %v, want 125", got.RemainingUSD)
	}
}

func TestContractSummaryKeepsCampaignAfterPaidTermExpires(t *testing.T) {
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, beijing)
	paidExpiry := now.Add(-time.Hour)
	giftExpiry := now.Add(24 * time.Hour)
	gift := 45.0
	got := ContractSummary(&Contract{Mode: ContractModeV2, Status: "active", UnitDailyUSD: 90, Quantity: 1, StartsAt: now.Add(-8 * 24 * time.Hour), ExpiresAt: paidExpiry}, []Lot{
		{SourceType: "campaign", Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: giftExpiry, DailyLimitUSD: &gift},
	}, 3, now)
	if got.ActiveLotCount != 1 || got.DailyLimitUSD == nil || *got.DailyLimitUSD != 45 || got.RemainingUSD == nil || *got.RemainingUSD != 42 {
		t.Fatalf("unexpected promo-only summary: %+v", got)
	}
}
