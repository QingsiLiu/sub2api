package trainingdata

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type sessionContextKey struct{}

type Session struct {
	manager      *Manager
	captureID    string
	startedAt    time.Time
	attemptCount atomic.Int64
	finished     atomic.Bool
	mu           sync.Mutex
	reasons      map[string]struct{}
}

func WithSession(ctx context.Context, session *Session) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if session == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionContextKey{}, session)
}

func SessionFromContext(ctx context.Context) *Session {
	if ctx == nil {
		return nil
	}
	session, _ := ctx.Value(sessionContextKey{}).(*Session)
	return session
}

func (s *Session) CaptureID() string {
	if s == nil {
		return ""
	}
	return s.captureID
}

func (s *Session) MarkIncomplete(reason string) {
	if s == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return
	}
	s.mu.Lock()
	s.reasons[reason] = struct{}{}
	s.mu.Unlock()
}

func (s *Session) RecordClientResponseHeaders(status int, headers http.Header) {
	if s == nil || s.finished.Load() {
		return
	}
	event := spoolEvent{
		kind: spoolClientResponseHeaders, captureID: s.captureID,
		headers: HeaderSnapshot{Status: status, Headers: SanitizeHeaders(headers)},
	}
	if !s.manager.tryEnqueue(event) {
		s.MarkIncomplete("client_response_headers_dropped")
	}
}

func (s *Session) RecordClientResponseChunk(data []byte) {
	if s == nil || len(data) == 0 || s.finished.Load() {
		return
	}
	event := spoolEvent{
		kind: spoolClientResponseChunk, captureID: s.captureID,
		data: append([]byte(nil), data...), atMS: time.Since(s.startedAt).Milliseconds(),
	}
	if !s.manager.tryEnqueue(event) {
		s.MarkIncomplete("client_response_body_dropped")
	}
}

func (s *Session) BeginUpstreamAttempt(req *http.Request) *Attempt {
	if s == nil || req == nil || s.finished.Load() {
		return nil
	}
	attemptID := int(s.attemptCount.Add(1))
	startedAt := time.Now()
	manifest := &AttemptManifest{
		AttemptID: attemptID, Method: req.Method,
		URL: SanitizeURL(req.URL.String()), StartedAt: startedAt.UTC(),
		RequestHeaders: SanitizeHeaders(req.Header), Model: requestModel(req),
	}
	var body []byte
	if req.GetBody != nil {
		reader, err := req.GetBody()
		if err == nil {
			limit := s.manager.cfg.CaptureMaxBodyBytes
			body, err = io.ReadAll(io.LimitReader(reader, limit+1))
			_ = reader.Close()
			if err != nil || int64(len(body)) > limit {
				body = nil
				s.MarkIncomplete("upstream_request_body_too_large_or_unreadable")
			} else if redacted, ok := RedactJSONBody(body); ok {
				body = redacted
			} else {
				body = nil
				s.MarkIncomplete("upstream_request_body_opaque")
			}
		} else {
			s.MarkIncomplete("upstream_request_body_unreadable")
		}
	} else if req.Body != nil {
		s.MarkIncomplete("upstream_request_body_not_replayable")
	}
	event := spoolEvent{kind: spoolAttemptStart, captureID: s.captureID, attemptID: attemptID, attempt: manifest, data: body}
	if !s.manager.tryEnqueue(event) {
		s.MarkIncomplete("upstream_attempt_start_dropped")
		return nil
	}
	return &Attempt{session: s, id: attemptID, startedAt: startedAt, complete: atomic.Bool{}}
}

func (s *Session) Finish(status int, headers http.Header) {
	if s == nil || !s.finished.CompareAndSwap(false, true) {
		return
	}
	s.mu.Lock()
	reasons := make([]string, 0, len(s.reasons))
	for reason := range s.reasons {
		reasons = append(reasons, reason)
	}
	s.mu.Unlock()
	s.manager.enqueueFinalize(finalizeEvent{
		captureID: s.captureID, finishedAt: time.Now().UTC(), status: status,
		headers: httpHeaderAlias(SanitizeHeaders(headers)), incompleteReason: reasons,
	})
}

type Attempt struct {
	session   *Session
	id        int
	startedAt time.Time
	complete  atomic.Bool
}

// RecordResponseHeaders snapshots the headers exactly as received from the
// upstream. Call it before any transport-level decompression mutates them.
func (a *Attempt) RecordResponseHeaders(resp *http.Response) {
	if a == nil || a.session == nil || resp == nil {
		return
	}
	event := spoolEvent{
		kind: spoolAttemptResponseHeaders, captureID: a.session.captureID, attemptID: a.id,
		headers: HeaderSnapshot{Status: resp.StatusCode, Headers: SanitizeHeaders(resp.Header)},
	}
	if !a.session.manager.tryEnqueue(event) {
		a.session.MarkIncomplete("upstream_response_headers_dropped")
	}
}

// CaptureResponseBody wraps the body at the representation consumed by the
// gateway. The shared HTTP repository calls this after decompression so the
// stored payload is usable JSON/SSE rather than gzip/brotli/zstd wire bytes.
func (a *Attempt) CaptureResponseBody(resp *http.Response) {
	if a == nil || a.session == nil || resp == nil {
		return
	}
	if resp.Body != nil {
		resp.Body = &captureResponseBody{source: resp.Body, attempt: a}
	} else {
		a.finish("", true)
	}
}

// RecordResponse remains as a safe convenience for callers that do not alter
// response encoding between header capture and body consumption.
func (a *Attempt) RecordResponse(resp *http.Response) {
	a.RecordResponseHeaders(resp)
	a.CaptureResponseBody(resp)
}

func (a *Attempt) Fail(err error) {
	if a == nil {
		return
	}
	errText := ""
	if err != nil {
		errText = redactSecretsInString(err.Error())
	}
	a.finish(errText, false)
}

func (a *Attempt) recordChunk(data []byte) {
	if a == nil || a.session == nil || len(data) == 0 {
		return
	}
	event := spoolEvent{
		kind: spoolAttemptResponseChunk, captureID: a.session.captureID, attemptID: a.id,
		data: append([]byte(nil), data...), atMS: time.Since(a.session.startedAt).Milliseconds(),
	}
	if !a.session.manager.tryEnqueue(event) {
		a.session.MarkIncomplete("upstream_response_body_dropped")
	}
}

func (a *Attempt) finish(errText string, complete bool) {
	if a == nil || !a.complete.CompareAndSwap(false, true) {
		return
	}
	event := spoolEvent{
		kind: spoolAttemptFinish, captureID: a.session.captureID, attemptID: a.id,
		errText: errText, complete: complete,
	}
	if !a.session.manager.tryEnqueue(event) {
		a.session.MarkIncomplete("upstream_attempt_finish_dropped")
	}
}

type captureResponseBody struct {
	source  io.ReadCloser
	attempt *Attempt
	eof     atomic.Bool
	closed  atomic.Bool
}

func (b *captureResponseBody) Read(target []byte) (int, error) {
	n, err := b.source.Read(target)
	if n > 0 {
		b.attempt.recordChunk(target[:n])
	}
	if errors.Is(err, io.EOF) {
		b.eof.Store(true)
		b.attempt.finish("", true)
	} else if err != nil {
		b.attempt.finish(redactSecretsInString(err.Error()), false)
	}
	return n, err
}

func (b *captureResponseBody) Close() error {
	if b.closed.CompareAndSwap(false, true) && !b.eof.Load() {
		b.attempt.session.MarkIncomplete("upstream_response_closed_before_eof")
		b.attempt.finish("response closed before EOF", false)
	}
	return b.source.Close()
}

func requestModel(req *http.Request) string {
	if req == nil {
		return ""
	}
	modelFromPath := func() string {
		if req.URL == nil {
			return ""
		}
		path := req.URL.Path
		marker := "/models/"
		index := strings.LastIndex(path, marker)
		if index < 0 {
			return ""
		}
		value := path[index+len(marker):]
		if colon := strings.IndexByte(value, ':'); colon >= 0 {
			value = value[:colon]
		}
		return strings.TrimSpace(value)
	}
	if req.GetBody == nil {
		return modelFromPath()
	}
	body, err := req.GetBody()
	if err != nil {
		return modelFromPath()
	}
	defer body.Close()
	limited, err := io.ReadAll(io.LimitReader(body, 2*1024*1024))
	if err != nil {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal(limited, &payload) != nil {
		return modelFromPath()
	}
	model, _ := payload["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return modelFromPath()
	}
	return model
}
