package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsCompositeRoutingRejectionKeepsClientIntent(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)
	gin.SetMode(gin.TestMode)
	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	key := &service.APIKey{ID: 7, User: &service.User{ID: 3}, RoutingMode: "composite", GroupIDs: []int64{22, 11}}
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		service.SetOpsIngressRequestContext(c, "gpt-unavailable", true)
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
		c.AbortWithStatusJSON(400, gin.H{"error": gin.H{"code": "MODEL_NOT_AVAILABLE", "type": "invalid_request_error", "message": "the selected groups do not declare support for this model and endpoint"}})
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, 400, recorder.Code)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, "gpt-unavailable", job.entry.Model)
	require.Equal(t, "gpt-unavailable", job.entry.RequestedModel)
	require.True(t, job.entry.Stream)
	require.NotNil(t, job.entry.RequestType)
	require.Equal(t, int16(service.RequestTypeStream), *job.entry.RequestType)
	require.Nil(t, job.entry.GroupID)
	require.Nil(t, job.entry.AccountID)
	require.Empty(t, job.entry.UpstreamModel)
	require.Empty(t, job.entry.UpstreamEndpoint)
	require.Equal(t, "routing", job.entry.ErrorPhase)
	require.Equal(t, []int64{22, 11}, job.entry.RequestedGroupIDs)
	key.GroupIDs[0] = 99
	require.Equal(t, []int64{22, 11}, job.entry.RequestedGroupIDs, "queue must own its snapshot")
}
