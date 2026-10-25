package routes

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func writeCompositeRouteError(c *gin.Context, err error) {
	reason := infraerrors.Reason(err)
	// geili hook: both failures happen before dispatch; pricing failures stay in
	// detailed Ops logs, while unsupported models use the ingress aggregation.
	if reason == "MODEL_NOT_AVAILABLE" || reason == "MODEL_PRICE_NOT_CONFIGURED" {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
	}
	if reason == "MODEL_NOT_AVAILABLE" {
		middleware.MarkIngressRejected(c, middleware.IngressRejectModelNotAllowed)
	}
	status := infraerrors.Code(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	errorType := "invalid_request_error"
	code, message := infraerrors.Reason(err), infraerrors.Message(err)
	if status >= http.StatusInternalServerError {
		// geili hook: keep the actual cause with the request trace, and never
		// classify a persistence failure as a client validation error in Ops.
		errorType = "api_error"
		if errors.Is(err, context.DeadlineExceeded) {
			status, code, message = http.StatusServiceUnavailable, "ROUTING_UNAVAILABLE", "Routing service temporarily unavailable. Please retry later."
		}
		if c.Request != nil {
			fields := []zap.Field{zap.Error(err), zap.Int("status_code", status), zap.String("route_phase", "before_dispatch")}
			if key, ok := middleware.GetAPIKeyFromContext(c); ok && key != nil {
				fields = append(fields, zap.Int64("user_id", key.UserID), zap.Int64("api_key_id", key.ID))
			}
			logger.FromContext(c.Request.Context()).Error("gateway.route_resolution_failed", fields...)
		}
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": errorType, "code": code, "message": message}})
}

func bindCompositeResolvedKey(c *gin.Context, keys *service.APIKeyService, original *service.APIKey, decision service.CompositeRouteDecision) bool {
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	key, err := keys.PrepareCompositeRoute(c.Request.Context(), original, decision, subscription != nil)
	if err != nil {
		writeCompositeRouteError(c, err)
		return false
	}
	c.Set(string(middleware.ContextKeyAPIKey), key)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, key.Group))
	return true
}
