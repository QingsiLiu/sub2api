package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type nativeLookupRepository struct {
	service.AUAPIImageTaskRepository
	task service.AUAPIImageTaskRecord
}

func (r *nativeLookupRepository) Get(context.Context, string) (*service.AUAPIImageTaskRecord, error) {
	return &r.task, nil
}

func TestAUAPIMediaCompositeKeyLookupRetainsOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, route, id, kind, operation string
		keyID                            int64
		auth                             bool
		want                             int
	}{
		{"video owner", "/v1/videos/:request_id", "auvidtask_" + strings.Repeat("a", 32), "video", "status", 2, true, 200},
		{"video foreign key", "/v1/videos/:request_id", "auvidtask_" + strings.Repeat("a", 32), "video", "status", 3, true, 404},
		{"video owned content", "/v1/videos/:request_id/content", "auvidtask_" + strings.Repeat("a", 32), "video", "content", 2, true, 307},
		{"video anonymous", "/v1/videos/:request_id", "auvidtask_" + strings.Repeat("a", 32), "video", "status", 2, false, 401},
		{"image owner", "/v1/images/tasks/:task_id", "auimgtask_" + strings.Repeat("b", 32), "image", "status", 2, true, 200},
		{"image foreign key", "/v1/images/tasks/:task_id", "auimgtask_" + strings.Repeat("b", 32), "image", "status", 3, true, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &nativeLookupRepository{task: service.AUAPIImageTaskRecord{TaskID: tc.id, UserID: 1, APIKeyID: 2, Kind: tc.kind, Phase: "done", Status: service.ImageTaskStatusCompleted, ExpiresAt: time.Now().Add(time.Hour), ResultJSON: []byte(`{"data":[{"url":"https://storage.example.invalid/owned.mp4"}]}`)}}
			h := handler.NewAsyncImageHandlerWithAUAPI(nil, nil, service.NewAUAPIImageTaskService(repo, nil, nil, &config.Config{}))
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if !tc.auth {
					c.AbortWithStatus(http.StatusUnauthorized)
					return
				}
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: tc.keyID, UserID: 1, BillingSource: service.BillingSourceBalance, RoutingMode: "composite", GroupIDs: []int64{4, 34}})
				c.Next()
			})
			// A nil resolver/service proves polling does not choose any new route.
			r.Use(explicitKeyRouting(nil, nil, nil, nil), middleware.GroupModelAllowlist(), middleware.RequireGroupAssignment(nil, middleware.AnthropicErrorWriter))
			r.GET(tc.route, func(c *gin.Context) {
				if tc.kind == "image" {
					h.Get(c)
				} else {
					require.True(t, h.TryAUAPIVideo(c, tc.operation))
				}
			})
			param := ":request_id"
			if tc.kind == "image" {
				param = ":task_id"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", strings.Replace(tc.route, param, tc.id, 1), nil))
			require.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
}
func TestAUAPIMediaLookupBypassRejectsOtherRoutesAndIdentifiers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		route, id, method string
		want              bool
	}{
		{"/v1/videos/:request_id", "auvidtask_" + strings.Repeat("a", 32), "GET", true},
		{"/videos/generations/:request_id/content", "auvidtask_" + strings.Repeat("a", 32), "GET", true},
		{"/v1/videos/:request_id", "auvidtask_pending", "GET", false},
		{"/v1/videos/:request_id", "auvidtask_" + strings.Repeat("z", 32), "GET", false},
		{"/v1/videos/:request_id", "auimgtask_" + strings.Repeat("a", 32), "GET", false},
		{"/v1/videos/:request_id", "grok_" + strings.Repeat("a", 32), "GET", false},
		{"/v1/videos/:request_id", "auvidtask_" + strings.Repeat("a", 32), "DELETE", false},
		{"/v1/responses/:request_id", "auvidtask_" + strings.Repeat("a", 32), "GET", false},
		{"/v1/videos/edits/:request_id", "auvidtask_" + strings.Repeat("a", 32), "GET", false},
	} {
		t.Run(tc.route+tc.id+tc.method, func(t *testing.T) {
			r := gin.New()
			r.Handle(tc.method, tc.route, func(c *gin.Context) { require.Equal(t, tc.want, ownedAUAPIMediaLookup(c)); c.Status(204) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, strings.Replace(tc.route, ":request_id", tc.id, 1), nil))
			require.Equal(t, 204, w.Code)
		})
	}
}
