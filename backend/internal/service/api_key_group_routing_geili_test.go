package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func ptrFloatRoute(v float64) *float64 { return &v }

func TestExplicitRoutePriceConfiguredUsesGroupMediaPrices(t *testing.T) {
	unpriced := &ResolvedPricing{Mode: BillingModeToken}
	pricedToken := &ResolvedPricing{Mode: BillingModeToken, BasePricing: &ModelPricing{InputPricePerToken: 1e-6}}
	imageGroup := &Group{ImagePrice1K: ptrFloatRoute(0.05), ImagePrice2K: ptrFloatRoute(0.08), ImagePrice4K: ptrFloatRoute(0.10)}
	videoGroup := &Group{VideoPrice480P: ptrFloatRoute(0.07), VideoPrice720P: ptrFloatRoute(0.09), VideoPrice1080P: ptrFloatRoute(0.12)}

	require.True(t, explicitRoutePriceConfigured(nil, "grok-4.6", pricedToken))
	require.False(t, explicitRoutePriceConfigured(nil, "grok-imagine", unpriced))
	require.False(t, explicitRoutePriceConfigured(&Group{}, "grok-imagine", unpriced))
	require.True(t, explicitRoutePriceConfigured(imageGroup, "grok-imagine", unpriced))
	require.True(t, explicitRoutePriceConfigured(imageGroup, "grok-imagine-edit", unpriced))
	require.True(t, explicitRoutePriceConfigured(imageGroup, "grok-imagine-image", unpriced))
	require.True(t, explicitRoutePriceConfigured(imageGroup, "gpt-image-2", unpriced))
	require.False(t, explicitRoutePriceConfigured(imageGroup, "grok-imagine-video", unpriced))
	require.True(t, explicitRoutePriceConfigured(videoGroup, "grok-imagine-video", unpriced))
	require.True(t, explicitRoutePriceConfigured(videoGroup, "grok-imagine-video-1.5", unpriced))
	require.False(t, explicitRoutePriceConfigured(videoGroup, "grok-4.6", unpriced))
}

type routingAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (r *routingAccountRepo) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]Account, error) {
	return r.accounts, nil
}

type routingUserRepo struct {
	UserRepository
	user *User
}

func (r *routingUserRepo) GetByID(context.Context, int64) (*User, error) { return r.user, nil }

type routingGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *routingGroupRepo) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	return r.groups[id], nil
}

func TestResolveExplicitKeyRoutesAcceptsGrokImagineGroupPrices(t *testing.T) {
	gid := int64(88)
	price1k := 0.05
	group := &Group{
		ID:             gid,
		Name:           "Grok（Heavy）",
		Status:         StatusActive,
		Platform:       PlatformGrok,
		ImagePrice1K:   &price1k,
		VideoPrice480P: ptrFloatRoute(0.07),
	}
	key := &APIKey{
		UserID:        3982,
		BillingSource: BillingSourceBalance,
		RoutingMode:   KeyRoutingSingle,
		GroupID:       &gid,
	}
	keys := &APIKeyService{
		userRepo:  &routingUserRepo{user: &User{ID: 3982}},
		groupRepo: &routingGroupRepo{groups: map[int64]*Group{gid: group}},
	}
	resolver := NewCompositeRouteResolver(nil)
	resolver.accountRepo = &routingAccountRepo{accounts: []Account{{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"grok-imagine":       "grok-imagine-image-quality",
				"grok-imagine-edit":  "grok-imagine-image-quality",
				"grok-imagine-video": "grok-imagine-video",
				"grok-4.6":           "grok-4.6",
			},
		},
	}}}
	resolver.SetRouteValidation(keys.groupRepo, NewModelPricingResolver(nil, NewBillingService(&config.Config{}, nil)))

	ctx := context.Background()
	image, err := keys.ResolveExplicitKeyRoutes(ctx, key, "grok-imagine", CompositeRouteEndpointImages, resolver)
	require.NoError(t, err)
	require.Len(t, image, 1)
	require.Equal(t, gid, *image[0].TargetGroupID)

	video, err := keys.ResolveExplicitKeyRoutes(ctx, key, "grok-imagine-video", CompositeRouteEndpointImages, resolver)
	require.NoError(t, err)
	require.Len(t, video, 1)

	models, err := keys.ExplicitKeyModels(ctx, key, resolver, CompositeRouteEndpointImages)
	require.NoError(t, err)
	require.Contains(t, models, "grok-imagine")
	require.Contains(t, models, "grok-imagine-video")
}

func TestResolveExplicitKeyRoutesRejectsImagineWithoutGroupPrices(t *testing.T) {
	gid := int64(88)
	group := &Group{ID: gid, Status: StatusActive, Platform: PlatformGrok}
	key := &APIKey{UserID: 1, BillingSource: BillingSourceBalance, RoutingMode: KeyRoutingSingle, GroupID: &gid}
	keys := &APIKeyService{
		userRepo:  &routingUserRepo{user: &User{ID: 1}},
		groupRepo: &routingGroupRepo{groups: map[int64]*Group{gid: group}},
	}
	resolver := NewCompositeRouteResolver(nil)
	resolver.accountRepo = &routingAccountRepo{accounts: []Account{{
		Platform: PlatformGrok,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"grok-imagine": "grok-imagine-image-quality"},
		},
	}}}
	resolver.SetRouteValidation(keys.groupRepo, NewModelPricingResolver(nil, NewBillingService(&config.Config{}, nil)))

	_, err := keys.ResolveExplicitKeyRoutes(context.Background(), key, "grok-imagine", CompositeRouteEndpointImages, resolver)
	require.ErrorIs(t, err, ErrCompositeModelUnpriced)
}
