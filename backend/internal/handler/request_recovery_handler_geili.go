package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Recovery deadlines stop a supplier attempt, not the downstream client. Keep
// these failures visible instead of treating them as silent client disconnects.
func requestRecoveryStopped(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	ctx := c.Request.Context()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || (ctx.Err() == nil && !service.RequestRecoveryAllowed(ctx)) {
		writeRequestRecoveryTimeout(c)
		return true
	}
	return false
}

// Bound account selection and queue waits as well as the actual Forward call.
// Before the first recoverable fault this preserves the original request ctx.
func armRequestRecoveryAttempt(c *gin.Context) context.CancelFunc {
	if c == nil || c.Request == nil {
		return func() {}
	}
	ctx, cancel := service.RequestRecoveryContext(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	return cancel
}

// WriteTextRecoveryFailure converts a buffered final JSON error after a neutral
// route heartbeat has already committed HTTP 200. Only routing uses this hook.
func WriteTextRecoveryFailure(c *gin.Context, status int, body []byte) {
	if c == nil || c.Writer == nil || service.IsResponseCommitted(c) {
		return
	}
	errType := gjson.GetBytes(body, "error.type").String()
	if errType == "" {
		errType = "upstream_error"
	}
	code := gjson.GetBytes(body, "error.code").String()
	message := gjson.GetBytes(body, "error.message").String()
	if message == "" {
		message = "Upstream request failed"
	}
	service.MarkOpsStreamFailure(c, errType, code, message, status)
	if inboundIsResponses(c) {
		_ = writeResponsesFailedSSE(c, errType, code, message)
	} else {
		errorObject := gin.H{"type": errType, "message": message}
		payload := gin.H{"error": errorObject}
		if code != "" {
			errorObject["code"] = code
		}
		if c.Request != nil && strings.HasSuffix(strings.TrimRight(c.Request.URL.Path, "/"), "/messages") {
			payload["type"] = "error"
		}
		encoded, _ := json.Marshal(payload)
		_, _ = c.Writer.WriteString("event: error\ndata: " + string(encoded) + "\n\n")
		c.Writer.Flush()
	}
	service.MarkResponseCommitted(c)
}

func writeRequestRecoveryTimeout(c *gin.Context) {
	if c == nil || c.Writer == nil || service.IsResponseCommitted(c) {
		return
	}
	const code = "upstream_recovery_timeout"
	const message = "Upstream recovery timeout budget exhausted"
	service.SetOpsUpstreamError(c, http.StatusGatewayTimeout, message, "")
	streamStarted := service.StopOpenAICompactSSEKeepaliveCommitted(c) || upstreamFailureStreamStarted(c, false)
	path := ""
	if c.Request != nil {
		path = strings.TrimRight(c.Request.URL.Path, "/")
	}
	errorObject := gin.H{"type": "upstream_error", "code": code, "message": message}
	if strings.HasSuffix(path, "/messages") {
		errorObject["type"] = "api_error"
	}
	if streamStarted {
		service.MarkOpsStreamFailure(c, "upstream_error", code, message, http.StatusGatewayTimeout)
		if inboundIsResponses(c) {
			_ = writeResponsesFailedSSE(c, "upstream_error", code, message)
		} else {
			payload := gin.H{"error": errorObject}
			if strings.HasSuffix(path, "/messages") {
				payload["type"] = "error"
			}
			body, _ := json.Marshal(payload)
			_, _ = c.Writer.WriteString("event: error\ndata: " + string(body) + "\n\n")
			c.Writer.Flush()
		}
	} else if strings.HasSuffix(path, "/messages") {
		c.JSON(http.StatusGatewayTimeout, gin.H{"type": "error", "error": errorObject})
	} else {
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": errorObject})
	}
	service.MarkResponseCommitted(c)
}

func upstreamFailureStreamStarted(c *gin.Context, streamStarted bool) bool {
	if streamStarted || c == nil || c.Writer == nil {
		return streamStarted
	}
	return c.Writer.Written() && strings.Contains(strings.ToLower(c.Writer.Header().Get("Content-Type")), "text/event-stream")
}

func waitForRequestRecovery(c *gin.Context, delay time.Duration) bool {
	if c == nil || c.Request == nil || failoverClientGone(c) {
		return false
	}
	ctx := c.Request.Context()
	if !service.BeginRequestRecovery(ctx) || delay >= service.RequestRecoveryRemaining(ctx) {
		writeRequestRecoveryTimeout(c)
		return false
	}
	waitCtx, cancel := service.RequestRecoveryContext(ctx)
	defer cancel()
	if !sleepWithContext(waitCtx, delay) {
		if !failoverClientGone(c) {
			writeRequestRecoveryTimeout(c)
		}
		return false
	}
	return !requestRecoveryStopped(c)
}
