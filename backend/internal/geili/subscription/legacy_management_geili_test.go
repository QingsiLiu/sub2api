package subscription

import (
	"context"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func legacyPreviewFixture() (*Contract, []Lot, map[int64]Plan, Plan, time.Time) {
	now := time.Date(2026, 9, 25, 15, 0, 0, 0, beijing)
	pid := int64(6)
	daily := 90.0
	p := Plan{ID: 6, Name: "week90", Kind: "week", DailyUSD: 90, PeriodDays: 7, Price: decimal.NewFromInt(7)}
	c := &Contract{SubscriptionID: 277, UserID: 4015, TermID: "legacy-277", Mode: ContractModeLegacy, Revision: 1, Status: "active", StartsAt: now.Add(-6 * 24 * time.Hour), ExpiresAt: now.Add(48 * time.Hour), PlanID: 6}
	lots := []Lot{{ID: 4, PlanID: &pid, Status: "active", SourceType: "legacy", StartsAt: c.StartsAt, ExpiresAt: now.Add(-time.Hour), DailyLimitUSD: &daily, DailyUsageUSD: 90}, {ID: 288, PlanID: &pid, Status: "active", SourceType: "payment", StartsAt: c.StartsAt, ExpiresAt: now.Add(24 * time.Hour), DailyLimitUSD: &daily, DailyUsageUSD: 50}, {ID: 295, PlanID: &pid, Status: "active", SourceType: "payment", StartsAt: c.StartsAt, ExpiresAt: c.ExpiresAt, DailyLimitUSD: &daily, DailyUsageUSD: 10}}
	return c, lots, map[int64]Plan{4: p, 288: p, 295: p}, p, now
}
func TestLegacyPreviewSelectedRenewalAndUpgrade(t *testing.T) {
	c, lots, plans, p, now := legacyPreviewFixture()
	change, detail, err := PreviewLegacy(c, lots, plans, p, "renew", []int64{288}, 0, 0, 2, now)
	require.NoError(t, err)
	require.Equal(t, "14", change.Amount.String())
	projected := ProjectLegacyLots(lots, detail)
	require.Equal(t, lots[1].ExpiresAt.Add(14*24*time.Hour), projected[1].ExpiresAt)
	require.Equal(t, lots[0], projected[0])
	require.Equal(t, lots[2], projected[2])
	require.Equal(t, 50.0, projected[1].DailyUsageUSD)
	target := p
	target.ID = 7
	target.DailyUSD = 180
	target.Price = decimal.NewFromInt(14)
	change, detail, err = PreviewLegacy(c, lots, plans, target, "upgrade", []int64{288, 295}, 0, 0, 0, now.Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, "5", change.Amount.String(), "ceil each lot independently: 2 + 3 days")
	projected = ProjectLegacyLots(lots, detail)
	require.Equal(t, lots[1].ExpiresAt, projected[1].ExpiresAt)
	require.Equal(t, 180.0, *projected[1].DailyLimitUSD)
	require.Equal(t, lots[0], projected[0])
	require.Equal(t, 10.0, projected[2].DailyUsageUSD)
}
func TestLegacyPreviewAdditionRules(t *testing.T) {
	c, lots, plans, p, now := legacyPreviewFixture()
	change, d, err := PreviewLegacy(c, lots, plans, p, "purchase", nil, 0, 3, 0, now)
	require.NoError(t, err)
	require.Equal(t, "21", change.Amount.String())
	require.Len(t, d.Lines, 3)
	require.Equal(t, now.Add(7*24*time.Hour), d.Lines[0].After.ExpiresAt)
	change, d, err = PreviewLegacy(c, lots, plans, p, "stack", nil, 295, 2, 0, now)
	require.NoError(t, err)
	require.Equal(t, "4", change.Amount.String())
	require.Equal(t, lots[2].ExpiresAt, d.Lines[0].After.ExpiresAt)
	_, _, err = PreviewLegacy(c, lots, plans, p, "stack", nil, 4, 1, 0, now)
	require.Error(t, err)
}
func TestLegacyRejectsUnsafeSelections(t *testing.T) {
	for _, ids := range [][]int64{nil, {4}, {288, 288}, {999}, {288, 999}} {
		c, lots, plans, p, now := legacyPreviewFixture()
		_, _, err := PreviewLegacy(c, lots, plans, p, "renew", ids, 0, 0, 1, now)
		require.Error(t, err)
	}
	for _, state := range []string{"refund_pending", "suspended", "refunded", "revoked"} {
		c, lots, plans, p, now := legacyPreviewFixture()
		lots[1].Status = state
		_, _, err := PreviewLegacy(c, lots, plans, p, "renew", []int64{288}, 0, 0, 1, now)
		require.Error(t, err)
	}
	c, lots, plans, p, now := legacyPreviewFixture()
	lots[1].SourceType = "campaign"
	_, _, err := PreviewLegacy(c, lots, plans, p, "renew", []int64{288}, 0, 0, 1, now)
	require.Error(t, err)
	delete(plans, 288)
	_, _, err = PreviewLegacy(c, lots, plans, p, "renew", []int64{295}, 0, 0, 1, now)
	require.NoError(t, err, "special sibling does not block recognized lot")
	c.Mode = ContractModeV2
	_, _, err = PreviewLegacy(c, lots, plans, p, "renew", []int64{295}, 0, 0, 1, now)
	require.Error(t, err)
}
func TestLegacyCommercialVersionIgnoresConsumptionButNotExpiry(t *testing.T) {
	c, lots, plans, p, now := legacyPreviewFixture()
	change, d, err := PreviewLegacy(c, lots, plans, p, "renew", []int64{288}, 0, 0, 1, now)
	require.NoError(t, err)
	lots[1].DailyUsageUSD += 15
	lots[1].LifetimeUsageUSD += 15
	require.NoError(t, ValidateLegacyChange(c, lots, change, d, now, true))
	require.Error(t, ValidateLegacyChange(c, lots, change, d, lots[1].ExpiresAt, true))
	lots[1].ExpiresAt = lots[1].ExpiresAt.Add(time.Second)
	require.Error(t, ValidateLegacyChange(c, lots, change, d, now, true))
}
func TestLegacyStorePartialRenewalIdempotencyAndRefund(t *testing.T) {
	c, _ := v2Store(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Microsecond)
	parent, plan := v2Parent(t, c, 90, 7, now)
	a := v2LegacyLot(t, c, parent, plan, 12, now)
	b := v2LegacyLot(t, c, parent, plan, 3, now)
	require.NoError(t, c.UserSubscriptionEntitlement.UpdateOneID(a.ID).SetExpiresAt(now.Add(time.Hour)).Exec(ctx))
	contract, err := EnsureContract(ctx, c, parent.ID, now)
	require.NoError(t, err)
	require.Equal(t, ContractModeLegacy, contract.Mode)
	lots, err := ReadLots(ctx, c, parent.ID)
	require.NoError(t, err)
	p := PlanFromEntity(plan)
	change, d, err := PreviewLegacy(contract, lots, map[int64]Plan{a.ID: p, b.ID: p}, p, "renew", []int64{a.ID}, 0, 0, 1, now)
	require.NoError(t, err)
	order := v2Order(t, c, parent)
	after, err := ApplyLegacyChange(ctx, c, change, d, order.ID, now)
	require.NoError(t, err)
	require.Equal(t, contract.TermID, after.TermID)
	_, err = ApplyLegacyChange(ctx, c, change, d, order.ID, now)
	require.NoError(t, err)
	live, err := ReadLots(ctx, c, parent.ID)
	require.NoError(t, err)
	for _, l := range live {
		if l.ID == a.ID {
			require.True(t, now.Add(time.Hour+7*24*time.Hour).Equal(l.ExpiresAt))
			require.Equal(t, 12.0, l.DailyUsageUSD)
		}
		if l.ID == b.ID {
			require.True(t, b.ExpiresAt.Equal(l.ExpiresAt))
		}
	}
	_, err = FreezeContractRefund(ctx, c, order.ID, now)
	require.NoError(t, err)
	restored, err := RevertContractChange(ctx, c, order.ID, now)
	require.NoError(t, err)
	require.Equal(t, ContractModeLegacy, restored.Mode)
	row, err := c.UserSubscriptionEntitlement.Get(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, now.Add(time.Hour).Equal(row.ExpiresAt))
	require.Equal(t, 12.0, row.DailyUsageUsd)
	_, err = RevertContractChange(ctx, c, order.ID, now)
	require.NoError(t, err)
}
