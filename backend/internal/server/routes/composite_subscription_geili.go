package routes

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func writeCompositeRouteError(c *gin.Context, err error) {
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
