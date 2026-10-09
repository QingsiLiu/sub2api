package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestRecoveryGeiliSelectionNeedsKnownCooldown(t *testing.T) {
	fs := NewFailoverState(10, false)
	fs.LastFailoverErr = newTestFailoverErr(503, false, false)
	fs.FailedAccountIDs[7] = struct{}{}
	start := time.Now()
	require.Equal(t, FailoverExhausted, fs.HandleSelectionExhausted(context.Background()))
	require.Contains(t, fs.FailedAccountIDs, int64(7))
	require.Less(t, time.Since(start), 100*time.Millisecond)
	for _, raw := range []string{"", "-1", "garbage"} {
		fs.LastFailoverErr.ResponseHeaders = http.Header{"Retry-After": []string{raw}}
		require.Equal(t, FailoverExhausted, fs.HandleSelectionExhausted(context.Background()))
	}
}

func TestRequestRecoveryGeiliHandlerDeadlineProducesProtocolFailure(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions"} {
		for _, stream := range []bool{false, true} {
			name := path + "/json"
			if stream {
				name = path + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				ctx := service.WithRequestRecovery(context.Background(), 10*time.Millisecond)
				c.Request = httptest.NewRequest(http.MethodPost, path, nil).WithContext(ctx)
				require.True(t, service.BeginRequestRecovery(ctx))
				attempt, cancel := service.RequestRecoveryContext(ctx)
				defer cancel()
				<-attempt.Done()
				if stream {
					c.Header("Content-Type", "text/event-stream")
					_, err := c.Writer.WriteString(": keepalive\n\n")
					require.NoError(t, err)
					c.Writer.Flush()
				}
				require.True(t, failoverClientGone(c))
				require.NoError(t, ctx.Err(), "the Python request remains connected")
				require.Contains(t, recorder.Body.String(), "upstream_recovery_timeout")
				require.NotEqual(t, statusClientClosedRequest, recorder.Code)
				if stream {
					require.Equal(t, http.StatusOK, recorder.Code)
					if path == "/v1/responses" {
						require.Contains(t, recorder.Body.String(), "event: response.failed")
						require.NotContains(t, recorder.Body.String(), "event: response.completed")
					} else {
						require.Contains(t, recorder.Body.String(), "event: error")
					}
				} else {
					require.Equal(t, http.StatusGatewayTimeout, recorder.Code)
				}
				firstBody := recorder.Body.String()
				require.True(t, failoverClientGone(c))
				require.Equal(t, firstBody, recorder.Body.String(), "emit a terminal failure once")
			})
		}
	}
}

func TestRequestRecoveryGeiliExplicitTotalDeadlineDoesNotBecome499(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
	require.True(t, failoverClientGone(c))
	require.Equal(t, http.StatusGatewayTimeout, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"type":"api_error"`)
}

func TestRequestRecoveryGeiliGenericFailuresDoNotConsumeFirstOutputCap(t *testing.T) {
	err := &service.UpstreamFailoverError{SafeToFailoverAfterWrite: true, ResponseBody: []byte(`{"error":{"type":"upstream_error","code":"upstream_stream_incomplete"}}`)}
	count := 0
	for range 10 {
		require.False(t, openAIFirstOutputFailoverExhausted(err, &count))
	}
	require.Zero(t, count)
}

func TestRequestRecoveryGeiliFinalFailureOriginAndHeartbeatTerminal(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Header("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString(": keepalive\n\n")
	require.NoError(t, err)
	(&GatewayHandler{}).handleCCFailoverExhausted(c, &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests, SafeToFailoverAfterWrite: true}, false)
	require.True(t, c.GetBool("geili_upstream_failure_final"))
	require.Contains(t, recorder.Body.String(), "upstream_stream_incomplete")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "upstream_stream_incomplete"))
	firstBody := recorder.Body.String()
	(&GatewayHandler{}).handleCCFailoverExhausted(c, &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests}, true)
	require.Equal(t, firstBody, recorder.Body.String())

	localRecorder := httptest.NewRecorder()
	local, _ := gin.CreateTestContext(localRecorder)
	(&OpenAIGatewayHandler{}).handleStreamingAwareErrorWithCode(local, http.StatusTooManyRequests, "rate_limit_error", "gateway_concurrency_limit", "Too many concurrent requests", false, false)
	require.False(t, local.GetBool("geili_upstream_failure_final"))
}

func TestRequestRecoveryGeiliSelectionWaitSharedAcrossGroups(t *testing.T) {
	ctx := service.WithRequestRecovery(context.Background(), time.Second)
	for range 3 {
		fs := NewFailoverState(10, false) // New selected group must not reset waits.
		fs.LastFailoverErr = newTestFailoverErr(503, false, false)
		fs.LastFailoverErr.SelectionRetryAfter = time.Now().Add(5 * time.Millisecond)
		fs.FailedAccountIDs[7] = struct{}{}
		require.Equal(t, FailoverContinue, fs.HandleSelectionExhausted(ctx))
		require.Empty(t, fs.FailedAccountIDs)
	}
	fs := NewFailoverState(10, false)
	fs.LastFailoverErr = newTestFailoverErr(503, false, false)
	fs.LastFailoverErr.SelectionRetryAfter = time.Now().Add(5 * time.Millisecond)
	require.Equal(t, FailoverExhausted, fs.HandleSelectionExhausted(ctx))
}

func TestRequestRecoveryGeiliSelectionCooldownExceedsBudget(t *testing.T) {
	ctx := service.WithRequestRecovery(context.Background(), 20*time.Millisecond)
	fs := NewFailoverState(10, false)
	fs.LastFailoverErr = newTestFailoverErr(503, false, false)
	fs.LastFailoverErr.ResponseHeaders = http.Header{"Retry-After": []string{"120"}}
	fs.FailedAccountIDs[7] = struct{}{}
	start := time.Now()
	require.Equal(t, FailoverExhausted, fs.HandleSelectionExhausted(ctx))
	require.Less(t, time.Since(start), 100*time.Millisecond)
	require.Contains(t, fs.FailedAccountIDs, int64(7))
}

func TestRequestRecoveryGeiliSelectionClientCancelsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(service.WithRequestRecovery(context.Background(), time.Second))
	defer cancel()
	fs := NewFailoverState(10, false)
	fs.LastFailoverErr = newTestFailoverErr(503, false, false)
	fs.LastFailoverErr.SelectionRetryAfter = time.Now().Add(500 * time.Millisecond)
	timer := time.AfterFunc(10*time.Millisecond, cancel)
	defer timer.Stop()
	require.Equal(t, FailoverCanceled, fs.HandleSelectionExhausted(ctx))
}

func TestRequestRecoveryGeiliFailoverStopsBeforeOverlongBackoff(t *testing.T) {
	ctx := service.WithRequestRecovery(context.Background(), 20*time.Millisecond)
	fs := NewFailoverState(10, false)
	err := newTestFailoverErr(503, false, true)
	err.RetryableOnSameAccount = true
	err.SameAccountRetryDelay = time.Second
	require.Equal(t, FailoverExhausted, fs.HandleFailoverError(ctx, &mockTempUnscheduler{}, 7, service.PlatformOpenAI, 3, err))
	require.NoError(t, ctx.Err(), "recovery exhaustion is not a client disconnect")
}

func TestRequestRecoveryGeiliRetryAfterDate(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	err := &service.UpstreamFailoverError{ResponseHeaders: http.Header{"Retry-After": []string{now.Add(time.Minute).UTC().Format(http.TimeFormat)}}}
	deadline, known := selectionRetryAfter(err, now)
	require.True(t, known)
	require.True(t, now.Add(time.Minute).Equal(deadline))
}
