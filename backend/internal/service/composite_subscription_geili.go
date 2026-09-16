package service

import (
	"context"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrCompositeTargetUnavailable     = infraerrors.ServiceUnavailable("ROUTE_PROFILE_DISABLED", "selected route account group is unavailable")
	ErrCompositePreferenceUnavailable = infraerrors.BadRequest("MODEL_ROUTE_NOT_FOUND", "the selected provider route does not support this model")
	ErrCompositeModelUnavailable      = infraerrors.Forbidden("MODEL_NOT_ALLOWED", "model is not allowed by the selected route group")
	ErrCompositeModelUnpriced         = infraerrors.BadRequest("MODEL_PRICE_NOT_CONFIGURED", "selected model has no configured price")
)

// SubscriptionBillingKey makes a request-local copy. Cached authentication
// objects and persisted balance prices must never be rewritten by a request.
func SubscriptionBillingKey(key *APIKey) *APIKey {
	if key == nil || key.Group == nil || key.BillingSource != "" {
		return key
	}
	copyKey, copyGroup := *key, *key.Group
	copyGroup.RateMultiplier = copyGroup.BillingRateMultiplier(true)
	copyGroup.SubscriptionType = SubscriptionTypeSubscription
	copyGroup.ImageRateIndependent = false
	copyGroup.VideoRateIndependent = false
	copyKey.Group = &copyGroup
	return &copyKey
}

// PrepareCompositeRoute carries the selected group through every downstream
// scheduler, pricing path and detached usage worker without resolving again.
func (s *APIKeyService) PrepareCompositeRoute(ctx context.Context, key *APIKey, decision CompositeRouteDecision, subscription bool) (*APIKey, error) {
	if key == nil || key.Group == nil {
		return key, nil
	}
	if key.SubscriptionID != nil && (!decision.Matched || decision.TargetGroupID == nil) {
		return nil, ErrCompositePreferenceUnavailable
	}
	if decision.TargetGroupID == nil {
		return key, nil
	}
	target := decision.TargetGroup
	if target == nil {
		var err error
		target, err = s.groupRepo.GetByIDLite(ctx, *decision.TargetGroupID)
		if err != nil {
			return nil, ErrCompositeTargetUnavailable
		}
	}
	if !target.IsActive() || target.Platform != decision.TargetPlatform {
		return nil, ErrCompositeTargetUnavailable
	}
	if !target.ModelAllowlist.Allows(decision.UpstreamModel) {
		return nil, ErrCompositeModelUnavailable
	}
	copyKey, copyGroup := *key, *target
	copyKey.Group, copyKey.GroupID, copyKey.CompositeRoute = &copyGroup, &copyGroup.ID, &decision
	if copyKey.User != nil {
		user := *copyKey.User
		user.UserGroupRPMOverride = nil
		copyKey.User = &user
	}
	if subscription {
		return SubscriptionBillingKey(&copyKey), nil
	}
	// Explicit routing alone does not grant subscription billing to a balance key.
	copyGroup.SubscriptionType = SubscriptionTypeStandard
	return &copyKey, nil
}

// SubscriptionRouteOption exposes only customer-facing route choices.
type SubscriptionRouteOption struct {
	Provider   string   `json:"provider"`
	ProfileKey string   `json:"profile_key"`
	GroupID    int64    `json:"group_id"`
	Name       string   `json:"name"`
	Multiplier float64  `json:"subscription_rate_multiplier"`
	Models     []string `json:"models"`
}

func (s *APIKeyService) GetSubscriptionRoutes(ctx context.Context, userID, groupID int64) ([]SubscriptionRouteOption, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group.Platform != PlatformComposite || !s.canUserBindGroup(ctx, user, group) {
		return nil, ErrGroupNotAllowed
	}
	options := []SubscriptionRouteOption{}
	if s.compositeRouteRepo == nil {
		return options, nil
	}
	routes, err := s.compositeRouteRepo.ListByGroup(ctx, groupID, false)
	if err != nil {
		return nil, err
	}
	for _, route := range routes {
		if route.TargetGroupID == nil {
			continue
		}
		target, err := s.groupRepo.GetByIDLite(ctx, *route.TargetGroupID)
		if err != nil || !target.IsActive() {
			continue
		}
		provider, ok := DetectModelPlatform(route.PublicModel)
		if !ok {
			provider = route.TargetPlatform
		}
		merged := false
		for i := range options {
			if options[i].Provider == provider && options[i].ProfileKey == route.ProfileKey && options[i].GroupID == target.ID {
				options[i].Models = append(options[i].Models, route.PublicModel)
				merged = true
				break
			}
		}
		if !merged {
			options = append(options, SubscriptionRouteOption{Provider: provider, ProfileKey: route.ProfileKey, GroupID: target.ID, Name: target.Name, Multiplier: target.BillingRateMultiplier(true), Models: []string{route.PublicModel}})
		}
	}
	return options, nil
}
