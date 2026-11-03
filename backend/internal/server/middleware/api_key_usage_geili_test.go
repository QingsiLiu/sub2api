//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayUsageGeiliBillingExemptionRequiresExactGETRoute(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/v1/usage", 200},
		{http.MethodGet, "/antigravity/v1/usage", 200},
		{http.MethodGet, "/v1/usage?model=ignored", 200},
		{http.MethodPost, "/v1/usage", 403},
		{http.MethodPost, "/antigravity/v1/usage", 403},
		{http.MethodGet, "/v1/usage/extra", 403},
		{http.MethodGet, "/other/v1/usage", 403},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			user := &service.User{ID: 7, Status: service.StatusActive}
			key := &service.APIKey{ID: 101, UserID: 7, User: user, Status: service.StatusAPIKeyExpired, BillingSource: service.BillingSourceBalance, RoutingMode: service.KeyRoutingComposite, GroupIDs: []int64{1, 2}}
			repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { copy := *key; return &copy, nil }}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			keys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			router := gin.New()
			router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(keys, nil, cfg)))
			req := httptest.NewRequest(tc.method, tc.path, nil)
			router.Handle(tc.method, req.URL.Path, func(c *gin.Context) { c.Status(200) })
			req.Header.Set("Authorization", "Bearer usage-exact-test")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
}
