package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *UsageHandler) SetResponseAuditService(s *service.ResponseAuditService) { h.responseAudit = s }
func responseAuditFilter(c *gin.Context) (service.ResponseAuditFilter, error) {
	now := time.Now().UTC()
	f := service.ResponseAuditFilter{From: now.Add(-24 * time.Hour), To: now, Page: 1, PageSize: 50}
	for _, v := range []struct {
		name string
		dst  *time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if raw := c.Query(v.name); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return f, fmt.Errorf("%s must be RFC3339", v.name)
			}
			*v.dst = t
		}
	}
	if !f.To.After(f.From) || f.To.Sub(f.From) > 7*24*time.Hour {
		return f, errors.New("time range must be positive and at most 7 days")
	}
	f.Status = strings.TrimSpace(c.Query("status"))
	if f.Status != "" && f.Status != "success" && f.Status != "partial_failure" && f.Status != "empty" && f.Status != "failed" && f.Status != "unknown" {
		return f, errors.New("invalid response status")
	}
	f.Model = strings.TrimSpace(c.Query("model"))
	f.Endpoint = strings.TrimSpace(c.Query("endpoint"))
	f.RequestID = strings.TrimSpace(c.Query("request_id"))
	if len(f.Model) > 100 || len(f.Endpoint) > 128 || len(f.RequestID) > 64 {
		return f, errors.New("filter exceeds metadata length limit")
	}
	for _, v := range []struct {
		name string
		dst  *int64
	}{{"user_id", &f.UserID}, {"api_key_id", &f.APIKeyID}, {"account_id", &f.AccountID}} {
		if raw := c.Query(v.name); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n <= 0 {
				return f, fmt.Errorf("invalid %s", v.name)
			}
			*v.dst = n
		}
	}
	for _, v := range []struct {
		name string
		dst  *int
	}{{"page", &f.Page}, {"page_size", &f.PageSize}} {
		if raw := c.Query(v.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return f, fmt.Errorf("invalid %s", v.name)
			}
			*v.dst = n
		}
	}
	if f.PageSize > 100 || f.Page > 100000 {
		return f, errors.New("pagination limit exceeded")
	}
	return f, nil
}
func (h *UsageHandler) ListResponseAudits(c *gin.Context) {
	if h.responseAudit == nil {
		response.Error(c, 503, "Response audit unavailable")
		return
	}
	f, err := responseAuditFilter(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx, stop := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer stop()
	rows, total, err := h.responseAudit.List(ctx, f)
	if err != nil {
		response.Error(c, 503, "Response audit query unavailable")
		return
	}
	response.Paginated(c, rows, total, f.Page, f.PageSize)
}
func (h *UsageHandler) GetResponseAudit(c *gin.Context) {
	if h.responseAudit == nil {
		response.Error(c, 503, "Response audit unavailable")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid audit id")
		return
	}
	ctx, stop := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer stop()
	a, err := h.responseAudit.Get(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "Response audit not found")
		return
	}
	if err != nil {
		response.Error(c, 503, "Response audit query unavailable")
		return
	}
	response.Success(c, a)
}
func (h *UsageHandler) ResponseAuditStats(c *gin.Context) {
	if h.responseAudit == nil {
		response.Error(c, 503, "Response audit unavailable")
		return
	}
	f, err := responseAuditFilter(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx, stop := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer stop()
	a, err := h.responseAudit.Stats(ctx, f)
	if err != nil {
		response.Error(c, 503, "Response audit query unavailable")
		return
	}
	response.Success(c, a)
}
func (h *UsageHandler) attachResponseAudits(c *gin.Context, rows []dto.AdminUsageLog) {
	if h.responseAudit == nil || len(rows) == 0 {
		return
	}
	keys := make([]service.ResponseAuditUsageKey, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, service.ResponseAuditUsageKey{APIKeyID: r.APIKeyID, RequestID: r.RequestID})
	}
	ctx, stop := context.WithTimeout(c.Request.Context(), time.Second)
	defer stop()
	m, err := h.responseAudit.Lookup(ctx, keys)
	if err != nil {
		return
	} // optional evidence never breaks financial reads
	for i := range rows {
		rows[i].ResponseAudit = m[keys[i]]
	}
}
