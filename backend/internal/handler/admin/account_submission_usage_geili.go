package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"strconv"
)

func (h *AccountHandler) SubmissionUsage(c *gin.Context) {
	var req struct {
		Token     string `json:"token"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	}
	if !decodeSubmissionJSON(c, &req) {
		return
	}
	usage, err := h.accountSubmission.Usage(c.Request.Context(), req.Token, req.StartDate, req.EndDate)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, usage)
}
func (h *AccountHandler) RevokeSubmissionView(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	if err = h.accountSubmission.RevokeView(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"revoked": true})
}
func (h *AccountHandler) ResetSubmissionView(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, service.ErrSubmissionInvalid)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	token, err := h.accountSubmission.ResetView(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"token": token})
}
