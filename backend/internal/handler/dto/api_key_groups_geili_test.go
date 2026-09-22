package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyFromServiceCompositeGroups(t *testing.T) {
	got := APIKeyFromService(&service.APIKey{RoutingMode: "composite", GroupIDs: []int64{22, 11}, Groups: []*service.Group{{ID: 22, Name: "Second"}, nil, {ID: 11, Name: "First"}}})
	require.Nil(t, got.GroupID)
	require.Nil(t, got.Group)
	require.Equal(t, []int64{22, 11}, got.GroupIDs)
	require.Len(t, got.Groups, 2)
	require.Equal(t, "Second", got.Groups[0].Name)
	require.Equal(t, "First", got.Groups[1].Name)
	require.Empty(t, APIKeyFromService(&service.APIKey{}).Groups)
}
