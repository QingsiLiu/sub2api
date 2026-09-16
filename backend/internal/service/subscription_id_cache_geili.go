package service

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
)

// SubscriptionIDCache separates the quota pool from both legacy and selected
// routing groups. Older test/cache implementations can keep the legacy port.
type SubscriptionIDCache interface {
	GetSubscriptionCacheByID(context.Context, int64) (*SubscriptionCacheData, error)
	SeedSubscriptionCacheByID(context.Context, int64, *SubscriptionCacheData) error
	UpdateSubscriptionUsageByID(context.Context, int64, float64) error
	InvalidateSubscriptionCacheByID(context.Context, int64) error
}

func (s *BillingCacheService) UpdateSubscriptionQuotaCache(ctx context.Context, sub *UserSubscription, cost float64) error {
	if sub == nil {
		return nil
	}
	if sub.PlanID == nil {
		return s.UpdateSubscriptionUsage(ctx, sub.UserID, sub.GroupID, cost)
	}
	if cache, ok := s.cache.(SubscriptionIDCache); ok {
		return cache.UpdateSubscriptionUsageByID(ctx, sub.ID, cost)
	}
	return nil
}
func (s *BillingCacheService) InvalidateSubscriptionByID(ctx context.Context, id int64) error {
	if s == nil {
		return nil
	}
	if cache, ok := s.cache.(SubscriptionIDCache); ok {
		if err := cache.InvalidateSubscriptionCacheByID(ctx, id); err != nil {
			return err
		}
	}
	return s.PublishSubscriptionCacheInvalidation(ctx, fmt.Sprintf("sub:id:v2:%d", id))
}
func (s *SubscriptionService) InvalidatePlanSubscriptions(ctx context.Context, planID int64) error {
	if s.entClient == nil {
		return nil
	}
	rows, err := s.entClient.UserSubscription.Query().Where(usersubscription.PlanIDEQ(planID)).All(ctx)
	if err != nil {
		return err
	}
	for _, sub := range rows {
		groupID := int64(0)
		if sub.GroupID != nil {
			groupID = *sub.GroupID
		}
		if err := s.invalidateSubscriptionCaches(sub.UserID, groupID, sub.ID); err != nil {
			return err
		}
	}
	return nil
}
