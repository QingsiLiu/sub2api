package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterPricingRoutes(v1 *gin.RouterGroup, h *handler.Handlers, jwtAuth middleware.JWTAuthMiddleware) {
	pricing := v1.Group("/pricing")
	pricing.GET("/catalog", h.PricingCatalog.Public)
	pricing.GET("/catalog/me", gin.HandlerFunc(jwtAuth), h.PricingCatalog.Mine)
}
