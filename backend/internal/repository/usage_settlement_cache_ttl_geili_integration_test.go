//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettlementCacheTTLGeiliDurableAndLegacyWriter(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, free := range []bool{false, true} {
			t.Run(map[bool]string{false: "new", true: "legacy"}[legacy]+map[bool]string{false: "-paid", true: "-free"}[free], func(t *testing.T) {
				ctx := context.Background()
				r, cmd, log := settlementFixture(t)
				log.CacheCreationTokens, log.CacheCreation30mTokens, log.CacheCreation12hTokens = 2399, 9, 2390
				if free {
					cmd.BalanceCost, cmd.APIKeyQuotaCost, log.ActualCost = 0, 0, 0
				}
				require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
				result, err := r.Apply(ctx, cmd)
				require.NoError(t, err)
				require.True(t, result.Applied)
				var before float64
				require.NoError(t, integrationDB.QueryRow("SELECT balance FROM users WHERE id=$1", cmd.UserID).Scan(&before))
				if legacy {
					// Old binaries decode the aggregate and money but do not know TTL
					// columns. Supplying their default zeros is equivalent to omission.
					log.CacheCreation30mTokens, log.CacheCreation12hTokens = 0, 0
					_, err = (&usageLogRepository{}).createSingle(ctx, integrationDB, log)
					require.NoError(t, err)
				}
				_, err = r.DeliverSettledUsage(ctx, 128)
				require.NoError(t, err)
				var short, long, count int
				var amount, after float64
				require.NoError(t, integrationDB.QueryRow("SELECT cache_creation_30m_tokens,cache_creation_12h_tokens,actual_cost FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", log.RequestID, cmd.APIKeyID).Scan(&short, &long, &amount))
				require.Equal(t, 9, short)
				require.Equal(t, 2390, long)
				require.Equal(t, cmd.BalanceCost, amount)
				require.NoError(t, integrationDB.QueryRow("SELECT balance FROM users WHERE id=$1", cmd.UserID).Scan(&after))
				require.Equal(t, before, after, "metadata delivery cannot debit again")
				require.NoError(t, integrationDB.QueryRow("SELECT count(*) FROM usage_settlement_receipts WHERE api_key_id=$1 AND delivered_at IS NOT NULL", cmd.APIKeyID).Scan(&count))
				require.Equal(t, 1, count)
			})
		}
	}
}

func TestSettlementCacheTTLGeiliLegacyPlaceholderAndIdentityGuard(t *testing.T) {
	ctx := context.Background()
	r, cmd, log := settlementFixture(t)
	log.CacheCreationTokens, log.CacheCreation30mTokens, log.CacheCreation12hTokens = 10, 3, 7
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	_, err := r.Apply(ctx, cmd)
	require.NoError(t, err)
	legacy := *log
	legacy.ActualCost, legacy.CacheCreationTokens = 0, 0
	legacy.CacheCreation30mTokens, legacy.CacheCreation12hTokens = 0, 0
	_, err = (&usageLogRepository{}).createSingle(ctx, integrationDB, &legacy)
	require.NoError(t, err)
	// This is the old placeholder restoration shape: no TTL fields in UPDATE.
	_, err = integrationDB.Exec("UPDATE usage_logs SET actual_cost=$2,cache_creation_tokens=10 WHERE id=$1", legacy.ID, log.ActualCost)
	require.NoError(t, err)
	var short, long int
	require.NoError(t, integrationDB.QueryRow("SELECT cache_creation_30m_tokens,cache_creation_12h_tokens FROM usage_logs WHERE id=$1", legacy.ID).Scan(&short, &long))
	require.Equal(t, 3, short)
	require.Equal(t, 7, long)
	// A mismatched financial amount must not import somebody else's metadata.
	_, err = integrationDB.Exec("UPDATE usage_logs SET actual_cost=9,cache_creation_30m_tokens=0,cache_creation_12h_tokens=0 WHERE id=$1", legacy.ID)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRow("SELECT cache_creation_30m_tokens,cache_creation_12h_tokens FROM usage_logs WHERE id=$1", legacy.ID).Scan(&short, &long))
	require.Zero(t, short)
	require.Zero(t, long)
	// Restore this owned fixture's financial identity before its cleanup.
	_, err = integrationDB.Exec("UPDATE usage_logs SET actual_cost=$2 WHERE id=$1", legacy.ID, log.ActualCost)
	require.NoError(t, err)
}
