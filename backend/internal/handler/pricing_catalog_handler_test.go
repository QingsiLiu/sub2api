package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pricingCatalogServiceStub struct {
	publicCatalog *service.PublicPricingCatalog
	publicErr     error
	publicCalls   int
}

func (s *pricingCatalogServiceStub) PublicCatalog(context.Context) (*service.PublicPricingCatalog, error) {
	s.publicCalls++
	return s.publicCatalog, s.publicErr
}

func newPricingHandlerContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, nil)
	return ctx, recorder
}

func TestPricingCatalogHandlerAdminPreviewSuccessIsPrivate(t *testing.T) {
	stub := &pricingCatalogServiceStub{publicCatalog: &service.PublicPricingCatalog{
		GeneratedAt: "2026-08-14T00:00:00Z",
		DataVersion: "abc123",
	}}
	handler := &PricingCatalogHandler{service: stub}
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/admin/pricing/catalog")

	handler.AdminPreview(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	require.Contains(t, recorder.Body.String(), `"generated_at":"2026-08-14T00:00:00Z"`)
	require.Equal(t, 1, stub.publicCalls)
}

func TestPricingCatalogHandlerAdminPreviewDisabledReturns404(t *testing.T) {
	stub := &pricingCatalogServiceStub{publicErr: service.ErrPublicPricingDisabled}
	handler := &PricingCatalogHandler{service: stub}
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/admin/pricing/catalog")

	handler.AdminPreview(ctx)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Empty(t, recorder.Header().Get("Cache-Control"))
}

func TestPricingCatalogHandlerAdminPreviewWithoutSnapshotReturns503(t *testing.T) {
	stub := &pricingCatalogServiceStub{publicErr: errors.New("database unavailable")}
	handler := &PricingCatalogHandler{service: stub}
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/admin/pricing/catalog")

	handler.AdminPreview(ctx)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "temporarily unavailable")
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
	ctx, recorder := newPricingHandlerContext(http.MethodGet, "/api/v1/admin/pricing/catalog")

	handler.AdminPreview(ctx)

	payload := recorder.Body.String()
	for _, forbidden := range []string{"channel_name", "account", "api_key", "user_id", "model_mapping", "cost_multiplier"} {
		require.False(t, strings.Contains(payload, forbidden), forbidden)
	}
}
