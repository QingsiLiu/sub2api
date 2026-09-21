package service

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type legacySubscriptionPaymentKey struct{}

var errSubscriptionGrantManual = infraerrors.Conflict("SUBSCRIPTION_GRANT_MANUAL_REVIEW", "these subscription rights cannot be applied without changing their value; administrator review is required")

func (s *SubscriptionService) assignPlanSubscription(ctx context.Context, input *AssignSubscriptionInput, extend, deferred bool) (*UserSubscription, bool, error) {
	if ctx.Value(legacySubscriptionPaymentKey{}) == true {
		return s.assignLegacyPlanSubscription(ctx, input, extend, deferred)
	}
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
	err := s.withSubscriptionUpdateTx(ctx, func(tc context.Context) error {
		c := dbent.TxFromContext(tc).Client()
		if _, err := c.User.Query().Unique(false).Where(user.IDEQ(input.UserID), geilisub.LockRows).Only(tc); err != nil {
			return err
		}
		if err := checkSubscriptionPending(tc, c, input.UserID); err != nil {
			return err
		}
		plan, err := c.SubscriptionPlan.Get(tc, *input.PlanID)
		if err != nil {
			return err
		}
		if plan.ArchivedAt != nil && !input.AllowArchivedPlan {
			return infraerrors.BadRequest("PLAN_ARCHIVED", "plan is archived")
		}
		target, err := paymentContractPlan(plan)
		if err != nil {
			return errSubscriptionGrantManual
		}
		now := time.Now()
		if s.now != nil {
			now = s.now()
		}
		current, err := geilisub.CurrentContract(tc, c, input.UserID, now)
		if err != nil {
			return err
		}
		if current != nil && current.Mode != geilisub.ContractModeV2 {
			return errSubscriptionGrantManual
		}
		days := input.ValidityDays
		if days == 0 {
			days = target.PeriodDays
		}
		if days <= 0 || days%target.PeriodDays != 0 || days/target.PeriodDays > 10 {
			return errSubscriptionGrantManual
		}
		operation, units, periods := "purchase", 1, 0
		if current != nil {
			if current.PlanID != target.ID {
				return geilisub.ErrContractTier
			}
			reused = true
			if !extend {
				result, err = s.userSubRepo.GetByID(tc, current.SubscriptionID)
				if err != nil {
					return err
				}
				candidate := *input
				candidate.ValidityDays = days
				if reason, conflict := detectAssignSemanticConflict(result, &candidate); conflict {
					return ErrSubscriptionAssignConflict.WithMetadata(map[string]string{"conflict_reason": reason})
				}
				return nil
			}
			if input.SourceType == "redeem" && current.Quantity != 1 {
				return errSubscriptionGrantManual
			}
			operation, units, periods = "renew", 0, days/target.PeriodDays
		}
		change, err := geilisub.PreviewContract(current, target, target, operation, units, periods, now)
		if err != nil {
			return err
		}
		if current == nil {
			if days != target.PeriodDays {
				return errSubscriptionGrantManual
			}
			parent, err := c.UserSubscription.Query().Where(usersubscription.UserIDEQ(input.UserID), usersubscription.PlanIDEQ(target.ID)).Order(dbent.Desc(usersubscription.FieldExpiresAt), dbent.Desc(usersubscription.FieldID)).First(tc)
			if err != nil && !dbent.IsNotFound(err) {
				return err
			}
			if parent == nil {
				b := c.UserSubscription.Create().SetUserID(input.UserID).SetPlanID(target.ID).SetStartsAt(now).SetExpiresAt(now).SetAssignedAt(now).SetStatus("expired").SetNotes(input.Notes)
				if input.AssignedBy > 0 {
					b.SetAssignedBy(input.AssignedBy)
				}
				parent, err = b.Save(tc)
				if err != nil {
					return err
				}
			} else {
				reused = true
				if _, err := geilisub.LockParent(tc, c, parent.ID); err != nil {
					return err
				}
				if _, err := geilisub.EnsureContract(tc, c, parent.ID, now); err != nil {
					return err
				}
			}
			change.After.UserID = input.UserID
			change.After.SubscriptionID = parent.ID
		}
		applied, err := geilisub.ApplyContractChange(tc, c, change, 0, grantSource(input), input.SourceReference, input.AssignedBy, now)
		if err != nil {
			return err
		}
		if input.Notes != "" {
			parent, err := c.UserSubscription.Get(tc, applied.SubscriptionID)
			if err != nil {
				return err
			}
			if err = c.UserSubscription.UpdateOneID(parent.ID).SetNotes(appendSubscriptionNotes(psStringValue(parent.Notes), input.Notes)).Exec(tc); err != nil {
				return err
			}
		}
		result, err = s.userSubRepo.GetByID(tc, applied.SubscriptionID)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if !deferred {
		if err = s.invalidateSubscriptionCaches(result.UserID, result.GroupID, result.ID); err != nil {
			return nil, false, fmt.Errorf("invalidate subscription grant: %w", err)
		}
	}
	return result, reused, nil
}
