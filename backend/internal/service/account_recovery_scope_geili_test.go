//go:build unit

package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountRecoveryScopeGeiliDedicatedHTTPGateDoesNotChangeRetryContract(t *testing.T) {
	for _, scope := range []string{"absent", "false", "true"} {
		t.Run(scope, func(t *testing.T) {
			ctx := WithRequestRecovery(context.Background(), time.Minute)
			if scope != "absent" {
				ctx = WithHTTPAccountRecoveryScopeGeili(ctx, scope == "true")
			}
			require.True(t, RequestRecoveryAllowed(ctx), "scope is not the ordinary upstream retry policy")
			require.Equal(t, time.Minute, RequestRecoveryRemaining(ctx))
			allowed := scope != "false"
			require.Equal(t, allowed, AccountRecoveryScopeAllowedGeili(ctx))
			require.Equal(t, allowed, recoverySelectionAllowedGeili(ctx, ErrNoAvailableAccounts))
			repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
			var probes atomic.Int64
			recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) { probes.Add(1); return true, nil }, nil)
			defer recovery.Stop()
			require.Equal(t, allowed, recovery.RecoverGroupGeili(ctx, 7, "claude-sonnet-4-5", nil))
			if allowed {
				require.Equal(t, int64(1), probes.Load())
			} else {
				require.Zero(t, probes.Load())
				require.Empty(t, repo.groups, "denied HTTP scope must not even discover candidates")
				state := requestRecoveryFromContext(ctx)
				require.True(t, state.deadline.IsZero())
				require.True(t, state.accountRecoveryDeadline.IsZero())
				require.True(t, BeginRequestRecovery(ctx), "normal generation retry remains available")
			}
		})
	}
}

func TestAccountRecoveryScopeGeiliFalseMarkerCannotBeOverriddenByCatalogOptIn(t *testing.T) {
	svc, key, resolver, group, _ := stoppedTextCatalogFixtureGeili(stoppedTextDeclaredAccountGeili())
	ctx := WithHTTPAccountRecoveryScopeGeili(context.Background(), false)
	require.False(t, ExplicitKeyStoppedTextRecoveryAllowedGeili(ctx, []byte(`{"model":"claude-sonnet-4-5","account_recovery_scope":true}`)))
	allowed, err := resolver.explicitRecoverableTextModelGeili(ctx, group, "claude-sonnet-4-5", CompositeRouteEndpointMessages)
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = svc.ResolveExplicitKeyRoutes(ctx, key, "claude-sonnet-4-5", CompositeRouteEndpointMessages, resolver, true)
	require.Error(t, err)
}
