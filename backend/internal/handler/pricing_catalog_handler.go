package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
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
	UserCatalog(ctx context.Context, userID int64) (*service.PublicPricingCatalog, error)
}

func NewPricingCatalogHandler(service *service.PublicPricingCatalogService) *PricingCatalogHandler {
	return &PricingCatalogHandler{service: service}
}

// Public serves default public-group multipliers.
// GET /api/v1/pricing/catalog
func (h *PricingCatalogHandler) Public(c *gin.Context) {
	catalog, err := h.service.PublicCatalog(c.Request.Context())
	if err != nil {
		if errors.Is(err, service.ErrPublicPricingDisabled) {
			response.NotFound(c, "Pricing catalog is disabled")
			return
		}
		response.Error(c, http.StatusServiceUnavailable, "Pricing catalog is temporarily unavailable")
		return
	}
	c.Header("Cache-Control", "public, max-age=15, stale-if-error=300")
	response.Success(c, catalog)
}

// Mine serves the same catalog filtered to the authenticated user's allowed
// groups and replaces the default multiplier with their settlement multiplier.
// GET /api/v1/pricing/catalog/me
func (h *PricingCatalogHandler) Mine(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	catalog, err := h.service.UserCatalog(c.Request.Context(), subject.UserID)
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
