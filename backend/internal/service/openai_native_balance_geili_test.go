//go:build unit

package service

import (
	"context"
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

// Handled plugins bypass HTTPUpstream. They must not need a runtime-verifier
// interface or perform an extra debt check before their existing RPC.
func TestOpenAINativePluginBalanceGeiliHandledBranchHasNoRuntimeVerifier(t *testing.T) {
	for _, adapter := range []string{"gateway", "admin-test"} {
		for _, path := range []string{"/backend-api/codex/images/generations", "/backend-api/codex/images/edits", "/v1/embeddings", "/v1/responses"} {
			t.Run(adapter+path, func(t *testing.T) {
				api := &nativePluginClientTestGeili{status: 200, body: `{}`}
				upstream := &pluginRoutingHTTPUpstream{}
				manager := newNativePluginManagerTestGeili(api)
				ctx, dispatch := ctxkey.WithUpstreamDispatch(context.Background())
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com"+path, nil)
				require.NoError(t, err)
				var response *http.Response
				if adapter == "gateway" {
					response, err = (&OpenAIGatewayService{pluginManager: manager, httpUpstream: upstream}).doOpenAIUpstream(request, "", directImagesTestAccount())
				} else {
					response, err = (&AccountTestService{pluginManager: manager, httpUpstream: upstream}).doOpenAIAccountTestUpstream(request, "", directImagesTestAccount(), false)
				}
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, int64(1), api.calls.Load())
				require.Zero(t, upstream.doCalls)
				require.True(t, dispatch.Started())
			})
		}
	}
}

type nativeImagesBalanceRepoTestGeili struct{ *transportRuntimeIORepoGeili }

func (*nativeImagesBalanceRepoTestGeili) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	return nil
}

func TestOpenAINativeImagesSupplierBalanceGeiliDoesNotPersistDebt(t *testing.T) {
	for _, source := range []string{"apikey", "plugin"} {
		for _, body := range []string{`{"error":{"code":"insufficient_balance","message":"declined"}}`, `{"error":{"message":"failed","cause":{"errorKey":"insufficient_balance"}}}`} {
			t.Run(source+body, func(t *testing.T) {
				account := newOpenAIImagesAPIKeyAccount()
				if source == "plugin" {
					account = directImagesTestAccount()
				}
				account.Status, account.Schedulable = StatusActive, true
				repo := &nativeImagesBalanceRepoTestGeili{newTransportRuntimeIORepoGeili(*account)}
				cache := &transportRuntimeIOCacheGeili{}
				upstream := &nativePaidDebtUpstreamGeili{response: &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}}
				svc := newOpenAIImagesTestService(upstream)
				svc.accountRepo = repo
				svc.rateLimitService = &RateLimitService{accountRepo: repo, tempUnschedCache: cache}
				if source == "plugin" {
					svc.pluginManager = newNativePluginManagerTestGeili(&nativePluginClientTestGeili{status: 400, body: body})
				}
				requestBody := []byte(`{"model":"gpt-image-2","prompt":"draw"}`)
				c, _ := newOpenAIImagesTestContext(t, requestBody)
				parsed, err := svc.ParseOpenAIImagesRequest(c, requestBody)
				require.NoError(t, err)
				result, err := svc.ForwardImages(context.Background(), c, account, requestBody, parsed, "")
				require.Error(t, err)
				require.Nil(t, result)
				require.Zero(t, repo.runtimeCalls.Load())
				require.Zero(t, cache.calls.Load())
				stored, err := repo.GetByID(context.Background(), account.ID)
				require.NoError(t, err)
				require.False(t, stored.RuntimeState.BalanceBlocked)
			})
		}
	}
}
