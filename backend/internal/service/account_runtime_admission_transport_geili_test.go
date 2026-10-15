//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountRuntimeAdmissionGeiliTransportAdaptersPreserveClassifiedDecision(t *testing.T) {
	adapters := []struct {
		name string
		run  func(context.Context, *gin.Context, *Account, error) error
	}{
		{"Gateway", func(ctx context.Context, c *gin.Context, a *Account, err error) error {
			return (&GatewayService{}).handleUpstreamTransportError(ctx, c, a, err, OpsUpstreamErrorEvent{})
		}},
		{"OpenAI", func(ctx context.Context, c *gin.Context, a *Account, err error) error {
			return (&OpenAIGatewayService{}).handleOpenAIUpstreamTransportError(ctx, c, a, err, false)
		}},
		{"Gemini", func(ctx context.Context, c *gin.Context, a *Account, err error) error {
			return (&GeminiMessagesCompatService{}).handleUpstreamTransportError(ctx, c, a, err)
		}},
		{"Antigravity compat", func(_ context.Context, c *gin.Context, _ *Account, err error) error {
			return (&AntigravityGatewayService{}).handleAntigravityCompatTransportError(c, err)
		}},
	}
	for _, adapter := range adapters {
		for _, reason := range []string{"verification_unavailable", "balance", "model_failure"} {
			t.Run(adapter.name+"/"+reason, func(t *testing.T) {
				c, writer := guardContextGeili("/v1/messages")
				account := stoppedRecoveryAccountGeili(1, 7)
				decision := NewAccountRuntimeAdmissionErrorGeili(reason)
				wrapped := fmt.Errorf("transport adapter wrapper: %w", decision)
				got := adapter.run(c.Request.Context(), c, &account, wrapped)
				require.Same(t, wrapped, got)
				var failure *UpstreamFailoverError
				require.ErrorAs(t, got, &failure)
				require.False(t, failure.ShouldReportAccountScheduleFailure())
				_, recorded := c.Get(OpsUpstreamErrorsKey)
				require.False(t, recorded)
				require.Empty(t, writer.Body.String())
				require.Nil(t, account.TempUnschedulableUntil)
			})
		}
	}
}

func TestAccountBalanceGuardGeiliSupplierDebtAfterOutputDoesNotPersistDebt(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(kind, func(t *testing.T) {
			account := newAnthropicOAuthAccountForPartialUsageTest()
			if kind == AccountTypeAPIKey {
				account = newAnthropicAPIKeyAccountForTest()
			}
			account.Status, account.Schedulable = StatusActive, true
			repo := newRecoveryRuntimeRepoGeili(*account)
			upstreamBody := anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, `{"type":"error","error":{"type":"invalid_request_error","code":"insufficient_balance","message":"credit balance is too low"}}`)
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			svc.accountRepo = repo
			svc.rateLimitService = &RateLimitService{accountRepo: repo}
			c, rec := guardContextGeili("/v1/messages")
			parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-opus-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`)), PlatformAnthropic)
			require.NoError(t, parseErr)
			result, err := svc.Forward(c.Request.Context(), c, account, parsed)
			require.Error(t, err)
			require.NotNil(t, result)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover), "delivered output cannot authorize replay")
			require.Contains(t, rec.Body.String(), "visible")
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error"))
			stored, readErr := repo.GetByID(context.Background(), account.ID)
			require.NoError(t, readErr)
			require.False(t, stored.RuntimeState.BalanceBlocked)
			require.True(t, stored.Schedulable)
		})
	}
}

type accountRuntimeAdmissionHTTPGeili struct {
	err   error
	calls int
}

func (u *accountRuntimeAdmissionHTTPGeili) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.calls++
	return nil, u.err
}
func (u *accountRuntimeAdmissionHTTPGeili) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestAccountRuntimeAdmissionGeiliAntigravityDoesNotRetryOrRecordNetworkFault(t *testing.T) {
	decision := NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
	upstream := &accountRuntimeAdmissionHTTPGeili{err: decision}
	c, _ := guardContextGeili("/v1/messages")
	account := &Account{ID: 3, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	_, err := (&AntigravityGatewayService{}).antigravityRetryLoop(antigravityRetryLoopParams{ctx: c.Request.Context(), c: c, account: account, accessToken: "probe", action: "streamGenerateContent", body: []byte(`{"model":"gemini-3.1-pro-high","request":{}}`), httpUpstream: upstream, requestedModel: "gemini-3.1-pro-high"})
	require.Same(t, decision, err)
	require.Equal(t, 1, upstream.calls)
	_, recorded := c.Get(OpsUpstreamErrorsKey)
	require.False(t, recorded)
	require.Nil(t, account.TempUnschedulableUntil)
}
