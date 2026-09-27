package subscription

import (
	"context"
	"reflect"
	"sort"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/shopspring/decimal"
)

// CampaignStackOffer is a read-only invitation; payment rechecks the locked pool.
type CampaignStackOffer struct {
	ExpiresAt    time.Time `json:"expires_at"`
	GiftDailyUSD float64   `json:"gift_daily_usd"`
}

// Only entitlement identity/terms are signed. Consumption and window resets
// must not invalidate an otherwise unchanged quote.
type CampaignStackGift struct {
	ID              int64     `json:"id"`
	SourceReference string    `json:"source_reference"`
	StartsAt        time.Time `json:"starts_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	DailyUSD        float64   `json:"daily_usd"`
}
type CampaignStackSnapshot struct {
	CampaignStackOffer
	Gifts []CampaignStackGift `json:"gifts"`
}

func CampaignStackCandidate(current *Contract, lots []Lot, now time.Time) *CampaignStackSnapshot {
	if current != nil && (current.Status != "active" || (current.Mode == ContractModeV2 && current.ExpiresAt.After(now))) {
		return nil
	}
	out := &CampaignStackSnapshot{}
	total := decimal.Zero
	for _, lot := range lots {
		if lot.Status == "refund_pending" {
			return nil
		}
		if lot.Status == "refunded" || lot.Status == "revoked" || !lot.ExpiresAt.After(now) {
			continue
		}
		if !lot.Active(now) || lot.SourceType != "campaign" || lot.ExpiresAt.Sub(lot.StartsAt) != 7*24*time.Hour || !finite(lot.DailyLimitUSD) || (lot.WeeklyLimitUSD != nil && *lot.WeeklyLimitUSD != 0) || (lot.MonthlyLimitUSD != nil && *lot.MonthlyLimitUSD != 0) {
			return nil
		}
		out.Gifts = append(out.Gifts, CampaignStackGift{ID: lot.ID, SourceReference: lot.SourceReference, StartsAt: lot.StartsAt.UTC(), ExpiresAt: lot.ExpiresAt.UTC(), DailyUSD: *lot.DailyLimitUSD})
		total = total.Add(decimal.NewFromFloat(*lot.DailyLimitUSD))
		if lot.ExpiresAt.After(out.ExpiresAt) {
			out.ExpiresAt = lot.ExpiresAt.UTC()
		}
	}
	if len(out.Gifts) == 0 {
		return nil
	}
	sort.Slice(out.Gifts, func(i, j int) bool { return out.Gifts[i].ID < out.Gifts[j].ID })
	out.GiftDailyUSD = total.InexactFloat64()
	return out
}

func SameCampaignStack(a, b *CampaignStackSnapshot) bool {
	return a != nil && b != nil && reflect.DeepEqual(a, b)
}

// Caller holds the user's lock. Lock/ensure the single parent before reading
// its authoritative contract and baseline; no existing contract is downgraded.
func CurrentCampaignStack(ctx context.Context, c *dbent.Client, userID int64, now time.Time) (*Contract, *CampaignStackSnapshot, error) {
	parents, err := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(userID), usersubscription.DeletedAtIsNil(), usersubscription.ExpiresAtGT(now), usersubscription.StatusIn("active", "suspended")).All(ctx)
	if err != nil || len(parents) != 1 {
		return nil, nil, err
	}
	parent, err := LockParent(ctx, c, parents[0].ID)
	if err != nil {
		return nil, nil, err
	}
	if parent.Status != "active" || parent.DeletedAt != nil || parent.StartsAt.After(now) || !parent.ExpiresAt.After(now) {
		return nil, nil, nil
	}
	current, err := LoadContract(ctx, c, parent.ID)
	if err != nil {
		return nil, nil, err
	}
	lots, err := ReadLots(ctx, c, parent.ID)
	if err != nil {
		return nil, nil, err
	}
	if CampaignStackCandidate(current, lots, now) == nil {
		return nil, nil, nil
	}
	if current == nil {
		current, err = EnsureContract(ctx, c, parent.ID, now)
		if err != nil {
			return nil, nil, err
		}
	}
	return current, CampaignStackCandidate(current, lots, now), nil
}

func PreviewCampaignStack(current *Contract, gift *CampaignStackSnapshot, target Plan, units, periods int, now time.Time) (Change, error) {
	out := Change{Operation: "stack", Units: units, CampaignStack: gift}
	if current == nil || gift == nil || current.Status != "active" || !gift.ExpiresAt.After(now) {
		return out, ErrStateConflict
	}
	if !target.valid() || target.Kind != ContractKindWeek {
		return out, ErrContractType
	}
	if units < 1 || units > 10 || periods != 0 {
		return out, ErrQuantity
	}
	before := *current
	out.Before = &before
	out.After = before
	out.After.Mode, out.After.Kind = ContractModeV2, ContractKindWeek
	out.After.PlanID, out.After.PlanName = target.ID, target.Name
	out.After.UnitDailyUSD, out.After.Quantity, out.After.PeriodDays = target.DailyUSD, units, 7
	out.After.ExpiresAt = gift.ExpiresAt
	out.After.Revision++
	out.BillableDays = RemainingDays(gift.ExpiresAt, now)
	out.Amount = target.Price.Mul(decimal.NewFromInt(int64(units * out.BillableDays))).Div(decimal.NewFromInt(7)).Round(2)
	return out, nil
}

func validateCampaignStackChange(ctx context.Context, c *dbent.Client, current *Contract, lots []Lot, change Change, now time.Time) error {
	if !contractMatches(current, change.Before) || !SameCampaignStack(change.CampaignStack, CampaignStackCandidate(current, lots, now)) {
		return ErrStateConflict
	}
	live, err := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(current.UserID), usersubscription.IDNEQ(current.SubscriptionID), usersubscription.DeletedAtIsNil(), usersubscription.ExpiresAtGT(now), usersubscription.StatusIn("active", "suspended")).Exist(ctx)
	if err != nil {
		return err
	}
	if live {
		return ErrContractCompatibility
	}
	if change.Operation != "stack" || change.After.Kind != ContractKindWeek || change.After.Quantity != change.Units || change.Units < 1 || change.Units > 10 || change.Periods != 0 || !change.After.ExpiresAt.Equal(change.CampaignStack.ExpiresAt) || !change.After.StartsAt.Equal(current.StartsAt) {
		return ErrStateConflict
	}
	return nil
}
