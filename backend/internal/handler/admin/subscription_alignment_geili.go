package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SubscriptionHandler) AlignLegacy(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid subscription")
		return
	}
	var req service.LegacyAlignmentRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid alignment request")
		return
	}
	result, err := h.subscriptionService.AlignLegacyEntitlements(c.Request.Context(), id, getAdminIDFromContext(c), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *SubscriptionHandler) LegacyRollout(c *gin.Context) {
	var result *service.LegacyRollout
	var err error
	if c.Request.Method == "PUT" {
		var req service.LegacyRollout
		if c.ShouldBindJSON(&req) != nil {
			response.BadRequest(c, "invalid rollout request")
			return
		}
		result, err = h.subscriptionService.SetLegacyRollout(c.Request.Context(), req)
	} else {
		result, err = h.subscriptionService.GetLegacyRollout(c.Request.Context())
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
