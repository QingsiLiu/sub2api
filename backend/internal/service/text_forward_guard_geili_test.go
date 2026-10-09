package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func guardContextGeili(path string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request = c.Request.WithContext(WithRequestRecovery(c.Request.Context(), time.Minute))
	return c, recorder
}

func TestTextForwardGuardGeili_DiscardsAttemptIdentityButKeepsTransportAlive(t *testing.T) {
	c, rec := guardContextGeili("/v1/responses")
	finish := BeginTextForwardGuard(c, true)
	c.Header("Content-Type", "text/event-stream")
	c.Header("X-Request-ID", "failed-A")
	_, err := c.Writer.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"failed-A\"}}\n\n: ping\n\n")
	require.NoError(t, err)
	c.Writer.Flush()
	require.False(t, c.Writer.Written())
	require.Equal(t, ": keepalive\n\n", rec.Body.String())
	require.Empty(t, rec.Header().Get("X-Request-ID"))
	err = finish(errors.New("upstream stream ended without terminal event"))
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.SafeToFailoverAfterWrite)
	require.NotContains(t, rec.Body.String(), "failed-A")
	finish = BeginTextForwardGuard(c, true)
	c.Header("Content-Type", "text/event-stream")
	_, err = c.Writer.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"healthy-B\"}}\n\n")
	require.NoError(t, err)
	require.False(t, c.Writer.Written())
	require.NotContains(t, rec.Body.String(), "healthy-B")
	_, err = c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n")
	require.NoError(t, err)
	require.NoError(t, finish(nil))
	require.Contains(t, rec.Body.String(), "healthy-B")
	require.Contains(t, rec.Body.String(), "answer")
	require.NotContains(t, rec.Body.String(), "failed-A")
}

func TestTextForwardGuardGeili_PartialDeliveryCannotReplayAndReportsFailureOnce(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			c, rec := guardContextGeili(path)
			finish := BeginTextForwardGuard(c, true)
			c.Header("Content-Type", "text/event-stream")
			_, err := c.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"visible\"}}]}\n\n")
			require.NoError(t, err)
			err = finish(errors.New("upstream stream ended without terminal event"))
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.Contains(t, rec.Body.String(), "visible")
			require.Equal(t, 1, strings.Count(rec.Body.String(), "Upstream response did not complete"))
			require.True(t, IsResponseCommitted(c))
		})
	}
}

func TestTextForwardGuardGeili_PreservesHardErrorAndDoesNotReplay(t *testing.T) {
	c, rec := guardContextGeili("/v1/messages")
	finish := BeginTextForwardGuard(c, false)
	err := GeiliUpstreamErrorFailure(c, []byte(`{"error":{"type":"invalid_request_error","message":"Exceeded maximum number of images (50) allowed in the request."}}`), 200, nil)
	err = finish(err)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "maximum number of images (50)")
	require.True(t, IsResponseCommitted(c))
}

func TestTextForwardGuardGeili_DoesNotCommitLocalOrCanceledFailures(t *testing.T) {
	c, rec := guardContextGeili("/v1/chat/completions")
	finish := BeginTextForwardGuard(c, false)
	require.Error(t, finish(errors.New("local request build failed")))
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	cancel()
	finish = BeginTextForwardGuard(c, true)
	err := finish(errors.New("upstream stream read error: connection reset"))
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Empty(t, rec.Body.String())
}

func TestTextForwardGuardGeili_FragmentedFailureDoesNotGetSecondTerminal(t *testing.T) {
	c, rec := guardContextGeili("/v1/responses")
	finish := BeginTextForwardGuard(c, true)
	c.Header("Content-Type", "text/event-stream")
	_, _ = c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\"}\n\n")
	_, _ = c.Writer.WriteString("data: {\"type\":\"response.")
	_, _ = c.Writer.WriteString("failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"supplier error\"}}}\n\n")
	require.Error(t, finish(errors.New("upstream response failed")))
	require.Equal(t, 1, strings.Count(rec.Body.String(), "response.failed"))
	require.NotContains(t, rec.Body.String(), "Upstream response did not complete")
}

func TestTextForwardGuardGeili_EmptyReasoningItemDoesNotBlockRecovery(t *testing.T) {
	c, rec := guardContextGeili("/v1/responses")
	finish := BeginTextForwardGuard(c, true)
	c.Header("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString("data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"failed-A\",\"type\":\"reasoning\",\"summary\":[]}}\n\n")
	require.NoError(t, err)
	err = finish(errors.New("upstream stream ended without terminal event"))
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.SafeToFailoverAfterWrite)
	require.Empty(t, rec.Body.String())
}

func TestTextForwardGuardGeili_BuiltinToolExecutionCannotReplay(t *testing.T) {
	c, rec := guardContextGeili("/v1/responses")
	finish := BeginTextForwardGuard(c, true)
	c.Header("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString("data: {\"type\":\"response.web_search_call.in_progress\",\"item_id\":\"search-A\"}\n\n")
	require.NoError(t, err)
	err = finish(errors.New("upstream stream ended without terminal event"))
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.Contains(t, rec.Body.String(), "search-A")
	require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.failed"`))
}

func TestTextForwardGuardGeili_CanonicalEmptyCompletedNeedsNoUsage(t *testing.T) {
	canonical := []byte(`{"type":"response.completed","response":{"id":"resp_empty","status":"completed","output":[]}}`)
	require.False(t, openAIResponsesCompletedEventIsEmpty(canonical, nil))
	incomplete := []byte(`{"type":"response.completed","response":{"id":"resp_empty","status":"completed"}}`)
	require.True(t, openAIResponsesCompletedEventIsEmpty(incomplete, nil))
}

func TestTextForwardGuardGeili_MultipleHeartbeatOnlyFailuresKeepLaterPreludePrivate(t *testing.T) {
	c, rec := guardContextGeili("/v1/responses")
	for _, id := range []string{"failed-A", "failed-B"} {
		finish := BeginTextForwardGuard(c, true)
		c.Header("Content-Type", "text/event-stream")
		_, err := c.Writer.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"" + id + "\"}}\n\n: ping\n\n")
		require.NoError(t, err)
		require.False(t, c.Writer.Written())
		err = finish(errors.New("upstream stream ended without terminal event"))
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.True(t, failover.SafeToFailoverAfterWrite)
		require.Equal(t, -1, GeiliTextForwardWrittenSize(c))
		require.NotContains(t, rec.Body.String(), id)
	}
	finish := BeginTextForwardGuard(c, true)
	c.Header("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"healthy-C\"}}\n\n")
	require.NoError(t, err)
	require.NotContains(t, rec.Body.String(), "healthy-C")
	_, err = c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"complete answer\"}\n\n")
	require.NoError(t, err)
	require.NoError(t, finish(nil))
	require.Positive(t, GeiliTextForwardWrittenSize(c))
	require.Contains(t, rec.Body.String(), "healthy-C")
}

func TestTextForwardGuardGeili_DoesNotHideExistingContent(t *testing.T) {
	c, _ := guardContextGeili("/v1/responses")
	_, err := c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"visible\"}\n\n")
	require.NoError(t, err)
	parent := c.Writer
	finish := BeginTextForwardGuard(c, true)
	require.Same(t, parent, c.Writer)
	failover := &UpstreamFailoverError{StatusCode: 503}
	require.Same(t, failover, finish(failover))
	require.False(t, failover.SafeToFailoverAfterWrite)
}
