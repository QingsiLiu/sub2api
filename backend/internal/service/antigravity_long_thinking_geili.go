package service

import (
	"time"

	"github.com/gin-gonic/gin"
)

func antigravityStreamModelGeili(c *gin.Context, fallback string) string {
	if c != nil {
		if model := c.GetString(OpsUpstreamModelKey); model != "" {
			return model
		}
	}
	return fallback
}

func (s *AntigravityGatewayService) antigravityStreamIntervalGeili(c *gin.Context, fallback string) time.Duration {
	if s.settingService == nil {
		return 0
	}
	return anthropicStreamIntervalGeili(s.settingService.cfg, antigravityStreamModelGeili(c, fallback))
}
