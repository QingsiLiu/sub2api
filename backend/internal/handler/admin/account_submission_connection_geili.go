package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) SubmissionConnection(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	if !decodeSubmissionJSON(c, &req) {
		return
	}
	result, err := h.accountSubmission.Connection(c.Request.Context(), req.Token)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) UpdateSubmissionConnection(c *gin.Context) {
	var req struct {
		Token      string                            `json:"token"`
		Version    int64                             `json:"version"`
		RequestID  string                            `json:"request_id"`
		Connection service.SubmissionConnectionInput `json:"connection"`
	}
	if !decodeSubmissionJSON(c, &req) {
		return
	}
	result, err := h.accountSubmission.UpdateConnection(c.Request.Context(), req.Token, req.Version, req.RequestID, req.Connection)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
