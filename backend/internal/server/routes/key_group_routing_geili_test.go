package routes

import (
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
