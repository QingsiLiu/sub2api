//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type nativeOpenAIHTTPTestGeili struct {
	HTTPUpstream
	admission     error
	verify        func(*http.Request, int64) error
	checks, calls atomic.Int64
	response      *http.Response
}

func (u *nativeOpenAIHTTPTestGeili) CheckNativeBalanceBeforeDispatchGeili(req *http.Request, id int64) error {
	u.checks.Add(1)
	if u.verify != nil {
		return u.verify(req, id)
	}
	return u.admission
}

func (u *nativeOpenAIHTTPTestGeili) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	if err := u.CheckNativeBalanceBeforeDispatchGeili(req, id); err != nil {
		return nil, err
	}
	u.calls.Add(1)
	ctxkey.MarkUpstreamDispatched(req.Context())
	if u.response != nil {
		return u.response, nil
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

func TestOpenAINativeAdmissionGeiliPreservesWrappedErrorWithoutDispatch(t *testing.T) {
	for _, endpoint := range []string{"embeddings", "apikey-images", "codex-images"} {
		for _, reason := range []string{"balance", "verification_unavailable"} {
			t.Run(endpoint+"/"+reason, func(t *testing.T) {
				admission := fmt.Errorf("native transport wrapper: %w", NewAccountRuntimeAdmissionErrorGeili(reason))
				upstream := &nativeOpenAIHTTPTestGeili{admission: admission}
				body := []byte(`{"model":"gpt-image-2","prompt":"draw","input":"hello"}`)
				c, rec := newOpenAIImagesTestContext(t, body)
				ctx, dispatch := ctxkey.WithUpstreamDispatch(context.Background())
				svc := newOpenAIImagesTestService(upstream)
				account := newOpenAIImagesAPIKeyAccount()
				var result *OpenAIForwardResult
				var err error
				if endpoint == "embeddings" {
					result, err = svc.ForwardEmbeddings(ctx, c, account, body, "")
				} else {
					if endpoint == "codex-images" {
						account = directImagesTestAccount()
					}
					parsed, parseErr := svc.ParseOpenAIImagesRequest(c, body)
					require.NoError(t, parseErr)
					result, err = svc.ForwardImages(ctx, c, account, body, parsed, "")
				}
				require.Same(t, admission, err, "must preserve wrapped scope and retry action")
				require.True(t, isAccountRuntimeAdmissionFailureGeili(err))
				require.Nil(t, result)
				require.False(t, dispatch.Started(), "no upstream dispatch, so no dispatched-request billing")
				require.Equal(t, int64(0), upstream.calls.Load())
				require.False(t, c.Writer.Written())
				require.Empty(t, rec.Body.String(), "no invented 502 downstream error")
				repo := newRecoveryRuntimeRepoGeili(*account)
				cache := newAccountDebtLatchTestGeili()
				(&RateLimitService{accountRepo: repo, tempUnschedCache: cache}).observeAccountResultGeili(ctx, c, account, "native-model", err)
				stored, readErr := repo.GetByID(context.Background(), account.ID)
				require.NoError(t, readErr)
				require.False(t, stored.RuntimeState.BalanceBlocked, "local admission creates no supplier observation")
				require.Empty(t, stored.RuntimeState.Models)
				require.Zero(t, cache.sets)
			})
		}
	}
}

type nativePluginClientTestGeili struct {
	pluginv1.TransportPluginClient
	calls  atomic.Int64
	status int32
	body   string
}

func (p *nativePluginClientTestGeili) Forward(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pluginv1.ForwardRequest, pluginv1.ForwardResponse], error) {
	p.calls.Add(1)
	return &nativePluginStreamTestGeili{ctx: ctx, frames: []*pluginv1.ForwardResponse{
		{Frame: &pluginv1.ForwardResponse_Start{Start: &pluginv1.ForwardResponseStart{StatusCode: p.status, Headers: headersToPlugin(http.Header{"Content-Type": []string{"application/json"}})}}},
		{Frame: &pluginv1.ForwardResponse_BodyChunk{BodyChunk: []byte(p.body)}},
		{Frame: &pluginv1.ForwardResponse_End{End: &pluginv1.ForwardResponseEnd{}}},
	}}, nil
}

type nativePluginStreamTestGeili struct {
	grpc.ClientStream
	ctx    context.Context
	frames []*pluginv1.ForwardResponse
}

func (s *nativePluginStreamTestGeili) Context() context.Context            { return s.ctx }
func (s *nativePluginStreamTestGeili) Send(*pluginv1.ForwardRequest) error { return nil }
func (s *nativePluginStreamTestGeili) CloseSend() error                    { return nil }
func (s *nativePluginStreamTestGeili) Recv() (*pluginv1.ForwardResponse, error) {
	if len(s.frames) == 0 {
		return nil, io.EOF
	}
	frame := s.frames[0]
	s.frames = s.frames[1:]
	return frame, nil
}

func newNativePluginManagerTestGeili(api *nativePluginClientTestGeili) *PluginManager {
	m := &PluginManager{}
	m.route.Store(&pluginRoute{rolloutPercent: 100, runtime: &pluginRuntime{
		api: api, client: hcplugin.NewClient(&hcplugin.ClientConfig{}), installation: &PluginInstallation{},
	}})
	return m
}

func TestOpenAINativePluginBalanceGeiliHandledBranchGuardsBeforeRPC(t *testing.T) {
	for _, adapter := range []string{"gateway", "admin-test"} {
		for _, mode := range []string{"debt", "verification-unavailable", "missing-verifier", "allowed", "text", "content-get", "probe-native"} {
			t.Run(adapter+"/"+mode, func(t *testing.T) {
				upstream := &nativeOpenAIHTTPTestGeili{}
				api := &nativePluginClientTestGeili{status: 200, body: `{}`}
				manager := newNativePluginManagerTestGeili(api)
				ctx, dispatch := ctxkey.WithUpstreamDispatch(context.Background())
				path, method := "/backend-api/codex/images/generations", http.MethodPost
				blocked := mode == "debt" || mode == "verification-unavailable" || mode == "missing-verifier" || mode == "probe-native"
				if mode == "text" {
					path = "/backend-api/codex/responses"
				}
				if mode == "content-get" {
					path, method = "/v1/images/content/task-1", http.MethodGet
				}
				if mode == "probe-native" {
					ctx = context.WithValue(ctx, accountRecoveryProbeContextGeili{}, true)
				}
				if mode == "verification-unavailable" {
					upstream.admission = NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
				}
				if mode == "debt" || mode == "probe-native" {
					upstream.admission = NewAccountRuntimeAdmissionErrorGeili("balance")
				}
				if mode == "text" || mode == "content-get" {
					upstream.admission = NewAccountRuntimeAdmissionErrorGeili("balance")
				}
				var provider HTTPUpstream = upstream
				if mode == "missing-verifier" {
					provider = &pluginRoutingHTTPUpstream{}
				}
				request, err := http.NewRequestWithContext(ctx, method, "https://chatgpt.com"+path, nil)
				require.NoError(t, err)
				account := directImagesTestAccount()
				account.Status, account.Schedulable = "inactive", false // native admin tests must ignore unrelated stops.
				var response *http.Response
				if adapter == "gateway" {
					response, err = (&OpenAIGatewayService{pluginManager: manager, httpUpstream: provider}).doOpenAIUpstream(request, "", account)
				} else {
					response, err = (&AccountTestService{pluginManager: manager, httpUpstream: provider}).doOpenAIAccountTestUpstream(request, "", account, false)
				}
				if blocked {
					require.True(t, isAccountRuntimeAdmissionFailureGeili(err))
					require.Nil(t, response)
					require.Equal(t, int64(0), api.calls.Load(), "no plugin RPC/egress")
					require.False(t, dispatch.Started())
				} else {
					require.NoError(t, err)
					_, err = io.ReadAll(response.Body)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					require.Equal(t, int64(1), api.calls.Load())
					require.True(t, dispatch.Started())
				}
				if mode == "text" || mode == "content-get" || mode == "missing-verifier" {
					require.Equal(t, int64(0), upstream.checks.Load())
				} else {
					require.Equal(t, int64(1), upstream.checks.Load())
				}
				require.Equal(t, int64(0), upstream.calls.Load(), "handled plugins never fall back to HTTP")
				require.Equal(t, int64(0), manager.route.Load().runtime.inFlight.Load())
			})
		}
	}
}

func TestOpenAINativePluginBalanceGeiliUnhandledKeepsLegacyFallback(t *testing.T) {
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		manager := &PluginManager{}
		if accountType == AccountTypeAPIKey {
			manager.route.Store(&pluginRoute{rolloutPercent: 100, unavailable: "not relevant to API keys"})
		}
		upstream := &pluginRoutingHTTPUpstream{} // no native plugin verifier
		request, err := http.NewRequest(http.MethodPost, "https://example.test/v1/images/generations", nil)
		require.NoError(t, err)
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: accountType}
		response, err := (&OpenAIGatewayService{pluginManager: manager, httpUpstream: upstream}).doOpenAIUpstream(request, "", account)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, 1, upstream.doCalls)
	}
}

func TestOpenAINativePluginBalanceGeiliAllNativeRoutesRejectForgedBypass(t *testing.T) {
	for _, path := range []string{"/v1/images/generations", "/backend-api/codex/images/edits", "/v1/embeddings"} {
		t.Run(path, func(t *testing.T) {
			api := &nativePluginClientTestGeili{status: 200, body: `{}`}
			upstream := &nativeOpenAIHTTPTestGeili{admission: NewAccountRuntimeAdmissionErrorGeili("balance")}
			ctx, dispatch := ctxkey.WithUpstreamDispatch(context.Background())
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test"+path+"?account_runtime_admitted=true", nil)
			require.NoError(t, err)
			request.Header.Set("X-Geili-Native-Balance-Verified", "true")
			svc := &OpenAIGatewayService{pluginManager: newNativePluginManagerTestGeili(api), httpUpstream: upstream}
			response, err := svc.doOpenAIUpstream(request, "", directImagesTestAccount())
			require.True(t, isAccountRuntimeAdmissionFailureGeili(err))
			require.Nil(t, response)
			require.Equal(t, int64(1), upstream.checks.Load())
			require.Equal(t, int64(0), api.calls.Load())
			require.False(t, dispatch.Started())
		})
	}
}

type nativeImagesBalanceRepoTestGeili struct{ *recoveryRuntimeRepoGeili }

func (*nativeImagesBalanceRepoTestGeili) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	return nil
}

func TestOpenAINativeImagesSupplierBalanceGeiliPersistsBeforeEarlyReturn(t *testing.T) {
	for _, source := range []string{"apikey", "plugin"} {
		for _, signal := range []string{"plain", "wrapped", "quota", "content-policy", "request-echo"} {
			t.Run(source+"/"+signal, func(t *testing.T) {
				account := newOpenAIImagesAPIKeyAccount()
				if source == "plugin" {
					account = directImagesTestAccount()
				}
				account.Status, account.Schedulable = StatusActive, true
				bodies := map[string]string{
					"plain":          `{"error":{"code":"insufficient_balance","message":"declined"}}`,
					"wrapped":        `{"error":{"message":"failed","cause":{"errorKey":"insufficient_balance"}}}`,
					"quota":          `{"error":{"code":"rate_limit_exceeded","message":"request quota exceeded"}}`,
					"content-policy": `{"error":{"code":"content_policy_violation","message":"request rejected"}}`,
					"request-echo":   `{"error":{"message":"invalid request"},"request":{"errorKey":"insufficient_balance"}}`,
				}
				repo := &nativeImagesBalanceRepoTestGeili{newRecoveryRuntimeRepoGeili(*account)}
				cache := newAccountDebtLatchTestGeili()
				upstream := &nativeOpenAIHTTPTestGeili{response: &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(bodies[signal]))}}
				upstream.verify = func(req *http.Request, id int64) error {
					verdict, err := repo.CheckAccountRuntimeAdmissionGeili(req.Context(), id, "")
					if err != nil {
						return err
					}
					if verdict.BalanceBlocked || cache.tokens[id] != "" {
						return NewAccountRuntimeAdmissionErrorGeili("balance")
					}
					return nil
				}
				svc := newOpenAIImagesTestService(upstream)
				svc.accountRepo = repo
				svc.rateLimitService = &RateLimitService{accountRepo: repo, tempUnschedCache: cache}
				api := &nativePluginClientTestGeili{status: 400, body: bodies[signal]}
				if source == "plugin" {
					svc.pluginManager = newNativePluginManagerTestGeili(api)
				}
				body := []byte(`{"model":"gpt-image-2","prompt":"draw"}`)
				c, _ := newOpenAIImagesTestContext(t, body)
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				result, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
				require.Error(t, err)
				require.Nil(t, result)
				stored, err := repo.GetByID(context.Background(), account.ID)
				require.NoError(t, err)
				debt := signal == "plain" || signal == "wrapped"
				require.Equal(t, debt, stored.RuntimeState.BalanceBlocked)
				require.Equal(t, debt, cache.tokens[account.ID] != "")
				if debt {
					require.Equal(t, "supplier_insufficient_balance", stored.RuntimeState.BalanceReason)
					// A stale account snapshot cannot dispatch the next native request.
					stale := newOpenAIImagesAPIKeyAccount()
					if source == "plugin" {
						stale = directImagesTestAccount()
					}
					ctx, dispatch := ctxkey.WithUpstreamDispatch(context.Background())
					c2, _ := newOpenAIImagesTestContext(t, body)
					result, err = svc.ForwardImages(ctx, c2, stale, body, parsed, "")
					require.True(t, isAccountRuntimeAdmissionFailureGeili(err))
					require.Nil(t, result)
					require.False(t, dispatch.Started())
					if source == "plugin" {
						require.Equal(t, int64(1), api.calls.Load())
					} else {
						require.Equal(t, int64(1), upstream.calls.Load())
					}
				}
			})
		}
	}
}

func TestOpenAINativeImagesSupplierBalanceGeiliPreservesScopedExclusions(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		cline      bool
	}{
		{"model-not-found", `{"error":{"code":"model_not_found","cause":{"errorKey":"insufficient_balance"}}}`, false},
		{"top-model-not-found", `{"code":"model_not_found","error":{"cause":{"errorKey":"insufficient_balance"}}}`, false},
		{"invalid-parameter", `{"error":{"code":"invalid_parameter","cause":{"errorKey":"insufficient_balance"}}}`, false},
		{"cline-wallet", `{"error":{"wallet":{"scope":"credits"},"cause":{"errorKey":"insufficient_balance"}}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := newOpenAIImagesAPIKeyAccount()
			if tc.cline {
				account = clineTestAccount(account.ID)
			}
			repo := newRecoveryRuntimeRepoGeili(*account)
			cache := newAccountDebtLatchTestGeili()
			svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo, tempUnschedCache: cache}}
			require.True(t, isOpenAIImagesInsufficientBalance([]byte(tc.body)), "strict image wrapper detection still applies")
			require.False(t, svc.observeOpenAIImagesSupplierBalanceGeili(context.Background(), account, 400, []byte(tc.body)))
			require.Zero(t, cache.sets)
			stored, err := repo.GetByID(context.Background(), account.ID)
			require.NoError(t, err)
			require.False(t, stored.RuntimeState.BalanceBlocked)
		})
	}
}
