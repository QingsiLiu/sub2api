package handler

import (
	"strings"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type BenefitCampaignHandler struct {
	service *service.BenefitCampaignService
}

func NewBenefitCampaignHandler(campaigns *service.BenefitCampaignService) *BenefitCampaignHandler {
	return &BenefitCampaignHandler{service: campaigns}
}

func (h *BenefitCampaignHandler) Current(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok { response.Unauthorized(c, "User not found in context"); return }
	view, err := h.service.Current(c.Request.Context(), subject.UserID, time.Now().UTC())
	if err != nil { response.ErrorFrom(c, err); return }
	response.Success(c, view)
}

func (h *BenefitCampaignHandler) Claim(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok { response.Unauthorized(c, "User not found in context"); return }
	slug := strings.TrimSpace(c.Param("slug"))
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	claim, err := h.service.Claim(c.Request.Context(), subject.UserID, slug, key, time.Now().UTC())
	if err != nil { response.ErrorFrom(c, err); return }
	response.Success(c, claim)
}
