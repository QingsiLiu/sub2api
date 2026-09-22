package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	BillingSourceBalance      = "balance"
	BillingSourceSubscription = "subscription"
	KeyRoutingSingle          = "single"
	KeyRoutingComposite       = "composite"
)

func (s *APIKeyService) resolveExplicitSubscription(ctx context.Context, userID int64, requested *int64) (*int64, error) {
	if requested != nil {
		if *requested <= 0 {
			return nil, ErrSubscriptionNotFound
		}
		sub, err := s.userSubRepo.GetByID(ctx, *requested)
		if err != nil || sub == nil || sub.UserID != userID || !sub.IsActive() || sub.StartsAt.After(time.Now()) {
			return nil, ErrSubscriptionNotFound
		}
		id := sub.ID
		return &id, nil
	}
	subscriptions, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	var selected *int64
	for _, sub := range subscriptions {
		if !sub.IsActive() || sub.StartsAt.After(time.Now()) {
			continue
		}
		if selected != nil {
			return nil, ErrSubscriptionSelectionRequired
		}
		id := sub.ID
		selected = &id
	}
	if selected == nil {
		return nil, ErrSubscriptionNotFound
	}
	return selected, nil
}

func (s *APIKeyService) loadExplicitGroups(ctx context.Context, ids []int64) (map[int64]*Group, error) {
	loaded := make(map[int64]*Group, len(ids))
	for _, id := range ids {
		if id <= 0 || loaded[id] != nil {
			continue
		}
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil {
			return nil, err
		}
		loaded[id] = group
	}
	return loaded, nil
}

func (s *APIKeyService) canBindExplicitGroup(ctx context.Context, user *User, group *Group) bool {
	if group == nil || !group.IsActive() || group.Platform == PlatformComposite {
		return false
	}
	if user.CanBindGroup(group.ID, group.IsExclusive) {
		return true
	}
	// Existing legacy subscriptions may already grant access to exclusive groups.
	return group.IsSubscriptionType() && s.canUserBindGroup(ctx, user, group)
}

// replaceCompositeGroup swaps every group on the same usage panel for the selected
// group and keeps the other panels untouched. A single-group edit on a composite
// key means "use this group for this panel", not "discard the rest of the key".
func replaceCompositeGroup(existing []int64, groups map[int64]*Group, selectedID int64) ([]int64, error) {
	selected := groups[selectedID]
	if selected == nil || !IsUsagePanel(selected.UsagePanel) {
		return nil, infraerrors.BadRequest("KEY_ROUTING_INVALID", "composite keys can only replace a group that belongs to a usage panel")
	}
	replaced := []int64{selectedID}
	seen := map[int64]bool{selectedID: true}
	for _, id := range existing {
		if id <= 0 || seen[id] {
			continue
		}
		group := groups[id]
		if group != nil && group.UsagePanel == selected.UsagePanel {
			continue
		}
		seen[id] = true
		replaced = append(replaced, id)
	}
	return replaced, nil
}

func (s *APIKeyService) validateSettlementRouting(ctx context.Context, user *User, source, mode string, groupID *int64, groupIDs []int64, subscriptionID *int64) (*int64, error) {
	if source != BillingSourceBalance && source != BillingSourceSubscription {
		return nil, infraerrors.BadRequest("BILLING_SOURCE_INVALID", "choose balance or subscription settlement")
	}
	ids := groupIDs
	switch mode {
	case KeyRoutingSingle:
		if groupID == nil || *groupID <= 0 || len(groupIDs) > 0 {
			return nil, infraerrors.BadRequest("KEY_ROUTING_INVALID", "single-group keys require group_id and no group_ids")
		}
		ids = []int64{*groupID}
	case KeyRoutingComposite:
		if groupID != nil || len(ids) == 0 {
			return nil, infraerrors.BadRequest("KEY_ROUTING_INVALID", "composite keys require ordered group_ids and no group_id")
		}
	default:
		return nil, infraerrors.BadRequest("KEY_ROUTING_INVALID", "routing_mode must be single or composite")
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, infraerrors.BadRequest("KEY_ROUTING_INVALID", "group IDs must be positive and unique")
		}
		seen[id] = true
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil || !s.canBindExplicitGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}
		if source == BillingSourceSubscription && !group.SubscriptionEnabled {
			return nil, infraerrors.Forbidden("GROUP_SUBSCRIPTION_DISABLED", "selected group does not allow subscription settlement")
		}
	}
	if source == BillingSourceBalance {
		if subscriptionID != nil {
			return nil, infraerrors.BadRequest("KEY_SUBSCRIPTION_UNEXPECTED", "balance keys cannot bind a subscription")
		}
		return nil, nil
	}
	return s.resolveExplicitSubscription(ctx, user.ID, subscriptionID)
}

// GetSettlementGroups preserves existing group visibility and exposes the rates
// for a requested settlement source without treating subscriptions as groups.
func (s *APIKeyService) GetSettlementGroups(ctx context.Context, userID int64, source string) ([]Group, error) {
	if source != BillingSourceBalance && source != BillingSourceSubscription {
		return nil, infraerrors.BadRequest("BILLING_SOURCE_INVALID", "choose balance or subscription settlement")
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	result := []Group{}
	for _, g := range groups {
		if !s.canBindExplicitGroup(ctx, user, &g) {
			continue
		}
		if source == BillingSourceSubscription && !g.SubscriptionEnabled {
			continue
		}
		result = append(result, g)
	}
	return result, nil
}

// ExplicitSingleGroup refreshes scope for model-free endpoints; a composite key
// must never reach a scheduler with a nil group (which means the global pool).
func (s *APIKeyService) ExplicitSingleGroup(ctx context.Context, key *APIKey) (*Group, error) {
	if key.UsesGroupListRouting() || key.GroupID == nil {
		return nil, infraerrors.BadRequest("MODEL_REQUIRED", "this endpoint requires a model or a single-group key")
	}
	owner, err := s.userRepo.GetByID(ctx, key.UserID)
	if err != nil {
		return nil, err
	}
	group, err := s.groupRepo.GetByIDLite(ctx, *key.GroupID)
	if err != nil || !s.canBindExplicitGroup(ctx, owner, group) {
		return nil, ErrGroupNotAllowed
	}
	if key.UsesSubscriptionBilling() && !group.SubscriptionEnabled {
		return nil, infraerrors.Forbidden("GROUP_SUBSCRIPTION_DISABLED", "selected group does not allow subscription settlement")
	}
	return group, nil
}
