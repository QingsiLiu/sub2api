package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterSupplierRoutesGeili exposes the supplier console. It never reuses /admin
// handlers directly: every route resolves ownership from the authenticated user.
func RegisterSupplierRoutesGeili(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth middleware.JWTAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	if h == nil || h.Admin == nil || h.Admin.Supplier == nil {
		return
	}
	s := h.Admin.Supplier
	supplier := v1.Group("/supplier")
	supplier.Use(gin.HandlerFunc(jwtAuth))
	supplier.Use(middleware.BackendModeUserGuard(settingService))
	supplier.Use(middleware.SupplierOnlyGeili())
	supplier.Use(panelRateLimiter.Global())
	supplier.Use(gin.HandlerFunc(auditLog))
	{
		supplier.GET("/meta", s.Meta)
		supplier.GET("/accounts", s.ListAccounts)
		supplier.POST("/accounts", s.CreateAccount)
		supplier.GET("/accounts/:id", s.GetAccount)
		supplier.PUT("/accounts/:id", s.UpdateAccount)
		supplier.DELETE("/accounts/:id", s.DeleteAccount)
		supplier.POST("/accounts/:id/pause", s.PauseAccount)
		supplier.POST("/accounts/:id/test", panelRateLimiter.Heavy(), s.TestAccount)
		supplier.GET("/usage", s.ListUsage)
		supplier.GET("/usage/stats", panelRateLimiter.Heavy(), s.UsageStats)
	}
}
