package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// PricingCatalogHandler exposes the strictly whitelisted model-pricing view.
// It never serializes channels, accounts, mappings, user IDs, keys, or admin data.
type PricingCatalogHandler struct {
	service pricingCatalogService
}

type pricingCatalogService interface {
	PublicCatalog(ctx context.Context) (*service.PublicPricingCatalog, error)
}

func NewPricingCatalogHandler(service *service.PublicPricingCatalogService) *PricingCatalogHandler {
	return &PricingCatalogHandler{service: service}
}

// AdminPreview serves the complete whitelisted catalog for administrator-only
// validation. The route is protected by AdminAuthMiddleware before this handler.
// GET /api/v1/admin/pricing/catalog
func (h *PricingCatalogHandler) AdminPreview(c *gin.Context) {
	catalog, err := h.service.PublicCatalog(c.Request.Context())
	if err != nil {
		if errors.Is(err, service.ErrPublicPricingDisabled) {
			response.NotFound(c, "Pricing catalog is disabled")
			return
		}
		response.Error(c, http.StatusServiceUnavailable, "Pricing catalog is temporarily unavailable")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	response.Success(c, catalog)
}
