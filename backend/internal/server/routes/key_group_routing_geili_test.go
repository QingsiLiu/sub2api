package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKeyRouteWriterRetriesOnlyUncommittedUpstreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		flush  bool
		retry  bool
	}{
		{"unavailable", 503, `{"error":{"type":"api_error"}}`, false, true},
		{"upstream limit", 429, `{"error":{"type":"upstream_error"}}`, false, true},
		{"subscription limit", 429, `{"error":{"code":"SUBSCRIPTION_QUOTA_EXCEEDED"}}`, false, false},
		{"local request failure", 503, `{"error":{"type":"invalid_request_error"}}`, false, false},
		{"invalid parameters", 400, `{"error":{"type":"invalid_request_error"}}`, false, false},
		{"stream already started", 200, `data: started`, true, false},
		{"successful content", 200, `data: content`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			w := newKeyRouteWriter(c.Writer)
			w.Header().Set("X-Attempt", "first")
			w.WriteHeader(tc.status)
			_, err := w.WriteString(tc.body)
			require.NoError(t, err)
			if tc.flush {
				w.Flush()
			}
			require.Equal(t, tc.retry, w.canRetry())
			if tc.retry {
				require.Empty(t, recorder.Body.String())
				require.Empty(t, recorder.Header().Get("X-Attempt"))
			}
			w.commit()
			require.Equal(t, tc.body, recorder.Body.String())
			require.Equal(t, tc.status, recorder.Code)
		})
	}
}

func TestExplicitKeyRoutingRejectsMissingOrConflictingModelsBeforeScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{}`, `{"model":"allowed","MODEL":"forbidden"}`, `{"model":"allowed","model":"forbidden"}`} {
		t.Run(body, func(t *testing.T) {
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{BillingSource: "subscription", RoutingMode: "composite", GroupIDs: []int64{1, 2}})
			})
			engine.Use(explicitKeyRouting(nil, nil, &handler.Handlers{}, nil))
			called := false
			engine.POST("/v1/chat/completions", func(c *gin.Context) { called = true; c.Status(200) })
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			out := httptest.NewRecorder()
			engine.ServeHTTP(out, r)
			require.Equal(t, 400, out.Code)
			require.False(t, called)
		})
	}
}

func TestExplicitKeyRoutingCapturesModelBeforeResolverFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		router := gin.New()
		var capturedModel string
		var capturedStream bool
		var capturedType int16
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{BillingSource: "balance", RoutingMode: "composite", GroupIDs: []int64{22, 11}})
			c.Next()
			capturedModel = c.GetString("ops_model")
			capturedStream = c.GetBool("ops_stream")
			v, _ := c.Get("ops_request_type")
			capturedType, _ = v.(int16)
		})
		router.Use(explicitKeyRouting(service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil), nil, &handler.Handlers{}, nil))
		router.POST("/v1/responses", func(c *gin.Context) { t.Fatal("must reject before handler") })
		body := `{"model":"gpt-unavailable","stream":false}`
		if stream {
			body = `{"model":"gpt-unavailable","stream":true}`
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		require.Equal(t, 503, out.Code)
		require.Equal(t, "gpt-unavailable", capturedModel)
		require.Equal(t, stream, capturedStream)
		if stream {
			require.Equal(t, int16(service.RequestTypeStream), capturedType)
		} else {
			require.Equal(t, int16(service.RequestTypeSync), capturedType)
		}
	}
}

type opsRouteUserRepo struct{ service.UserRepository }

func (opsRouteUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 1}, nil
}

type opsRouteGroupRepo struct{ service.GroupRepository }

func (opsRouteGroupRepo) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return &service.Group{ID: 22, Status: service.StatusActive, Platform: service.PlatformGemini}, nil
}

func TestCompositeModelEndpointRejectionCapturesIntentBeforeHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groups := opsRouteGroupRepo{}
	keys := service.NewAPIKeyService(nil, opsRouteUserRepo{}, groups, nil, nil, nil, nil)
	resolver := service.NewCompositeRouteResolver(nil)
	resolver.SetRouteValidation(groups, service.NewModelPricingResolver(nil, nil))
	router := gin.New()
	var model string
	var isLocalModelError bool
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{UserID: 1, BillingSource: "balance", RoutingMode: "composite", GroupIDs: []int64{22}})
		c.Next()
		model = c.GetString("ops_model")
		isLocalModelError = service.OpsClientBusinessLimitedReason(c) == service.OpsClientBusinessLimitedReasonLocalModelConfiguration
	})
	router.Use(explicitKeyRouting(keys, resolver, &handler.Handlers{}, nil))
	router.POST("/v1/embeddings", func(c *gin.Context) { t.Fatal("must reject before dispatch") })
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"text-embedding-fixture","input":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	require.Equal(t, 400, out.Code)
	require.Contains(t, out.Body.String(), "MODEL_NOT_AVAILABLE")
	require.Equal(t, "text-embedding-fixture", model)
	require.True(t, isLocalModelError)
}

func TestCompositeUnpricedModelIsLocalButStillLogged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	service.SetOpsIngressRequestContext(c, "unpriced-fixture", true)
	writeCompositeRouteError(c, service.ErrCompositeModelUnpriced)
	require.Equal(t, http.StatusBadRequest, out.Code)
	require.Contains(t, out.Body.String(), "MODEL_PRICE_NOT_CONFIGURED")
	require.Equal(t, service.OpsClientBusinessLimitedReasonLocalModelConfiguration, service.OpsClientBusinessLimitedReason(c))
	_, rejected := middleware.GetIngressRejectReason(c)
	require.False(t, rejected, "pricing configuration errors must remain in detailed Ops logs")
}

func TestGeminiRoutingRejectionRetainsNativeStreamingType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, action := range []string{"generateContent", "streamGenerateContent"} {
		t.Run(action, func(t *testing.T) {
			router := gin.New()
			var stream bool
			var requestType any
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{BillingSource: "balance", RoutingMode: "composite", GroupIDs: []int64{22}})
				c.Next()
				stream = c.GetBool("ops_stream")
				requestType, _ = c.Get("ops_request_type")
			})
			router.Use(explicitKeyRouting(service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil), nil, &handler.Handlers{}, nil))
			router.POST("/v1beta/models/*modelAction", func(c *gin.Context) { t.Fatal("must reject before dispatch") })
			request := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-fixture:"+action, strings.NewReader(`{"contents":[]}`))
			request.Header.Set("Content-Type", "application/json")
			out := httptest.NewRecorder()
			router.ServeHTTP(out, request)
			require.Equal(t, 503, out.Code)
			require.Equal(t, action == "streamGenerateContent", stream)
			require.Equal(t, int16(service.RequestTypeFromLegacy(stream, false)), requestType)
		})
	}
}
