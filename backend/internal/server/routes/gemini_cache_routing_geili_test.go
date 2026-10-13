package routes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cacheRouteAccountsGeili struct{ service.AccountRepository }

func (cacheRouteAccountsGeili) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return []service.Account{{Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}}}}, nil
}

type cacheRouteGroupsGeili struct{ service.GroupRepository }

func (cacheRouteGroupsGeili) GetByIDLite(context.Context, int64) (*service.Group, error) {
	inputPrice, outputPrice := 1e-6, 2e-6
	return &service.Group{ID: 22, Status: service.StatusActive, Platform: service.PlatformGemini, SubscriptionEnabled: true,
		ModelPricing: []service.ChannelModelPricing{{Models: []string{"gemini-2.5-flash"}, BillingMode: service.BillingModeToken,
			InputPrice: &inputPrice, OutputPrice: &outputPrice}}}, nil
}

func TestGeminiCacheCreateExplicitRoutingGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, source := range []string{service.BillingSourceBalance, service.BillingSourceSubscription} {
		for _, tc := range []struct {
			name, path, body string
			status           int
		}{
			{"standard resource model", "/v1beta/cachedContents", `{"model":"models/gemini-2.5-flash","nonce":9007199254740993}`, 200},
			{"bare model", "/v1beta/cachedContents", `{"model":"gemini-2.5-flash"}`, 200},
			{"trimmed resource model", "/v1beta/cachedContents", `{"model":" models/gemini-2.5-flash "}`, 200},
			{"unknown cache model", "/v1beta/cachedContents", `{"model":"models/gemini-unknown"}`, 400},
			{"conflicting duplicate models", "/v1beta/cachedContents", `{"model":"gemini-2.5-flash","model":"models/gemini-2.5-flash"}`, 400},
			{"unrelated model unchanged", "/v1/chat/completions", `{"model":"models/gemini-2.5-flash"}`, 400},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				groups := cacheRouteGroupsGeili{}
				keys := service.NewAPIKeyService(nil, opsRouteUserRepo{}, groups, nil, nil, nil, nil)
				resolver := service.NewCompositeRouteResolver(nil)
				cfg := &config.Config{}
				pricing := service.NewModelPricingResolver(nil, service.NewBillingService(cfg, nil))
				service.NewGatewayService(cacheRouteAccountsGeili{}, groups, nil, nil, nil, nil, nil, nil,
					cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, pricing, resolver, nil, nil)
				gid, sid := int64(22), int64(77)
				key := &service.APIKey{UserID: 1, BillingSource: source, RoutingMode: service.KeyRoutingSingle, GroupID: &gid}
				if source == service.BillingSourceSubscription {
					key.SubscriptionID = &sid
				}
				router := gin.New()
				router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
				router.Use(explicitKeyRouting(keys, resolver, &handler.Handlers{}, cfg))
				calls := 0
				router.POST(tc.path, func(c *gin.Context) {
					calls++
					body, err := io.ReadAll(c.Request.Body)
					require.NoError(t, err)
					require.Equal(t, tc.body, string(body), "authorization must not rewrite the original body")
					bound, ok := middleware.GetAPIKeyFromContext(c)
					require.True(t, ok)
					require.Equal(t, source, bound.BillingSource)
					require.Equal(t, key.SubscriptionID, bound.SubscriptionID)
					require.Equal(t, gid, *bound.GroupID)
					c.Status(http.StatusOK)
				})
				req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				out := httptest.NewRecorder()
				router.ServeHTTP(out, req)
				require.Equal(t, tc.status, out.Code, out.Body.String())
				if tc.status == 200 {
					require.Equal(t, 1, calls)
				} else {
					require.Zero(t, calls)
				}
			})
		}
	}
}
