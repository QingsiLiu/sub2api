package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStrictAnthropicReadPreservesDecodedBody(t *testing.T) {
	for _, body := range []string{"{\n \"model\": \"test\",\"unknown\":9007199254740993 }\n", "{\"text\":\"raw\ncontrol\"}"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		out, err := readAnthropicGatewayRequestBody(req, nil, &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})
		require.NoError(t, err)
		require.Equal(t, body, string(out))
	}
}
func TestStrictAnthropicAdmissionDoesNotStartResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Set("gateway_suppress_wait_ping", true)
	h := &ConcurrencyHelper{pingFormat: SSEPingFormatClaude, pingInterval: time.Millisecond}
	started := false
	// The timeout precedes the first slot poll: exercise a real queued wait with
	// many potential ping ticks without requiring a cache or upstream service.
	_, err := h.waitForSlotWithPingTimeout(c, "account", 1, 1, 15*time.Millisecond, true, &started, false)
	require.Error(t, err)
	require.False(t, started)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}
