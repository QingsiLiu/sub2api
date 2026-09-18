//go:build unit

package admin

import (
	"encoding/json"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionAuditAdminOrderIncludesModeAndQuantity(t *testing.T) {
	result := sanitizeAdminPaymentOrderForResponse(&dbent.PaymentOrder{ID: 1, OrderType: "subscription", SubscriptionMode: "stack", SubscriptionQuantity: 2})
	data, err := json.Marshal(result)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	require.Equal(t, "stack", fields["subscription_mode"])
	require.Equal(t, float64(2), fields["subscription_quantity"])
}
