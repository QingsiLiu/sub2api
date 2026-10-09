package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// TryAUAPIVideo preserves existing Grok routes when the request is not owned
// by an AUAPI account. Task lookups resolve ownership without selecting a new account.
func (h *AsyncImageHandler) TryAUAPIVideo(c *gin.Context, operation string) bool {
	if h == nil || h.auapi == nil {
		return false
	}
	id := c.Param("request_id")
	if operation != "create" && !strings.HasPrefix(id, "auvidtask_") {
		return false
	}
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.ID <= 0 || key.UserID <= 0 {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return true
	}
	if operation != "create" {
		task, err := h.auapi.Get(c.Request.Context(), service.ImageTaskOwner{UserID: key.UserID, APIKeyID: key.ID}, id)
		if err != nil {
			imageTaskError(c, err)
			return true
		}
		c.Header("Cache-Control", "no-store")
		if operation == "content" {
			if task.Status != service.ImageTaskStatusCompleted || task.VideoURL == "" {
				imageTaskJSONError(c, http.StatusConflict, "task_not_complete", "video content is not ready")
				return true
			}
			c.Redirect(http.StatusTemporaryRedirect, task.VideoURL)
		} else {
			c.JSON(http.StatusOK, task)
		}
		return true
	}
	if key.GroupID == nil {
		return false
	}
	body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", "cannot read video request")
		return true
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var request struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if json.Unmarshal(body, &request) != nil {
		imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", "invalid video JSON")
		return true
	}
	available, err := h.auapi.Available(c.Request.Context(), *key.GroupID, request.Model)
	if err != nil {
		imageTaskError(c, err)
		return true
	}
	if !available {
		return false
	}
	if !service.GroupAllowsImageGeneration(key.Group) {
		imageTaskJSONError(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return true
	}
	// Use the same prompt moderation path as existing Grok video generation;
	// only model/prompt enter that path, not provider-specific video parameters.
	moderation, _ := json.Marshal(map[string]string{"model": request.Model, "prompt": request.Prompt})
	if !h.checkAUAPIVideoPrompt(c, key, request.Model, moderation) {
		return true
	}
	sub, _ := middleware2.GetSubscriptionFromContext(c)
	task, replayed, err := h.auapi.SubmitVideo(c.Request.Context(), key, sub, body, c.GetHeader("Idempotency-Key"))
	if err != nil {
		imageTaskError(c, err)
		return true
	}
	if replayed {
		if err := service.CancelReplayedSubscriptionAdmission(c.Request.Context(), sub); err != nil {
			imageTaskJSONError(c, http.StatusServiceUnavailable, "api_error", "cannot release replay admission")
			return true
		}
	}
	prefix := "/v1"
	if !strings.HasPrefix(c.Request.URL.Path, "/v1/") {
		prefix = ""
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Location", prefix+"/videos/"+task.ID)
	c.Header("Retry-After", "3")
	c.JSON(http.StatusAccepted, task)
	return true
}

// Video moderation must not run the Images endpoint parser: that parser rejects
// /videos before auditing the prompt. The audit service and protocol are shared
// with existing media generation, with the real account/key context preserved.
func (h *AsyncImageHandler) checkAUAPIVideoPrompt(c *gin.Context, key *service.APIKey, model string, body []byte) bool {
	if h.openAI == nil {
		return true
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		imageTaskJSONError(c, http.StatusInternalServerError, "api_error", "User context not found")
		return false
	}
	decision := h.openAI.checkSecurityAudit(c, requestLogger(c, "handler.auapi_video.security_audit"), key, subject, service.ContentModerationProtocolOpenAIImages, model, body)
	if decision != nil && !decision.AllowNextStage {
		h.openAI.openAISecurityAuditError(c, decision)
		return false
	}
	return true
}
