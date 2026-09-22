package routes

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
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
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "code": infraerrors.Reason(err), "message": infraerrors.Message(err)}})
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
