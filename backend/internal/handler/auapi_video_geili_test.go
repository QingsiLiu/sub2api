package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type auapiVideoLookupRepo struct {
	service.AUAPIImageTaskRepository
	record service.AUAPIImageTaskRecord
}

func (r *auapiVideoLookupRepo) Get(context.Context, string) (*service.AUAPIImageTaskRecord, error) {
	return &r.record, nil
}

type auapiNoVideoAccountRepo struct{ service.AccountRepository }

func (auapiNoVideoAccountRepo) ListByGroup(context.Context, int64) ([]service.Account, error) {
	return nil, nil
}
func TestAUAPIVideoLookupOwnershipAndContentGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, op, status string
		owner            int64
		want             int
	}{
		{"owned status", "status", service.ImageTaskStatusProcessing, 2, 200},
		{"foreign key", "status", service.ImageTaskStatusCompleted, 3, 404},
		{"unfinished content", "content", service.ImageTaskStatusProcessing, 2, 409},
		{"owned content", "content", service.ImageTaskStatusCompleted, 2, 307},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &auapiVideoLookupRepo{record: service.AUAPIImageTaskRecord{TaskID: "auvidtask_test", UserID: 1, APIKeyID: 2, Kind: "video", Phase: "poll", Status: tc.status, ExpiresAt: time.Now().Add(time.Hour), ResultJSON: []byte(`{"data":[{"url":"https://storage.example.invalid/owned.mp4"}]}`)}}
			h := NewAsyncImageHandlerWithAUAPI(nil, nil, service.NewAUAPIImageTaskService(repo, nil, nil, &config.Config{}))
			router := gin.New()
			router.GET("/v1/videos/:request_id", func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: tc.owner, UserID: 1})
				require.True(t, h.TryAUAPIVideo(c, tc.op))
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", "/v1/videos/auvidtask_test", nil))
			require.Equal(t, tc.want, w.Code)
			if tc.want == 307 {
				require.Equal(t, "https://storage.example.invalid/owned.mp4", w.Header().Get("Location"))
			}
		})
	}
}
func TestAUAPIVideoFallbackPreservesOriginalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAsyncImageHandlerWithAUAPI(nil, nil, service.NewAUAPIImageTaskService(&auapiVideoLookupRepo{}, auapiNoVideoAccountRepo{}, nil, &config.Config{}))
	const body = `{"model":"grok-imagine-video","prompt":"a boat"}`
	group := int64(1)
	router := gin.New()
	router.POST("/v1/videos", func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
		require.False(t, h.TryAUAPIVideo(c, "create"))
		rest, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(rest))
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/v1/videos", strings.NewReader(body)))
	require.Equal(t, 204, w.Code)
}
