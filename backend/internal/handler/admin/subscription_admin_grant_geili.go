package admin

import (
	"context"
	"strconv"

	geilisub "github.com/Wei-Shaw/sub2api/internal/geili/subscription"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Grant previews (apply=false) or issues (apply=true) an administrator daily
// entitlement. Applying requires an Idempotency-Key; the service also dedupes
// on it durably, so a replay after the request cache expires stays safe.
// POST /api/v1/admin/subscriptions/grant
func (h *SubscriptionHandler) Grant(c *gin.Context) {
	var req service.AdminGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid grant request")
		return
	}
	if !req.Apply {
		result, err := h.subscriptionService.GrantAdminEntitlement(c.Request.Context(), getAdminIDFromContext(c), req)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, result)
		return
	}
	req.IdempotencyKey = c.GetHeader("Idempotency-Key")
	if !geilisub.ValidGrantKey(req.IdempotencyKey) {
		response.ErrorFrom(c, geilisub.ErrAdminGrantKey)
		return
	}
	// Fingerprint the terms only: a retry after a lost response may carry a
	// fresh preview snapshot, and the service replays the saved grant for it.
	terms := struct {
		UserID        int64   `json:"user_id"`
		DailyLimitUSD float64 `json:"daily_limit_usd"`
		Days          int     `json:"days"`
		Reason        string  `json:"reason"`
	}{req.UserID, req.DailyLimitUSD, req.Days, req.Reason}
	executeAdminIdempotentJSON(c, "admin.subscriptions.grant", terms, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.subscriptionService.GrantAdminEntitlement(ctx, getAdminIDFromContext(c), req)
	})
}

type terminateGrantRequest struct {
	Reason string `json:"reason"`
}

// TerminateGrant ends a live administrator grant now.
// POST /api/v1/admin/subscriptions/:id/entitlements/:eid/terminate
func (h *SubscriptionHandler) TerminateGrant(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid subscription")
		return
	}
	eid, err := strconv.ParseInt(c.Param("eid"), 10, 64)
	if err != nil || eid <= 0 {
		response.BadRequest(c, "invalid entitlement")
		return
	}
	var req terminateGrantRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid terminate request")
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if !geilisub.ValidGrantKey(key) {
		response.ErrorFrom(c, geilisub.ErrAdminGrantKey)
		return
	}
	payload := struct {
		SubscriptionID int64  `json:"subscription_id"`
		EntitlementID  int64  `json:"entitlement_id"`
		Reason         string `json:"reason"`
	}{id, eid, req.Reason}
	executeAdminIdempotentJSON(c, "admin.subscriptions.terminate-grant", payload, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.subscriptionService.TerminateAdminGrant(ctx, getAdminIDFromContext(c), id, eid, req.Reason, key)
	})
}

// Entitlements lists a pool's lots, projected daily limits and operation log.
// GET /api/v1/admin/subscriptions/:id/entitlements
func (h *SubscriptionHandler) Entitlements(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid subscription")
		return
	}
	result, err := h.subscriptionService.AdminEntitlements(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
