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

func TestContractCampaignAllocationPrecedesEarlierPaidExpiry(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, beijing)
	paid, gift := 90.0, 45.0
	lots := []Lot{
		{ID: 1, SourceType: "payment", Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), DailyLimitUSD: &paid},
		{ID: 2, SourceType: "campaign", Status: "active", StartsAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour), DailyLimitUSD: &gift},
	}
	for _, allocate := range []struct {
		name string
		fn   func([]Lot, float64, time.Time) ([]Lot, error)
	}{{"v2", AllocateContract}, {"legacy", Allocate}} {
		t.Run(allocate.name, func(t *testing.T) {
			got, err := allocate.fn(lots, 50, now)
			if err != nil {
				t.Fatal(err)
			}
			if got[0].ID != 2 || got[0].DailyUsageUSD != 45 || got[1].DailyUsageUSD != 5 {
				t.Fatalf("expected gift 45 then paid 5: %+v", got)
			}
		})
	}
}

func TestContractCampaignLegacyRenewDoesNotExtendGift(t *testing.T) {
	now := time.Now()
	paid, gift := 90.0, 45.0
	lots := []Lot{{ID: 1, SourceType: "payment", Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), DailyLimitUSD: &paid}, {ID: 2, SourceType: "campaign", Status: "active", StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(7 * 24 * time.Hour), DailyLimitUSD: &gift}}
	got, active, err := PreviewPurchase(lots, "renew", 1, 30, &paid, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("paid renewable count %d; want 1", active)
	}
	for _, lot := range got {
		if lot.ID == 2 && !lot.ExpiresAt.Equal(lots[1].ExpiresAt) {
			t.Fatal("paid renewal extended gift")
		}
		if lot.ID == 1 && !lot.ExpiresAt.Equal(lots[0].ExpiresAt.AddDate(0, 0, 30)) {
			t.Fatal("paid lot not renewed")
		}
	}
}
