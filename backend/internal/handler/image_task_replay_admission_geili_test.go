package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type auapiReplayAccountRepo struct{ service.AccountRepository }

func (r auapiReplayAccountRepo) ListByGroup(context.Context, int64) ([]service.Account, error) {
	return []service.Account{{ID: 3, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"image_provider": "auapi"}}}, nil
}

type auapiReplayTaskRepo struct {
	service.AUAPIImageTaskRepository
	task service.AUAPIImageTaskRecord
}

func (r *auapiReplayTaskRepo) GetByIdempotency(context.Context, int64, int64, string) (*service.AUAPIImageTaskRecord, error) {
	copy := r.task
	return &copy, nil
}

func TestAUAPIRepeatedAcceptedRequestsCancelOnlyNewReplayAdmissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The provider payload hash is distinct from the raw public request body.
	canonical := []byte(`{"model":"gpt-image-2","input":{"prompt":"cat"},"parameters":{"n":1,"resolution":"1k"}}`)
	hash := service.HashUsageRequestPayload(canonical)
	repository := &auapiReplayTaskRepo{task: service.AUAPIImageTaskRecord{TaskID: "auimgtask_original", UserID: 7, APIKeyID: 9, RequestHash: hash, AdmissionKey: "original-task-admission", Status: service.ImageTaskStatusProcessing, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}}
	cfg := &config.Config{}
	cfg.AUAPIImage.Enabled = true
	auapi := service.NewAUAPIImageTaskService(repository, auapiReplayAccountRepo{}, func() (*service.ImageResultUploader, bool) { return nil, true }, cfg)
	h := NewAsyncImageHandlerWithAUAPI(nil, nil, auapi)
	var mu sync.Mutex
	cancelled := map[string]int{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(3)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 9, UserID: 7, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, AllowImageGeneration: true}})
		admission := &service.UserSubscription{ID: 11, UserID: 7, AdmissionKey: c.GetHeader("X-Fixture-Admission")}
		c.Set(string(middleware2.ContextKeySubscription), admission)
		ctx := service.WithSubscriptionAdmissionCancellation(c.Request.Context(), func(_ context.Context, sub *service.UserSubscription) error {
			mu.Lock()
			defer mu.Unlock()
			cancelled[sub.AdmissionKey]++
			return nil
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.POST("/v1/images/generations/async", h.Submit)
	const concurrent = 16
	var wg sync.WaitGroup
	codes := make(chan int, concurrent)
	bodies := make(chan string, concurrent)
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations/async", strings.NewReader(`{"model":"gpt-image-2","prompt":"cat"}`))
			req.Header.Set("Idempotency-Key", "same-key")
			req.Header.Set("X-Fixture-Admission", fmt.Sprintf("replay-admission-%d", i))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			codes <- w.Code
			bodies <- w.Body.String()
		}(i)
	}
	wg.Wait()
	close(codes)
	close(bodies)
	for status := range codes {
		require.Equal(t, http.StatusAccepted, status)
	}
	for body := range bodies {
		var response map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &response))
		require.Equal(t, "auimgtask_original", response["task_id"])
	}
	require.Len(t, cancelled, concurrent)
	require.Zero(t, cancelled["original-task-admission"])
	for i := 0; i < concurrent; i++ {
		require.Equal(t, 1, cancelled[fmt.Sprintf("replay-admission-%d", i)])
	}
	require.Equal(t, "original-task-admission", repository.task.AdmissionKey)
}
