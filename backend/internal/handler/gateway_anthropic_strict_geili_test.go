package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestStrictAnthropicAttemptUsesClientBytes(t *testing.T) {
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}}
	raw := []byte("{ \"model\":\"claude-sonnet-4-6[1m]\", \"max_tokens\":16, \"messages\":[] }\n")

	parsed, err := parseAnthropicGatewayRequest(raw, nil, apiKey)
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-6", parsed.Model, "routing keeps the compatibility view")
	routed := append([]byte(nil), parsed.Body.Bytes()...)

	attempt, err := parsed.CloneForBody(routed)
	require.NoError(t, err)
	require.True(t, useStrictAnthropicBody(attempt, routed, raw))
	require.Equal(t, string(raw), string(attempt.Body.Bytes()), "strict upstream receives the caller's exact bytes")
	require.Equal(t, "claude-sonnet-4-6", attempt.Model)

	rewritten, err := parsed.CloneForBody([]byte(`{"model":"claude-opus-5","max_tokens":16,"messages":[]}`))
	require.NoError(t, err)
	require.False(t, useStrictAnthropicBody(rewritten, routed, raw), "group policy rewrites are rejected, not forwarded")
}

func TestStrictAnthropicLenientRepairStaysCompatibilityOnly(t *testing.T) {
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}}
	raw := []byte("{\"model\":\"claude-opus-5\",\"messages\":[{\"role\":\"user\",\"content\":\"a\tb\"}]}")

	parsed, err := parseAnthropicGatewayRequest(raw, nil, apiKey)
	require.NoError(t, err, "compatibility accounts keep the lenient JSON repair")
	routed := append([]byte(nil), parsed.Body.Bytes()...)
	require.NotEqual(t, string(raw), string(routed))

	attempt, err := parsed.CloneForBody(routed)
	require.NoError(t, err)
	require.False(t, useStrictAnthropicBody(attempt, routed, raw), "strict accounts never forward repaired JSON")
}
