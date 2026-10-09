package service

// A transport heartbeat commits HTTP headers, but not a model answer. Keep
// attempt preludes private until content arrives so another supplier can still
// complete the same Python request without leaking a second response identity.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const geiliTextNeutralSizeKey = "geili_text_neutral_writer_size"

type geiliTextForwardWriter struct {
	context *gin.Context
	gin.ResponseWriter
	headers                            http.Header
	stage                              *openAIFirstOutputStage
	pending                            bytes.Buffer
	status, initialSize                int
	stream, semantic, terminal, failed bool
	responseID, model                  string
	failurePayload                     []byte
	writeErr                           error
	clientDisconnected                 bool
	failureTerminalAccepted            bool
	unreplayable                       bool
	terminalPending                    bytes.Buffer
}

// BeginTextForwardGuard wraps one account attempt, never the whole routing
// loop. Its returned cleanup must run before restoring the caller's writer.
func BeginTextForwardGuard(c *gin.Context, stream bool) func(error) error {
	if c == nil || c.Writer == nil || c.Request == nil {
		return func(err error) error { return err }
	}
	if _, ok := c.Writer.(*geiliTextForwardWriter); ok {
		return func(err error) error { return err }
	}
	// An existing answer owns the transport; never hide it from WS fallback
	// or account replay decisions. Known neutral keepalives are discounted.
	if GeiliTextForwardWrittenSize(c) > 0 {
		return func(err error) error { return err }
	}
	parent := c.Writer
	c.Set("geili_supplier_failure", nil)
	w := &geiliTextForwardWriter{context: c, ResponseWriter: parent, headers: parent.Header().Clone(),
		stage: newDefaultOpenAIFirstOutputStage(), status: http.StatusOK,
		initialSize: parent.Size(), stream: stream}
	c.Writer = w
	return func(err error) error {
		defer func() { _ = w.stage.Close(); c.Writer = parent }()
		if w.stream && w.pending.Len() > 0 {
			frame := w.pending.Bytes()
			payload := geiliSSEPayload(frame)
			geiliObserveUpstreamOperation(c, payload, geiliBufferedFrameType(frame, payload))
		}
		if w.writeErr != nil && err == nil && !w.clientDisconnected && c.Request.Context().Err() == nil {
			err = w.writeErr
		}
		if w.failed && err == nil && !w.failureTerminalAccepted {
			err = errors.New("upstream response failed")
		}
		var decidedFailover *UpstreamFailoverError
		if w.unreplayable && errors.As(err, &decidedFailover) {
			decidedFailover.SafeToFailoverAfterWrite = false
			decidedFailover.NextAccountAction = NextAccountStop
		}
		if !w.semantic && w.status >= http.StatusBadRequest && w.stage.Buffered() > 0 && !errors.As(err, &decidedFailover) {
			// An explicit administrator passthrough/local protocol decision owns
			// this error envelope. Its marker cannot be overridden by retry inference.
			if commitErr := w.commit(); commitErr != nil && err == nil {
				err = commitErr
			}
			return err
		}
		if w.semantic {
			var failover *UpstreamFailoverError
			if errors.As(err, &failover) {
				failover.SafeToFailoverAfterWrite = false
			}
			if w.failed {
				MarkResponseCommitted(c)
			}
		}
		if err != nil && !w.semantic && !w.unreplayable && !strings.Contains(strings.ToLower(err.Error()), "passthrough") {
			var failover *UpstreamFailoverError
			if errors.As(err, &failover) {
				failover.SafeToFailoverAfterWrite = true
			} else if len(w.failurePayload) != 0 {
				err = GeiliUpstreamErrorFailure(c, w.failurePayload, w.status, w.headers)
			} else {
				err = GeiliUpstreamReadFailure(c, err)
			}
			if errors.As(err, &failover) {
				// A terminal decision from this attempt was not delivered. The next
				// account must not inherit its response-committed suppression flag.
				c.Set(ResponseCommittedKey, false)
				if !BeginRequestRecovery(c.Request.Context()) {
					failover.NextAccountAction = NextAccountStop
				}
				return err
			}
		}
		var supplier *geiliUpstreamPayloadFailure
		if errors.As(err, &supplier) && !w.semantic {
			// Error-only attempts carry no response identity or prelude to preserve.
			_ = w.stage.Close()
			w.stage = newDefaultOpenAIFirstOutputStage()
			w.pending.Reset()
			if w.stream {
				w.headers.Set("Content-Type", "text/event-stream")
				if commitErr := w.commit(); commitErr != nil {
					return commitErr
				}
				GeiliWriteStreamFailure(c, err)
			} else {
				w.status = supplier.status
				w.headers.Set("Content-Type", "application/json; charset=utf-8")
				if commitErr := w.commit(); commitErr != nil {
					return commitErr
				}
				writeGeiliJSONFailure(c, supplier)
			}
			return err
		}
		if err != nil && !w.semantic && w.stage.Buffered() == 0 {
			return err // Let the handler render local/build failures before committing.
		}
		if !w.semantic {
			if commitErr := w.commit(); commitErr != nil && err == nil {
				err = commitErr
			}
		}
		if err != nil && w.stream && !w.terminal && !w.failed && c.Request.Context().Err() == nil {
			GeiliWriteStreamFailure(c, err)
		}
		return err
	}
}

// GeiliTextForwardWrittenSize discounts the exact neutral transport snapshot,
// without adding byte counts that a protocol reader may already have recorded.
func GeiliTextForwardWrittenSize(c *gin.Context) int {
	size := OpenAICompactKeepaliveAdjustedWrittenSize(c)
	if c != nil && c.Writer != nil {
		neutralSize, neutral := c.Get(geiliTextNeutralSizeKey)
		if neutral && neutralSize != nil && neutralSize == c.Writer.Size() {
			return -1
		}
	}
	return size
}

func GeiliTextForwardStreaming(c *gin.Context) bool {
	if c != nil {
		if w, ok := c.Writer.(*geiliTextForwardWriter); ok {
			return w.stream
		}
	}
	return false
}

// A writer can detect disconnect before net/http cancels the request context.
// Preserve the existing successful usage-draining result in that interval.
func GeiliMarkTextClientDisconnect(c *gin.Context) {
	if c != nil {
		if w, ok := c.Writer.(*geiliTextForwardWriter); ok {
			w.clientDisconnected = true
		}
	}
}

// A synchronous Responses result can legitimately have status=failed with a
// billable usage result and no transport error. Keep that explicit protocol state.
func geiliAcceptTextFailureTerminal(c *gin.Context) {
	if c != nil {
		if w, ok := c.Writer.(*geiliTextForwardWriter); ok && !w.stream {
			w.failureTerminalAccepted = true
		}
	}
}

func (w *geiliTextForwardWriter) Header() http.Header {
	if w.semantic {
		return w.ResponseWriter.Header()
	}
	return w.headers
}
func (w *geiliTextForwardWriter) WriteHeader(code int) {
	if !w.semantic {
		w.status = code
	}
}
func (w *geiliTextForwardWriter) WriteHeaderNow() {}
func (w *geiliTextForwardWriter) Status() int {
	if w.semantic {
		return w.ResponseWriter.Status()
	}
	return w.status
}
func (w *geiliTextForwardWriter) Size() int {
	if w.semantic {
		return w.ResponseWriter.Size()
	}
	return w.initialSize
}
func (w *geiliTextForwardWriter) Written() bool { return w.semantic }
func (w *geiliTextForwardWriter) Flush() {
	if w.semantic && !w.clientDisconnected {
		w.ResponseWriter.Flush()
	}
}
func (w *geiliTextForwardWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *geiliTextForwardWriter) commit() error {
	if w.semantic {
		return nil
	}
	for k, v := range w.headers {
		w.ResponseWriter.Header()[k] = append([]string(nil), v...)
	}
	w.ResponseWriter.WriteHeader(w.status)
	w.semantic = true
	w.context.Set(geiliTextNeutralSizeKey, nil)
	return w.stage.CommitTo(w.ResponseWriter)
}

func (w *geiliTextForwardWriter) Write(p []byte) (int, error) {
	if w.clientDisconnected {
		return len(p), nil // Usage drain may continue; the detached caller receives no more bytes.
	}
	if w.semantic {
		w.observeTerminalChunks(p)
		return w.ResponseWriter.Write(p)
	}
	if !w.stream || w.status >= http.StatusBadRequest || !strings.Contains(w.headers.Get("Content-Type"), "text/event-stream") {
		// Gin emits JSON in one write. Inspect error envelopes before committing
		// an HTTP 200 supplied by a compatibility upstream.
		if gjson.ValidBytes(p) && (gjson.GetBytes(p, "error").IsObject() || gjson.GetBytes(p, "status").String() == "failed") {
			w.failed = true
			w.failurePayload = append([]byte(nil), p...)
			return w.stage.Write(p)
		}
		if w.status >= 400 {
			return w.stage.Write(p)
		}
		if err := w.commit(); err != nil {
			w.writeErr = err
			return 0, err
		}
		return w.ResponseWriter.Write(p)
	}
	if w.pending.Len()+len(p)+int(w.stage.Buffered()) > openAIFirstOutputStageMaxBytes {
		w.writeErr = errOpenAIFirstOutputStageLimit
		return 0, w.writeErr
	}
	_, _ = w.pending.Write(p)
	for {
		raw := w.pending.Bytes()
		end, delimiter := bytes.Index(raw, []byte("\n\n")), 2
		if crlf := bytes.Index(raw, []byte("\r\n\r\n")); crlf >= 0 && (end < 0 || crlf < end) {
			end, delimiter = crlf, 4
		}
		if end < 0 {
			break
		}
		frame := append([]byte(nil), w.pending.Next(end+delimiter)...)
		payload := geiliSSEPayload(frame)
		kind := gjson.GetBytes(payload, "type").String()
		if len(payload) == 0 || kind == "ping" || kind == "keepalive" {
			// Only protocol-neutral transport bytes escape the private attempt.
			w.ResponseWriter.Header().Set("Content-Type", "text/event-stream")
			w.ResponseWriter.Header().Set("Cache-Control", "no-cache")
			w.ResponseWriter.Header().Set("X-Accel-Buffering", "no")
			if _, err := w.ResponseWriter.Write([]byte(": keepalive\n\n")); err != nil {
				w.writeErr = err
				return 0, err
			}
			w.ResponseWriter.Flush()
			w.context.Set(geiliTextNeutralSizeKey, w.ResponseWriter.Size())
			continue
		}
		w.observeTerminal(frame)
		geiliObserveUpstreamOperation(w.context, payload, geiliBufferedFrameType(frame, payload))
		if _, err := w.stage.Write(frame); err != nil {
			w.writeErr = err
			return 0, err
		}
		if geiliSSESemantic(payload) || (w.terminal && !w.failed) {
			if err := w.commit(); err != nil {
				w.writeErr = err
				return 0, err
			}
			if w.pending.Len() > 0 {
				rest := w.pending.Next(w.pending.Len())
				w.observeTerminalChunks(rest)
				_, w.writeErr = w.ResponseWriter.Write(rest)
			}
			break
		}
	}
	return len(p), w.writeErr
}

func geiliSSEPayload(frame []byte) []byte {
	var data []string
	for _, line := range strings.Split(strings.ReplaceAll(string(frame), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return []byte(strings.Join(data, "\n"))
}

func geiliSSESemantic(payload []byte) bool {
	kind := gjson.GetBytes(payload, "type").String()
	if kind == "content_block_start" {
		block := gjson.GetBytes(payload, "content_block")
		switch block.Get("type").String() {
		case "tool_use", "server_tool_use", "redacted_thinking":
			return true
		}
		return block.Get("text").String() != "" || block.Get("thinking").String() != ""
	}
	if kind == "content_block_delta" {
		delta := gjson.GetBytes(payload, "delta")
		return delta.Get("text").String() != "" || delta.Get("thinking").String() != "" || delta.Get("partial_json").String() != "" || delta.Get("signature").String() != ""
	}
	if strings.HasPrefix(kind, "response.") {
		for _, tool := range []string{"web_search_call", "file_search_call", "code_interpreter_call", "image_generation_call", "computer_call"} {
			if strings.HasPrefix(kind, "response."+tool+".") {
				return true
			}
		}
		if strings.HasSuffix(kind, ".delta") {
			return gjson.GetBytes(payload, "delta").String() != ""
		}
		if kind == "response.output_item.added" {
			return openAIStreamAddedEventStartsClientOutput(payload, kind)
		}
		return kind == "response.output_item.done" && openAIStreamAddedEventStartsClientOutput(payload, "response.output_item.added")
	}
	for _, choice := range gjson.GetBytes(payload, "choices").Array() {
		delta := choice.Get("delta")
		if delta.Get("content").String() != "" || delta.Get("reasoning_content").String() != "" || len(delta.Get("tool_calls").Array()) != 0 || delta.Get("function_call").Exists() {
			return true
		}
	}
	return false
}

// Synchronous conversion may consume built-in progress without emitting it.
// Observing actual execution still forbids replay of the logical request.
func geiliObserveUpstreamOperation(c *gin.Context, payload []byte, eventType ...string) {
	kind := gjson.GetBytes(payload, "type").String()
	if kind == "" {
		kind = firstNonEmpty(eventType...)
	}
	operation := false
	for _, tool := range []string{"web_search_call", "file_search_call", "code_interpreter_call", "image_generation_call", "computer_call"} {
		operation = operation || strings.HasPrefix(kind, "response."+tool+".")
	}
	if kind == "response.output_item.added" || kind == "response.output_item.done" {
		switch gjson.GetBytes(payload, "item.type").String() {
		case "web_search_call", "file_search_call", "code_interpreter_call", "image_generation_call", "computer_call":
			operation = true
		}
	}
	if kind == "content_block_start" && gjson.GetBytes(payload, "content_block.type").String() == "server_tool_use" {
		operation = true
	}
	if !operation || c == nil {
		return
	}
	if w, ok := c.Writer.(*geiliTextForwardWriter); ok {
		w.unreplayable = true
	}
	if c.Request != nil {
		forbidRequestReplayGeili(c.Request.Context())
	}
}

func (w *geiliTextForwardWriter) observeTerminal(frame []byte) {
	payload := geiliSSEPayload(frame)
	if len(payload) == 0 && gjson.ValidBytes(frame) {
		payload = frame
	}
	kind := gjson.GetBytes(payload, "type").String()
	if id := gjson.GetBytes(payload, "response.id").String(); id != "" {
		w.responseID = id
	}
	if model := gjson.GetBytes(payload, "response.model").String(); model != "" {
		w.model = model
	}
	switch kind {
	case "error", "response.failed":
		w.failed = true
		w.failurePayload = append([]byte(nil), payload...)
	case "response.completed", "response.incomplete", "message_stop":
		w.terminal = true
	}
	if gjson.GetBytes(payload, "error").IsObject() {
		w.failed = true
		w.failurePayload = append([]byte(nil), payload...)
	}
	for _, choice := range gjson.GetBytes(payload, "choices").Array() {
		if reason := choice.Get("finish_reason"); reason.Type == gjson.String && reason.String() != "" {
			w.terminal = true
		}
	}
}

func geiliPreOutput(c *gin.Context) bool {
	if c == nil || c.Writer == nil {
		return true
	}
	if w, ok := c.Writer.(*geiliTextForwardWriter); ok {
		return !w.semantic && !w.unreplayable
	}
	return !c.Writer.Written()
}

// GeiliUpstreamReadFailure only proposes replay for positive transport/completion
// failures. Arbitrary local errors, oversized input and canceled callers stay final.
func GeiliUpstreamReadFailure(c *gin.Context, err error) error {
	if err == nil || !geiliPreOutput(c) {
		return err
	}
	if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var existing *UpstreamFailoverError
	if errors.As(err, &existing) {
		existing.SafeToFailoverAfterWrite = true
		return err
	}
	if code, message, transport := OpenAIUpstreamStreamReadErrorDetails(err); transport {
		payload, _ := json.Marshal(gin.H{"error": gin.H{"type": "upstream_error", "code": code, "message": message}})
		return &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: payload, SafeToFailoverAfterWrite: true}
	}
	msg := strings.ToLower(err.Error())
	retry := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, errOpenAIFirstOutputStageLimit)
	for _, marker := range []string{"stream data interval timeout", "missing terminal", "without a terminal", "without terminal", "stream ended without", "upstream stream disconnected", "stream read error", "connection reset", "broken pipe", "streamtruncated", "upstream stream has invalid", "invalid upstream JSON response", "upstream SSE event exceeds"} {
		retry = retry || strings.Contains(msg, strings.ToLower(marker))
	}
	if !retry {
		return err
	}
	code, message := "upstream_stream_incomplete", "Upstream response did not complete"
	if strings.Contains(msg, "stream data interval timeout") {
		code, message = "upstream_stream_idle", "Upstream stream data interval timeout"
	}
	payload, _ := json.Marshal(gin.H{"error": gin.H{"type": "upstream_error", "code": code, "message": message}})
	return &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: payload, SafeToFailoverAfterWrite: true}
}

type geiliUpstreamPayloadFailure struct {
	status  int
	payload []byte
	message string
	headers http.Header
}

func (e *geiliUpstreamPayloadFailure) Error() string { return "upstream response failed: " + e.message }

func GeiliUpstreamErrorFailure(c *gin.Context, payload []byte, status int, headers http.Header) error {
	message := sanitizeUpstreamErrorMessage(ExtractUpstreamErrorMessage(payload))
	if nested := geiliPayloadErrorField(payload, "message"); nested != "" {
		message = sanitizeUpstreamErrorMessage(nested)
	}
	if message == "" {
		message = "Upstream response failed"
	}
	semantic := openAIStreamFailedEventSemanticStatus(payload, message)
	if status >= 400 {
		semantic = status
	}
	if semantic < 400 {
		semantic = http.StatusBadGateway
	}
	if isOpenAIInstantInferenceQuotaError(message, payload) {
		semantic = http.StatusTooManyRequests
	}
	kind := strings.ToLower(geiliPayloadErrorField(payload, "type"))
	if status < 400 {
		switch kind {
		case "overloaded_error":
			semantic = 529
		case "rate_limit_error":
			semantic = http.StatusTooManyRequests
		}
	}
	// Known input errors inside HTTP 200 retain their input-error semantics.
	if status < 400 && (kind == "invalid_request_error" || kind == "validation_error") {
		semantic = http.StatusBadRequest
	}
	failure := &geiliUpstreamPayloadFailure{status: semantic, payload: append([]byte(nil), payload...), message: message, headers: headers.Clone()}
	if c != nil {
		c.Set("geili_supplier_failure", failure)
	}
	if !geiliPreOutput(c) || (c != nil && c.Request != nil && c.Request.Context().Err() != nil) {
		return failure
	}
	retry := semantic == 429 || semantic == 529 || status >= 500 || geiliExplicitServerFailure(payload) || openAIStreamErrorEventShouldFailover(payload, message)
	if semantic == http.StatusBadRequest {
		retry = isOpenAIInstantInferenceQuotaError(message, payload)
	}
	if isOpenAIContextWindowError(message, payload) {
		retry = false
		failure.status = http.StatusBadRequest
	}
	if hit, _, _ := detectOpenAICyberPolicy(payload); hit {
		retry = false
		failure.status = http.StatusBadRequest
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "maximum number of images") || strings.Contains(lower, "output token maximum") {
		retry = false
		if status < 400 {
			failure.status = http.StatusBadRequest
		}
	}
	if !retry {
		return failure
	}
	return &UpstreamFailoverError{StatusCode: semantic, ResponseBody: append([]byte(nil), payload...), ResponseHeaders: headers.Clone(), SafeToFailoverAfterWrite: true}
}

func geiliExplicitServerFailure(payload []byte) bool {
	for _, path := range openAIStreamErrorStatusPaths {
		status := int(gjson.GetBytes(payload, path).Int())
		if status >= 500 && status <= 599 {
			return true
		}
	}
	for _, path := range []string{"error.type", "response.error.type"} {
		switch strings.ToLower(gjson.GetBytes(payload, path).String()) {
		case "server_error", "api_error", "upstream_error", "overloaded_error":
			return true
		}
	}
	return false
}

func writeGeiliJSONFailure(c *gin.Context, failure *geiliUpstreamPayloadFailure) {
	kind := "upstream_error"
	if failure.status == http.StatusBadRequest {
		kind = "invalid_request_error"
	}
	errorBody := gin.H{"type": kind, "message": failure.message}
	for _, field := range []string{"type", "code", "param"} {
		if value := geiliPayloadErrorField(failure.payload, field); value != "" {
			errorBody[field] = value
		}
	}
	body := gin.H{"error": errorBody}
	if c.Request != nil && strings.HasSuffix(strings.TrimRight(c.Request.URL.Path, "/"), "/messages") {
		body["type"] = "error"
	}
	c.JSON(failure.status, body)
	MarkResponseCommitted(c)
}

func geiliPayloadErrorField(payload []byte, field string) string {
	for _, prefix := range []string{"error.", "response.error."} {
		if value := gjson.GetBytes(payload, prefix+field).String(); value != "" {
			return value
		}
	}
	return ""
}

func (w *geiliTextForwardWriter) observeTerminalChunks(p []byte) {
	if !w.stream {
		return
	}
	_, _ = w.terminalPending.Write(p)
	for {
		raw := w.terminalPending.Bytes()
		end, delimiter := bytes.Index(raw, []byte("\n\n")), 2
		if crlf := bytes.Index(raw, []byte("\r\n\r\n")); crlf >= 0 && (end < 0 || crlf < end) {
			end, delimiter = crlf, 4
		}
		if end < 0 {
			break
		}
		w.observeTerminal(w.terminalPending.Next(end + delimiter))
	}
	if w.terminalPending.Len() > openAIFirstOutputStageMaxBytes {
		w.terminalPending.Reset()
	}
}

// GeiliWriteStreamFailure is deliberately protocol-specific. A failed stream
// must not get a synthetic success terminal or concatenate a second answer.
func GeiliWriteStreamFailure(c *gin.Context, err error) {
	if c == nil || c.Writer == nil || err == nil {
		return
	}
	message := "Upstream response did not complete"
	var supplier *geiliUpstreamPayloadFailure
	if errors.As(err, &supplier) {
		message = supplier.message
	}
	path := ""
	if c.Request != nil {
		path = strings.TrimRight(c.Request.URL.Path, "/")
	}
	errorBody := gin.H{"type": "upstream_error", "code": "upstream_stream_incomplete", "message": message}
	if errors.Is(err, context.DeadlineExceeded) || (c.Request != nil && !RequestRecoveryAllowed(c.Request.Context()) && c.Request.Context().Err() != context.Canceled) {
		errorBody["code"] = "upstream_recovery_timeout"
		message = "Upstream recovery timeout budget exhausted"
		errorBody["message"] = message
	}
	if supplier != nil {
		for _, field := range []string{"type", "code", "param"} {
			value := gjson.GetBytes(supplier.payload, "error."+field)
			if !value.Exists() {
				value = gjson.GetBytes(supplier.payload, "response.error."+field)
			}
			if value.Type == gjson.String && value.String() != "" {
				errorBody[field] = value.String()
			}
		}
	}
	var frame string
	switch {
	case strings.HasSuffix(path, "/messages"):
		body, _ := json.Marshal(gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": message}})
		frame = "event: error\ndata: " + string(body) + "\n\n"
	case strings.HasSuffix(path, "/chat/completions"):
		body, _ := json.Marshal(gin.H{"error": errorBody})
		frame = "data: " + string(body) + "\n\n"
		if supplier != nil && supplier.status < 500 {
			frame += "data: [DONE]\n\n" // End the explicit supplier error wire, never synthesize finish_reason.
		}
	default:
		responseID, model := "", ""
		if w, ok := c.Writer.(*geiliTextForwardWriter); ok {
			responseID, model = w.responseID, w.model
		}
		body, _ := json.Marshal(gin.H{"error": errorBody})
		frame = buildOpenAIResponseFailedSSE(responseID, model, body, message)
	}
	_, _ = c.Writer.WriteString(frame)
	c.Writer.Flush()
	MarkResponseCommitted(c)
	MarkOpsStreamFailure(c, "upstream_error", "upstream_stream_incomplete", message, http.StatusBadGateway)
}
