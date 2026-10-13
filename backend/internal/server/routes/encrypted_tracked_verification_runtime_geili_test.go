package routes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type runtimeTrackedLookupFailureGeili struct{}

func (runtimeTrackedLookupFailureGeili) Error() string                             { return "private backend failure" }
func (runtimeTrackedLookupFailureGeili) Unwrap() error                             { return context.DeadlineExceeded }
func (runtimeTrackedLookupFailureGeili) KnownOpenAIEncryptedSourceHintGeili() bool { return true }
func (runtimeTrackedLookupFailureGeili) EncryptedRouteLookupPhaseGeili() string    { return "query" }

type runtimeTrackedFailureCacheGeili struct {
	*runtimeEncryptedCaptureCacheGeili
}

func (*runtimeTrackedFailureCacheGeili) GetOpenAIEncryptedRoutesGeili(context.Context, int64, []string) (map[string]service.OpenAIEncryptedRouteGeili, error) {
	return nil, fmt.Errorf("wrapped: %w", runtimeTrackedLookupFailureGeili{})
}

type runtimeVerificationHTTPGeili struct {
	runtimeEncryptedHTTPGeili
	calls atomic.Int64
}

func (h *runtimeVerificationHTTPGeili) Do(r *http.Request, p string, id int64, n int) (*http.Response, error) {
	h.calls.Add(1)
	return h.runtimeEncryptedHTTPGeili.Do(r, p, id, n)
}
func TestEncryptedTrackedVerificationHTTPRuntimeStopsBeforeModelGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			cache := &runtimeTrackedFailureCacheGeili{runtimeEncryptedCaptureCacheGeili: &runtimeEncryptedCaptureCacheGeili{earlyEncryptedCacheGeili: &earlyEncryptedCacheGeili{records: map[string]service.OpenAIEncryptedRouteGeili{}}}}
			cfg := &config.Config{}
			settings := service.NewSettingService(&runtimeEncryptedSettingsGeili{}, cfg)
			upstream := &runtimeVerificationHTTPGeili{}
			gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, settings, nil)
			group := int64(132)
			key := &service.APIKey{ID: 2001, UserID: 9001, GroupID: &group, GroupIDs: []int64{132}, Group: &service.Group{ID: group, Platform: service.PlatformOpenAI}}
			account := &service.Account{ID: 3002, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-key", "base_url": "https://synthetic.example/fixture"}, Extra: map[string]any{"openai_responses_mode": "force_responses", "openai_passthrough": passthrough}}
			body := []byte(`{"model":"gpt-6-astra","input":[{"type":"reasoning","id":"old","encrypted_content":"tracked-synthetic","summary":[{"type":"summary_text","text":"keep progress"}]},{"role":"user","content":"continue","nonce":9007199254740993}],"stream":false}`)
			original := append([]byte(nil), body...)
			router := gin.New()
			router.POST("/v1/responses", func(c *gin.Context) {
				c.Set("api_key", key)
				if _, err := gateway.PrepareOpenAIEncryptedRouteGeili(c, key, body); err != nil {
					require.False(t, service.OpenAIEncryptedRoutePinnedGeili(c.Request.Context()))
					require.False(t, service.OpenAIEncryptedRouteRecoveryEnabledGeili(c.Request.Context()))
					require.False(t, service.ClaimOpenAIEncryptedRecoveryGeili(c.Request.Context()))
					(&handler.OpenAIGatewayHandler{}).WriteOpenAIEncryptedRouteErrorGeili(c, err)
					return
				}
				_, err := gateway.Forward(c.Request.Context(), c, account, body)
				require.NoError(t, err)
			})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)))
			require.Equal(t, 503, rec.Code)
			var payload struct {
				Error struct {
					Code string `json:"code"`
				}
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
			require.Equal(t, "encrypted_session_verification_unavailable", payload.Error.Code)
			require.Zero(t, upstream.calls.Load(), "no model call means no upstream output or model usage")
			require.Empty(t, cache.records)
			require.Equal(t, original, body)
			require.NotContains(t, rec.Body.String(), "private")
			require.NotContains(t, rec.Body.String(), "tracked-synthetic")
		})
	}
}

type verificationRouteGroupsGeili struct{ service.GroupRepository }

func (verificationRouteGroupsGeili) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	price := 1e-6
	return &service.Group{ID: id, Status: service.StatusActive, Platform: service.PlatformOpenAI, RateMultiplier: 1, ModelPricing: []service.ChannelModelPricing{{Models: []string{"gpt-6-astra"}, BillingMode: service.BillingModeToken, InputPrice: &price, OutputPrice: &price}}}, nil
}

type verificationRouteAccountsGeili struct{ service.AccountRepository }

func (verificationRouteAccountsGeili) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return []service.Account{{ID: 3002, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}}}, nil
}
func TestEncryptedRouteRegisteredMiddlewareAbortsVerificationAndRevocationGeili(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{{"verification", 503, "encrypted_session_verification_unavailable"}, {"revocation", 409, "encrypted_session_route_conflict"}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			groups := verificationRouteGroupsGeili{}
			resolver := service.NewCompositeRouteResolver(nil)
			pricing := service.NewModelPricingResolver(nil, service.NewBillingService(cfg, nil))
			// The same normal constructor used in production wires the catalog into the resolver.
			service.NewGatewayService(verificationRouteAccountsGeili{}, groups, nil, nil, opsRouteUserRepo{}, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, pricing, resolver, nil, nil)
			keys := service.NewAPIKeyService(nil, opsRouteUserRepo{}, groups, nil, nil, nil, nil)
			group := int64(132)
			key := &service.APIKey{ID: 2001, UserID: 9001, BillingSource: service.BillingSourceBalance, RoutingMode: service.KeyRoutingComposite, GroupIDs: []int64{132}, GroupID: &group}
			source := &runtimeEncryptedCaptureCacheGeili{earlyEncryptedCacheGeili: &earlyEncryptedCacheGeili{records: map[string]service.OpenAIEncryptedRouteGeili{}}}
			var cache service.GatewayCache = source
			if tc.status == 503 {
				cache = &runtimeTrackedFailureCacheGeili{runtimeEncryptedCaptureCacheGeili: source}
			} else {
				digest := fmt.Sprintf("%x", sha256.Sum256([]byte("tracked-synthetic")))
				source.records[digest] = service.OpenAIEncryptedRouteGeili{Digest: digest, GroupID: 131, AccountID: 3001, Model: "gpt-6-astra"}
			}
			settings := service.NewSettingService(&runtimeEncryptedSettingsGeili{}, cfg)
			gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, settings, nil)
			openAI := handler.NewOpenAIGatewayHandler(gateway, nil, nil, keys, nil, nil, nil, nil, cfg)
			h := &handler.Handlers{OpenAIGateway: openAI}
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
			router.Use(explicitKeyRouting(keys, resolver, h, cfg))
			downstream := 0
			router.POST("/v1/responses", func(c *gin.Context) {
				downstream++
				// A downstream handler rejection must not append SSE to the first JSON error.
				openAI.WriteOpenAIEncryptedRouteErrorGeili(c, &service.OpenAIEncryptedRouteErrorGeili{Status: tc.status, Code: tc.code, Message: "downstream rejection"})
			})
			body := []byte(`{"model":"gpt-6-astra","input":[{"type":"compaction","encrypted_content":"tracked-synthetic"}],"stream":false}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code)
			t.Logf("status=%d downstream_calls=%d valid_json=%t appended_sse=%t", rec.Code, downstream, json.Valid(rec.Body.Bytes()), strings.Contains(rec.Body.String(), "event: response.failed"))
			require.Zero(t, downstream, "registered Gin middleware must stop the remaining handler chain")
			require.True(t, json.Valid(rec.Body.Bytes()), "early rejection must remain a single JSON error")
			var payload struct {
				Error struct {
					Code string `json:"code"`
				}
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
			require.Equal(t, tc.code, payload.Error.Code)
		})
	}
}
