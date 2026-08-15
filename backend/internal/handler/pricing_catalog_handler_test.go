package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pricingCatalogServiceStub struct {
	publicCatalog *service.PublicPricingCatalog
	userCatalog   *service.PublicPricingCatalog
	publicErr     error
	userErr       error
	userID        int64
	publicCalls   int
	userCalls     int
}

func (s *pricingCatalogServiceStub) PublicCatalog(context.Context) (*service.PublicPricingCatalog, error) {
	s.publicCalls++
	return s.publicCatalog, s.publicErr
}

func (s *pricingCatalogServiceStub) UserCatalog(_ context.Context, userID int64) (*service.PublicPricingCatalog, error) {
	s.userCalls++
	s.userID = userID
	return s.userCatalog, s.userErr
}

func newPricingHandlerContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, nil)
	return ctx, recorder
}

func TestPricingCatalogHandlerPublicSuccessIsPubliclyCacheable(t *testing.T) {
	stub := &pricingCatalogServiceStub{publicCatalog: &service.PublicPricingCatalog{
		GeneratedAt: "2026-08-14T00:00:00Z",
		DataVersion: "abc123",
	}}
	handler := &PricingCatalogHandler{service: stub}
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/pricing/catalog")

	handler.Public(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "public, max-age=15, stale-if-error=300", recorder.Header().Get("Cache-Control"))
	require.Contains(t, recorder.Body.String(), `"generated_at":"2026-08-14T00:00:00Z"`)
	require.Equal(t, 1, stub.publicCalls)
}

func TestPricingCatalogHandlerPublicDisabledReturns404(t *testing.T) {
	stub := &pricingCatalogServiceStub{publicErr: service.ErrPublicPricingDisabled}
	handler := &PricingCatalogHandler{service: stub}
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/pricing/catalog")

	handler.Public(ctx)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Empty(t, recorder.Header().Get("Cache-Control"))
}

func TestPricingCatalogHandlerPublicWithoutSnapshotReturns503(t *testing.T) {
	stub := &pricingCatalogServiceStub{publicErr: errors.New("database unavailable")}
	handler := &PricingCatalogHandler{service: stub}
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/pricing/catalog")

	handler.Public(ctx)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "temporarily unavailable")
}

func TestPricingCatalogHandlerMineRequiresAuthentication(t *testing.T) {
	stub := &pricingCatalogServiceStub{}
	handler := &PricingCatalogHandler{service: stub}
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/pricing/catalog/me")

	handler.Mine(ctx)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, stub.userCalls)
}

func TestPricingCatalogHandlerMineUsesSubjectAndDisablesCaching(t *testing.T) {
	stub := &pricingCatalogServiceStub{userCatalog: &service.PublicPricingCatalog{
		GeneratedAt: "2026-08-14T00:00:00Z",
		DataVersion: "personal",
	}}
	handler := &PricingCatalogHandler{service: stub}
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/pricing/catalog/me")
	ctx.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})

	handler.Mine(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, int64(42), stub.userID)
	require.Equal(t, 1, stub.userCalls)
}

func TestPricingCatalogHandlerResponseDoesNotLeakForbiddenFields(t *testing.T) {
	stub := &pricingCatalogServiceStub{publicCatalog: &service.PublicPricingCatalog{
		GeneratedAt: "2026-08-14T00:00:00Z",
		Models: []service.PublicPricingModel{{
			ModelID:     "gpt-5.4",
			DisplayName: "GPT-5.4",
		}},
	}}
	handler := &PricingCatalogHandler{service: stub}
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/pricing/catalog")

	handler.Public(ctx)

	payload := recorder.Body.String()
	for _, forbidden := range []string{"channel_name", "account", "api_key", "user_id", "model_mapping", "cost_multiplier"} {
		require.False(t, strings.Contains(payload, forbidden), forbidden)
	}
}
