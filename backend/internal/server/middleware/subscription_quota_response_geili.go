package middleware

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	pkgerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// abortSubscriptionQuotaGeili gives preflight and locked admission the same
// protocol response. A race at admission is quota exhaustion, not a 403.
func abortSubscriptionQuotaGeili(c *gin.Context, err error, google bool) bool {
	if !errors.Is(err, service.ErrDailyLimitExceeded) && !errors.Is(err, service.ErrWeeklyLimitExceeded) && !errors.Is(err, service.ErrMonthlyLimitExceeded) {
		return false
	}
	app := pkgerrors.FromError(err)
	code := "USAGE_LIMIT_EXCEEDED"
	message := app.Message
	if reset := app.Metadata["window_resets_at"]; reset != "" {
		if at, parseErr := time.Parse(time.RFC3339, reset); parseErr == nil {
			seconds := int(math.Ceil(time.Until(at).Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			c.Header("Retry-After", strconv.Itoa(seconds))
			message += "; resets at " + reset
		}
		code = app.Reason
	}
	if google {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"code": http.StatusTooManyRequests, "message": message, "status": "RESOURCE_EXHAUSTED", "details": []gin.H{{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": app.Reason, "domain": "sub2api", "metadata": app.Metadata}}}})
	} else {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"code": code, "message": message, "metadata": app.Metadata, "error": gin.H{"type": "rate_limit_error", "code": app.Reason, "message": message, "metadata": app.Metadata}})
	}
	return true
}
