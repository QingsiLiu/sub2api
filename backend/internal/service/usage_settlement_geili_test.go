package service

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageSettlementDetailFreezesSafeWhitelist(t *testing.T) {
	tier := "priority"
	log := &UsageLog{UserID: 1, APIKeyID: 2, AccountID: 3, RequestID: "request", Model: "model", ActualCost: .125, InputTokens: 42, ServiceTier: &tier, CreatedAt: time.Now(), ImageSizeBreakdown: map[string]int{"1024x1024": 1}, User: &User{PasswordHash: "secret-password"}, APIKey: &APIKey{Key: "sk-secret-key"}, Account: &Account{Credentials: map[string]any{"api_key": "secret-supplier"}}}
	d, err := NewUsageSettlementDetail(log)
	require.NoError(t, err)
	tier = "changed"
	log.ImageSizeBreakdown["1024x1024"] = 99
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret")
	require.NotContains(t, string(raw), "password")
	require.Contains(t, string(raw), `"input_tokens":42`)
	require.Contains(t, string(raw), `"actual_cost":0.125`)
	restored := d.UsageLog()
	require.Equal(t, "priority", *restored.ServiceTier)
	require.Equal(t, 1, restored.ImageSizeBreakdown["1024x1024"])
	require.Nil(t, restored.User)
	require.Nil(t, restored.APIKey)
	require.Nil(t, restored.Account)
	// The command must never persist the original association-rich log.
	cmd := UsageBillingCommand{UsageDetail: log}
	raw, err = json.Marshal(cmd)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(raw), "secret"))
}

func TestUsageSettlementRejectsNonFiniteAndMixedMoney(t *testing.T) {
	for _, amount := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		require.Error(t, ValidateUsageSettlementCommand(&UsageBillingCommand{RequestID: "r", UserID: 1, BalanceCost: amount}))
	}
	require.Error(t, ValidateUsageSettlementCommand(&UsageBillingCommand{RequestID: "r", UserID: 1, BalanceCost: 1, SubscriptionCost: 1}))
	require.Error(t, ValidateUsageSettlementCommand(&UsageBillingCommand{RequestID: "r", UserID: 1, SubscriptionCost: 1}))
	require.NoError(t, ValidateUsageSettlementCommand(&UsageBillingCommand{RequestID: "r", UserID: 1}))
	_, err := NewUsageSettlementDetail(&UsageLog{ActualCost: math.NaN()})
	require.Error(t, err)
}
