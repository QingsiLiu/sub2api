package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RegisterAccountSubmissionPublicRoutes(v1 *gin.RouterGroup, h *handler.Handlers, redisClient *redis.Client) {
	limiter := middleware.NewRateLimiter(redisClient)
	group := v1.Group("/account-submissions")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Next()
	})
	// Separate from configurable panel limits: this credential-bearing public entry is fail-closed.
	group.Use(limiter.LimitWithOptions("account-submission", 30, time.Minute, middleware.RateLimitOptions{FailureMode: middleware.RateLimitFailClose}))
	group.POST("/inspect", h.Admin.Account.InspectSubmissionInvite)
	group.POST("/submit", h.Admin.Account.SubmitAccountKey)
}
