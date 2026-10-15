package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SetManualBalanceStopGeili is an administrative label, with no supplier I/O.
// Clearing it leaves the account stopped until separately enabled or recovered.
func (h *AccountHandler) SetManualBalanceStopGeili(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req struct {
		Blocked *bool `json:"blocked"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Blocked == nil {
		response.BadRequest(c, "A boolean blocked field is required")
		return
	}
	account, err := h.rateLimitService.SetManualBalanceStopGeili(c.Request.Context(), id, *req.Blocked)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.buildAccountResponseWithRuntime(c.Request.Context(), account))
}

// RecheckAccountGeili uses the runtime recovery service. Clearing state alone
// cannot prove a model is usable or acknowledge an unpaid upstream balance.
func (h *AccountHandler) RecheckAccountGeili(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req struct {
		Model            string `json:"model"`
		ConfirmRecharged bool   `json:"confirm_recharged"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid recheck request")
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" || len(req.Model) > 100 {
		response.BadRequest(c, "Model is required and must not exceed 100 bytes")
		return
	}
	if h.rateLimitService == nil || h.rateLimitService.AccountRecoveryGeili() == nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "Account recovery service unavailable", "ACCOUNT_RECOVERY_UNAVAILABLE", nil)
		return
	}
	account, err := h.rateLimitService.AccountRecoveryGeili().RecheckAccountGeili(c.Request.Context(), id, req.Model, req.ConfirmRecharged)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccountRechargeConfirmationRequiredGeili):
			response.ErrorWithDetails(c, http.StatusConflict, "Confirm this upstream account has been recharged before rechecking", "ACCOUNT_RECHARGE_CONFIRMATION_REQUIRED", nil)
		case errors.Is(err, service.ErrAccountBalanceBlockedGeili):
			response.ErrorWithDetails(c, http.StatusConflict, "Upstream account balance is still insufficient", "ACCOUNT_BALANCE_BLOCKED", nil)
		case errors.Is(err, service.ErrAccountProbeUnhealthyGeili):
			response.ErrorWithDetails(c, http.StatusServiceUnavailable, "Model check failed; stopped state retained", "ACCOUNT_PROBE_UNHEALTHY", nil)
		case errors.Is(err, service.ErrAccountRuntimeConflictGeili):
			response.ErrorWithDetails(c, http.StatusConflict, "Account changed during the check; recheck its current state", "ACCOUNT_RUNTIME_CONFLICT", nil)
		default:
			response.ErrorFrom(c, err)
		}
		return
	}
	response.Success(c, h.buildAccountResponseWithRuntime(c.Request.Context(), account))
}
