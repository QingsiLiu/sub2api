package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Composite Key routing (routes/key_group_routing_geili.go) and the Codex 429
// shim recognise upstream-side 429s by these exact type/message pairs. Changing
// them upstream must fail here instead of silently ending cross-group retry.
func TestUpstreamRateLimitClientContractForGeiliRouting(t *testing.T) {
	status, kind, message := (&OpenAIGatewayHandler{}).mapUpstreamError(http.StatusTooManyRequests)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", kind)
	require.Equal(t, "Upstream rate limit exceeded, please retry later", message)

	status, kind, message = (&GatewayHandler{}).mapUpstreamError(http.StatusTooManyRequests)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", kind)
	require.Equal(t, "Upstream rate limit exceeded, please retry later", message)

	cls := classifySelectionFailureError(errors.New("no available accounts: rate_limited=3"), noAccountErrorClassification{Status: http.StatusServiceUnavailable})
	require.Equal(t, http.StatusTooManyRequests, cls.Status)
	require.Equal(t, "rate_limit_error", cls.ErrType)
	require.Equal(t, "All available accounts are currently rate-limited. Please retry later.", cls.Message)

	status, kind, code, _ := concurrencyErrorResponse(&WaitQueueFullError{}, "user")
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", kind)
	require.Equal(t, gatewayQueueFullCode, code)
	status, kind, code, _ = concurrencyErrorResponse(&ConcurrencyError{SlotType: "user"}, "user")
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", kind)
	require.Equal(t, gatewayConcurrencyLimitCode, code)
}
