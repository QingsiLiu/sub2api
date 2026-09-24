package admin

import (
	"strconv"
	"time"

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

type CreateBenefitCampaignRequest struct {
	Slug                string    `json:"slug" binding:"required"`
	Title               string    `json:"title" binding:"required"`
	Status              string    `json:"status"`
	StartsAt            time.Time `json:"starts_at" binding:"required"`
	ClaimEndsAt         time.Time `json:"claim_ends_at" binding:"required"`
	EligibilityStartsAt time.Time `json:"eligibility_starts_at" binding:"required"`
	EligibilityEndsAt   time.Time `json:"eligibility_ends_at" binding:"required"`
	DurationDays        int       `json:"duration_days" binding:"required,min=1,max=365"`
	DailyLimitUSD       float64   `json:"daily_limit_usd" binding:"gte=0"`
	ResetMode           string    `json:"reset_mode"`
	MaxClaims           *int64    `json:"max_claims"`
}

func (h *BenefitCampaignHandler) List(c *gin.Context) {
	items, err := h.service.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *BenefitCampaignHandler) Create(c *gin.Context) {
	var req CreateBenefitCampaignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	out, err := h.service.Create(c.Request.Context(), service.CreateBenefitCampaignInput{Slug: req.Slug, Title: req.Title, Status: req.Status, StartsAt: req.StartsAt, ClaimEndsAt: req.ClaimEndsAt, EligibilityStartsAt: req.EligibilityStartsAt, EligibilityEndsAt: req.EligibilityEndsAt, DurationDays: req.DurationDays, DailyLimitUSD: req.DailyLimitUSD, ResetMode: req.ResetMode, MaxClaims: req.MaxClaims}, getAdminIDFromContext(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, out)
}

func (h *BenefitCampaignHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}
	var req CreateBenefitCampaignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	out, err := h.service.Update(c.Request.Context(), id, service.CreateBenefitCampaignInput{Slug: req.Slug, Title: req.Title, Status: req.Status, StartsAt: req.StartsAt, ClaimEndsAt: req.ClaimEndsAt, EligibilityStartsAt: req.EligibilityStartsAt, EligibilityEndsAt: req.EligibilityEndsAt, DurationDays: req.DurationDays, DailyLimitUSD: req.DailyLimitUSD, ResetMode: req.ResetMode, MaxClaims: req.MaxClaims}, getAdminIDFromContext(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *BenefitCampaignHandler) Stats(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}
	out, err := h.service.Stats(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *BenefitCampaignHandler) SetStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.service.SetStatus(c.Request.Context(), id, req.Status, getAdminIDFromContext(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "ok"})
}

func (h *BenefitCampaignHandler) Snapshot(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}
	count, err := h.service.Snapshot(c.Request.Context(), id, time.Now().UTC())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"eligible_count": count})
}
