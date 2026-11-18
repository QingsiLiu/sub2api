package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNativeAnthropicAuditGeiliConsumesToolArgumentsOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, audit := WithResponseAudit(context.Background(), nil, ResponseAudit{Protocol: "messages"}, false)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	frames := []string{
		`{"type":"message_start","message":{"id":"msg_audit","type":"message","role":"assistant","model":"claude-sonnet-4","content":[],"usage":{"input_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_audit","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a\"}"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	}
	var wire string
	for _, frame := range frames {
		wire += "data: " + frame + "\n\n"
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
	_, err := (&OpenAIGatewayService{}).handleNativeAnthropicStreamingResponse(ctx, resp, c, &Account{}, "claude-sonnet-4", "claude-sonnet-4", "claude-sonnet-4", nil, time.Now())
	require.NoError(t, err)
	require.True(t, audit.up.tool, "one valid JSON tool argument must remain valid in upstream audit evidence")
	require.Equal(t, "message_stop", audit.up.terminal)
	require.Equal(t, wire, rec.Body.String(), "audit observation preserves raw SSE bytes")
}
