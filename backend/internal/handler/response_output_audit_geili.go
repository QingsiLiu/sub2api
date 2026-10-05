package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Installed after API-key authentication and before keepalive/routing. Reads
// only outbound bytes; never consumes or rewrites request/response bodies.
func ResponseOutputAudit(svc *service.ResponseAuditService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		protocol := ""
		switch {
		case strings.HasSuffix(path, "/messages"):
			protocol = "messages"
		case strings.HasSuffix(path, "/chat/completions"):
			protocol = "chat"
		case strings.HasSuffix(path, "/responses"):
			protocol = "responses"
		}
		ws := c.Request.Method == "GET" && strings.EqualFold(c.GetHeader("Upgrade"), "websocket")
		if protocol == "" || (c.Request.Method != "POST" && !ws) {
			c.Next()
			return
		}
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key == nil {
			c.Next()
			return
		}
		requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
		clientID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
		base := service.ResponseAudit{UserID: key.UserID, APIKeyID: key.ID, RequestID: requestID, ClientRequestID: clientID, Endpoint: path, Protocol: protocol}
		if key.GroupID != nil {
			base.GroupID = *key.GroupID
		}
		if clientID != "" {
			base.UsageRequestID = "client:" + clientID
		} else if requestID != "" {
			base.UsageRequestID = "local:" + requestID
		}
		// WS billing has per-turn identities; an HTTP session identity must not
		// incorrectly link every turn to the same receipt.
		if ws {
			base.UsageRequestID = ""
		}
		ctx, audit := service.WithResponseAudit(c.Request.Context(), svc, base, ws)
		c.Request = c.Request.WithContext(ctx)
		if !ws {
			c.Writer = &responseAuditWriter{ResponseWriter: c.Writer, audit: audit}
		}
		defer func() {
			accountID, _ := c.Get(opsAccountIDKey)
			account, _ := accountID.(int64)
			model, _ := c.Get(opsModelKey)
			modelName, _ := model.(string)
			if id := c.Writer.Header().Get("X-Request-Id"); id != requestID {
				audit.UpstreamRequestID(id)
			}
			audit.Attribute(account, 0, modelName)
			audit.Finish(c.Writer.Status(), c.Request.Context().Err() != nil)
		}()
		c.Next()
	}
}

type responseAuditWriter struct {
	gin.ResponseWriter
	audit *service.ResponseAuditSession
}

func (w *responseAuditWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseAuditWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.audit.Write(p, n, err, strings.Contains(w.Header().Get("Content-Type"), "text/event-stream"))
	return n, err
}
func (w *responseAuditWriter) WriteString(p string) (int, error) { return w.Write([]byte(p)) }
