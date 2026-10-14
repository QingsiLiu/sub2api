//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type modelRuntimePolicyRepoGeili struct {
	*recoveryRuntimeRepoGeili
	permanentWrites atomic.Int64
}

func (r *modelRuntimePolicyRepoGeili) SetError(context.Context, int64, string) error {
	r.permanentWrites.Add(1)
	return nil
}

func TestRateLimitRuntimeGeili5xxBypassesPoolCustomPermanentPolicy(t *testing.T) {
	for _, status := range []int{500, 503, 529} {
		for _, pool := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/pool=%v", status, pool), func(t *testing.T) {
				a := stoppedRecoveryAccountGeili(1, 7)
				a.Status = StatusActive
				a.Schedulable = true
				a.Credentials["pool_mode"] = pool
				a.Credentials["custom_error_codes_enabled"] = true
				a.Credentials["custom_error_codes"] = []any{float64(status)}
				r := &modelRuntimePolicyRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(a)}
				limits := &RateLimitService{accountRepo: r}
				require.Equal(t, ErrorPolicyNone, limits.CheckErrorPolicy(context.Background(), &a, status, []byte(`{"error":{"type":"overloaded_error"}}`), "claude-opus-5"))
				for i := 0; i < 3; i++ {
					require.False(t, limits.HandleUpstreamError(context.Background(), &a, status, http.Header{}, []byte(`{"error":{"type":"api_error"}}`), "claude-opus-5"))
				}
				state, err := r.GetAccountRuntimeGeili(context.Background(), 1)
				require.NoError(t, err)
				require.Equal(t, 3, state.Models["claude-opus-5"].FailureCount)
				require.Zero(t, r.permanentWrites.Load())
				require.True(t, a.Schedulable)
			})
		}
	}
}

func TestRateLimitRuntimeGeiliReceiptDeduplicatesHandlerAndObserverModelAlias(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	r := &modelRuntimePolicyRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(a)}
	limits := &RateLimitService{accountRepo: r}
	ctx := WithAccountModelFailureObservationGeili(context.Background())
	ctx = context.WithValue(ctx, accountRuntimeForwardModelKeyGeili{}, "requested-alias")
	limits.HandleUpstreamError(ctx, &a, 503, http.Header{}, nil, "mapped-upstream-model")
	require.False(t, ClaimAccountModelFailureObservationGeili(ctx, 1, "requested-alias"))
	state, err := r.GetAccountRuntimeGeili(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, state.Models["requested-alias"].FailureCount)
	require.NotContains(t, state.Models, "mapped-upstream-model")
}

func TestAccountModelFailureObservationGeiliConcurrentClaimsOnce(t *testing.T) {
	ctx := WithAccountModelFailureObservationGeili(context.Background())
	var wins atomic.Int64
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if ClaimAccountModelFailureObservationGeili(ctx, 17, "m") {
				wins.Add(1)
			}
		}()
	}
	group.Wait()
	require.Equal(t, int64(1), wins.Load())
	require.True(t, ClaimAccountModelFailureObservationGeili(ctx, 18, "m"))
	require.True(t, ClaimAccountModelFailureObservationGeili(ctx, 17, "other"))
}

func TestAccountGenericTemporaryStopGeiliPreservesProviderAndModelWindows(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Hour)
	for _, test := range []struct {
		reason  string
		generic bool
	}{{"upstream connection timeout", true}, {`{"status_code":503,"scope":"account"}`, true}, {`{"status_code":429,"scope":"account"}`, false}, {`{"status_code":503,"scope":"model"}`, false}, {"provider usage quota window exhausted", false}} {
		a := &Account{TempUnschedulableUntil: &until, TempUnschedulableReason: test.reason}
		require.Equal(t, test.generic, AccountGenericTemporaryStopGeili(a, now), test.reason)
	}
}
