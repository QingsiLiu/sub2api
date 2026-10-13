package service

import (
	"context"
	"time"
)

// checkFixedChargeSubscriptionGeili uses the quota owner across provider groups.
// Fixed storage charges must fit the plan or shared entitlement pool in full.
func (s *BillingCacheService) checkFixedChargeSubscriptionGeili(ctx context.Context, userID int64, group *Group, sub *UserSubscription, pending float64) error {
	if sub == nil || sub.UserID != userID {
		return ErrSubscriptionNotFound
	}
	if s.subRepo != nil {
		fresh, err := s.subRepo.GetByID(ctx, sub.ID)
		if err != nil {
			return ErrBillingServiceUnavailable.WithCause(err)
		}
		if fresh == nil || fresh.UserID != userID {
			return ErrSubscriptionNotFound
		}
		sub = fresh
	} else if sub.Contract == nil && len(sub.Entitlements) == 0 && sub.Plan == nil {
		return s.checkFixedChargeSubscription(ctx, userID, group, pending)
	}
	now := time.Now()
	if sub.Status != SubscriptionStatusActive || sub.StartsAt.After(now) || (sub.PlanID != nil && sub.Plan == nil) {
		return ErrSubscriptionInvalid
	}
	if sub.Contract != nil || len(sub.Entitlements) > 0 {
		summary := sub.AggregateQuotaSummary()
		if summary == nil || summary.ActiveLotCount == 0 {
			return ErrSubscriptionInvalid
		}
		if summary.AvailableUSD < pending {
			return DailyQuotaExceeded(sub, now)
		}
		return nil
	}
	if now.After(sub.ExpiresAt) {
		return ErrSubscriptionInvalid
	}
	daily, weekly, monthly := sub.QuotaLimits(sub.Group)
	if daily != nil && *daily > 0 && sub.DailyUsageUSD+pending > *daily {
		return ErrDailyLimitExceeded
	}
	if weekly != nil && *weekly > 0 && sub.WeeklyUsageUSD+pending > *weekly {
		return ErrWeeklyLimitExceeded
	}
	if monthly != nil && *monthly > 0 && sub.MonthlyUsageUSD+pending > *monthly {
		return ErrMonthlyLimitExceeded
	}
	return nil
}
