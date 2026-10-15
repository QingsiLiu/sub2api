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

// The OAuth mimic stream relays whole SSE blocks, not single data lines.
func TestRestoreToolNamesInBytesHandlesSSEBlocks(t *testing.T) {
	fakeName := "cc_" + "sess_audit"
	rw := &ToolNameRewrite{Reverse: map[string]string{fakeName: "sessions_audit"}}
	block := "event: content_block_start\r\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"name\":\"" + fakeName + "\",\"input\":{}}}\r\n\r\n"
	want := strings.Replace(block, `"name":"`+fakeName+`"`, `"name":"sessions_audit"`, 1)
	require.Equal(t, want, string(restoreToolNamesInBytes([]byte(block), rw)))

	text := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"" + fakeName + "\"}}\n\n"
	require.Equal(t, text, string(restoreToolNamesInBytes([]byte(text), rw)))

	pretty := "{\n  \"content\": [\n    {\"type\": \"tool_use\", \"name\": \"" + fakeName + "\"}\n  ]\n}"
	require.Contains(t, string(restoreToolNamesInBytes([]byte(pretty), rw)), `"name": "sessions_audit"`)
}

// A silent strict upstream receives the protocol's own ping, never inside an event.
func TestStrictAnthropicStreamKeepaliveOnlyBetweenEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reader, writer := io.Pipe()
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}}
	svc := newForwardPartialUsageServiceForTest(upstream)
	svc.cfg.Gateway.StreamKeepaliveInterval = 1
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	go func() {
		_, _ = io.WriteString(writer, "event: message_start\r\n")
		time.Sleep(1300 * time.Millisecond)
		_, _ = io.WriteString(writer, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\r\n\r\n")
		time.Sleep(2300 * time.Millisecond)
		_, _ = io.WriteString(writer, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\r\n\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n")
		_ = writer.Close()
	}()
	result, err := svc.handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), upstream.resp, c, newAnthropicAPIKeyAccountForTest(), time.Now(), "claude-fable-5-1")
	require.NoError(t, err)
	require.Equal(t, 3, result.usage.InputTokens)
	body := rec.Body.String()
	ping := "event: ping\ndata: {\"type\": \"ping\"}\n\n"
	require.Contains(t, body, ping)
	for i := strings.Index(body, ping); i >= 0; {
		require.True(t, i == 0 || strings.HasSuffix(body[:i], "\n\n") || strings.HasSuffix(body[:i], "\r\n\r\n"), "keepalive only between complete events")
		next := strings.Index(body[i+len(ping):], ping)
		if next < 0 {
			break
		}
		i += len(ping) + next
	}
	require.Equal(t, "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\r\n\r\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\r\n\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n",
		strings.ReplaceAll(body, ping, ""), "provider bytes stay unchanged around keepalives")
}

func TestStrictAnthropicFailoverExhaustionWritesNativeError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	failover := &UpstreamFailoverError{StatusCode: 529, ResponseBody: []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`),
		ResponseHeaders: http.Header{"Content-Type": {"application/json"}, "Request-Id": {"req_native"}, "Set-Cookie": {"secret"}}, StrictPassthrough: true}
	require.False(t, WriteStrictAnthropicFailoverGeili(c, failover, true), "a committed stream keeps in-stream errors")
	require.False(t, WriteStrictAnthropicFailoverGeili(c, &UpstreamFailoverError{StatusCode: 529}, false), "compatibility errors keep the mapped envelope")
	require.True(t, WriteStrictAnthropicFailoverGeili(c, failover, false))
	require.Equal(t, 529, rec.Code)
	require.Equal(t, string(failover.ResponseBody), rec.Body.String())
	require.Equal(t, "req_native", rec.Header().Get("Request-Id"))
	require.Empty(t, rec.Header().Get("Set-Cookie"))
}
