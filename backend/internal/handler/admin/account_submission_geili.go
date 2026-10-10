package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) SetAccountSubmissionService(s *service.AccountSubmissionService) {
	h.accountSubmission = s
}
func (h *BackupHandler) SetAccountSubmissionService(s *service.AccountSubmissionService) {
	h.accountSubmission = s
}

// Never expose a decoder error: it may include attacker-controlled credential material.
func decodeSubmissionJSON(c *gin.Context, out any) bool {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return false
	}
	return true
}

func (h *AccountHandler) CreateSubmissionInvite(c *gin.Context) {
	config := service.AccountSubmissionConfig{Concurrency: 1, Priority: 50, RateMultiplier: 1}
	if !decodeSubmissionJSON(c, &config) {
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Administrator authentication required")
		return
	}
	invite, token, err := h.accountSubmission.Create(c.Request.Context(), subject.UserID, config)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"invite": invite, "token": token})
}

func (h *AccountHandler) ListSubmissionInvites(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 || page > 100000 || size < 1 || size > 100 {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	items, total, err := h.accountSubmission.List(c.Request.Context(), page, size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

func (h *AccountHandler) RevokeSubmissionInvite(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	if err := h.accountSubmission.Revoke(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"revoked": true})
}

type submissionPublicRequest struct {
	Token  string `json:"token"`
	APIKey string `json:"api_key"`
}

func (h *AccountHandler) InspectSubmissionInvite(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	if !decodeSubmissionJSON(c, &req) {
		return
	}
	invite, err := h.accountSubmission.Inspect(c.Request.Context(), req.Token)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if invite.Status != "pending" && invite.Status != "submitted" {
		response.ErrorFrom(c, service.ErrSubmissionInactive)
		return
	}
	// No internal names, IDs, groups or proxy configuration on this public endpoint.
	response.Success(c, gin.H{"platform": invite.Config.Platform, "status": invite.Status, "expires_at": invite.ExpiresAt})
}

func (h *AccountHandler) SubmitAccountKey(c *gin.Context) {
	var req submissionPublicRequest
	if !decodeSubmissionJSON(c, &req) {
		return
	}
	if err := h.accountSubmission.Submit(c.Request.Context(), req.Token, req.APIKey); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"status": "submitted"})
}

func (h *AccountHandler) protectedSubmissionAccounts(c *gin.Context, ids []int64) (map[int64]bool, error) {
	// Optional only for legacy handler test fixtures; production wiring always attaches the service.
	if h.accountSubmission == nil {
		return map[int64]bool{}, nil
	}
	return h.accountSubmission.ProtectedAccountIDs(c.Request.Context(), ids)
}
