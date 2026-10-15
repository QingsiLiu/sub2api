package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) AccountRuntimePresentationGeili() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(service.WithAccountRuntimePresentationGeili(c.Request.Context()))
		c.Next()
	}
}
