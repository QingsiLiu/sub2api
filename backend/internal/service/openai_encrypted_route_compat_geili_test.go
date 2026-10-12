package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncryptedRouteCompatibilityGeiliHTTPActualWireModelAliasOnce(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportAny} {
			t.Run(string(transport)+map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
				svc, a, ctx, group, state := encryptedRouteWaitFixtureGeili(t)
				const public = "synthetic-public-alias"
				const actual = "synthetic-first-upstream"
				const remapped = "synthetic-wrong-second-hop"
				a.Credentials["model_mapping"] = map[string]any{public: actual, actual: remapped}
				a.Extra["openai_passthrough"] = passthrough
				wire := actual
				if passthrough {
					wire = public
				}
				// This is the exact point used by HTTP Forward after final wire model resolution.
				WithOpenAIEncryptedActualOutputOriginGeili(ctx, &group, a, wire)
				require.Equal(t, wire, state.origin.Model)
				state.owner.Model = state.origin.Model
				state.owner.BackendFingerprint = encryptedBackendFingerprintGeili(a)
				svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*a}}
				selection, handled, err := svc.selectOpenAIEncryptedRouteGeili(ctx, &group, "session", public, PlatformOpenAI, nil, transport, OpenAIEndpointCapabilityResponses, "", false, false)
				require.NoError(t, err)
				require.True(t, handled)
				require.NotNil(t, selection)
				require.Equal(t, a.ID, selection.Account.ID)
				state.owner.Model = remapped
				selection, _, err = svc.selectOpenAIEncryptedRouteGeili(ctx, &group, "session", public, PlatformOpenAI, nil, transport, OpenAIEndpointCapabilityResponses, "", false, false)
				require.Error(t, err)
				require.Nil(t, selection)
				var failure *OpenAIEncryptedRouteErrorGeili
				require.ErrorAs(t, err, &failure)
				require.Equal(t, 409, failure.Status)
			})
		}
	}
}
func TestEncryptedRouteCompatibilityGeiliLegacyCompositeActualTargetMembershipAndOrigin(t *testing.T) {
	svc, a, ctx, target, state := encryptedRouteWaitFixtureGeili(t)
	legacy := int64(9999)
	ctx = WithCompositeRouteDecision(ctx, CompositeRouteDecision{Matched: true, TargetPlatform: PlatformOpenAI, TargetGroupID: &target})
	WithOpenAIEncryptedOutputOriginGeili(ctx, &legacy, a, "gpt-6-astra")
	require.Equal(t, target, state.origin.GroupID)
	WithOpenAIEncryptedActualOutputOriginGeili(ctx, &legacy, a, "gpt-6-astra")
	require.Equal(t, target, state.origin.GroupID)
	cache := &encryptedRouteCacheFixtureGeili{}
	svc.cache = cache
	state.observed = map[string]bool{}
	output := []byte(`{"object":"response","status":"completed","output":[{"type":"reasoning","encrypted_content":"synthetic-composite-origin"}]}`)
	ObserveOpenAIEncryptedOutputGeili(ctx, output)
	digest := encryptedDigestsGeili([]byte(`{"input":[{"type":"reasoning","encrypted_content":"synthetic-composite-origin"}]}`))[0]
	require.Equal(t, target, cache.routes[3885][digest].GroupID)
	selection, handled, err := svc.selectOpenAIEncryptedRouteGeili(ctx, &legacy, "session", "gpt-6-astra", PlatformOpenAI, nil, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityResponses, "", false, false)
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, a.ID, selection.Account.ID)
	// The legacy Key group cannot authorize an account removed from actual target.
	a.GroupIDs = []int64{legacy}
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{*a}}
	selection, _, err = svc.selectOpenAIEncryptedRouteGeili(ctx, &legacy, "session", "gpt-6-astra", PlatformOpenAI, nil, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityResponses, "", false, false)
	require.Error(t, err)
	require.Nil(t, selection)
	var failure *OpenAIEncryptedRouteErrorGeili
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "encrypted_session_route_conflict", failure.Code)
}
func TestEncryptedRouteCompatibilityGeiliActualTargetAllowsNilLegacyGroupOrigin(t *testing.T) {
	_, a, ctx, target, state := encryptedRouteWaitFixtureGeili(t)
	ctx = WithCompositeRouteDecision(ctx, CompositeRouteDecision{Matched: true, TargetPlatform: PlatformOpenAI, TargetGroupID: &target})
	WithOpenAIEncryptedActualOutputOriginGeili(ctx, nil, a, "gpt-6-astra")
	require.NotNil(t, state.origin)
	require.Equal(t, target, state.origin.GroupID)
}
