package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
		{"mapped upstream limit", 429, `{"error":{"type":"rate_limit_error","message":"Upstream rate limit exceeded, please retry later"}}`, false, true},
		{"anthropic mapped upstream limit", 429, `{"type":"error","error":{"type":"rate_limit_error","message":"Upstream rate limit exceeded, please retry later"}}`, false, true},
		{"all accounts rate limited", 429, `{"error":{"type":"rate_limit_error","message":"All available accounts are currently rate-limited. Please retry later."}}`, false, true},
		{"unknown rate limit", 429, `{"error":{"type":"rate_limit_error","message":"slow down"}}`, false, false},
		{"queue full", 429, `{"error":{"type":"rate_limit_error","code":"gateway_queue_full","message":"Too many pending requests, please retry later"}}`, false, false},
		{"concurrency limit", 429, `{"error":{"type":"rate_limit_error","code":"gateway_concurrency_limit","message":"Upstream rate limit exceeded, please retry later"}}`, false, false},
		{"user rpm", 429, `{"error":{"code":"USER_RPM_EXCEEDED","message":"user requests-per-minute limit exceeded"}}`, false, false},
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

func TestKeyRouteWriterDistinguishesUpstreamConcurrencyFromLocal(t *testing.T) {
	for _, upstream := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		writer := newKeyRouteWriter(c.Writer)
		writer.upstreamFailure = upstream
		writer.WriteHeader(http.StatusTooManyRequests)
		_, err := writer.WriteString(`{"error":{"type":"rate_limit_error","code":"gateway_concurrency_limit","message":"concurrency limit exceeded"}}`)
		require.NoError(t, err)
		require.Equal(t, upstream, writer.canRetry())
	}
}

func TestKeyRouteWriterSupplierHeartbeatAllowsNextGroupWithoutHeaderLeak(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	first := newKeyRouteWriter(c.Writer)
	first.Header().Set("Content-Type", "text/event-stream")
	first.Header().Set("X-Upstream-Request-ID", "failed-attempt")
	_, err := first.WriteString(": keepalive\n\n")
	require.NoError(t, err)
	first.Flush()
	require.False(t, first.Written())
	require.Equal(t, -1, first.Size())
	require.Empty(t, recorder.Header().Get("X-Upstream-Request-ID"))
	first.WriteHeader(http.StatusBadGateway)
	_, err = first.WriteString(`{"error":{"type":"upstream_error","message":"temporarily unavailable"}}`)
	require.NoError(t, err)
	require.True(t, first.canRetry())

	second := newKeyRouteWriter(c.Writer)
	second.Header().Set("Content-Type", "text/event-stream")
	second.Header().Set("X-Upstream-Request-ID", "successful-attempt")
	_, err = second.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"complete answer\"}}]}\n\n")
	require.NoError(t, err)
	second.commit()
	require.True(t, second.Written())
	require.False(t, second.canRetry())
	require.Contains(t, recorder.Body.String(), "complete answer")
	require.NotContains(t, recorder.Body.String(), "temporarily unavailable")
}

func TestKeyRouteWriterFinalFailureAfterPreviousGroupHeartbeatIsProtocolError(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			first := newKeyRouteWriter(c.Writer)
			first.Header().Set("Content-Type", "text/event-stream")
			_, err := first.WriteString(": keepalive\n\n")
			require.NoError(t, err)
			first.Flush()
			// The next group emits its final rejection with no heartbeat of its own.
			last := newKeyRouteWriter(c.Writer)
			last.ctx = c
			last.Header().Set("Content-Type", "application/json")
			last.WriteHeader(http.StatusTooManyRequests)
			_, err = last.WriteString(`{"error":{"type":"rate_limit_error","code":"upstream_rate_limited","message":"All suppliers are rate limited"}}`)
			require.NoError(t, err)
			service.MarkResponseCommitted(c) // JSON is buffered, not on the wire.
			last.commit()
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
			require.Contains(t, recorder.Body.String(), "All suppliers are rate limited")
			require.Contains(t, recorder.Body.String(), "upstream_rate_limited")
			if path == "/v1/responses" {
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed"))
			} else {
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
			}
		})
	}
}

func TestExplicitKeyRoutingInitializesRecoveryForLegacyTextOnly(t *testing.T) {
	for _, tc := range []struct {
		path   string
		budget time.Duration
	}{
		{"/v1/messages", 7 * time.Second},
		{"/v1/responses", 7 * time.Second},
		{"/backend-api/codex/responses", 7 * time.Second},
		{"/v1/chat/completions", 7 * time.Second},
		{"/v1/images/generations", 600 * time.Second},
		{"/v1/videos", 600 * time.Second},
		{"/v1/messages/count_tokens", 600 * time.Second},
	} {
		t.Run(tc.path, func(t *testing.T) {
			router := gin.New()
			cfg := &config.Config{Gateway: config.GatewayConfig{RequestRecoveryTimeoutSeconds: 7}}
			router.Use(explicitKeyRouting(nil, nil, nil, cfg))
			router.POST(tc.path, func(c *gin.Context) {
				require.Equal(t, tc.budget, service.RequestRecoveryRemaining(c.Request.Context()))
				c.Status(200)
			})
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{}`)))
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	request.Header.Set("Upgrade", "websocket")
	require.False(t, isRecoveryTextRequest(request))
}

func TestExplicitKeyRoutingLongThinkingDefaultAndExplicitRootBudget(t *testing.T) {
	for _, tc := range []struct {
		name         string
		model        string
		gateway      config.GatewayConfig
		parentBudget time.Duration
		want         time.Duration
	}{
		{name: "ordinary-default", model: "gpt-6-sol", want: 600 * time.Second},
		{name: "astra-default", model: "gpt-6-astra", want: 1800 * time.Second},
		{name: "opus-default", model: "claude-opus-5-5", want: 1800 * time.Second},
		{name: "explicit600", model: "gpt-6-astra", gateway: config.GatewayConfig{KeyGroupRequestTimeoutSeconds: 600, KeyGroupRequestTimeoutExplicit: true}, want: 600 * time.Second},
		{name: "explicit90", model: "gpt-6-astra", gateway: config.GatewayConfig{KeyGroupRequestTimeoutSeconds: 90}, want: 90 * time.Second},
		{name: "parent-shorter", model: "gpt-6-astra", parentBudget: 20 * time.Second, want: 20 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			var observed time.Duration
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{BillingSource: "balance", RoutingMode: "composite", GroupIDs: []int64{22, 11}})
				c.Next()
				deadline, ok := c.Request.Context().Deadline()
				require.True(t, ok)
				observed = time.Until(deadline)
			})
			router.Use(explicitKeyRouting(service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil), nil, &handler.Handlers{}, &config.Config{Gateway: tc.gateway}))
			router.POST("/v1/responses", func(c *gin.Context) { t.Fatal("must reject fixture before dispatch") })
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"`+tc.model+`"}`))
			if tc.parentBudget > 0 {
				ctx, cancel := context.WithTimeout(request.Context(), tc.parentBudget)
				defer cancel()
				request = request.WithContext(ctx)
			}
			router.ServeHTTP(httptest.NewRecorder(), request)
			require.InDelta(t, tc.want.Seconds(), observed.Seconds(), 1)
		})
	}
}

func TestLongThinkingKeyRouteAliasBudgetUsesOriginalIngressAndExplicitLimit(t *testing.T) {
	started := time.Now().Add(-100 * time.Second)
	for _, tc := range []struct {
		name    string
		gateway config.GatewayConfig
		want    time.Duration
	}{
		{name: "long alias", want: 1700 * time.Second},
		{name: "explicit600", gateway: config.GatewayConfig{KeyGroupRequestTimeoutSeconds: 600, KeyGroupRequestTimeoutExplicit: true}, want: 500 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := longThinkingKeyRouteContext(context.Background(), started, &config.Config{Gateway: tc.gateway})
			defer cancel()
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.InDelta(t, tc.want.Seconds(), time.Until(deadline).Seconds(), 1)
			// The first failure must freeze recovery against the enlarged root,
			// not the obsolete ordinary600 default.
			ctx = service.WithRequestRecovery(ctx, 600*time.Second)
			require.True(t, service.BeginRequestRecovery(ctx))
			if !tc.gateway.KeyGroupRequestTimeoutExplicit {
				require.InDelta(t, 600, service.RequestRecoveryRemaining(ctx).Seconds(), 1)
			}
		})
	}
	parent, cancelParent := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelParent()
	ctx, cancel := longThinkingKeyRouteContext(parent, started, nil)
	defer cancel()
	deadline, _ := ctx.Deadline()
	require.InDelta(t, 15, time.Until(deadline).Seconds(), 1)
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
