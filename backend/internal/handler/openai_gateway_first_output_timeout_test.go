package handler

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIForwardMayFailoverOnlyAfterNonSemanticWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	before := service.GeiliTextForwardWrittenSize(c)

	_, err := fmt.Fprint(c.Writer, ":\n\n")
	require.NoError(t, err)
	c.Writer.Flush()

	require.True(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{
		SafeToFailoverAfterWrite: true,
	}))
	require.False(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{}))
}

func TestOpenAIForwardMayFailoverRejectsExistingAnswerAndDiscountsHeartbeat(t *testing.T) {
	for _, safe := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		_, err := c.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"already delivered\"}}]}\n\n")
		require.NoError(t, err)
		before := service.GeiliTextForwardWrittenSize(c)
		require.Positive(t, before)
		require.False(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{SafeToFailoverAfterWrite: safe}), "existing answer must forbid replay even when this attempt wrote nothing")
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	heartbeat := ": keepalive\n\n"
	_, err := c.Writer.WriteString(heartbeat)
	require.NoError(t, err)
	// Match the existing streaming keepalive producer's recorded byte contract.
	c.Set("openai_stream_keepalive_bytes", len(heartbeat))
	before := service.GeiliTextForwardWrittenSize(c)
	require.Equal(t, -1, before)
	require.True(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{}), "only a known heartbeat remains replayable")
	// Raw pre-attempt heartbeat bytes used to collide with an equal-length
	// answer after subtraction, incorrectly satisfying the equality shortcut.
	_, err = c.Writer.WriteString(strings.Repeat("x", len(heartbeat)))
	require.NoError(t, err)
	require.False(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{}))
}

func TestOpenAIFirstOutputFailoverStopsAfterOneAccountSwitch(t *testing.T) {
	failoverErr := &service.UpstreamFailoverError{SafeToFailoverAfterWrite: true, ResponseBody: []byte(`{"error":{"type":"first_output_timeout"}}`)}
	count := 0

	require.False(t, openAIFirstOutputFailoverExhausted(failoverErr, &count))
	require.Equal(t, 1, count)
	require.True(t, openAIFirstOutputFailoverExhausted(failoverErr, &count))
	require.Equal(t, 1, count)
}

func TestOpenAIRequestAllowsFailoverReplayStopsCanceledClient(t *testing.T) {
	require.False(t, openAIRequestAllowsFailoverReplay(nil))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	requestCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(requestCtx)

	require.True(t, openAIRequestAllowsFailoverReplay(c))
	cancel()
	require.False(t, openAIRequestAllowsFailoverReplay(c))
}
