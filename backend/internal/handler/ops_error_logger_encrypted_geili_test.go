package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEncryptedOpsScopedFailureGeiliPersistsRealSLAClassification(t *testing.T) {
	for _, tc := range []struct {
		name, code      string
		status          int
		count, business bool
	}{
		{"source_unavailable", "encrypted_session_route_unavailable", 503, true, false},
		{"invalid_cipher", "invalid_encrypted_content", 400, true, false},
		{"permission_conflict", "encrypted_session_route_conflict", 409, false, true},
		{"conflict_count_flag_does_not_override", "encrypted_session_route_conflict", 409, true, true},
		{"unflagged_source", "encrypted_session_route_unavailable", 503, false, true},
		{"unrelated_request_scoped", "PROHIBITED_CONTENT", 400, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 4)
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			setOpsRequestContext(c, "gpt-6-astra", true)
			c.Set(service.OpsUpstreamStatusCodeKey, 429)
			c.Set(service.OpsUpstreamErrorMessageKey, "earlier supplier attempt")
			service.MarkOpsStreamErrorValue(c, service.OpsStreamError{ErrType: "api_error", Code: tc.code, Message: "continuity failed", IntendedStatus: tc.status, CountTowardsSLA: tc.count, RequestScoped: true})
			repo := &ingressRejectOpsRepo{}
			ops := service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			logOpsStreamError(c, ops, http.StatusOK)
			require.Equal(t, int64(1), OpsErrorLogQueueLength())
			job := <-opsErrorLogQueue
			flushOpsErrorLogBatch([]opsErrorLogJob{job})
			require.Len(t, repo.entries, 1)
			persisted := repo.entries[0]
			require.Equal(t, tc.status, persisted.StatusCode)
			require.Equal(t, tc.business, persisted.IsBusinessLimited)
			require.Nil(t, persisted.UpstreamStatusCode, "do not attribute residual supplier fault")
			require.Nil(t, persisted.UpstreamErrorMessage)
			require.Nil(t, persisted.UpstreamErrorsJSON)
		})
	}
}

func TestEncryptedOpsHTTPConflictGeiliIsBusinessAdmission(t *testing.T) {
	for _, tc := range []struct {
		code     string
		status   int
		business bool
	}{
		{"encrypted_session_route_conflict", 409, true},
		{"unrelated_conflict", 409, false},
		{"encrypted_session_route_conflict", 503, false},
	} {
		t.Run(tc.code+http.StatusText(tc.status), func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 4)
			gin.SetMode(gin.TestMode)
			repo := &ingressRejectOpsRepo{}
			ops := service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST("/v1/responses", func(c *gin.Context) {
				c.JSON(tc.status, gin.H{"error": gin.H{"type": "api_error", "code": tc.code, "message": "local route conflict"}})
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			require.Equal(t, tc.status, recorder.Code)
			require.Equal(t, int64(1), OpsErrorLogQueueLength())
			job := <-opsErrorLogQueue
			flushOpsErrorLogBatch([]opsErrorLogJob{job})
			require.Len(t, repo.entries, 1)
			require.Equal(t, tc.status, repo.entries[0].StatusCode)
			require.Equal(t, tc.business, repo.entries[0].IsBusinessLimited)
		})
	}
}
