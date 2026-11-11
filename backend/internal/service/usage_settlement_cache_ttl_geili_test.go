package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageSettlementCacheTTLGeiliFrozenRoundTrip(t *testing.T) {
	input := &UsageLog{CacheCreationTokens: 2399, CacheCreation30mTokens: 9, CacheCreation12hTokens: 2390, ActualCost: .125}
	detail, err := NewUsageSettlementDetail(input)
	require.NoError(t, err)
	input.CacheCreation12hTokens = 0
	raw, err := json.Marshal(detail)
	require.NoError(t, err)
	var decoded UsageSettlementDetail
	require.NoError(t, json.Unmarshal(raw, &decoded))
	log := decoded.UsageLog()
	require.Equal(t, 2399, log.CacheCreationTokens)
	require.Equal(t, 9, log.CacheCreation30mTokens)
	require.Equal(t, 2390, log.CacheCreation12hTokens)
	require.Equal(t, .125, log.ActualCost)
	require.Zero(t, log.CacheCreation5mTokens)
	require.Zero(t, log.CacheCreation1hTokens)
}
