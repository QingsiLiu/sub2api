package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiSupplierFailureGeiliExhaustionProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages", "/v1beta/models/test:generateContent"} {
		for _, started := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/json", true: "/stream"}[started], func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"test"}`))
				if started {
					c.Header("Content-Type", "text/event-stream")
					_, _ = c.Writer.WriteString(": keepalive\n\n")
					c.Writer.Flush()
				}
				f := &service.UpstreamFailoverError{StatusCode: 400, Reason: service.GatewayFailureReason("supplier_capacity_unavailable")}
				require.True(t, (&GatewayHandler{}).writeGeminiSupplierUnavailableGeili(c, f, started))
				require.Contains(t, w.Body.String(), "upstream_unavailable")
				require.NotContains(t, w.Body.String(), "余额")
				if started {
					require.Equal(t, 200, w.Code)
					if path == "/v1/responses" {
						require.Equal(t, 1, strings.Count(w.Body.String(), "event: response.failed"))
						require.NotContains(t, w.Body.String(), "response.completed")
					}
				} else {
					require.Equal(t, 503, w.Code)
					var body map[string]any
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				}
			})
		}
	}
}

func TestGeminiSupplierFailureGeiliUnclassifiedAndCommitted(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	h := &GatewayHandler{}
	require.False(t, h.writeGeminiSupplierUnavailableGeili(c, &service.UpstreamFailoverError{StatusCode: 400}, false))
	require.Empty(t, w.Body.String())
	_, _ = c.Writer.WriteString("already delivered")
	service.MarkResponseCommitted(c)
	require.True(t, h.writeGeminiSupplierUnavailableGeili(c, &service.UpstreamFailoverError{Reason: "supplier_capacity_unavailable"}, true))
	require.Equal(t, "already delivered", w.Body.String())
}

func TestGeminiSupplierFailureGeiliLastUnknownErrorIsNotOverridden(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request = c.Request.WithContext(service.WithRequestRecovery(context.Background(), time.Minute))
	service.RecordGeminiSupplierFailureGeili(c.Request.Context(), &service.Account{ID: 1, Platform: service.PlatformGemini}, "test", false)
	h := &GatewayHandler{}
	require.False(t, h.geminiSupplierSelectionFailureGeili(c, service.ErrNoAvailableAccounts, &service.UpstreamFailoverError{StatusCode: 403}, false))
	require.Empty(t, w.Body.String())
	// A new group with only previously excluded targets still reports availability.
	require.True(t, h.geminiSupplierSelectionFailureGeili(c, service.ErrNoAvailableAccounts, nil, false))
	require.Equal(t, 503, w.Code)
}

func TestGeminiSupplierFailureGeiliNativeBufferedHeartbeat(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/test:streamGenerateContent", nil)
	c.Header("Content-Type", "text/event-stream")
	_, _ = c.Writer.WriteString(": keepalive\n\n")
	c.Writer.Flush()
	WriteTextRecoveryFailure(c, 503, []byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"Upstream resources are temporarily unavailable","details":[{"reason":"upstream_unavailable"}]}}`))
	require.Contains(t, w.Body.String(), `data: {"error":{"code":503`)
	require.Contains(t, w.Body.String(), `"reason":"upstream_unavailable"`)
	require.NotContains(t, w.Body.String(), "event: error")
}

func TestGeminiSupplierFailureGeiliCarryOnlyDiagnostics(t *testing.T) {
	dst, _ := gin.CreateTestContext(httptest.NewRecorder())
	src, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := service.WithRequestRecovery(context.Background(), time.Minute)
	src.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	service.RecordGeminiSupplierFailureGeili(ctx, &service.Account{ID: 1, Platform: service.PlatformGemini}, "model", false)
	events := []*service.OpsUpstreamErrorEvent{{AccountID: 1, UpstreamStatusCode: 403}, {AccountID: 2, UpstreamStatusCode: 400}}
	src.Set(service.OpsUpstreamErrorsKey, events)
	src.Set(service.OpsUpstreamStatusCodeKey, 400)
	src.Set(service.ResponseCommittedKey, true)
	src.Set("local_admission_error", true)
	CarryGeminiSupplierFailureContextGeili(dst, src)
	value, _ := dst.Get(service.OpsUpstreamErrorsKey)
	require.Equal(t, events, value)
	require.Equal(t, 400, dst.GetInt(service.OpsUpstreamStatusCodeKey))
	require.False(t, service.IsResponseCommitted(dst))
	require.False(t, dst.GetBool("local_admission_error"))
	// Appending another group's event must not mutate the prior slice.
	carried := value.([]*service.OpsUpstreamErrorEvent)
	carried = append(carried, &service.OpsUpstreamErrorEvent{AccountID: 3})
	require.Len(t, events, 2)
	require.Len(t, carried, 3)
}
