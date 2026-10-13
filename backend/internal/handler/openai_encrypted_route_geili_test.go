package handler

import (
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"net/http/httptest"
	"testing"
)

func TestEncryptedRouteGeiliHandlerPreservesWrappedConflictAndCommittedStream(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	err := fmt.Errorf("selection: %w", &service.OpenAIEncryptedRouteErrorGeili{Status: 409, Code: "encrypted_session_route_conflict", Message: "original route permission changed"})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h.WriteOpenAIEncryptedRouteErrorGeili(c, err)
	require.Equal(t, 409, recorder.Code)
	require.Equal(t, "encrypted_session_route_conflict", gjson.Get(recorder.Body.String(), "error.code").String())
	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Header("Content-Type", "text/event-stream")
	_, writeErr := c.Writer.WriteString(": keepalive\n\n")
	require.NoError(t, writeErr)
	c.Writer.Flush()
	h.WriteOpenAIEncryptedRouteErrorGeili(c, err)
	require.Equal(t, 200, recorder.Code)
	require.Contains(t, recorder.Body.String(), "event: response.failed")
	require.Contains(t, recorder.Body.String(), `"status":"failed"`)
	require.NotContains(t, recorder.Body.String(), "response.completed")
	streamErr, ok := service.GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "encrypted_session_route_conflict", streamErr.Code)
	require.Equal(t, 409, streamErr.IntendedStatus)
	require.False(t, streamErr.CountTowardsSLA, "permission conflict retains logical409 business rejection")
}

func TestEncryptedRouteGeiliWSAdmissionFailureDoesNotPenalizeUpstreamAccount(t *testing.T) {
	for _, routeErr := range []error{service.ErrOpenAIEncryptedRouteUnavailableGeili, &service.OpenAIEncryptedRouteErrorGeili{Status: 409, Code: "encrypted_session_route_conflict", Message: "group revoked"}} {
		err := service.NewOpenAIWSClientCloseError(1013, "reconnect", routeErr)
		require.False(t, shouldReportOpenAIWSProxyAccountFailure(err))
	}
}
