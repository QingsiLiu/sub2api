//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSettlementCompleteRetryDoesNotRewriteFrozenMetadata(t *testing.T) {
	r, cmd, log := settlementFixture(t)
	ctx := context.Background()
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	version := func() string {
		var xmin string
		require.NoError(t, integrationDB.QueryRow(`SELECT xmin::text FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&xmin))
		return xmin
	}
	first := version()
	retry := *log
	retry.InputTokens++
	require.NoError(t, r.PrepareSettlement(ctx, cmd, &retry))
	require.Equal(t, first, version(), "complete immutable evidence needs no metadata UPDATE")
	changed := *cmd
	changed.BalanceCost++
	require.ErrorIs(t, r.PrepareSettlement(ctx, &changed, log), service.ErrUsageBillingRequestConflict)
	require.Equal(t, first, version(), "conflicting money still rejected without modifying evidence")
	_, err := integrationDB.Exec(`UPDATE usage_settlement_receipts SET detail=NULL,record_completeness='partial',usage_request_id=NULL WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID)
	require.NoError(t, err)
	require.NoError(t, r.PrepareSettlement(ctx, cmd, log))
	var tokens int
	var usageID, completeness string
	require.NoError(t, integrationDB.QueryRow(`SELECT (detail->>'input_tokens')::int,usage_request_id,record_completeness FROM usage_settlement_receipts WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).Scan(&tokens, &usageID, &completeness))
	require.Equal(t, log.InputTokens, tokens)
	require.Equal(t, log.RequestID, usageID)
	require.Equal(t, "complete", completeness)
	upgraded := version()
	require.NoError(t, r.PrepareSettlement(ctx, cmd, &retry))
	require.Equal(t, upgraded, version())
}
