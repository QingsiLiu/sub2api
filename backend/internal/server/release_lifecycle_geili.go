package server

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/releaselifecycle"
	"github.com/gin-gonic/gin"
)

func releaseLifecycleGeili() gin.HandlerFunc {
	return releaseLifecycleForGeili(releaselifecycle.Default)
}

func releaseLifecycleForGeili(controller *releaselifecycle.Controller) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/health" {
			c.Next()
			return
		}
		kind := "http"
		if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
			kind = "websocket"
		}
		done := controller.Begin(kind)
		defer done()
		c.Next()
	}
}
