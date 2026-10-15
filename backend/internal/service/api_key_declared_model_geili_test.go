package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExplicitDeclaredModelGeiliExactOnlyAvoidsCatalog(t *testing.T) {
	for _, tt := range []struct {
		name, declared, request string
		enabled                 bool
		calls                   int
	}{
		{"exact", "model", "model", true, 0}, {"declaration whitespace", " model ", "model", true, 0},
		{"case equivalent", "MODEL", "model", true, 1}, {"wildcard", "mod*", "model", true, 1},
		{"alias", "model", "model-thinking", true, 1}, {"disabled", "model", "model", false, 1},
		{"request whitespace", "model", " model ", true, 1}, {"empty", "", "", true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &thinModelCatalogRepoGeili{accounts: []Account{{Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{tt.request: tt.request}}}}}
			r := &CompositeRouteResolver{accountRepo: repo}
			g := &Group{ID: 7, Platform: PlatformAnthropic, ModelAllowlist: GroupModelAllowlist{Enabled: tt.enabled, Models: []string{tt.declared}}}
			got, err := r.explicitRequestedModelSupportedGeili(context.Background(), g, tt.request)
			require.NoError(t, err)
			// Compare the old full catalog result, including case/alias normalization.
			models, err := r.ExplicitGroupModels(context.Background(), g)
			require.NoError(t, err)
			want := false
			for _, m := range models {
				if m == tt.request {
					want = true
				}
			}
			require.Equal(t, want, got)
			require.Equal(t, tt.calls+1, repo.catalog)
		})
	}
}

func TestExplicitDeclaredModelGeiliDependencyAndCatalogFailures(t *testing.T) {
	g := &Group{ID: 7, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"model"}}}
	_, err := (&CompositeRouteResolver{}).explicitRequestedModelSupportedGeili(context.Background(), g, "model")
	require.Error(t, err)
	failed := errors.New("catalog offline")
	repo := &thinModelCatalogRepoGeili{err: failed}
	r := &CompositeRouteResolver{accountRepo: repo}
	ok, err := r.explicitRequestedModelSupportedGeili(context.Background(), g, "model")
	require.True(t, ok)
	require.NoError(t, err)
	require.Zero(t, repo.catalog)
	_, err = r.explicitRequestedModelSupportedGeili(context.Background(), g, "unknown")
	require.ErrorIs(t, err, failed)
	_, err = r.ExplicitGroupModels(context.Background(), g)
	require.ErrorIs(t, err, failed, "model listing still reads its catalog")
}

func TestExplicitDeclaredModelGeiliRoutingKeepsAuthorizationEndpointAndPricing(t *testing.T) {
	for _, scenario := range []string{"allowed", "exclusive", "inactive", "wrong_endpoint", "unpriced", "subscription_disabled"} {
		t.Run(scenario, func(t *testing.T) {
			gid := int64(7)
			model := "grok-imagine"
			g := &Group{ID: gid, Status: StatusActive, Platform: PlatformGrok, ImagePrice1K: ptrFloatRoute(.05), ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{model}}}
			key := &APIKey{UserID: 1, BillingSource: BillingSourceBalance, RoutingMode: KeyRoutingSingle, GroupID: &gid}
			svc := &APIKeyService{userRepo: &routingUserRepo{user: &User{ID: 1}}, groupRepo: &routingGroupRepo{groups: map[int64]*Group{gid: g}}}
			repo := &thinModelCatalogRepoGeili{err: errors.New("must not enumerate directory")}
			r := &CompositeRouteResolver{accountRepo: repo}
			r.SetRouteValidation(svc.groupRepo, NewModelPricingResolver(nil, NewBillingService(&config.Config{}, nil)))
			endpoint := CompositeRouteEndpointImages
			switch scenario {
			case "exclusive":
				g.IsExclusive = true
			case "inactive":
				g.Status = "inactive"
			case "wrong_endpoint":
				endpoint = CompositeRouteEndpointEmbeddings
			case "unpriced":
				g.ImagePrice1K = nil
			case "subscription_disabled":
				key.BillingSource = BillingSourceSubscription
			}
			rows, err := svc.ResolveExplicitKeyRoutes(context.Background(), key, model, endpoint, r)
			if scenario == "allowed" {
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.Equal(t, gid, *rows[0].TargetGroupID)
			} else {
				require.Error(t, err)
			}
			require.Zero(t, repo.catalog)
		})
	}
}
