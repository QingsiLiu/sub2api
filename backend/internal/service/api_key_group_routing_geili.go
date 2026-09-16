package service

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// KeyRouteAttempt contains non-secret evidence of cross-group failover.
type KeyRouteAttempt struct {
	GroupID int64  `json:"group_id"`
	Reason  string `json:"reason"`
}

// ExplicitGroupModels builds a catalog from declared models, not transport
// platform inference. OpenAI-compatible API-key pools must declare their models.
func (r *CompositeRouteResolver) ExplicitGroupModels(ctx context.Context, group *Group) ([]string, error) {
	seen := map[string]bool{}
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model != "" && !strings.Contains(model, "*") && group.ModelAllowlist.Allows(model) {
			seen[model] = true
		}
	}
	if group.ModelAllowlist.Enabled {
		for _, model := range group.ModelAllowlist.Models {
			add(model)
		}
	}
	if r.accountRepo == nil {
		return nil, infraerrors.ServiceUnavailable("MODEL_CATALOG_UNAVAILABLE", "account model catalog is unavailable")
	}
	accounts, err := r.accountRepo.ListModelAvailabilityCandidates(ctx, &group.ID, []string{group.Platform}, false)
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		a := &accounts[i]
		for model := range a.GetModelMapping() {
			add(model)
		}
		if a.Type == AccountTypeOAuth {
			switch a.Platform {
			case PlatformOpenAI:
				for _, model := range openai.DefaultModelIDs() {
					add(model)
				}
			case PlatformAnthropic:
				for _, model := range claude.DefaultModels {
					add(model.ID)
				}
			case PlatformGemini:
				for _, model := range geminicli.DefaultModels {
					add(model.ID)
				}
			}
		}
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

func keyGroupEndpointSupported(group *Group, endpoint string) bool {
	switch endpoint {
	case CompositeRouteEndpointGemini:
		return group.Platform == PlatformGemini || group.Platform == PlatformAntigravity
	case CompositeRouteEndpointEmbeddings:
		return group.Platform == PlatformOpenAI
	case CompositeRouteEndpointImages:
		return group.Platform == PlatformOpenAI || group.Platform == PlatformGrok
	default:
		return isConcreteRequestPlatform(group.Platform)
	}
}

// ResolveExplicitKeyRoutes freezes the ordered candidates once for the request.
// It deliberately does not use DetectModelPlatform: two OpenAI transport groups
// may expose unrelated GPT and national/custom model IDs.
func (s *APIKeyService) ResolveExplicitKeyRoutes(ctx context.Context, key *APIKey, model, endpoint string, resolver *CompositeRouteResolver) ([]CompositeRouteDecision, error) {
	if key == nil || key.BillingSource == "" {
		return nil, nil
	}
	if resolver == nil || resolver.pricing == nil {
		return nil, infraerrors.ServiceUnavailable("ROUTING_UNAVAILABLE", "routing validation is unavailable")
	}
	ids := key.GroupIDs
	if !key.UsesGroupListRouting() && key.GroupID != nil {
		ids = []int64{*key.GroupID}
	}
	owner, err := s.userRepo.GetByID(ctx, key.UserID)
	if err != nil {
		return nil, err
	}
	decisions := []CompositeRouteDecision{}
	for _, id := range ids {
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil || !s.canBindExplicitGroup(ctx, owner, group) {
			if !key.UsesGroupListRouting() {
				return nil, ErrGroupNotAllowed
			}
			continue
		}
		if key.UsesSubscriptionBilling() && !group.SubscriptionEnabled {
			if !key.UsesGroupListRouting() {
				return nil, infraerrors.Forbidden("GROUP_SUBSCRIPTION_DISABLED", "selected group does not allow subscription settlement")
			}
			continue
		}
		if !keyGroupEndpointSupported(group, endpoint) {
			continue
		}
		models, err := resolver.ExplicitGroupModels(ctx, group)
		if err != nil {
			return nil, err
		}
		supported := false
		for _, known := range models {
			if known == model {
				supported = true
				break
			}
		}
		if !supported {
			continue
		}
		price := resolver.pricing.Resolve(ctx, PricingInput{Model: model, GroupID: &group.ID, Group: group})
		if price == nil || (price.Mode == BillingModeToken && price.BasePricing == nil) {
			return nil, ErrCompositeModelUnpriced
		}
		snapshot := *group
		decisions = append(decisions, CompositeRouteDecision{Matched: true, Source: "key_groups", PublicModel: model, UpstreamModel: model, TargetGroup: &snapshot, TargetGroupID: &snapshot.ID, TargetPlatform: snapshot.Platform, Endpoint: endpoint})
	}
	if len(decisions) == 0 {
		return nil, infraerrors.BadRequest("MODEL_NOT_AVAILABLE", "the selected groups do not declare support for this model and endpoint")
	}
	return decisions, nil
}

func (s *APIKeyService) ExplicitKeyModels(ctx context.Context, key *APIKey, resolver *CompositeRouteResolver, constraints ...string) ([]string, error) {
	ids := key.GroupIDs
	if !key.UsesGroupListRouting() && key.GroupID != nil {
		ids = []int64{*key.GroupID}
	}
	owner, err := s.userRepo.GetByID(ctx, key.UserID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		g, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil || !s.canBindExplicitGroup(ctx, owner, g) {
			continue
		}
		if key.UsesSubscriptionBilling() && !g.SubscriptionEnabled {
			continue
		}
		if len(constraints) > 0 && !keyGroupEndpointSupported(g, constraints[0]) {
			continue
		}
		if len(constraints) > 1 && constraints[1] != "" && g.Platform != constraints[1] {
			continue
		}
		models, err := resolver.ExplicitGroupModels(ctx, g)
		if err != nil {
			return nil, err
		}
		for _, model := range models {
			price := resolver.pricing.Resolve(ctx, PricingInput{Model: model, GroupID: &g.ID, Group: g})
			if price != nil && (price.Mode != BillingModeToken || price.BasePricing != nil) {
				seen[model] = true
			}
		}
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}
