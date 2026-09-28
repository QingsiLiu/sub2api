package routes

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const codexTestUA = "codex_cli_rs/0.157.1 (Mac OS 15.0.0; arm64) vscode/1.104.0 (codex_vscode; 0.157.1)"

func serveCodexRetryable(t *testing.T, path, ua string, write func(c *gin.Context)) (*httptest.ResponseRecorder, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	innerStatus := 0
	engine.Use(codexRetryableRateLimit(nil, handler.OpsErrorLoggerMiddleware(nil)))
	engine.Use(func(c *gin.Context) {
		c.Next()
		innerStatus = c.Writer.Status()
	})
	engine.POST(path, write)
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5.6","stream":true}`))
	r.Header.Set("User-Agent", ua)
	out := httptest.NewRecorder()
	engine.ServeHTTP(out, r)
	return out, innerStatus
}

func TestCodexRetryableRateLimitRewritesTransient429(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		retryAfter string
		wantAfter  string
	}{
		{"upstream limit", `{"error":{"type":"rate_limit_error","message":"Upstream rate limit exceeded, please retry later"}}`, "", "5"},
		{"upstream retry-after kept", `{"error":{"type":"rate_limit_error","message":"Upstream rate limit exceeded, please retry later"}}`, "17", "17"},
		{"all accounts limited", `{"error":{"type":"rate_limit_error","message":"All available accounts are currently rate-limited. Please retry later."}}`, "", "5"},
		{"passthrough upstream", `{"error":{"type":"upstream_error","message":"当前分组上游负载已饱和"}}`, "", "5"},
		{"queue full", `{"error":{"type":"rate_limit_error","code":"gateway_queue_full","message":"Too many pending requests, please retry later"}}`, "", "5"},
		{"concurrency", `{"error":{"type":"rate_limit_error","code":"gateway_concurrency_limit","message":"Concurrency limit exceeded for user, please retry later"}}`, "", "5"},
		{"user rpm", `{"error":{"type":"rate_limit_exceeded","message":"user requests-per-minute limit exceeded"}}`, "42", "42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, inner := serveCodexRetryable(t, "/v1/responses", codexTestUA, func(c *gin.Context) {
				if tc.retryAfter != "" {
					c.Header("Retry-After", tc.retryAfter)
				}
				c.Header("X-Request-Id", "req-1")
				c.Data(http.StatusTooManyRequests, "application/json", []byte(tc.body))
			})
			require.Equal(t, http.StatusTooManyRequests, inner, "ops must still observe the original 429")
			require.Equal(t, http.StatusServiceUnavailable, out.Code)
			require.Equal(t, tc.wantAfter, out.Header().Get("Retry-After"))
			require.Equal(t, "req-1", out.Header().Get("X-Request-Id"))
			body := out.Body.Bytes()
			require.Equal(t, "slow_down", gjson.GetBytes(body, "error.code").String())
			require.Equal(t, "rate_limit_error", gjson.GetBytes(body, "error.type").String())
			require.Equal(t, gjson.Get(tc.body, "error.message").String(), gjson.GetBytes(body, "error.message").String())
		})
	}
}

func TestCodexRetryableRateLimitKeepsFinal429(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"rate_limit_exceeded","message":"api key 5h rate limit exceeded"}}`,
		`{"error":{"type":"rate_limit_error","code":"SUBSCRIPTION_QUOTA_EXCEEDED","message":"daily usage limit exceeded"}}`,
		`{"error":{"type":"upstream_error","message":"You exceeded your current quota"}}`,
		`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`,
		`{"error":{"type":"rate_limit_error","message":"slow down"}}`,
		`not json`,
	} {
		t.Run(body, func(t *testing.T) {
			out, _ := serveCodexRetryable(t, "/v1/responses", codexTestUA, func(c *gin.Context) {
				c.Data(http.StatusTooManyRequests, "application/json", []byte(body))
			})
			require.Equal(t, http.StatusTooManyRequests, out.Code)
			require.Equal(t, body, out.Body.String())
			require.Empty(t, out.Header().Get("Retry-After"))
		})
	}
}

func TestCodexRetryableRateLimitScope(t *testing.T) {
	limited := func(c *gin.Context) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"type": "rate_limit_error", "message": "Upstream rate limit exceeded, please retry later"}})
	}
	for _, tc := range []struct {
		name, path, ua string
		want           int
	}{
		{"codex responses", "/v1/responses", codexTestUA, http.StatusServiceUnavailable},
		{"codex root responses", "/responses", "codex-tui/0.157.1 (Mac OS 15.0.0; arm64)", http.StatusServiceUnavailable},
		{"codex direct compact", "/backend-api/codex/responses/compact", codexTestUA, http.StatusServiceUnavailable},
		{"other sdk", "/v1/responses", "OpenAI/Python 2.1.0", http.StatusTooManyRequests},
		{"codex chat completions", "/v1/chat/completions", codexTestUA, http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := serveCodexRetryable(t, tc.path, tc.ua, limited)
			require.Equal(t, tc.want, out.Code)
		})
	}
}

func TestCodexRetryableRateLimitPassesThroughOtherResponses(t *testing.T) {
	out, inner := serveCodexRetryable(t, "/v1/responses", codexTestUA, func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString("data: one\n\n")
		c.Writer.Flush()
		_, _ = c.Writer.WriteString("data: two\n\n")
	})
	require.Equal(t, http.StatusOK, inner)
	require.Equal(t, http.StatusOK, out.Code)
	require.Equal(t, "data: one\n\ndata: two\n\n", out.Body.String())

	out, _ = serveCodexRetryable(t, "/v1/responses", codexTestUA, func(c *gin.Context) {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Upstream service temporarily unavailable"}})
	})
	require.Equal(t, http.StatusBadGateway, out.Code)

	out, _ = serveCodexRetryable(t, "/v1/responses", codexTestUA, func(c *gin.Context) {
		c.AbortWithStatus(http.StatusTooManyRequests)
	})
	require.Equal(t, http.StatusTooManyRequests, out.Code)
	require.Empty(t, out.Body.String())
}

// Composite Key rejections reach the shim only after the last group commits.
func TestCodexRetryableRateLimitAfterCompositeCommit(t *testing.T) {
	out, inner := serveCodexRetryable(t, "/v1/responses", codexTestUA, func(c *gin.Context) {
		w := newKeyRouteWriter(c.Writer)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.WriteString(`{"error":{"type":"rate_limit_error","message":"Upstream rate limit exceeded, please retry later"}}`)
		w.commit()
	})
	require.Equal(t, http.StatusTooManyRequests, inner)
	require.Equal(t, http.StatusServiceUnavailable, out.Code)
	require.Equal(t, "slow_down", gjson.GetBytes(out.Body.Bytes(), "error.code").String())
}

// Every gateway chain reuses opsErrorLogger, so wrapping it once covers the
// /v1, root alias and /backend-api/codex Responses routes.
func TestCodexRetryableRateLimitWrapsGatewayOpsLogger(t *testing.T) {
	source, err := os.ReadFile("gateway.go")
	require.NoError(t, err)
	require.Contains(t, string(source), "opsErrorLogger = codexRetryableRateLimit(settingService, opsErrorLogger)")
}
