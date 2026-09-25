package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestFinancialUsageDTOExplicitUnknownIsNull(t *testing.T) {
	log := &service.UsageLog{ID: -12, UserID: 7, APIKeyID: 9, ActualCost: 10.84225728, Financial: &service.UsageFinancialMetadata{RecordSource: "historical_recovery", RecordCompleteness: "partial", UnknownFields: []string{"account_id", "model", "input_tokens", "total_cost", "created_at"}}}
	data, err := json.Marshal(UsageLogFromService(log))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	for _, field := range log.Financial.UnknownFields {
		value, present := got[field]
		require.True(t, present, field)
		require.Nil(t, value, field)
	}
	require.Equal(t, log.ActualCost, got["actual_cost"])
	require.Equal(t, "historical_recovery", got["record_source"])
	require.Nil(t, got["completed_at"])
	require.NotContains(t, got, "account_rate_multiplier")
}

func TestFinancialUsageAdminMarshalerRetainsAdminFields(t *testing.T) {
	upstream := "private-upstream"
	rate := 2.0
	log := &service.UsageLog{ID: 7, Model: "public", UpstreamModel: &upstream, AccountRateMultiplier: &rate, Financial: &service.UsageFinancialMetadata{RecordSource: "live", RecordCompleteness: "complete", UnknownFields: []string{}}}
	data, err := json.Marshal(UsageLogFromServiceAdmin(log))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, upstream, got["upstream_model"])
	require.Equal(t, rate, got["account_rate_multiplier"])
	require.Equal(t, "public", got["model"])
}
