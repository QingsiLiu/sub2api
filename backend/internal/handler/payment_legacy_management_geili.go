package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *PaymentHandler) LegacySubscriptionOptions(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	result, err := h.paymentService.LegacySubscriptionOptions(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
