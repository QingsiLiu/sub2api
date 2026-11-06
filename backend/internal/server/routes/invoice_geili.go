package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerInvoiceRoutesGeili exposes invoice issuing to administrator sessions.
// Scoped admin API keys are denied by default because no permission maps here.
func registerInvoiceRoutesGeili(admin *gin.RouterGroup, h *handler.Handlers) {
	if h == nil || h.Admin == nil || h.Admin.Invoice == nil {
		return
	}
	inv := h.Admin.Invoice
	invoices := admin.Group("/invoices")
	{
		invoices.GET("/issuer", inv.GetIssuer)
		invoices.PUT("/issuer", inv.UpdateIssuer)
		invoices.GET("", inv.List)
		invoices.POST("", inv.Create)
		invoices.GET("/:id", inv.Get)
		invoices.POST("/:id/void", inv.Void)
	}
}
