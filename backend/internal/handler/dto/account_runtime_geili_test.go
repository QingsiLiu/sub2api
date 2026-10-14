package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRuntimeGeiliSurvivesLiteProjectionWithoutChangingStatus(t *testing.T) {
	account := &service.Account{ID: 45, Status: service.StatusActive, Schedulable: true}
	full := AccountFromService(account)
	shallow := AccountFromServiceShallow(account)
	lite := AccountListItemFromAccount(shallow)
	require.NotNil(t, full.RuntimeState)
	require.Equal(t, service.StatusActive, full.Status)
	require.Equal(t, full.RuntimeState, shallow.RuntimeState)
	require.Equal(t, full.RuntimeState, lite.RuntimeState)
	require.Equal(t, full.Status, lite.Status)
}
