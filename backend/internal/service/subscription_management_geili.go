package service

import (
	"context"
	"time"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
)

func (s *SubscriptionService) ExtendSelectedEntitlements(ctx context.Context, id int64, days int, ids []int64, actor int64) (*UserSubscription, error) {
	if days == 0 || days > MaxValidityDays || days < -MaxValidityDays {
		return nil, geilisub.ErrStateConflict
	}
	if r, ok := s.userSubRepo.(interface {
		AdjustEntitlements(context.Context, int64, []int64, int, int64) error
	}); ok {
		if err := r.AdjustEntitlements(ctx, id, ids, days, actor); err != nil {
			return nil, err
		}
		sub, err := s.userSubRepo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := s.invalidateSubscriptionCaches(sub.UserID, sub.GroupID, id); err != nil {
			return sub, err
		}
		return sub, nil
	}
	if len(ids) > 0 {
		return nil, geilisub.ErrSelection
	}
	return s.ExtendSubscription(ctx, id, days)
}
func effectiveSubscriptionSummary(sub *UserSubscription, now time.Time) {
	if len(sub.Entitlements) == 0 {
		return
	}
	a := geilisub.Aggregate(sub.Entitlements, now)
	sub.QuotaSummary = &a
	sub.DailyUsageUSD = a.DailyUsageUSD
	sub.WeeklyUsageUSD = a.WeeklyUsageUSD
	sub.MonthlyUsageUSD = a.MonthlyUsageUSD
	if a.ExpiresAt != nil {
		sub.ExpiresAt = *a.ExpiresAt
	}
	if sub.Status == "active" && a.ActiveLotCount == 0 {
		sub.Status = "expired"
	}
}
