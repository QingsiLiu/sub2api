package service

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/subscriptionplan"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// assignPlanSubscription serializes creation/renewal by user, so concurrent
// payments cannot create two pools for the same plan or lose renewal days.
func (s *SubscriptionService) assignPlanSubscription(ctx context.Context, input *AssignSubscriptionInput, extend, deferInvalidation bool) (*UserSubscription, bool, error) {
	if input == nil || input.PlanID == nil || *input.PlanID <= 0 {
		return nil, false, ErrSubscriptionNilInput
	}
	if input.GroupID > 0 {
		return nil, false, infraerrors.BadRequest("SUBSCRIPTION_TARGET_AMBIGUOUS", "choose a plan or a legacy group, not both")
	}
	if s.entClient == nil {
		return nil, false, infraerrors.ServiceUnavailable("SUBSCRIPTION_REPOSITORY_UNAVAILABLE", "plan assignment requires database transactions")
	}
	var result *UserSubscription
	reused := false
	err := s.withSubscriptionUpdateTx(ctx, func(txCtx context.Context) error {
		client := dbent.TxFromContext(txCtx).Client()
		if _, err := client.User.Query().Where(user.IDEQ(input.UserID)).ForUpdate().Only(txCtx); err != nil {
			return fmt.Errorf("lock subscription owner: %w", err)
		}
		plan, err := client.SubscriptionPlan.Get(txCtx, *input.PlanID)
		if err != nil {
			return infraerrors.NotFound("PLAN_NOT_FOUND", "subscription plan not found")
		}
		if plan.ArchivedAt != nil && !input.AllowArchivedPlan {
			return infraerrors.BadRequest("PLAN_ARCHIVED", "plan is archived")
		}
		days := input.ValidityDays
		if days <= 0 {
			days = psComputeValidityDays(plan.ValidityDays, plan.ValidityUnit)
		}
		days = normalizeAssignValidityDays(days)
		existing, err := client.UserSubscription.Query().Where(usersubscription.UserIDEQ(input.UserID), usersubscription.PlanIDEQ(plan.ID)).Only(txCtx)
		if dbent.IsNotFound(err) && plan.GroupID != nil {
			// Preserve old group-based renewal only for unclassified/compatibility
			// subscriptions. Other product subscriptions remain distinct quota pools.
			existing, err = client.UserSubscription.Query().Where(usersubscription.UserIDEQ(input.UserID), usersubscription.GroupIDEQ(*plan.GroupID), usersubscription.Or(usersubscription.PlanIDIsNil(), usersubscription.HasPlanWith(subscriptionplan.IsLegacyCompatEQ(true)))).Only(txCtx)
			if err == nil {
				existing, err = client.UserSubscription.UpdateOneID(existing.ID).SetPlanID(plan.ID).Save(txCtx)
			}
		}
		if err != nil && !dbent.IsNotFound(err) {
			return err
		}
		if existing != nil {
			reused = true
			sub, err := s.userSubRepo.GetByID(txCtx, existing.ID)
			if err != nil {
				return err
			}
			if extend || sub.IsExpired() || sub.Status == SubscriptionStatusExpired {
				if err := s.updateExistingSubscriptionTerm(txCtx, sub.ID, days, input.Notes, !extend); err != nil {
					return err
				}
			} else {
				request := *input
				request.ValidityDays = days
				if reason, conflict := detectAssignSemanticConflict(sub, &request); conflict {
					return ErrSubscriptionAssignConflict.WithMetadata(map[string]string{"conflict_reason": reason})
				}
			}
			result, err = s.userSubRepo.GetByID(txCtx, sub.ID)
			return err
		}
		now := time.Now()
		if s.now != nil {
			now = s.now()
		}
		expires := now.AddDate(0, 0, days)
		if expires.After(MaxExpiresAt) {
			expires = MaxExpiresAt
		}
		sub := &UserSubscription{UserID: input.UserID, PlanID: &plan.ID, StartsAt: now, ExpiresAt: expires, Status: SubscriptionStatusActive, AssignedAt: now, Notes: input.Notes}
		if input.AssignedBy > 0 {
			sub.AssignedBy = &input.AssignedBy
		}
		if err := s.userSubRepo.Create(txCtx, sub); err != nil {
			return err
		}
		result, err = s.userSubRepo.GetByID(txCtx, sub.ID)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if !deferInvalidation {
		s.maybeInvalidateAssignmentCaches(input.UserID, result.GroupID, false)
		if s.billingCacheService != nil {
			_ = s.billingCacheService.InvalidateSubscriptionByID(ctx, result.ID)
		}
	}
	return result, reused, nil
}

// FindByUserAndPlan also recognizes old unclassified compatibility subscriptions
// for renewal/refund of orders created before migration.
func (s *SubscriptionService) FindByUserAndPlan(ctx context.Context, userID, planID int64) (*UserSubscription, error) {
	if s.entClient == nil {
		return nil, ErrSubscriptionNotFound
	}
	client := s.entClient
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}
	row, err := client.UserSubscription.Query().Where(usersubscription.UserIDEQ(userID), usersubscription.PlanIDEQ(planID)).Only(ctx)
	if dbent.IsNotFound(err) {
		plan, planErr := client.SubscriptionPlan.Get(ctx, planID)
		if planErr != nil || plan.GroupID == nil {
			return nil, ErrSubscriptionNotFound
		}
		row, err = client.UserSubscription.Query().Where(usersubscription.UserIDEQ(userID), usersubscription.GroupIDEQ(*plan.GroupID), usersubscription.Or(usersubscription.PlanIDIsNil(), usersubscription.HasPlanWith(subscriptionplan.IsLegacyCompatEQ(true)))).Only(ctx)
	}
	if dbent.IsNotFound(err) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.userSubRepo.GetByID(ctx, row.ID)
}
