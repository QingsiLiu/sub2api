//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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

func TestAccountBalanceGuardGeiliPrecedesPoolAndCustomErrorRules(t *testing.T) {
	for _, method := range []string{"policy", "upstream"} {
		for _, pool := range []bool{false, true} {
			for _, custom := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/pool=%v/custom=%v", method, pool, custom), func(t *testing.T) {
					a := stoppedRecoveryAccountGeili(1, 7)
					a.Status, a.Schedulable = StatusActive, true
					a.Credentials["pool_mode"] = pool
					a.Credentials["custom_error_codes_enabled"] = custom
					a.Credentials["custom_error_codes"] = []any{499.0}
					r := newRecoveryRuntimeRepoGeili(a)
					svc := &RateLimitService{accountRepo: r}
					body := []byte(`{"error":{"message":"Your credit balance is too low"}}`)
					if method == "policy" {
						require.Equal(t, ErrorPolicyTempUnscheduled, svc.CheckErrorPolicy(context.Background(), &a, 402, body, "m"))
					} else {
						require.True(t, svc.HandleUpstreamError(context.Background(), &a, 402, http.Header{}, body, "m"))
					}
					require.Equal(t, StatusError, a.Status)
					require.False(t, a.Schedulable)
					stored, e := r.GetByID(context.Background(), 1)
					require.NoError(t, e)
					require.True(t, stored.RuntimeState.BalanceBlocked)
					require.Equal(t, "supplier_insufficient_balance", stored.RuntimeState.BalanceReason)
				})
			}
		}
	}
}

func TestAccountBalanceGuardGeiliPersistenceFailureStillStopsAttempt(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	a.Status, a.Schedulable = StatusActive, true
	r := newRecoveryRuntimeRepoGeili(a)
	r.markError = errors.New("database unavailable")
	svc := &RateLimitService{accountRepo: r}
	require.True(t, svc.stopSupplierBalanceGeili(context.Background(), &a, 403, []byte(`{"error":{"code":"insufficient_balance"}}`)))
	require.Equal(t, StatusError, a.Status)
	require.False(t, a.Schedulable)
}

func TestAccountBalanceGuardGeiliBlocksEveryModelAndDoesNotReenterDuringProbe(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	r := newRecoveryRuntimeRepoGeili(a)
	require.NoError(t, r.MarkAccountBalanceBlockedGeili(context.Background(), 1, "supplier_insufficient_balance"))
	for _, model := range []string{"claude-opus-5", "claude-sonnet-5-5"} {
		err := checkRuntimeBeforeForwardGeili(context.Background(), r, &a, model)
		var f *UpstreamFailoverError
		require.ErrorAs(t, err, &f)
		require.Equal(t, GatewayFailureScopeAccount, f.Scope)
		require.Equal(t, NextAccountRetry, f.NextAccountAction)
		require.Contains(t, string(f.ResponseBody), "supplier_balance_exhausted")
	}
	probeCtx := context.WithValue(context.Background(), accountRecoveryProbeContextGeili{}, true)
	require.NoError(t, checkRuntimeBeforeForwardGeili(probeCtx, r, &a, "m"))
}

func TestAccountBalanceGuardGeiliReadFailureFailsClosed(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	r := newRecoveryRuntimeRepoGeili(a)
	r.admissionError = errors.New("private database address")
	err := checkRuntimeBeforeForwardGeili(context.Background(), r, &a, "m")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private database address")
}

func TestAccountBalanceGuardGeiliObservedBalanceDisablesSameAccountRetry(t *testing.T) {
	a := stoppedRecoveryAccountGeili(1, 7)
	r := newRecoveryRuntimeRepoGeili(a)
	svc := &RateLimitService{accountRepo: r}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	f := &UpstreamFailoverError{StatusCode: 402, ResponseBody: []byte(`{"error":{"message":"insufficient balance"}}`), RetryableOnSameAccount: true, Scope: GatewayFailureScopeRequest}
	svc.observeAccountResultGeili(context.Background(), c, &a, "m", f)
	require.False(t, f.RetryableOnSameAccount)
	require.Equal(t, NextAccountRetry, f.NextAccountAction)
	stored, _ := r.GetByID(context.Background(), 1)
	require.True(t, stored.RuntimeState.BalanceBlocked)
	require.Equal(t, int64(1), stored.RuntimeState.Version)
	svc.observeAccountResultGeili(context.Background(), c, &a, "m", f)
	stored, _ = r.GetByID(context.Background(), 1)
	require.Equal(t, int64(1), stored.RuntimeState.Version, "same attempt observation is idempotent")
}

func TestAccountBalanceGuardGeiliLocalProtocolAndCanceledClientDoNotPunishSupplier(t *testing.T) {
	for _, scenario := range []string{"local_protocol", "cancel", "probe"} {
		t.Run(scenario, func(t *testing.T) {
			a := stoppedRecoveryAccountGeili(1, 7)
			r := newRecoveryRuntimeRepoGeili(a)
			svc := &RateLimitService{accountRepo: r}
			ctx := context.Background()
			var err error = &UpstreamFailoverError{StatusCode: 502, ResponseBody: []byte(`{"error":{"message":"temporary gateway failure"}}`)}
			switch scenario {
			case "local_protocol":
				err = &anthropicStreamDiagnosticGeiliError{code: "upstream_tool_arguments_invalid", detail: map[string]any{"content_block_index": 0}}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "probe":
				ctx = context.WithValue(ctx, accountRecoveryProbeContextGeili{}, true)
			}
			svc.observeAccountResultGeili(ctx, nil, &a, "m", err)
			stored, _ := r.GetByID(context.Background(), 1)
			require.Empty(t, stored.RuntimeState.Models)
			require.False(t, stored.RuntimeState.BalanceBlocked)
		})
	}
}
