//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type nativePaidDebtUpstreamGeili struct {
	response *http.Response
	err      error
	cancel   context.CancelFunc
	requests int
	reads    int
}

func (u *nativePaidDebtUpstreamGeili) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.requests++
	if u.cancel != nil {
		u.cancel()
	}
	// The final transport rejects admission before reading the request body or
	// setting its dispatch marker. Preserve that contract at the service seam.
	if u.err != nil {
		return nil, u.err
	}
	ctxkey.MarkUpstreamDispatched(req.Context())
	if req.Body != nil {
		u.reads++
		_, _ = io.Copy(io.Discard, req.Body)
	}
	return u.response, nil
}
func (u *nativePaidDebtUpstreamGeili) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

type nativePaidDebtRepoGeili struct {
	*recoveryRuntimeRepoGeili
	markCalls int
}

func (r *nativePaidDebtRepoGeili) MarkAccountBalanceBlockedGeili(ctx context.Context, id int64, reason string) error {
	r.markCalls++
	return r.recoveryRuntimeRepoGeili.MarkAccountBalanceBlockedGeili(ctx, id, reason)
}

func nativePaidDebtAccountGeili(kind string) *Account {
	switch kind {
	case "seedance":
		return seedanceTestAccount()
	case "cache":
		return &Account{ID: 9, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-key"}}
	default:
		return grokMediaContentTestAccount()
	}
}

func runNativePaidDebtGeili(t *testing.T, kind string, ctx context.Context, a *Account, up HTTPUpstream, limits *RateLimitService) (error, string) {
	t.Helper()
	c, w := grokMediaContentTestContext(http.MethodPost, "/native-paid", nil)
	c.Request = c.Request.WithContext(ctx)
	svc := &OpenAIGatewayService{httpUpstream: up, rateLimitService: limits, cfg: &config.Config{}}
	var err error
	switch kind {
	case "seedance":
		_, err = svc.ForwardSeedance(ctx, c, a, SeedanceEndpointCreate, "", []byte(`{"model":"video","content":[{"type":"text","text":"waves"}]}`))
	case "cache":
		cache := &GeminiCachedContentService{compat: &GeminiMessagesCompatService{httpUpstream: up, rateLimitService: limits, cfg: &config.Config{}}}
		_, err = cache.doUpstream(ctx, a, http.MethodPost, "https://example.test/v1beta/cachedContents", []byte(`{"model":"gemini-fixture"}`), geminiCachedContentMutationUpstreamTimeout)
	case "grok-images":
		_, err = svc.ForwardGrokMedia(ctx, c, a, GrokMediaEndpointImagesGenerations, "", []byte(`{"model":"grok-imagine-image","prompt":"waves"}`), "application/json")
	default:
		_, err = svc.ForwardGrokMedia(ctx, c, a, GrokMediaEndpointVideosGenerations, "", []byte(`{"model":"grok-imagine-video","prompt":"waves","duration":6}`), "application/json")
	}
	return err, w.Body.String()
}

func TestNativePaidDebtGeiliLocalAdmissionKeepsErrorAndNeverMarksAccepted(t *testing.T) {
	for _, kind := range []string{"grok-videos", "grok-images", "seedance", "cache"} {
		t.Run(kind, func(t *testing.T) {
			denied := NewAccountRuntimeAdmissionErrorGeili("balance")
			up := &nativePaidDebtUpstreamGeili{err: denied}
			ctx, dispatched := ctxkey.WithUpstreamDispatch(context.Background())
			err, _ := runNativePaidDebtGeili(t, kind, ctx, nativePaidDebtAccountGeili(kind), up, nil)
			require.Same(t, denied, err)
			require.False(t, dispatched.Started(), "local rejection cannot retain an accepted-media financial reservation")
			require.NotContains(t, err.Error(), "outcome unknown")
			require.Equal(t, 1, up.requests)
			require.Zero(t, up.reads)
		})
	}
}

func TestNativePaidDebtGeiliSupplierErrorDoesNotAutomaticallyMarkDebt(t *testing.T) {
	for _, kind := range []string{"grok-videos", "grok-images", "seedance", "cache"} {
		for _, status := range []int{http.StatusPaymentRequired, http.StatusServiceUnavailable} {
			t.Run(kind+"/"+http.StatusText(status), func(t *testing.T) {
				a := nativePaidDebtAccountGeili(kind)
				a.Status, a.Schedulable = StatusActive, true
				a.Credentials["pool_mode"] = true
				r := &nativePaidDebtRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(*a)}
				limits := &RateLimitService{accountRepo: r}
				body := `{"error":{"code":"insufficient_balance","message":"Supplier credit balance is too low"}}`
				response := grokMediaContentStatusResponse(body)
				response.StatusCode = status
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				up := &nativePaidDebtUpstreamGeili{response: response, cancel: cancel}
				err, clientBody := runNativePaidDebtGeili(t, kind, ctx, a, up, limits)
				require.Error(t, err)
				require.True(t, errors.Is(ctx.Err(), context.Canceled))
				require.Zero(t, r.markCalls)
				stored, e := r.GetByID(context.Background(), a.ID)
				require.NoError(t, e)
				require.False(t, stored.RuntimeState.BalanceBlocked)
				require.Empty(t, stored.RuntimeState.BalanceReason)
				if kind == "seedance" {
					require.JSONEq(t, body, clientBody, "preserve the native vendor error")
				}
				if kind == "cache" {
					var cacheErr *GeminiCachedContentUpstreamError
					require.ErrorAs(t, err, &cacheErr)
					require.Equal(t, body, string(cacheErr.Body))
				}
			})
		}
	}
}

func TestNativePaidDebtGeiliRequestEchoAndQuotaNeverBecomeDebt(t *testing.T) {
	for _, kind := range []string{"grok-images", "seedance", "cache"} {
		for _, body := range []string{
			`{"error":{"code":"invalid_request","message":"invalid request"},"request":{"prompt":"insufficient balance"}}`,
			`{"error":{"code":"rate_limit_exceeded","message":"request quota exceeded"}}`,
		} {
			a := nativePaidDebtAccountGeili(kind)
			r := &nativePaidDebtRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(*a)}
			response := grokMediaContentStatusResponse(body)
			response.StatusCode = http.StatusBadRequest
			up := &nativePaidDebtUpstreamGeili{response: response}
			_, _ = runNativePaidDebtGeili(t, kind, context.Background(), a, up, &RateLimitService{accountRepo: r})
			require.Zero(t, r.markCalls, kind)
		}
	}
}

func TestNativePaidDebtGeiliExistingCacheMutationKeepsLegacyBehavior(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		a := nativePaidDebtAccountGeili("cache")
		r := &nativePaidDebtRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(*a)}
		response := grokMediaContentStatusResponse(`{"error":{"code":"insufficient_balance"}}`)
		response.StatusCode = http.StatusPaymentRequired
		up := &nativePaidDebtUpstreamGeili{response: response}
		cache := &GeminiCachedContentService{compat: &GeminiMessagesCompatService{httpUpstream: up, rateLimitService: &RateLimitService{accountRepo: r}, cfg: &config.Config{}}}
		_, err := cache.doUpstream(context.Background(), a, method, "https://example.test/v1beta/cachedContents/existing", nil, geminiCachedContentMutationUpstreamTimeout)
		var native *GeminiCachedContentUpstreamError
		require.ErrorAs(t, err, &native)
		require.True(t, strings.Contains(string(native.Body), "insufficient_balance"))
		require.Zero(t, r.markCalls, method)
	}
}

type nativePaidDebtUnexpectedEOFGeili struct{ body []byte }

func (r *nativePaidDebtUnexpectedEOFGeili) Read(p []byte) (int, error) {
	if len(r.body) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.body)
	r.body = r.body[n:]
	if len(r.body) == 0 {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

func TestNativePaidDebtGeiliIncompleteErrorKeepsNativeFailureWithoutDebt(t *testing.T) {
	for _, kind := range []string{"grok-videos", "seedance", "cache"} {
		t.Run(kind, func(t *testing.T) {
			a := nativePaidDebtAccountGeili(kind)
			a.Credentials["pool_mode"] = true
			r := &nativePaidDebtRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(*a)}
			response := grokMediaContentStatusResponse("")
			response.StatusCode = http.StatusServiceUnavailable
			response.Body = io.NopCloser(&nativePaidDebtUnexpectedEOFGeili{body: []byte("{\"error\":{\"code\":\"insufficient_balance\"}}")})
			up := &nativePaidDebtUpstreamGeili{response: response}
			err, _ := runNativePaidDebtGeili(t, kind, context.Background(), a, up, &RateLimitService{accountRepo: r})
			require.Error(t, err)
			require.Zero(t, r.markCalls)
			if kind != "grok-videos" {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF, "retain the native read failure")
			}
		})
	}
}

func TestNativePaidDebtGeiliVideoPollDoesNotInstallNewDebtPolicy(t *testing.T) {
	a := nativePaidDebtAccountGeili("grok-videos")
	a.Credentials["pool_mode"] = true
	r := &nativePaidDebtRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili(*a)}
	response := grokMediaContentStatusResponse("{\"error\":{\"code\":\"insufficient_balance\"}}")
	response.StatusCode = http.StatusPaymentRequired
	up := &nativePaidDebtUpstreamGeili{response: response}
	svc := &OpenAIGatewayService{httpUpstream: up, rateLimitService: &RateLimitService{accountRepo: r}, cfg: &config.Config{}}
	c, _ := grokMediaContentTestContext(http.MethodGet, "/v1/videos/generations/existing", nil)
	_, err := svc.ForwardGrokMedia(context.Background(), c, a, GrokMediaEndpointVideoStatus, "existing", nil, "")
	require.Error(t, err)
	require.Zero(t, r.markCalls)
	require.Equal(t, 1, up.requests)
}

func TestNativePaidDebtGeiliSuccessfulDispatchAddsNoRuntimeIO(t *testing.T) {
	for _, kind := range []string{"seedance", "cache", "grok-images", "grok-videos"} {
		t.Run(kind, func(t *testing.T) {
			account := nativePaidDebtAccountGeili(kind)
			account.Credentials["pool_mode"] = true
			repo := newTransportRuntimeIORepoGeili(*account)
			cache := &transportRuntimeIOCacheGeili{}
			response := grokMediaContentStatusResponse(`{"id":"task_fixture","request_id":"task_fixture","data":[{"url":"https://cdn.example.test/image.png"}]}`)
			upstream := &nativePaidDebtUpstreamGeili{response: response}
			ctx, dispatch := ctxkey.WithUpstreamDispatch(context.Background())
			err, _ := runNativePaidDebtGeili(t, kind, ctx, account, upstream, &RateLimitService{accountRepo: repo, tempUnschedCache: cache})
			require.NoError(t, err)
			require.True(t, dispatch.Started())
			require.Equal(t, 1, upstream.requests)
			require.Zero(t, repo.runtimeCalls.Load())
			require.Zero(t, cache.calls.Load())
		})
	}
}
