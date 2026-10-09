//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func anthropicCompletionFixtureGeili(parts ...string) string {
	var out strings.Builder
	for _, part := range parts {
		_, _ = out.WriteString("data: ")
		_, _ = out.WriteString(part)
		_, _ = out.WriteString("\n\n")
	}
	return out.String()
}

const (
	geiliAnthropicStart  = `{"type":"message_start","message":{"id":"failed_attempt_id","type":"message","role":"assistant","content":[],"model":"claude-opus-5-5","usage":{"input_tokens":10}}}`
	geiliAnthropicBlock  = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
	geiliAnthropicText   = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}`
	geiliAnthropicClosed = `{"type":"content_block_stop","index":0}`
	geiliAnthropicReason = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`
	geiliAnthropicStop   = `{"type":"message_stop"}`
)

type anthropicDisconnectWriterGeili struct {
	gin.ResponseWriter
	failed             bool
	writesAfterFailure int
}

func (w *anthropicDisconnectWriterGeili) Write(p []byte) (int, error) {
	if w.failed {
		w.writesAfterFailure++
		return 0, io.ErrClosedPipe
	}
	if strings.Contains(string(p), "visible") {
		w.failed = true
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(p)
}

func (w *anthropicDisconnectWriterGeili) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func TestAnthropicCompletionGeili_ConvertedPublicForwardDrainsUsageAfterWriteDisconnect(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			writer := &anthropicDisconnectWriterGeili{ResponseWriter: c.Writer}
			c.Writer = writer
			pr, pw := io.Pipe()
			defer func() { _ = pw.Close() }()
			go func() {
				defer func() { _ = pw.Close() }()
				_, _ = io.WriteString(pw, anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText))
				// A disconnected writer must not receive scheduled heartbeats while
				// the already started upstream generation drains its final usage.
				time.Sleep(1200 * time.Millisecond)
				_, _ = io.WriteString(pw, anthropicCompletionFixtureGeili(geiliAnthropicClosed, geiliAnthropicReason, geiliAnthropicStop))
			}()
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:   pr}}
			svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamKeepaliveInterval: 1, StreamDataIntervalTimeout: 2, LongThinkingStreamDataIntervalTimeout: 3}}, httpUpstream: upstream}
			account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-key"}}
			body := []byte(`{"model":"claude-opus-5-5","stream":true,"messages":[{"role":"user","content":"hello"}],"input":"hello"}`)
			var result *ForwardResult
			var err error
			if path == "/v1/chat/completions" {
				result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
			} else {
				result, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
			}
			require.NoError(t, err, "write-detected disconnect must preserve successful bounded usage drain")
			require.NotNil(t, result)
			require.True(t, result.ClientDisconnect)
			require.Equal(t, 10, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.NoError(t, c.Request.Context().Err(), "test real writer failure before cancellation propagation")
			require.True(t, writer.failed)
			require.Zero(t, writer.writesAfterFailure)
		})
	}
}

func TestAnthropicCompletionGeili_NativeReadersDrainUsageAfterWriteDisconnect(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			writer := &anthropicDisconnectWriterGeili{ResponseWriter: c.Writer}
			c.Writer = writer
			finish := BeginTextForwardGuard(c, true)
			pr, pw := io.Pipe()
			defer func() { _ = pw.Close() }()
			go func() {
				defer func() { _ = pw.Close() }()
				_, _ = io.WriteString(pw, anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText))
				time.Sleep(1200 * time.Millisecond)
				_, _ = io.WriteString(pw, anthropicCompletionFixtureGeili(geiliAnthropicClosed, geiliAnthropicReason, geiliAnthropicStop))
			}()
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: pr}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamKeepaliveInterval: 1, StreamDataIntervalTimeout: 2, LongThinkingStreamDataIntervalTimeout: 3}}}
			var result *OpenAIForwardResult
			var err error
			switch path {
			case "/v1/chat/completions":
				result, err = svc.handleCCStreamingFromNativeAnthropic(resp, c, "glm-5", "glm-5", "glm-5", nil, time.Now())
			case "/v1/responses":
				result, err = svc.handleResponsesStreamingFromNativeAnthropic(resp, c, "glm-5", "glm-5", "glm-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			default:
				result, err = svc.handleNativeAnthropicStreamingResponse(context.Background(), resp, c, &Account{}, "glm-5", "glm-5", "glm-5", nil, time.Now())
			}
			err = finish(err)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.ClientDisconnect)
			require.Equal(t, 10, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.NoError(t, c.Request.Context().Err())
			require.True(t, writer.failed)
			require.Zero(t, writer.writesAfterFailure, "a scheduled heartbeat must not touch a disconnected parent writer")
		})
	}
}

func TestAnthropicCompletionGeili_ValidAndTruncatedConversions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handlers := []struct {
		name, path string
		stream     bool
		run        func(*http.Response, *gin.Context) error
	}{
		{"anthropic CC buffered", "/v1/chat/completions", false, func(r *http.Response, c *gin.Context) error {
			_, err := (&GatewayService{}).handleCCBufferedFromAnthropic(r, c, "gpt-6-astra", "claude-opus-5-5", nil, time.Now())
			return err
		}},
		{"anthropic CC stream", "/v1/chat/completions", true, func(r *http.Response, c *gin.Context) error {
			_, err := (&GatewayService{}).handleCCStreamingFromAnthropic(r, c, "gpt-6-astra", "claude-opus-5-5", nil, time.Now())
			return err
		}},
		{"anthropic Responses buffered", "/v1/responses", false, func(r *http.Response, c *gin.Context) error {
			_, err := (&GatewayService{}).handleResponsesBufferedStreamingResponse(r, c, "gpt-6-astra", "claude-opus-5-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			return err
		}},
		{"anthropic Responses stream", "/v1/responses", true, func(r *http.Response, c *gin.Context) error {
			_, err := (&GatewayService{}).handleResponsesStreamingResponse(r, c, "gpt-6-astra", "claude-opus-5-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			return err
		}},
		{"CN CC buffered", "/v1/chat/completions", false, func(r *http.Response, c *gin.Context) error {
			_, err := (&OpenAIGatewayService{}).handleCCBufferedFromNativeAnthropic(r, c, "gpt-6-astra", "claude-opus-5-5", "claude-opus-5-5", nil, time.Now())
			return err
		}},
		{"CN CC stream", "/v1/chat/completions", true, func(r *http.Response, c *gin.Context) error {
			_, err := (&OpenAIGatewayService{}).handleCCStreamingFromNativeAnthropic(r, c, "gpt-6-astra", "claude-opus-5-5", "claude-opus-5-5", nil, time.Now())
			return err
		}},
		{"CN Responses buffered", "/v1/responses", false, func(r *http.Response, c *gin.Context) error {
			_, err := (&OpenAIGatewayService{}).handleResponsesBufferedFromNativeAnthropic(r, c, "gpt-6-astra", "claude-opus-5-5", "claude-opus-5-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			return err
		}},
		{"CN Responses stream", "/v1/responses", true, func(r *http.Response, c *gin.Context) error {
			_, err := (&OpenAIGatewayService{}).handleResponsesStreamingFromNativeAnthropic(r, c, "gpt-6-astra", "claude-opus-5-5", "claude-opus-5-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			return err
		}},
		{"native Messages stream", "/v1/messages", true, func(r *http.Response, c *gin.Context) error {
			_, err := (&OpenAIGatewayService{}).handleNativeAnthropicStreamingResponse(context.Background(), r, c, &Account{}, "claude-opus-5-5", "claude-opus-5-5", "claude-opus-5-5", nil, time.Now())
			return err
		}},
		{"Messages passthrough stream", "/v1/messages", true, func(r *http.Response, c *gin.Context) error {
			_, err := (&GatewayService{}).handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), r, c, &Account{}, time.Now(), "claude-opus-5-5")
			return err
		}},
		{"Messages gateway stream", "/v1/messages", true, func(r *http.Response, c *gin.Context) error {
			_, err := (&GatewayService{rateLimitService: &RateLimitService{}}).handleStreamingResponse(context.Background(), r, c, &Account{}, time.Now(), "claude-opus-5-5", "claude-opus-5-5", false)
			return err
		}},
	}
	cases := []struct {
		name, body       string
		valid, preOutput bool
	}{
		{"complete", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, geiliAnthropicClosed, geiliAnthropicReason, geiliAnthropicStop), true, false},
		{"compatible EOF", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, geiliAnthropicClosed, geiliAnthropicReason), true, false},
		{"empty answer", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicReason, geiliAnthropicStop), true, true},
		{"no events", "", false, true},
		{"only ping", anthropicCompletionFixtureGeili(`{"type":"ping"}`), false, true},
		{"only prelude", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock), false, true},
		{"truncated text", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText), false, false},
		{"stop with open block", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, geiliAnthropicReason, geiliAnthropicStop), false, false},
		{"stop without reason", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicStop), false, true},
		{"bare DONE", anthropicCompletionFixtureGeili("[DONE]"), false, true},
		{"error before output", anthropicCompletionFixtureGeili(geiliAnthropicStart, `{"type":"error","error":{"type":"overloaded_error","message":"Upstream service temporarily unavailable"}}`), false, true},
		{"error after output", anthropicCompletionFixtureGeili(geiliAnthropicStart, geiliAnthropicBlock, geiliAnthropicText, `{"type":"error","error":{"type":"overloaded_error","message":"Upstream service temporarily unavailable"}}`), false, false},
	}
	for _, handler := range handlers {
		for _, tc := range cases {
			t.Run(handler.name+"/"+tc.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", handler.path, nil)
				finish := BeginTextForwardGuard(c, handler.stream)
				err := handler.run(&http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": []string{"private_attempt_header"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, c)
				err = finish(err)
				if tc.valid {
					require.NoError(t, err)
					if tc.name != "empty answer" {
						require.Contains(t, rec.Body.String(), "visible")
					}
					return
				}
				require.Error(t, err)
				var failover *UpstreamFailoverError
				if tc.preOutput || !handler.stream {
					require.ErrorAs(t, err, &failover)
					require.NotContains(t, rec.Body.String(), "failed_attempt_id")
					require.Empty(t, rec.Header().Get("X-Request-Id"))
				} else {
					require.False(t, errors.As(err, &failover))
					require.Contains(t, rec.Body.String(), "visible")
					require.Contains(t, rec.Body.String(), "error")
				}
				require.NotContains(t, rec.Body.String(), `"status":"completed"`)
				require.NotContains(t, rec.Body.String(), `"finish_reason":"stop"`)
				require.NotContains(t, rec.Body.String(), "data: [DONE]")
			})
		}
	}
}

func TestAnthropicCompletionGeili_JSONFailures(t *testing.T) {
	handlers := []struct {
		name string
		run  func(*http.Response, *gin.Context) error
	}{
		{"gateway", func(r *http.Response, c *gin.Context) error {
			_, err := (&GatewayService{rateLimitService: &RateLimitService{}}).handleNonStreamingResponse(context.Background(), r, c, &Account{}, "claude-opus-5-5", "claude-opus-5-5")
			return err
		}},
		{"passthrough", func(r *http.Response, c *gin.Context) error {
			_, err := (&GatewayService{}).handleNonStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), r, c, &Account{})
			return err
		}},
		{"native", func(r *http.Response, c *gin.Context) error {
			_, err := (&OpenAIGatewayService{}).handleNativeAnthropicBufferedResponse(context.Background(), r, c, &Account{}, "claude-opus-5-5", "claude-opus-5-5", "claude-opus-5-5", nil, time.Now())
			return err
		}},
	}
	cases := []struct {
		name, body string
		status     int
		retry      bool
	}{
		{"valid empty answer", `{"type":"message","id":"msg_empty","content":[],"stop_reason":"end_turn"}`, 200, false},
		{"usage alone", `{"type":"message","usage":{"input_tokens":2,"output_tokens":3}}`, 502, true},
		{"temporary error in HTTP200", `{"type":"error","error":{"type":"overloaded_error","message":"Upstream temporarily unavailable"}}`, 529, true},
		{"images hard error in HTTP200", `{"type":"error","error":{"type":"invalid_request_error","message":"Exceeded maximum number of images (50) allowed in the request."}}`, 400, false},
	}
	for _, handler := range handlers {
		for _, tc := range cases {
			t.Run(handler.name+"/"+tc.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
				finish := BeginTextForwardGuard(c, false)
				err := finish(handler.run(&http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, c))
				if tc.status == 200 {
					require.NoError(t, err)
					require.JSONEq(t, tc.body, rec.Body.String())
					return
				}
				require.Error(t, err)
				var failure *UpstreamFailoverError
				if tc.retry {
					require.ErrorAs(t, err, &failure)
					require.Empty(t, rec.Body.String())
				} else {
					require.False(t, errors.As(err, &failure))
					require.Equal(t, tc.status, rec.Code)
					require.Contains(t, rec.Body.String(), "maximum number of images")
				}
			})
		}
	}
}

func TestAnthropicCompletionGeili_ToolAndFraming(t *testing.T) {
	for _, toolJSON := range []string{`{"large":9007199254740993}`, `{"large":`} {
		for _, style := range []string{"compact", "CRLF", "multi-line"} {
			t.Run(toolJSON+"/"+style, func(t *testing.T) {
				body := anthropicCompletionFixtureGeili(geiliAnthropicStart, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_1","name":"read","input":{}}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":`+strconv.Quote(toolJSON)+`}}`, geiliAnthropicClosed, `{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`)
				if style == "compact" {
					body = strings.ReplaceAll(body, "data: ", "data:")
				}
				if style == "CRLF" {
					body = strings.ReplaceAll(body, "\n", "\r\n")
				}
				if style == "multi-line" {
					body = strings.ReplaceAll(body, `"type":"message_start",`, `"type":"message_start",`+"\ndata: ")
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				tracker, err := readAnthropicCompletionGeili(c, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil, "claude-opus-5-5", false, nil)
				if strings.HasSuffix(toolJSON, "}") {
					require.NoError(t, err)
					require.Equal(t, toolJSON, string(tracker.response.Content[0].Input))
				} else {
					require.Error(t, err)
				}
			})
		}
	}
}

func TestAnthropicCompletionGeili_IdleRecoveryAfterHeartbeat(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	finish := BeginTextForwardGuard(c, true)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	go func() { _, _ = io.WriteString(pw, anthropicCompletionFixtureGeili(geiliAnthropicStart)) }()
	_, err := readAnthropicCompletionGeili(c, &http.Response{StatusCode: 200, Body: pr}, &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 2, StreamKeepaliveInterval: 1}}, "gpt-4", true, nil)
	err = finish(err)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Contains(t, rec.Body.String(), ": keepalive")
	require.NotContains(t, rec.Body.String(), "failed_attempt_id")
}
