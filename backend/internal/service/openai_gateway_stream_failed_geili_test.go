package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type sseTestFrame struct {
	event string
	data  string
}

func parseSSETestFrames(t *testing.T, body string) []sseTestFrame {
	t.Helper()
	var frames []sseTestFrame
	for _, raw := range strings.Split(strings.TrimSpace(body), "\n\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, ":") {
			continue
		}
		var frame sseTestFrame
		for _, line := range strings.Split(raw, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				frame.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				frame.data = strings.TrimPrefix(line, "data: ")
			}
		}
		frames = append(frames, frame)
	}
	return frames
}

func newOpenAIStreamFailedTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, rec
}

func newOpenAIStreamFailedTestService(intervalSeconds int) *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		StreamDataIntervalTimeout: intervalSeconds,
		MaxLineSize:               defaultMaxLineSize,
	}}}
}

func requireResponsesFailedFrame(t *testing.T, frame sseTestFrame, wantCode string) {
	t.Helper()
	require.Equal(t, "response.failed", frame.event)
	require.Equal(t, "response.failed", gjson.Get(frame.data, "type").String())
	require.True(t, gjson.Get(frame.data, "sequence_number").Exists())
	require.Equal(t, "failed", gjson.Get(frame.data, "response.status").String())
	require.Equal(t, "response", gjson.Get(frame.data, "response.object").String())
	require.Positive(t, gjson.Get(frame.data, "response.created_at").Int())
	require.True(t, gjson.Get(frame.data, "response.output").IsArray())
	require.Equal(t, wantCode, gjson.Get(frame.data, "response.error.code").String())
	require.NotEmpty(t, gjson.Get(frame.data, "response.error.message").String())
}

func TestOpenAIStreamingTimeoutEndsWithResponseFailedForAPIKeyAccount(t *testing.T) {
	svc := newOpenAIStreamFailedTestService(1)
	c, rec := newOpenAIStreamFailedTestContext()
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close(); _ = pr.Close() }()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "gpt-6-astra", "gpt-6-astra")
	require.ErrorContains(t, err, "stream data interval timeout")

	frames := parseSSETestFrames(t, rec.Body.String())
	require.Len(t, frames, 1, "Responses clients must not receive a bare error frame")
	requireResponsesFailedFrame(t, frames[0], "stream_timeout")
	require.Equal(t, "gpt-6-astra", gjson.Get(frames[0].data, "response.model").String())
	require.True(t, IsResponseCommitted(c))
}

func TestOpenAIStreamingTimeoutOAuthAccountSendsOnlyResponseFailed(t *testing.T) {
	svc := newOpenAIStreamFailedTestService(1)
	c, rec := newOpenAIStreamFailedTestContext()
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close(); _ = pr.Close() }()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, newOpenAIOAuthNamespaceTestAccount(), time.Now(), "gpt-6-astra", "gpt-6-astra")
	require.ErrorContains(t, err, "stream data interval timeout")

	frames := parseSSETestFrames(t, rec.Body.String())
	require.Len(t, frames, 1, "Codex must not receive a bare error frame")
	requireResponsesFailedFrame(t, frames[0], "stream_timeout")
	require.True(t, IsResponseCommitted(c))
}

func TestOpenAIStreamingMidStreamReadErrorEndsWithResponseFailed(t *testing.T) {
	svc := newOpenAIStreamFailedTestService(0)
	c, rec := newOpenAIStreamFailedTestContext()
	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}
	go func() {
		_, _ = pw.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"))
		_ = pw.CloseWithError(io.ErrUnexpectedEOF)
	}()

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, newOpenAIOAuthNamespaceTestAccount(), time.Now(), "gpt-6-astra", "gpt-6-astra")
	require.ErrorContains(t, err, "stream read error")

	frames := parseSSETestFrames(t, rec.Body.String())
	require.Len(t, frames, 2)
	require.Equal(t, "response.output_text.delta", frames[0].event)
	requireResponsesFailedFrame(t, frames[1], OpenAIUpstreamStreamReadErrorCode)
	require.True(t, IsResponseCommitted(c))
}
