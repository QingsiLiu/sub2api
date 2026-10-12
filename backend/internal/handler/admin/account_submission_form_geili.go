package admin

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) SubmissionFormService() *service.AccountSubmissionService {
	return h.accountSubmission
}

// Reuse the official request schema and service validation, with no administrator
// idempotency snapshots, response credentials, privacy calls or capability probes.
func (h *AccountHandler) CreateSubmissionFormAccount(c *gin.Context) {
	var req CreateAccountRequest
	if c.ShouldBindJSON(&req) != nil {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	_, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name: req.Name, Notes: req.Notes, Platform: req.Platform, Type: req.Type, Credentials: req.Credentials, Extra: req.Extra,
		ProxyID: req.ProxyID, Concurrency: req.Concurrency, Priority: req.Priority, RateMultiplier: req.RateMultiplier,
		LoadFactor: req.LoadFactor, GroupIDs: req.GroupIDs, ExpiresAt: req.ExpiresAt, AutoPauseOnExpired: req.AutoPauseOnExpired,
		ProbeEnabled: req.ProbeEnabled, SkipDefaultGroupBind: true, SkipMixedChannelCheck: req.ConfirmMixedChannelRisk != nil && *req.ConfirmMixedChannelRisk,
	})
	if err != nil {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	response.Success(c, gin.H{"id": 0, "status": "submitted"})
}

// A single invitation creates one new account; never update an existing identity
// through the administrator import path or enumerate that path's results.
func (h *AccountHandler) ValidateSingleSubmissionCodexImport(payload map[string]any) bool {
	raw, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	var req CodexSessionImportRequest
	if json.Unmarshal(raw, &req) != nil {
		return false
	}
	entries, err := parseCodexSessionImportEntries(req)
	return err == nil && len(entries) == 1
}

func (h *AccountHandler) ImportSubmissionCodexSession(c *gin.Context) {
	var req CodexSessionImportRequest
	if c.ShouldBindJSON(&req) != nil {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	entries, err := parseCodexSessionImportEntries(req)
	if err != nil || len(entries) != 1 {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	no := false
	req.UpdateExisting = &no
	result, err := h.importCodexSessions(c.Request.Context(), req, entries)
	if err != nil || result.Created != 1 || result.Failed != 0 {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	response.Success(c, gin.H{"created": 1, "updated": 0, "skipped": 0, "failed": 0})
}

func (h *AccountHandler) applySubmissionPreviewProxyGeili(c *gin.Context, a *service.Account) bool {
	if !service.IsSubmissionPublicEgressGeili(c.Request.Context()) {
		return true
	}
	value, _ := c.Get("geili_submission_proxy_id")
	id, _ := value.(*int64)
	if id == nil {
		return true
	}
	proxy, err := h.adminService.GetProxy(c.Request.Context(), *id)
	if err != nil || proxy == nil {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return false
	}
	a.ProxyID = id
	a.Proxy = proxy
	return true
}
