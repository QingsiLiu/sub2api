package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterPricingRoutes(v1 *gin.RouterGroup, h *handler.Handlers, adminAuth middleware.AdminAuthMiddleware) {
	pricing := v1.Group("/admin/pricing")
	pricing.Use(gin.HandlerFunc(adminAuth))
	pricing.GET("/catalog", h.PricingCatalog.AdminPreview)
}
