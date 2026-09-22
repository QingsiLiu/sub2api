package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCompositeKeyListsHydrateOrderedGroups(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	owner := mustCreateAPIKeyRepoUser(t, ctx, client, "composite-groups@example.invalid")
	first, err := client.Group.Create().SetName("First").Save(ctx)
	require.NoError(t, err)
	second, err := client.Group.Create().SetName("Second").Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{UserID: owner.ID, Key: "sk-composite-fixture", Name: "Composite", Status: service.StatusActive, RoutingMode: "composite", BillingSource: "balance", GroupIDs: []int64{second.ID, first.ID}}
	require.NoError(t, repo.Create(ctx, key))
	list, _, err := repo.ListByUserID(ctx, owner.ID, pagination.PaginationParams{Page: 1, PageSize: 20}, service.APIKeyListFilters{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Nil(t, list[0].GroupID)
	require.Nil(t, list[0].Group)
	require.Len(t, list[0].Groups, 2)
	require.Equal(t, "Second", list[0].Groups[0].Name)
	require.Equal(t, "First", list[0].Groups[1].Name)
	all, err := repo.ListAllByUserID(ctx, owner.ID, service.APIKeyListFilters{})
	require.NoError(t, err)
	require.Equal(t, list[0].Groups, all[0].Groups)
	byID, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, list[0].Groups, byID.Groups)
	byKey, err := repo.GetByKey(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, list[0].Groups, byKey.Groups)
}
