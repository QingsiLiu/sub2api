package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
)

// SetOpsIngressRequestContext records client intent before routing can reject a request.
// The upstream model and target group are deliberately left unset until selected.
func SetOpsIngressRequestContext(c *gin.Context, model string, stream bool) {
	if c == nil {
		return
	}
	model = strings.TrimSpace(model)
	c.Set("ops_model", model)
	c.Set("ops_stream", stream)
	requestType := int16(RequestTypeSync)
	if stream {
		requestType = int16(RequestTypeStream)
	}
	c.Set("ops_request_type", requestType)
	if c.Request != nil && model != "" {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Model, model))
	}
}
