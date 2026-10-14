package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type thinModelCatalogRepoGeili struct {
	AccountRepository
	catalog, availability int
	accounts              []Account
	err                   error
	groupID               int64
	platforms             []string
	includeGrouped        bool
}

func (r *thinModelCatalogRepoGeili) ListModelCatalogCandidatesGeili(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]Account, error) {
	r.catalog++
	r.groupID, r.platforms, r.includeGrouped = *groupID, platforms, includeGrouped
	return r.accounts, r.err
}

func (r *thinModelCatalogRepoGeili) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]Account, error) {
	r.availability++
	return r.accounts, r.err
}

func TestExplicitGroupModelCatalogGeiliOptionalProjectionKeepsDeclarationsAndAllowlist(t *testing.T) {
	repo := &thinModelCatalogRepoGeili{accounts: []Account{{Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"allowed": "upstream", "blocked": "upstream", "wildcard-*": "upstream"}}}}}
	resolver := &CompositeRouteResolver{accountRepo: repo}
	group := &Group{ID: 7, Platform: PlatformAnthropic, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"allowed", "declared"}}}
	models, err := resolver.ExplicitGroupModels(context.Background(), group)
	require.NoError(t, err)
	require.Equal(t, []string{"allowed", "declared"}, models)
	require.Equal(t, 1, repo.catalog)
	require.Zero(t, repo.availability)
	require.Equal(t, int64(7), repo.groupID)
	require.Equal(t, []string{PlatformAnthropic}, repo.platforms)
	require.False(t, repo.includeGrouped)
	failed := errors.New("catalog unavailable")
	repo.err = failed
	_, err = resolver.ExplicitGroupModels(context.Background(), group)
	require.ErrorIs(t, err, failed)
}

func TestExplicitGroupModelCatalogGeiliLegacyRepositoryRetainsAvailabilityContract(t *testing.T) {
	repo := &routingAccountRepo{accounts: []Account{{Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"declared": "upstream"}}}}}
	resolver := &CompositeRouteResolver{accountRepo: repo}
	models, err := resolver.ExplicitGroupModels(context.Background(), &Group{ID: 7, Platform: PlatformAnthropic})
	require.NoError(t, err)
	require.Equal(t, []string{"declared"}, models)
}
