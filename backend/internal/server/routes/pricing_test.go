package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPricingCatalogRouteRequiresAdministratorAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	handlers := &handler.Handlers{PricingCatalog: handler.NewPricingCatalogHandler(nil)}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.AbortWithStatus(http.StatusForbidden)
	})

	RegisterPricingRoutes(v1, handlers, adminAuth)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/pricing/catalog", nil))
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestLegacyPublicPricingCatalogRoutesAreNotRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	handlers := &handler.Handlers{PricingCatalog: handler.NewPricingCatalogHandler(nil)}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Next()
	})

	RegisterPricingRoutes(v1, handlers, adminAuth)

	for _, path := range []string{"/api/v1/pricing/catalog", "/api/v1/pricing/catalog/me"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, recorder.Code, path)
	}
}
