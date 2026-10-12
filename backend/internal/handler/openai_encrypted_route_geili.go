package handler

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"time"
)

func (h *OpenAIGatewayHandler) PrepareOpenAIEncryptedRouteGeili(c *gin.Context, key *service.APIKey, body []byte) (*service.OpenAIEncryptedRouteGeili, error) {
	if h == nil || h.gatewayService == nil {
		return nil, nil
	}
	return h.gatewayService.PrepareOpenAIEncryptedRouteGeili(c, key, body)
}

func (h *OpenAIGatewayHandler) authorizeEncryptedWSTurnGeili(ctx context.Context, key *service.APIKey, model string) error {
	denied := func() error {
		return &service.OpenAIEncryptedRouteErrorGeili{Status: 409, Code: "encrypted_session_route_conflict", Message: "此 Key 的当前权限已不能继续原会话线路，请重新连接"}
	}
	if h.apiKeyService == nil || key == nil || key.GroupID == nil {
		return denied()
	}
	check, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	fresh, err := h.apiKeyService.GetByID(check, key.ID)
	if err != nil {
		return service.ErrOpenAIEncryptedRouteUnavailableGeili
	}
	if fresh == nil || fresh.UserID != key.UserID || !fresh.IsActive() || fresh.IsExpired() || fresh.IsQuotaExhausted() || fresh.User == nil || !fresh.User.IsActive() || fresh.BillingSource != key.BillingSource {
		return denied()
	}
	if fresh.BillingSource != "" {
		candidates, err := h.apiKeyService.ResolveExplicitKeyRoutes(check, fresh, model, service.CompositeRouteEndpointResponses, h.compositeResolver)
		if err != nil {
			return service.ErrOpenAIEncryptedRouteUnavailableGeili
		}
		for _, candidate := range candidates {
			if candidate.TargetGroupID != nil && *candidate.TargetGroupID == *key.GroupID && candidate.TargetPlatform == service.PlatformOpenAI {
				return nil
			}
		}
		return denied()
	}
	if fresh.GroupID == nil || *fresh.GroupID != *key.GroupID || fresh.Group == nil || !fresh.Group.IsActive() || !fresh.Group.ModelAllowlist.Allows(model) {
		return denied()
	}
	if fresh.Group.Platform == service.PlatformComposite {
		if h.compositeResolver == nil {
			return denied()
		}
		decision, resolveErr := h.compositeResolver.Resolve(check, fresh.Group.ID, model, service.CompositeRouteEndpointResponses)
		if resolveErr != nil {
			return service.ErrOpenAIEncryptedRouteUnavailableGeili
		}
		currentTarget, ok := service.CompositeTargetGroupIDFromContext(ctx)
		if !ok || !decision.Matched || decision.TargetGroupID == nil || *decision.TargetGroupID != currentTarget || decision.TargetPlatform != service.PlatformOpenAI {
			return denied()
		}
	}
	groups, err := h.apiKeyService.GetAvailableGroups(check, fresh.UserID)
	if err != nil {
		return service.ErrOpenAIEncryptedRouteUnavailableGeili
	}
	for _, group := range groups {
		if group.ID == *key.GroupID {
			return nil
		}
	}
	return denied()
}

func (h *OpenAIGatewayHandler) WriteOpenAIEncryptedRouteErrorGeili(c *gin.Context, err error) {
	status, code, message := 503, "encrypted_session_route_unavailable", "原会话线路暂时不可用，请稍后重试或联系技术支持"
	var typed *service.OpenAIEncryptedRouteErrorGeili
	if errors.As(err, &typed) {
		status, code, message = typed.Status, typed.Code, typed.Message
	}
	if c.Writer.Written() {
		payload, _ := json.Marshal(gin.H{"type": "response.failed", "response": gin.H{"status": "failed", "error": gin.H{"code": code, "message": message}}})
		_, _ = c.Writer.Write(append(append([]byte("event: response.failed\ndata: "), payload...), []byte("\n\n")...))
		c.Writer.Flush()
		return
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "type": "api_error", "message": message}})
}

// The WebSocket HTTP upgrade has no model/body. Authorize the actual first
// frame before choosing its account and freeze billing to the chosen group.
func (h *OpenAIGatewayHandler) prepareEncryptedWSFirstRouteGeili(c *gin.Context, key *service.APIKey, body []byte, model string) (*service.APIKey, error) {
	owner, err := h.PrepareOpenAIEncryptedRouteGeili(c, key, body)
	if err != nil || owner == nil {
		return key, err
	}
	if key.BillingSource == "" {
		if key.GroupID == nil {
			return key, service.ErrOpenAIEncryptedRouteUnavailableGeili
		}
		decision := service.CompositeRouteDecision{TargetGroupID: key.GroupID, TargetGroup: key.Group, TargetPlatform: service.PlatformOpenAI}
		if key.Group != nil && key.Group.Platform == service.PlatformComposite {
			if h.compositeResolver == nil {
				return key, service.ErrOpenAIEncryptedRouteUnavailableGeili
			}
			resolved, resolveErr := h.compositeResolver.Resolve(c.Request.Context(), key.Group.ID, model, service.CompositeRouteEndpointResponses)
			if resolveErr != nil {
				return key, resolveErr
			}
			if !resolved.Matched || resolved.TargetGroupID == nil {
				return key, service.ErrOpenAIEncryptedRouteUnavailableGeili
			}
			decision = resolved
		}
		candidates := service.FilterOpenAIEncryptedRoutesGeili([]service.CompositeRouteDecision{decision}, owner)
		if len(candidates) == 0 {
			return key, &service.OpenAIEncryptedRouteErrorGeili{Status: 409, Code: "encrypted_session_route_conflict", Message: "原会话线路不在此 Key 的可用分组中"}
		}
		if decision.Matched {
			next := *key
			next.CompositeRoute = &decision
			c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
			c.Set(string(middleware.ContextKeyAPIKey), &next)
			return &next, nil
		}
		return key, nil
	}
	candidates, err := h.apiKeyService.ResolveExplicitKeyRoutes(c.Request.Context(), key, model, service.CompositeRouteEndpointResponses, h.compositeResolver)
	if err != nil {
		return key, err
	}
	candidates = service.FilterOpenAIEncryptedRoutesGeili(candidates, owner)
	if len(candidates) == 0 {
		return key, &service.OpenAIEncryptedRouteErrorGeili{Status: 409, Code: "encrypted_session_route_conflict", Message: "原会话线路不在此 Key 的可用分组中"}
	}
	decision := candidates[0]
	next := *key
	group := *decision.TargetGroup
	next.Group = &group
	next.GroupID = &group.ID
	next.CompositeRoute = &decision
	if key.User != nil {
		user := *key.User
		user.UserGroupRPMOverride = nil
		next.User = &user
	}
	c.Set(string(middleware.ContextKeyAPIKey), &next)
	ctx := service.WithCompositeRouteDecision(c.Request.Context(), decision)
	c.Request = c.Request.WithContext(context.WithValue(ctx, ctxkey.Group, &group))
	return &next, nil
}

func writeEncryptedWSRouteErrorGeili(ctx context.Context, conn *coderws.Conn, err error) {
	code, message := "encrypted_session_route_unavailable", "原会话线路暂时不可用，请稍后重试或联系技术支持"
	var typed *service.OpenAIEncryptedRouteErrorGeili
	if errors.As(err, &typed) {
		code, message = typed.Code, typed.Message
	}
	payload, _ := json.Marshal(gin.H{"type": "error", "error": gin.H{"type": "api_error", "code": code, "message": message}})
	_ = conn.Write(ctx, coderws.MessageText, payload)
	closeOpenAIClientWS(conn, coderws.StatusTryAgainLater, code)
}
