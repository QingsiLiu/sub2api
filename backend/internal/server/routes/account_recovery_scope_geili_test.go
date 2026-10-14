package routes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type noHTTPProbeRouteRepoGeili struct {
	service.AccountRepository
	service.AccountRuntimeRepositoryGeili
	queries atomic.Int64
}

func (r *noHTTPProbeRouteRepoGeili) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, string, int64, string) ([]service.Account, *pagination.PaginationResult, error) {
	r.queries.Add(1)
	return nil, &pagination.PaginationResult{}, nil
}
func (r *noHTTPProbeRouteRepoGeili) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return nil, nil
}

type httpProbeScopeGroupsGeili struct{ service.GroupRepository }

func (httpProbeScopeGroupsGeili) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return &service.Group{ID: 22, Status: service.StatusActive, Platform: service.PlatformOpenAI, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5.1"}}}, nil
}

type countedHTTPProbeScopeBodyGeili struct {
	io.ReadCloser
	bytes int
}

func (r *countedHTTPProbeScopeBodyGeili) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.bytes += n
	return n, err
}

func TestHTTPAccountRecoveryScopeGeiliMediaBodiesDeniedForLegacyAndWhitelistedKeys(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, tc := range []struct {
			name, body string
			allowed    bool
		}{
			{"function tools", `{"model":"gpt-5.1","tools":[{"type":"function","name":"lookup"}]}`, true},
			{"tool history", `{"model":"gpt-5.1","input":[{"type":"function_call_output","call_id":"c1","output":"ok"}]}`, true},
			{"image tool", `{"model":"gpt-5.1","tools":[{"type":"image_generation"}]}`, false},
			{"video tool", `{"model":"gpt-5.1","tools":[{"type":"video_generation"}]}`, false},
			{"audio output", `{"model":"gpt-5.1","modalities":["audio"]}`, false},
		} {
			t.Run(tc.name+"/"+map[bool]string{false: "legacy", true: "whitelisted"}[explicit], func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				repo := &noHTTPProbeRouteRepoGeili{}
				var probes atomic.Int64
				recovery := service.NewAccountRecoveryServiceGeili(repo, func(context.Context, *service.Account, string) (bool, error) { probes.Add(1); return true, nil }, nil)
				defer recovery.Stop()
				groups := httpProbeScopeGroupsGeili{}
				keys := service.NewAPIKeyService(nil, opsRouteUserRepo{}, groups, nil, nil, nil, nil)
				resolver := service.NewCompositeRouteResolver(nil)
				cfg := &config.Config{}
				pricing := service.NewModelPricingResolver(nil, service.NewBillingService(cfg, nil))
				service.NewGatewayService(repo, groups, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, pricing, resolver, nil, nil)
				original := &countedHTTPProbeScopeBodyGeili{ReadCloser: io.NopCloser(strings.NewReader(tc.body))}
				assertScope := func(c *gin.Context) {
					require.Equal(t, tc.allowed, service.AccountRecoveryScopeAllowedGeili(c.Request.Context()))
					reread, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
					require.NoError(t, err)
					require.Equal(t, tc.body, string(reread))
					require.Equal(t, len(tc.body), original.bytes, "scope and explicit dispatch reuse one original body read")
					if !tc.allowed {
						ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Millisecond)
						defer cancel()
						require.False(t, recovery.RecoverGroupGeili(ctx, 22, "gpt-5.1", nil))
						require.Zero(t, repo.queries.Load())
						require.Zero(t, probes.Load())
					}
				}
				router := gin.New()
				if explicit {
					gid := int64(22)
					router.Use(func(c *gin.Context) {
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
						c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{UserID: 1, BillingSource: service.BillingSourceBalance, RoutingMode: service.KeyRoutingSingle, GroupID: &gid})
						c.Next()
						assertScope(c)
					})
				}
				router.Use(explicitKeyRouting(keys, resolver, &handler.Handlers{OpenAIGateway: &handler.OpenAIGatewayHandler{}}, cfg))
				router.POST("/v1/responses", func(c *gin.Context) { assertScope(c); c.Status(http.StatusNoContent) })
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				request.Body = original
				request.ContentLength = int64(len(tc.body))
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				if explicit {
					require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
					require.Contains(t, recorder.Body.String(), "Service temporarily unavailable")
				} else {
					require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}

type fixedHTTPProbeScopeBodyErrorGeili struct{ err error }

func (r *fixedHTTPProbeScopeBodyErrorGeili) Read([]byte) (int, error) { return 0, r.err }
func (r *fixedHTTPProbeScopeBodyErrorGeili) Close() error             { return nil }
func TestHTTPAccountRecoveryScopeGeiliPrereadFailureKeepsHandlerError(t *testing.T) {
	expected := errors.New("fixture read error")
	router := gin.New()
	router.Use(explicitKeyRouting(nil, nil, nil, nil))
	router.POST("/v1/messages", func(c *gin.Context) {
		require.False(t, service.AccountRecoveryScopeAllowedGeili(c.Request.Context()))
		_, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		require.ErrorIs(t, err, expected)
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Body = &fixedHTTPProbeScopeBodyErrorGeili{err: expected}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestHTTPAccountRecoveryScopeGeiliNonTextHasNoPrereadOrProbe(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent"},
		{http.MethodPost, "/v1beta/models/gemini-2.5-flash:streamGenerateContent"},
		{http.MethodPost, "/v1beta/cachedContents"},
		{http.MethodPost, "/v1/responses/compact"},
		{http.MethodPost, "/v1/embeddings"},
		{http.MethodGet, "/v1beta/models"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			repo := &noHTTPProbeRouteRepoGeili{}
			var probes atomic.Int64
			recovery := service.NewAccountRecoveryServiceGeili(repo, func(context.Context, *service.Account, string) (bool, error) { probes.Add(1); return true, nil }, nil)
			defer recovery.Stop()
			payload := `{"model":"gemini-2.5-flash","account_recovery_scope":true}`
			original := &countedHTTPProbeScopeBodyGeili{ReadCloser: io.NopCloser(strings.NewReader(payload))}
			router := gin.New()
			router.Use(explicitKeyRouting(nil, nil, nil, nil))
			router.Any("/*path", func(c *gin.Context) {
				require.Zero(t, original.bytes, "native/GET/compact endpoints keep their original body read path")
				require.False(t, service.AccountRecoveryScopeAllowedGeili(c.Request.Context()))
				ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Millisecond)
				defer cancel()
				require.False(t, recovery.RecoverGroupGeili(ctx, 22, "gemini-2.5-flash", nil))
				require.Zero(t, repo.queries.Load())
				require.Zero(t, probes.Load())
				body, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, payload, string(body))
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Body = original
			req.ContentLength = int64(len(payload))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}
