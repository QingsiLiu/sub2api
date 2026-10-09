//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func textBufferedServiceGeili() *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		StreamDataIntervalTimeout: 1, LongThinkingStreamDataIntervalTimeout: 3,
	}}}
}

func textBufferedResponseGeili(contentType, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"Content-Type": []string{contentType}, "X-Request-Id": []string{"private-A"},
	}, Body: io.NopCloser(strings.NewReader(body))}
}

func textBufferedCompletedGeili(id, text string, usage bool) string {
	content := `[]`
	if text != "" {
		content = `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}]`
	}
	usageJSON := ""
	if usage {
		usageJSON = `,"usage":{"input_tokens":11,"output_tokens":5}`
	}
	return `data: {"type":"response.completed","response":{"id":"` + id + `","object":"response","status":"completed","model":"gpt-6-astra","output":` + content + usageJSON + `}}` + "\n\n"
}

func textBufferedAttemptGeili(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, resp *http.Response, model string) (*openaiNonStreamingResult, error) {
	finish := BeginTextForwardGuard(c, false)
	result, err := s.handleNonStreamingResponse(ctx, resp, c, newNonStreamingFailoverAccount(), model, model)
	return result, finish(err)
}

func TestTextBufferedReadGeiliIncompleteEOFCanRecoverWithoutLeakingAttempt(t *testing.T) {
	for _, tc := range []struct{ name, contentType, body string }{
		{"empty SSE EOF", "text/event-stream", ""},
		{"only heartbeat", "text/event-stream", ": ping\n\n"},
		{"only opening", "text/event-stream", "data: {\"type\":\"response.created\",\"response\":{\"id\":\"private-A\"}}\n\n"},
		{"text and usage without terminal", "text/event-stream", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial-A\"}\n\ndata: {\"usage\":{\"input_tokens\":11,\"output_tokens\":5}}\n\ndata: [DONE]\n\n"},
		{"DONE alone", "text/event-stream", "data: [DONE]\n\n"},
		{"empty JSON EOF", "application/json", ""},
		{"truncated JSON EOF", "application/json", `{"id":"private-A","output":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := guardContextGeili("/v1/responses")
			s := textBufferedServiceGeili()
			result, err := textBufferedAttemptGeili(s, c.Request.Context(), c, textBufferedResponseGeili(tc.contentType, tc.body), "gpt-6-astra")
			require.Nil(t, result)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.SafeToFailoverAfterWrite)
			require.True(t, failover.ShouldRetryNextAccount())
			require.Empty(t, rec.Body.String())
			require.Empty(t, rec.Header().Get("X-Request-Id"))
			require.False(t, c.Writer.Written())

			result, err = textBufferedAttemptGeili(s, c.Request.Context(), c, textBufferedResponseGeili("text/event-stream", textBufferedCompletedGeili("healthy-B", "complete B", true)), "gpt-6-astra")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 11, result.InputTokens)
			require.Equal(t, 5, result.OutputTokens)
			require.Equal(t, "completed", gjson.Get(rec.Body.String(), "status").String())
			require.Contains(t, rec.Body.String(), "complete B")
			require.NotContains(t, rec.Body.String(), "private-A")
			require.NotContains(t, rec.Body.String(), "partial-A")
		})
	}
}

func TestTextBufferedReadGeiliJSON200ErrorsKeepTheirSemanticStatus(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		retry      bool
		status     int
	}{
		{"instant quota", `{"id":"private-A","error":{"type":"upstream_error","message":"Insufficient quota available for instant inference."}}`, true, 429},
		{"upstream service unavailable", `{"id":"private-A","error":{"type":"upstream_error","message":"Upstream service temporarily unavailable","status":503}}`, true, 502},
		{"image hard limit", `{"id":"private-A","error":{"type":"invalid_request_error","message":"Exceeded maximum number of images (50) allowed in the request."}}`, false, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := guardContextGeili("/v1/responses")
			result, err := textBufferedAttemptGeili(textBufferedServiceGeili(), c.Request.Context(), c, textBufferedResponseGeili("application/json", tc.body), "gpt-6-astra")
			require.Nil(t, result)
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.Equal(t, tc.retry, errors.As(err, &failover))
			if tc.retry {
				require.True(t, failover.SafeToFailoverAfterWrite)
				require.Equal(t, tc.status, failover.StatusCode)
				require.False(t, c.Writer.Written())
				require.Empty(t, rec.Body.String())
			} else {
				require.Equal(t, tc.status, rec.Code)
				require.Contains(t, rec.Body.String(), "maximum number of images")
			}
			require.NotContains(t, rec.Body.String(), "private-A")
			require.Empty(t, rec.Header().Get("X-Request-Id"))
		})
	}
}

type textBufferedObservedBodyGeili struct {
	io.ReadCloser
	closed atomic.Bool
	err    chan error
}

func (b *textBufferedObservedBodyGeili) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && b.err != nil {
		select {
		case b.err <- err:
		default:
		}
	}
	return n, err
}
func (b *textBufferedObservedBodyGeili) Close() error {
	b.closed.Store(true)
	return b.ReadCloser.Close()
}

func textBufferedPipeGeili() (*http.Response, *io.PipeWriter, *textBufferedObservedBodyGeili) {
	pr, pw := io.Pipe()
	body := &textBufferedObservedBodyGeili{ReadCloser: pr}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body}, pw, body
}

func TestTextBufferedReadGeiliIdleBudgetsAndLongThinkingWait(t *testing.T) {
	for _, tc := range []struct {
		name, model   string
		idle          time.Duration
		completeAfter time.Duration
	}{
		{"ordinary one second", "gpt-6-luna", time.Second, 0},
		{"long thinking three seconds", "gpt-6-astra", 3 * time.Second, 0},
		{"long thinking exceeds ordinary wait", "gpt-6-astra", 3 * time.Second, 1400 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, rec := guardContextGeili("/v1/responses")
			resp, pw, body := textBufferedPipeGeili()
			defer func() { _ = pw.Close() }()
			if tc.completeAfter > 0 {
				go func() {
					time.Sleep(tc.completeAfter)
					_, _ = pw.Write([]byte(textBufferedCompletedGeili("slow-complete", "answer", true)))
				}()
			}
			started := time.Now()
			result, err := textBufferedAttemptGeili(textBufferedServiceGeili(), c.Request.Context(), c, resp, tc.model)
			elapsed := time.Since(started)
			require.True(t, body.closed.Load())
			if tc.completeAfter > 0 {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.GreaterOrEqual(t, elapsed, tc.completeAfter)
				require.Contains(t, rec.Body.String(), "answer")
			} else {
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.True(t, failover.SafeToFailoverAfterWrite)
				require.Equal(t, "upstream_stream_idle", gjson.GetBytes(failover.ResponseBody, "error.code").String())
				require.Nil(t, result)
				require.Empty(t, rec.Body.String())
				require.GreaterOrEqual(t, elapsed, tc.idle)
			}
			require.Less(t, elapsed, tc.idle+4*time.Second)
		})
	}
}

func TestTextBufferedReadGeiliTerminalStopsWithoutEOF(t *testing.T) {
	c, rec := guardContextGeili("/v1/responses")
	resp, pw, body := textBufferedPipeGeili()
	defer func() { _ = pw.Close() }()
	written := make(chan error, 1)
	go func() {
		_, err := pw.Write([]byte(textBufferedCompletedGeili("complete-before-EOF", "answer", true)))
		written <- err
	}()
	started := time.Now()
	result, err := textBufferedAttemptGeili(textBufferedServiceGeili(), c.Request.Context(), c, resp, "gpt-6-astra")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Less(t, time.Since(started), time.Second)
	require.NoError(t, <-written)
	require.True(t, body.closed.Load())
	require.Equal(t, 11, result.InputTokens)
	require.Contains(t, rec.Body.String(), "complete-before-EOF")
}

func TestTextBufferedReadGeiliIncompleteIsFinalWithoutEOFOrReplay(t *testing.T) {
	c, rec := guardContextGeili("/v1/responses")
	resp, pw, body := textBufferedPipeGeili()
	defer func() { _ = pw.Close() }()
	payload := strings.ReplaceAll(textBufferedCompletedGeili("incomplete-limit", "partial by limit", true), "response.completed", "response.incomplete")
	payload = strings.ReplaceAll(payload, `"status":"completed"`, `"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}`)
	go func() { _, _ = pw.Write([]byte(payload)) }()
	started := time.Now()
	result, err := textBufferedAttemptGeili(textBufferedServiceGeili(), c.Request.Context(), c, resp, "gpt-6-astra")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Less(t, time.Since(started), time.Second)
	require.True(t, body.closed.Load())
	require.Equal(t, 11, result.InputTokens)
	require.Equal(t, 5, result.OutputTokens)
	require.Equal(t, "incomplete", gjson.Get(rec.Body.String(), "status").String())
	require.Equal(t, "max_output_tokens", gjson.Get(rec.Body.String(), "incomplete_details.reason").String())
	require.Contains(t, rec.Body.String(), "partial by limit")
	require.NotContains(t, rec.Body.String(), `"status":"completed"`)
}

func TestTextBufferedReadGeiliEmptyCompletedNeedsNoUsage(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			c, rec := guardContextGeili("/v1/responses")
			payload := textBufferedCompletedGeili("valid-empty", "", false)
			if contentType == "application/json" {
				payload = gjson.Get(string(geiliSSEPayload([]byte(payload))), "response").Raw
			}
			result, err := textBufferedAttemptGeili(textBufferedServiceGeili(), c.Request.Context(), c, textBufferedResponseGeili(contentType, payload), "gpt-6-astra")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Zero(t, result.InputTokens)
			require.Zero(t, result.OutputTokens)
			require.Equal(t, "completed", gjson.Get(rec.Body.String(), "status").String())
			require.True(t, gjson.Get(rec.Body.String(), "output").IsArray())
			require.Empty(t, gjson.Get(rec.Body.String(), "output").Array())
		})
	}
}

func TestTextBufferedReadGeiliShortRecoveryDeadlineClosesBlockedRead(t *testing.T) {
	c, rec := guardContextGeili("/v1/responses")
	root := WithRequestRecovery(context.Background(), 25*time.Millisecond)
	require.True(t, BeginRequestRecovery(root))
	attempt, cancel := RequestRecoveryContext(root)
	defer cancel()
	c.Request = c.Request.WithContext(attempt)
	resp, pw, body := textBufferedPipeGeili()
	defer func() { _ = pw.Close() }()
	started := time.Now()
	result, err := textBufferedAttemptGeili(textBufferedServiceGeili(), attempt, c, resp, "gpt-6-astra")
	require.Nil(t, result)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.NoError(t, root.Err())
	require.False(t, RequestRecoveryAllowed(root))
	require.True(t, body.closed.Load())
	require.Less(t, time.Since(started), time.Second)
	require.Empty(t, rec.Body.String())
}

func TestTextBufferedReadGeiliCancelledRequestDrainsUsageWithoutRecovery(t *testing.T) {
	c, rec := guardContextGeili("/v1/responses")
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	resp, pw, body := textBufferedPipeGeili()
	defer func() { _ = pw.Close() }()
	go func() {
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\"}\n\n"))
		cancel()
		time.Sleep(30 * time.Millisecond)
		_, _ = pw.Write([]byte(textBufferedCompletedGeili("drained", "answer", true)))
	}()
	payload, err := textBufferedServiceGeili().readTextBufferedBodyGeili(ctx, resp, c, "gpt-6-astra")
	require.NoError(t, err)
	require.Contains(t, string(payload), `"input_tokens":11`)
	require.Contains(t, string(payload), `"output_tokens":5`)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.False(t, BeginRequestRecovery(ctx))
	require.True(t, body.closed.Load())
	require.Empty(t, rec.Body.String())
}

func TestTextBufferedReadGeiliCancelledDrainIsBounded(t *testing.T) {
	t.Parallel()
	c, rec := guardContextGeili("/v1/responses")
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	resp, pw, body := textBufferedPipeGeili()
	defer func() { _ = pw.Close() }()
	s := textBufferedServiceGeili()
	s.cfg.Gateway.StreamDataIntervalTimeout = 0
	started := time.Now()
	payload, err := s.readTextBufferedBodyGeili(ctx, resp, c, "gpt-6-astra")
	require.Nil(t, payload)
	require.ErrorIs(t, err, context.Canceled)
	require.GreaterOrEqual(t, time.Since(started), 8*time.Second)
	require.Less(t, time.Since(started), 12*time.Second)
	require.True(t, body.closed.Load())
	require.False(t, BeginRequestRecovery(ctx))
	require.Empty(t, rec.Body.String())
}

func TestTextBufferedReadGeiliRealHTTP2ResetRecoversWithinOneClientCall(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(fmt.Sprintf("content_already_delivered_%t", delivered), func(t *testing.T) {
			testTextBufferedRealHTTP2ResetGeili(t, delivered)
		})
	}
}

func testTextBufferedRealHTTP2ResetGeili(t *testing.T, delivered bool) {
	var upstreamAttempts, downstreamCalls atomic.Int32
	abortA := make(chan struct{})
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAttempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Path == "/A" {
			w.Header().Set("X-Request-ID", "private-A")
			_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"private-A\"}}\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			select {
			case <-abortA:
			case <-r.Context().Done():
			}
			panic(http.ErrAbortHandler) // net/http sends a real HTTP/2 RST_STREAM.
		}
		_, _ = io.WriteString(w, textBufferedCompletedGeili("healthy-B", "complete B", true))
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	client := upstream.Client()
	client.Timeout = 5 * time.Second
	rawReadError := make(chan error, 1)
	firstFailure := make(chan error, 1)
	finalUsage := make(chan *openaiNonStreamingResult, 1)
	serverError := make(chan error, 1)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamCalls.Add(1)
		c, _ := gin.CreateTestContext(w)
		c.Request = r.WithContext(WithRequestRecovery(r.Context(), time.Minute))
		if delivered {
			c.Header("Content-Type", "text/event-stream")
			_, _ = c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"visible answer\"}\n\n")
			c.Writer.Flush()
		}
		s := textBufferedServiceGeili()
		for _, path := range []string{"/A", "/B"} {
			resp, err := client.Get(upstream.URL + path)
			if err != nil {
				serverError <- err
				return
			}
			if resp.ProtoMajor != 2 {
				serverError <- fmt.Errorf("expected HTTP/2, got %s", resp.Proto)
				_ = resp.Body.Close()
				return
			}
			if path == "/A" {
				resp.Body = &textBufferedObservedBodyGeili{ReadCloser: resp.Body, err: rawReadError}
				close(abortA)
			}
			result, err := textBufferedAttemptGeili(s, c.Request.Context(), c, resp, "gpt-6-astra")
			if path == "/A" {
				firstFailure <- err
				var failover *UpstreamFailoverError
				if errors.As(err, &failover) && failover.SafeToFailoverAfterWrite && BeginRequestRecovery(c.Request.Context()) {
					continue
				}
			}
			if err != nil {
				if delivered {
					GeiliWriteStreamFailure(c, err)
				} else {
					serverError <- err
				}
				return
			}
			finalUsage <- result
			return
		}
	}))
	defer gateway.Close()
	resp, err := gateway.Client().Post(gateway.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-6-astra","stream":false,"input":"hello"}`))
	require.NoError(t, err)
	payload, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	select {
	case err := <-serverError:
		require.NoError(t, err)
	default:
	}
	require.EqualValues(t, 1, downstreamCalls.Load())
	if delivered {
		require.EqualValues(t, 1, upstreamAttempts.Load())
	} else {
		require.EqualValues(t, 2, upstreamAttempts.Load())
	}
	select {
	case err := <-rawReadError:
		require.Contains(t, strings.ToLower(err.Error()), "stream error")
		require.Contains(t, err.Error(), "INTERNAL_ERROR")
		t.Logf("real upstream HTTP/2 read failure: %T: %v", err, err)
	default:
		t.Fatal("expected a real HTTP/2 reset read error")
	}
	firstErr := <-firstFailure
	if delivered {
		var failover *UpstreamFailoverError
		require.False(t, errors.As(firstErr, &failover), "an existing client answer makes the real reset final")
		code, _, classified := OpenAIUpstreamStreamReadErrorDetails(firstErr)
		require.True(t, classified)
		require.Equal(t, OpenAIUpstreamHTTP2StreamErrorCode, code)
		require.Contains(t, string(payload), "visible answer")
		require.Equal(t, 1, strings.Count(string(payload), `"type":"response.failed"`))
		require.NotContains(t, string(payload), "healthy-B")
		require.NotContains(t, string(payload), "private-A")
		return
	}
	var failover *UpstreamFailoverError
	require.ErrorAs(t, firstErr, &failover)
	require.True(t, failover.SafeToFailoverAfterWrite)
	require.Equal(t, OpenAIUpstreamHTTP2StreamErrorCode, gjson.GetBytes(failover.ResponseBody, "error.code").String())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "healthy-B", gjson.GetBytes(payload, "id").String())
	require.Equal(t, "completed", gjson.GetBytes(payload, "status").String())
	require.Contains(t, string(payload), "complete B")
	require.NotContains(t, string(payload), "private-A")
	require.Empty(t, resp.Header.Get("X-Request-ID"))
	result := <-finalUsage
	require.NotNil(t, result)
	require.Equal(t, 11, result.InputTokens)
	require.Equal(t, 5, result.OutputTokens)
}
