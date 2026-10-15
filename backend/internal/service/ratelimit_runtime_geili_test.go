//go:build unit

package service

import (
	"context"
	"net/http"
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

func TestRateLimitRuntimeGeiliExistingPolicyHasNoNewCounters(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	a.Status, a.Schedulable = StatusActive, true
	a.Credentials["custom_error_codes_enabled"] = true
	a.Credentials["custom_error_codes"] = []any{float64(500)}
	repo := &modelRuntimePolicyRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(a)}
	limits := &RateLimitService{accountRepo: repo}
	ctx := context.Background()
	require.True(t, limits.HandleUpstreamError(ctx, &a, 500, http.Header{}, []byte(`{"error":{"message":"synthetic native media fault"}}`), "native-image-model"))
	require.EqualValues(t, 1, repo.permanentWrites.Load(), "native media keeps its existing recovery policy")
	state, err := repo.GetAccountRuntimeGeili(ctx, a.ID)
	require.NoError(t, err)
	require.Empty(t, state.Models, "no text-only counter may accumulate without a matching success callback")
}
