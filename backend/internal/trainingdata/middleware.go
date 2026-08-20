package trainingdata

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// Middleware captures only authenticated inference routes. It deliberately
// skips dashboards, usage endpoints, model discovery, billing, admin APIs and
// media task/status routes: those are operational data, not model usage data.
func Middleware(manager *Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil || !manager.Active() || !isCapturableInferenceRoute(c) {
			c.Next()
			return
		}
		apiKey, ok := middleware2.GetAPIKeyFromContext(c)
		subject, subjectOK := middleware2.GetAuthSubjectFromContext(c)
		if !ok || !subjectOK || apiKey == nil || subject.UserID <= 0 || apiKey.ID <= 0 {
			c.Next()
			return
		}

		body, bodyReason := captureRequestBody(c.Request, manager.cfg.CaptureMaxBodyBytes)
		requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
		clientRequestID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
		model, stream := requestModelAndStream(body)
		protocol := protocolForPath(c.Request.URL.Path)
		session := manager.Begin(BeginInput{
			UserSubjectRef:    manager.UserSubjectRef(subject.UserID),
			APIKeySubjectRef:  manager.APIKeySubjectRef(apiKey.ID),
			RequestID:         strings.TrimSpace(requestID),
			ClientRequestID:   strings.TrimSpace(clientRequestID),
			Method:            c.Request.Method,
			Route:             c.Request.URL.Path,
			Protocol:          protocol,
			ClientModel:       model,
			Stream:            stream,
			Headers:           c.Request.Header,
			RequestBody:       body,
			IncompleteReasons: bodyReason,
		})
		if session == nil {
			c.Next()
			return
		}
		ctx := WithSession(c.Request.Context(), session)
		c.Request = c.Request.WithContext(ctx)
		originalWriter := c.Writer
		writer := &captureResponseWriter{ResponseWriter: originalWriter, session: session}
		c.Writer = writer
		defer func() {
			if c.Writer == writer {
				c.Writer = originalWriter
			}
			session.Finish(writer.Status(), writer.Header())
		}()
		c.Next()
	}
}

func isCapturableInferenceRoute(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil || c.Request.Method != http.MethodPost {
		return false
	}
	path := c.Request.URL.Path
	if strings.HasSuffix(path, "/messages") || isPathOrSubpath(path, "/responses") || strings.HasSuffix(path, "/chat/completions") {
		return true
	}
	if strings.HasSuffix(path, "/alpha/search") {
		return true
	}
	// Native Gemini inference routes contain /models/ and use POST. countTokens
	// is intentionally excluded because it does not contain a model response.
	return strings.Contains(path, "/v1beta/models/") &&
		(strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent"))
}

func isPathOrSubpath(path, base string) bool {
	return path == base || strings.HasPrefix(path, base+"/") || strings.HasSuffix(path, base) || strings.Contains(path, base+"/")
}

func protocolForPath(path string) string {
	switch {
	case strings.HasSuffix(path, "/messages"):
		return "anthropic_messages"
	case isPathOrSubpath(path, "/responses"):
		return "openai_responses"
	case strings.HasSuffix(path, "/chat/completions"):
		return "openai_chat_completions"
	case strings.HasSuffix(path, "/alpha/search"):
		return "openai_alpha_search"
	case strings.Contains(path, "/v1beta/models/"):
		return "gemini"
	default:
		return "unknown"
	}
}

func captureRequestBody(request *http.Request, maxBytes int64) ([]byte, []string) {
	if request == nil || request.Body == nil {
		return nil, nil
	}
	if maxBytes <= 0 {
		maxBytes = 32 * 1024 * 1024
	}
	if request.ContentLength > maxBytes {
		return nil, []string{"client_request_body_too_large"}
	}
	originalBody := request.Body
	readLimit := maxBytes + 1
	body, err := io.ReadAll(io.LimitReader(originalBody, readLimit))
	if err != nil {
		request.Body = &replayReadCloser{
			Reader: io.MultiReader(bytes.NewReader(body), originalBody),
			Closer: originalBody,
		}
		return nil, []string{"client_request_body_read_failed"}
	}
	if int64(len(body)) > maxBytes {
		request.Body = &replayReadCloser{
			Reader: io.MultiReader(bytes.NewReader(body), originalBody),
			Closer: originalBody,
		}
		return nil, []string{"client_request_body_too_large"}
	}
	_ = originalBody.Close()
	request.Body = io.NopCloser(bytes.NewReader(body))
	redacted, ok := RedactJSONBody(body)
	if !ok {
		return nil, []string{"client_request_body_opaque"}
	}
	return redacted, nil
}

type replayReadCloser struct {
	io.Reader
	io.Closer
}

func requestModelAndStream(body []byte) (string, bool) {
	if len(body) == 0 {
		return "", false
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return "", false
	}
	model, _ := payload["model"].(string)
	stream, _ := payload["stream"].(bool)
	return strings.TrimSpace(model), stream
}

type captureResponseWriter struct {
	gin.ResponseWriter
	session       *Session
	headerWritten bool
	hijacked      bool
}

func (w *captureResponseWriter) WriteHeader(statusCode int) {
	if !w.headerWritten {
		w.headerWritten = true
		w.session.RecordClientResponseHeaders(statusCode, w.Header())
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *captureResponseWriter) Write(data []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(data)
	if written > 0 {
		w.session.RecordClientResponseChunk(data[:written])
	}
	return written, err
}

func (w *captureResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *captureResponseWriter) Flush() {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	w.ResponseWriter.Flush()
}

func (w *captureResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	w.session.MarkIncomplete("client_websocket_hijack_not_captured")
	return w.ResponseWriter.Hijack()
}

func (w *captureResponseWriter) CloseNotify() <-chan bool {
	return w.ResponseWriter.CloseNotify()
}

func (w *captureResponseWriter) Pusher() http.Pusher {
	return w.ResponseWriter.Pusher()
}

func (w *captureResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

var _ gin.ResponseWriter = (*captureResponseWriter)(nil)
var _ http.Flusher = (*captureResponseWriter)(nil)
var _ http.Hijacker = (*captureResponseWriter)(nil)
