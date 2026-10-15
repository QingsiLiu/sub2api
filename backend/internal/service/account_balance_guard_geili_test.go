//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSupplierBalanceFailureGeiliOnlySupplierErrorFields(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		blocked    bool
	}{
		{"official", `{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API."}}`, true},
		{"nested responses", `{"response":{"error":{"code":"insufficient_balance","message":"declined"}}}`, true},
		{"Chinese", `{"error":{"message":"预扣费额度失败，账户余额不足"}}`, true},
		{"top-level error code", `{"code":"insufficient_credit"}`, true},
		{"request echo", `{"error":{"message":"invalid request"},"input":"insufficient balance"}`, false},
		{"quota without money evidence", `{"error":{"code":"rate_limit_exceeded","message":"request quota exceeded"}}`, false},
		{"HTML echo", `<html>insufficient balance</html>`, false},
		{"JSON malformed", `{"error":{"message":"insufficient balance"}`, false},
		{"authentication", `{"error":{"type":"authentication_error","message":"invalid API key"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.blocked, supplierBalanceFailureGeili([]byte(tc.body))) })
	}
}
