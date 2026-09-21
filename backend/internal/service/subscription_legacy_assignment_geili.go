package service

import (
	"context"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
)

// Group-era grants use the same lot accounting while preserving the old target identity.
func (s *SubscriptionService) assignLegacyEntitled(ctx context.Context, input *AssignSubscriptionInput, extend, deferred bool) (*UserSubscription, bool, error) {
	// geili hook: group-era grants need explicit review; old paid orders keep their promise.
	if ctx.Value(legacySubscriptionPaymentKey{}) != true {
		return nil, false, errSubscriptionGrantManual
	}
	group, err := s.groupRepo.GetByID(ctx, input.GroupID)
	if err != nil {
		return nil, false, err
	}
	if !group.IsSubscriptionType() {
		return nil, false, ErrGroupNotSubscriptionType
	}
	var result *UserSubscription
	reused := false
	err = s.withSubscriptionUpdateTx(ctx, func(tc context.Context) error {
		c := dbent.TxFromContext(tc).Client()
		if _, err := c.User.Query().Unique(false).Where(user.IDEQ(input.UserID), geilisub.LockRows).Only(tc); err != nil {
			return err
		}
		row, err := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(input.UserID), usersubscription.GroupIDEQ(input.GroupID)).Only(tc)
		if err != nil && !dbent.IsNotFound(err) {
			return err
		}
		days := normalizeAssignValidityDays(input.ValidityDays)
		now := s.now()
		if row != nil {
			reused = true
			sub, err := s.userSubRepo.GetByID(tc, row.ID)
			if err != nil {
				return err
			}
			if !extend && sub.Status == "active" && sub.ExpiresAt.After(now) {
				if reason, conflict := detectAssignSemanticConflict(sub, input); conflict {
					return ErrSubscriptionAssignConflict.WithMetadata(map[string]string{"conflict_reason": reason})
				}
				result = sub
				return nil
			}
			if _, err := geilisub.LockParent(tc, c, row.ID); err != nil {
				return err
			}
			if _, err := geilisub.EnsureLegacyLot(tc, c, row.ID); err != nil {
				return err
			}
			if _, err := geilisub.EnsureContract(tc, c, row.ID, now); err != nil {
				return err
			}
			if err := geilisub.Purchase(tc, c, row.ID, row.PlanID, 0, days, 1, "renew", group.DailyLimitUSD, group.WeeklyLimitUSD, group.MonthlyLimitUSD, grantSource(input), input.SourceReference, input.AssignedBy, now); err != nil {
				return err
			}
			if _, err := geilisub.MarkLegacyContract(tc, c, row.ID, now); err != nil {
				return err
			}
			if err := s.userSubRepo.UpdateNotes(tc, row.ID, appendSubscriptionNotes(sub.Notes, input.Notes)); err != nil {
				return err
			}
			result, err = s.userSubRepo.GetByID(tc, row.ID)
			return err
		}
		sub := &UserSubscription{UserID: input.UserID, GroupID: input.GroupID, StartsAt: now, ExpiresAt: now.AddDate(0, 0, days), Status: "active", AssignedAt: now, Notes: input.Notes}
		if sub.ExpiresAt.After(MaxExpiresAt) {
			sub.ExpiresAt = MaxExpiresAt
		}
		if input.AssignedBy > 0 {
			sub.AssignedBy = &input.AssignedBy
		}
		if err := s.userSubRepo.Create(tc, sub); err != nil {
			return err
		}
		if err := geilisub.Purchase(tc, c, sub.ID, nil, 0, days, 1, "stack", group.DailyLimitUSD, group.WeeklyLimitUSD, group.MonthlyLimitUSD, grantSource(input), input.SourceReference, input.AssignedBy, now); err != nil {
			return err
		}
		if _, err := geilisub.MarkLegacyContract(tc, c, sub.ID, now); err != nil {
			return err
		}
		result, err = s.userSubRepo.GetByID(tc, sub.ID)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if !deferred {
		err = s.invalidateSubscriptionCaches(result.UserID, result.GroupID, result.ID)
	}
	return result, reused, err
}

// Zero lots is an explicit legacy state, never a substitute for a failed query.
func subscriptionMissing(err error) bool { return errors.Is(err, ErrSubscriptionNotFound) }
