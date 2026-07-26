//go:build unit

package repository

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBuildSchedulerMetadataAccount_KeepsOpenAIWSFlags(t *testing.T) {
	account := service.Account{
		ID:       42,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			"openai_oauth_responses_websockets_v2_mode":    service.OpenAIWSIngressModePassthrough,
			"openai_ws_force_http":                         true,
			"mixed_scheduling":                             true,
			"unused_large_field":                           "drop-me",
		},
	}

	got := buildSchedulerMetadataAccount(account)

	require.Equal(t, true, got.Extra["openai_oauth_responses_websockets_v2_enabled"])
	require.Equal(t, service.OpenAIWSIngressModePassthrough, got.Extra["openai_oauth_responses_websockets_v2_mode"])
	require.Equal(t, true, got.Extra["openai_ws_force_http"])
	require.Equal(t, true, got.Extra["mixed_scheduling"])
	require.Nil(t, got.Extra["unused_large_field"])
}

func TestBuildSchedulerMetadataAccount_KeepsScalarAccountGroups(t *testing.T) {
	account := service.Account{
		ID: 42,
		AccountGroups: []service.AccountGroup{
			{
				AccountID: 42,
				GroupID:   101,
				Priority:  7,
				CreatedAt: time.Unix(100, 0),
				Account:   &service.Account{ID: 999},
				Group:     &service.Group{ID: 888},
			},
			{
				AccountID: 42,
				GroupID:   0,
				Priority:  99,
			},
		},
	}

	got := buildSchedulerMetadataAccount(account)

	require.Len(t, got.AccountGroups, 1)
	require.Equal(t, int64(42), got.AccountGroups[0].AccountID)
	require.Equal(t, int64(101), got.AccountGroups[0].GroupID)
	require.Equal(t, 7, got.AccountGroups[0].Priority)
	require.Equal(t, time.Unix(100, 0), got.AccountGroups[0].CreatedAt)
	require.Nil(t, got.AccountGroups[0].Account)
	require.Nil(t, got.AccountGroups[0].Group)

	payload, err := json.Marshal(got)
	require.NoError(t, err)
	var roundTrip service.Account
	require.NoError(t, json.Unmarshal(payload, &roundTrip))
	require.Len(t, roundTrip.AccountGroups, 1)
	require.Equal(t, int64(101), roundTrip.AccountGroups[0].GroupID)
	require.Equal(t, 7, roundTrip.AccountGroups[0].Priority)
}
