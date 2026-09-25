package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubscriptionVideoLookupUsesExactProviderIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ method, path, url, want string }{{"GET", "/v1/videos/:request_id", "/v1/videos/xai-task", "xai-task"}, {"GET", "/v1/videos/:request_id/content", "/v1/videos/xai-task/content", "xai-task"}, {"GET", "/api/v3/contents/generations/tasks/:task_id", "/api/v3/contents/generations/tasks/ark-task", "seedance:ark-task"}, {"DELETE", "/v3/contents/generations/tasks/:task_id", "/v3/contents/generations/tasks/ark-task", "seedance:ark-task"}, {"POST", "/api/v3/contents/generations/tasks/:task_id", "/api/v3/contents/generations/tasks/ark-task", ""}, {"GET", "/other/:task_id", "/other/ark-task", ""}, {"DELETE", "/v1/videos/:request_id", "/v1/videos/xai-task", ""}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := gin.New()
			r.Handle(tc.method, tc.path, func(c *gin.Context) {
				require.Equal(t, tc.want, subscriptionMediaLookupTaskID(c))
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.url, nil))
			require.Equal(t, http.StatusNoContent, w.Code)
		})
	}
}

func TestSubscriptionVideoLookupAllRegisteredAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, prefix := range []string{"", "/v1"} {
		for _, kind := range []string{"", "/generations", "/edits", "/extensions"} {
			for _, suffix := range []string{"", "/content"} {
				path := prefix + "/videos" + kind + "/:request_id" + suffix
				url := prefix + "/videos" + kind + "/owned-task" + suffix
				t.Run(path, func(t *testing.T) {
					r := gin.New()
					r.GET(path, func(c *gin.Context) { require.Equal(t, "owned-task", subscriptionMediaLookupTaskID(c)); c.Status(204) })
					w := httptest.NewRecorder()
					r.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
					require.Equal(t, 204, w.Code)
				})
			}
		}
	}
}
