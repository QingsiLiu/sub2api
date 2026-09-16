package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type CompositeRouteResolver struct {
	accountRepo            AccountRepository
	groupRepo              GroupRepository
	pricing                *ModelPricingResolver
	repo                   CompositeModelRouteRepository
	modelOwnershipResolver CompositeModelOwnershipResolver
}

func NewCompositeRouteResolver(repo CompositeModelRouteRepository) *CompositeRouteResolver {
	return &CompositeRouteResolver{repo: repo}
}

func (r *CompositeRouteResolver) SetRouteValidation(groups GroupRepository, pricing *ModelPricingResolver) {
	r.groupRepo, r.pricing = groups, pricing
}

func (r *CompositeRouteResolver) SetModelOwnershipResolver(resolver CompositeModelOwnershipResolver) {
	if r != nil {
		r.modelOwnershipResolver = resolver
	}
}

func (r *CompositeRouteResolver) Resolve(ctx context.Context, groupID int64, model, endpoint string) (CompositeRouteDecision, error) {
	return r.ResolveForPreferences(ctx, groupID, model, endpoint, nil)
}

// ResolveForPreferences prefers an explicit profile key for the model's
// detected provider while preserving the legacy route ordering when no
// preference or matching profile is configured.
func (r *CompositeRouteResolver) ResolveForPreferences(ctx context.Context, groupID int64, model, endpoint string, preferences map[string]string) (CompositeRouteDecision, error) {
	model = strings.TrimSpace(model)
	endpoint = normalizeCompositeRouteEndpoint(endpoint)
	decision := CompositeRouteDecision{
		GroupID:     groupID,
		PublicModel: model,
		Endpoint:    endpoint,
	}
	if model == "" {
		decision.Reason = "model is required"
		return decision, nil
	}

	if r != nil && r.repo != nil && groupID > 0 {
		routes, err := r.repo.ListByGroup(ctx, groupID, false)
		if err != nil {
			return decision, fmt.Errorf("list composite routes: %w", err)
		}
		preferred := ""
		if platform, detectable := DetectModelPlatform(model); detectable && preferences != nil {
			preferred = strings.ToLower(strings.TrimSpace(preferences[platform]))
		}
		if route, ok := matchCompositeRouteWithProfile(routes, model, endpoint, preferred); ok {
			var target *Group
			if route.TargetGroupID != nil && r.groupRepo != nil {
				var err error
				target, err = r.groupRepo.GetByIDLite(ctx, *route.TargetGroupID)
				if err != nil || target == nil || !target.IsActive() || target.Platform != route.TargetPlatform {
					return decision, ErrCompositeTargetUnavailable
				}
				billable := strings.TrimSpace(route.UpstreamModel)
				if billable == "" {
					billable = model
				}
				if !target.ModelAllowlist.Allows(billable) {
					return decision, ErrCompositeModelUnavailable
				}
				if r.pricing != nil {
					price := r.pricing.Resolve(ctx, PricingInput{Model: billable, GroupID: &target.ID, Group: target})
					if price == nil || (price.Mode == BillingModeToken && price.BasePricing == nil) {
						return decision, ErrCompositeModelUnpriced
					}
				}
			}

			upstreamModel := strings.TrimSpace(route.UpstreamModel)
			if upstreamModel == "" {
				upstreamModel = model
			}
			return CompositeRouteDecision{
				Matched:        true,
				Source:         CompositeRouteSourceExplicit,
				TargetGroup:    target,
				GroupID:        groupID,
				PublicModel:    model,
				TargetPlatform: route.TargetPlatform,
				TargetGroupID:  route.TargetGroupID,
				UpstreamModel:  upstreamModel,
				Endpoint:       endpoint,
				Route:          &route,
			}, nil
		}
		if preferred != "" {
			return decision, ErrCompositePreferenceUnavailable
		}
	}

	if r != nil && r.modelOwnershipResolver != nil && groupID > 0 {
		ownership, err := r.modelOwnershipResolver(ctx, groupID, model)
		if err != nil {
			// A recognizable model can still use the existing detector when the
			// account catalog is temporarily unavailable. Unknown aliases cannot.
			if _, detectable := DetectModelPlatform(model); !detectable {
				return decision, fmt.Errorf("resolve account model ownership: %w", err)
			}
		} else if ownership.Ambiguous {
			decision.Reason = "model is exposed by multiple provider platforms"
			return decision, nil
		} else if ownership.Matched {
			platform := strings.TrimSpace(ownership.TargetPlatform)
			if !isConcreteRequestPlatform(platform) {
				decision.Reason = "account model ownership has no concrete target platform"
				return decision, nil
			}
			return CompositeRouteDecision{
				Matched:        true,
				Source:         CompositeRouteSourceAccount,
				GroupID:        groupID,
				PublicModel:    model,
				TargetPlatform: platform,
				UpstreamModel:  model,
				Endpoint:       endpoint,
			}, nil
		}
	}

	if platform, ok := DetectModelPlatform(model); ok {
		return CompositeRouteDecision{
			Matched:        true,
			Source:         CompositeRouteSourceDetector,
			GroupID:        groupID,
			PublicModel:    model,
			TargetPlatform: platform,
			UpstreamModel:  model,
			Endpoint:       endpoint,
		}, nil
	}
	decision.Reason = "no explicit route or built-in detector match"
	return decision, nil
}

func matchCompositeRouteWithProfile(routes []CompositeModelRoute, model, endpoint, profile string) (CompositeModelRoute, bool) {
	if strings.TrimSpace(profile) != "" {
		filtered := make([]CompositeModelRoute, 0, len(routes))
		for _, route := range routes {
			if strings.EqualFold(strings.TrimSpace(route.ProfileKey), profile) {
				filtered = append(filtered, route)
			}
		}
		return matchCompositeRoute(filtered, model, endpoint)
	}
	return matchCompositeRoute(routes, model, endpoint)
}

func matchCompositeRoute(routes []CompositeModelRoute, model, endpoint string) (CompositeModelRoute, bool) {
	if len(routes) == 0 {
		return CompositeModelRoute{}, false
	}

	type candidate struct {
		route          CompositeModelRoute
		matchStrength  int
		endpointWeight int
		prefixLen      int
	}
	candidates := make([]candidate, 0, len(routes))
	for _, route := range routes {
		route.Endpoint = normalizeCompositeRouteEndpoint(route.Endpoint)
		if route.Endpoint != endpoint && route.Endpoint != CompositeRouteEndpointAny {
			continue
		}
		route.MatchType = normalizeCompositeRouteMatchType(route.MatchType)
		publicModel := strings.TrimSpace(route.PublicModel)
		if publicModel == "" {
			continue
		}

		matchStrength := 0
		prefixLen := len(publicModel)
		switch route.MatchType {
		case CompositeRouteMatchExact:
			if publicModel != model {
				continue
			}
			matchStrength = 2
		case CompositeRouteMatchPrefix:
			if !strings.HasPrefix(model, publicModel) {
				continue
			}
			matchStrength = 1
		default:
			continue
		}
		endpointWeight := 0
		if route.Endpoint == endpoint {
			endpointWeight = 1
		}
		candidates = append(candidates, candidate{
			route:          route,
			matchStrength:  matchStrength,
			endpointWeight: endpointWeight,
			prefixLen:      prefixLen,
		})
	}
	if len(candidates) == 0 {
		return CompositeModelRoute{}, false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.matchStrength != b.matchStrength {
			return a.matchStrength > b.matchStrength
		}
		if a.endpointWeight != b.endpointWeight {
			return a.endpointWeight > b.endpointWeight
		}
		if a.prefixLen != b.prefixLen {
			return a.prefixLen > b.prefixLen
		}
		if a.route.Priority != b.route.Priority {
			return a.route.Priority < b.route.Priority
		}
		return a.route.ID < b.route.ID
	})
	return candidates[0].route, true
}
