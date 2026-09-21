package repository

import (
	"context"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// attachSubscriptionContract is a read-only projection. The migration/admission
// transaction creates the ledger baseline; a dashboard read never changes it.
func attachSubscriptionContract(ctx context.Context, c *dbent.Client, sub *service.UserSubscription, now time.Time) error {
	if sub == nil {
		return nil
	}
	contract, err := geilisub.LoadContract(ctx, c, sub.ID)
	if err != nil {
		return err
	}
	if contract == nil {
		return nil
	}
	used, err := geilisub.ReadDailyUsage(ctx, c, sub.ID, contract.TermID, now)
	if err != nil {
		return err
	}
	if sub.DeletedAt != nil || (sub.Status != "active" && sub.Status != "expired") {
		snapshot := *contract
		snapshot.Status = sub.Status
		contract = &snapshot
	}
	sub.Contract = contract
	sub.QuotaUsageDate = geilisub.DayStart(now)
	sub.DailyUsageUSD = used
	sub.LedgerDailyUsageUSD = &used
	sub.QuotaSummary = sub.QuotaSummaryAt(now)
	sub.DailyUsageUSD = sub.QuotaSummary.DailyUsageUSD
	sub.WeeklyUsageUSD = 0
	sub.MonthlyUsageUSD = 0
	day := geilisub.DayStart(now)
	sub.DailyWindowStart = &day
	sub.WeeklyWindowStart = nil
	sub.MonthlyWindowStart = nil
	if contract.Mode == "v2" {
		sub.StartsAt = contract.StartsAt
		sub.ExpiresAt = contract.ExpiresAt
		if sub.Status == "active" && !contract.ExpiresAt.After(now) {
			sub.Status = "expired"
		}
	}
	return nil
}

func (r *userSubscriptionRepository) projectContract(ctx context.Context, sub *service.UserSubscription) (*service.UserSubscription, error) {
	if err := attachSubscriptionContract(ctx, clientFromContext(ctx, r.client), sub, time.Now()); err != nil {
		return nil, err
	}
	return sub, nil
}
func (r *userSubscriptionRepository) projectContracts(ctx context.Context, subs []service.UserSubscription) ([]service.UserSubscription, error) {
	now := time.Now()
	for i := range subs {
		if err := attachSubscriptionContract(ctx, clientFromContext(ctx, r.client), &subs[i], now); err != nil {
			return nil, err
		}
	}
	return subs, nil
}
