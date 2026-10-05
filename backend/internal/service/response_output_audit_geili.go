package service

// Response audits are observational. They must never participate in billing
// fingerprints, settlement commands, replay decisions or account scheduling.
import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const responseAuditMaxBytes = 1 << 20

type ResponseAudit struct {
	ID                  int64     `json:"id"`
	AuditRequestID      string    `json:"audit_request_id"`
	Turn                int       `json:"turn"`
	RequestID           string    `json:"request_id"`
	ClientRequestID     string    `json:"client_request_id"`
	UsageRequestID      string    `json:"usage_request_id,omitempty"`
	UpstreamRequestID   string    `json:"upstream_request_id,omitempty"`
	UserID              int64     `json:"user_id"`
	APIKeyID            int64     `json:"api_key_id"`
	AccountID           int64     `json:"account_id"`
	GroupID             int64     `json:"group_id"`
	Model               string    `json:"model"`
	Endpoint            string    `json:"endpoint"`
	Protocol            string    `json:"protocol"`
	HTTPStatus          int       `json:"http_status"`
	Status              string    `json:"status"`
	Reason              string    `json:"reason"`
	TextWritten         bool      `json:"text_written"`
	ReasoningWritten    bool      `json:"reasoning_written"`
	ToolWritten         bool      `json:"complete_tool_written"`
	UpstreamContentSeen bool      `json:"upstream_content_seen"`
	UpstreamTerminal    string    `json:"upstream_terminal,omitempty"`
	Terminal            string    `json:"terminal,omitempty"`
	TerminalWritten     bool      `json:"terminal_written"`
	WriteFailed         bool      `json:"write_failed"`
	ClientDisconnected  bool      `json:"client_disconnected"`
	UsagePresent        bool      `json:"usage_present"`
	FirstOutputMs       *int64    `json:"first_output_ms,omitempty"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at"`
	// These fields are resolved read-only from existing receipts at query time.
	SettlementState string  `json:"settlement_state"`
	ReceiptID       *int64  `json:"receipt_id,omitempty"`
	ChargedAmount   *string `json:"charged_amount,omitempty"`
}

type ResponseAuditFilter struct {
	From, To                           time.Time
	Status, Model, Endpoint, RequestID string
	UserID, APIKeyID, AccountID        int64
	Page, PageSize                     int
}

type ResponseAuditStats struct {
	Total                int64            `json:"total"`
	Counts               map[string]int64 `json:"counts"`
	MissingTerminal      int64            `json:"missing_terminal"`
	WriteFailed          int64            `json:"write_failed"`
	EmptyChargedReceipts int64            `json:"empty_charged_receipts"`
	ObservationStartedAt *time.Time       `json:"observation_started_at"`
	ProcessStartedAt     time.Time        `json:"process_started_at"`
	WriteFailures        uint64           `json:"write_failures_since_start"`
	Dropped              uint64           `json:"dropped_since_start"`
}

type ResponseAuditUsageKey struct {
	APIKeyID  int64
	RequestID string
}
type ResponseAuditRepository interface {
	Save(context.Context, *ResponseAudit) error
	List(context.Context, ResponseAuditFilter) ([]ResponseAudit, int64, error)
	Get(context.Context, int64) (*ResponseAudit, error)
	Stats(context.Context, ResponseAuditFilter) (*ResponseAuditStats, error)
	Lookup(context.Context, []ResponseAuditUsageKey) (map[ResponseAuditUsageKey]*ResponseAudit, error)
	Cleanup(context.Context, time.Time) error
}

type ResponseAuditService struct {
	repo       ResponseAuditRepository
	pool       *UsageRecordWorkerPool
	started    time.Time
	failures   atomic.Uint64
	dropped    atomic.Uint64
	cancel     context.CancelFunc
	workCtx    context.Context
	workCancel context.CancelFunc
	wg         sync.WaitGroup
}

func NewResponseAuditService(repo ResponseAuditRepository, cfg *config.Config) *ResponseAuditService {
	// Separate pool; use existing concurrency/timeout defaults, but drop instead
	// of synchronous overflow so optional observation never stalls inference.
	opts := usageRecordPoolOptionsFromConfig(cfg)
	opts.OverflowPolicy = config.UsageRecordOverflowPolicyDrop
	opts.AutoScaleEnabled = false
	// Leave database capacity for settlement; observation is a small bounded consumer.
	opts.WorkerCount = min(opts.WorkerCount, 2)
	opts.QueueSize = min(opts.QueueSize, 2048)
	opts.TaskTimeout = min(opts.TaskTimeout, 2*time.Second)
	s := &ResponseAuditService{repo: repo, pool: NewUsageRecordWorkerPoolWithOptions(opts), started: time.Now().UTC()}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.workCtx, s.workCancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				if err := repo.Cleanup(cleanCtx, time.Now().AddDate(0, 0, -30)); err != nil {
					logger.L().Warn("response_audit.cleanup_failed", zap.Error(err))
				}
				stop()
			}
		}
	}()
	return s
}

func (s *ResponseAuditService) Stop() {
	if s != nil {
		s.cancel()
		s.wg.Wait()
		// Briefly drain healthy observation; cancel SQL and skip queued work if
		// storage is unavailable. Optional audit must not delay process shutdown.
		timer := time.AfterFunc(2*time.Second, s.workCancel)
		s.pool.Stop()
		timer.Stop()
		s.workCancel()
	}
}
func (s *ResponseAuditService) Submit(a ResponseAudit) {
	if s == nil {
		return
	}
	// Immutable value with only a scalar pointer. No bodies or shared parser state.
	if a.FirstOutputMs != nil {
		v := *a.FirstOutputMs
		a.FirstOutputMs = &v
	}
	mode := s.pool.Submit(func(ctx context.Context) {
		if s.workCtx.Err() != nil {
			s.dropped.Add(1)
			return
		}
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(s.workCtx, cancel)
		defer func() { stop(); cancel() }()
		var err error
		for i := 0; i < 3; i++ {
			err = s.repo.Save(ctx, &a)
			if err == nil {
				return
			}
			select {
			case <-ctx.Done():
				i = 3
			case <-time.After(time.Duration(i+1) * 100 * time.Millisecond):
			}
		}
		s.failures.Add(1)
		logger.L().Warn("response_audit.write_failed", zap.String("audit_request_id", a.AuditRequestID), zap.Int("turn", a.Turn), zap.Error(err))
	})
	if mode.Dropped() {
		s.dropped.Add(1)
	}
}
func (s *ResponseAuditService) List(ctx context.Context, f ResponseAuditFilter) ([]ResponseAudit, int64, error) {
	return s.repo.List(ctx, f)
}
func (s *ResponseAuditService) Get(ctx context.Context, id int64) (*ResponseAudit, error) {
	return s.repo.Get(ctx, id)
}
func (s *ResponseAuditService) Lookup(ctx context.Context, k []ResponseAuditUsageKey) (map[ResponseAuditUsageKey]*ResponseAudit, error) {
	return s.repo.Lookup(ctx, k)
}
func (s *ResponseAuditService) Stats(ctx context.Context, f ResponseAuditFilter) (*ResponseAuditStats, error) {
	a, err := s.repo.Stats(ctx, f)
	if err == nil {
		a.ProcessStartedAt = s.started
		a.WriteFailures = s.failures.Load()
		a.Dropped = s.dropped.Load()
	}
	return a, err
}

type responseAuditContextKey struct{}
type auditTool struct {
	name, id, args        string
	overflow, emptyObject bool
}
type auditEvidence struct {
	argsBytes                                                      int
	text, reasoning, tool, opaque, invalid, failed, normal, finish bool
	terminal                                                       string
	tools                                                          map[string]*auditTool
}
type ResponseAuditSession struct {
	mu                 sync.Mutex
	svc                *ResponseAuditService
	base               ResponseAudit
	current            ResponseAudit
	down, up           auditEvidence
	buffer, data       []byte
	event              string
	stream, ws, closed bool
}

func WithResponseAudit(ctx context.Context, svc *ResponseAuditService, base ResponseAudit, ws bool) (context.Context, *ResponseAuditSession) {
	base.AuditRequestID = uuid.NewString()
	base.StartedAt = time.Now().UTC()
	if ws {
		base.Turn = 1
	}
	s := &ResponseAuditSession{svc: svc, base: base, current: base, ws: ws}
	return context.WithValue(ctx, responseAuditContextKey{}, s), s
}
func ResponseAuditFromContext(ctx context.Context) *ResponseAuditSession {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(responseAuditContextKey{}).(*ResponseAuditSession)
	return s
}
func (s *ResponseAuditSession) BeginTurn(turn int, model string) {
	if s == nil || !s.ws {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn <= s.current.Turn {
		if model != "" {
			s.current.Model = model
		}
		return
	}
	if !s.closed {
		s.finishLocked(0, false)
	}
	s.current = s.base
	s.current.Turn = turn
	s.current.Model = model
	s.current.StartedAt = time.Now().UTC()
	s.down = auditEvidence{}
	s.up = auditEvidence{}
	s.closed = false
}
func (s *ResponseAuditSession) Attribute(accountID, groupID int64, model string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if accountID > 0 {
		s.current.AccountID = accountID
	}
	if groupID > 0 {
		s.current.GroupID = groupID
	}
	if model != "" {
		s.current.Model = model
	}
}
func (s *ResponseAuditSession) LinkUsage(apiKeyID int64, usageID, upstreamID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.current.APIKeyID = apiKeyID
	s.current.UsageRequestID = usageID
	s.current.UpstreamRequestID = upstreamID
}
func (s *ResponseAuditSession) UpstreamRequestID(id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.current.UpstreamRequestID = auditBound(id, 128)
	}
}
func (s *ResponseAuditSession) Upstream(payload []byte, event string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if id := firstValidTrimmedGJSONString(payload, "response.id", "message.id", "id"); id != "" {
		s.current.UpstreamRequestID = auditBound(id, 128)
	}
	s.observe(&s.up, payload, event)
}
func (s *ResponseAuditSession) WSWrite(payload []byte, err error) {
	if s == nil || !s.ws {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	id := firstValidTrimmedGJSONString(payload, "response.id", "id")
	if id != "" {
		s.current.UpstreamRequestID = id
		if s.current.UsageRequestID == "" {
			s.current.UsageRequestID = id
		}
	}
	s.observe(&s.up, payload, "")
	if err != nil {
		s.current.WriteFailed = true
		s.finishLocked(101, true)
		return
	}
	s.observe(&s.down, payload, "")
	s.markOutputLocked()
	if s.down.terminal != "" {
		s.finishLocked(101, false)
	}
}
func ObserveResponseAuditWSWrite(ctx context.Context, payload []byte, err error) {
	ResponseAuditFromContext(ctx).WSWrite(payload, err)
}
func (s *ResponseAuditSession) Write(p []byte, n int, err error, stream bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.down.terminal != "" {
		return
	}
	s.stream = stream
	if err != nil || n < len(p) {
		s.current.WriteFailed = true
	}
	if n <= 0 {
		return
	}
	p = p[:n]
	if len(s.buffer)+len(p) > responseAuditMaxBytes {
		s.down.opaque = true
		s.buffer = nil
		return
	}
	s.buffer = append(s.buffer, p...)
	if !stream {
		return
	}
	for {
		i := strings.IndexByte(string(s.buffer), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSuffix(string(s.buffer[:i]), "\r")
		s.buffer = s.buffer[i+1:]
		if line == "" {
			s.flushEventLocked()
		} else if strings.HasPrefix(line, "event:") {
			s.event = strings.TrimSpace(line[6:])
		} else if strings.HasPrefix(line, "data:") {
			if len(s.data)+len(line) > responseAuditMaxBytes {
				s.down.opaque = true
				s.data = nil
			} else {
				if len(s.data) > 0 {
					s.data = append(s.data, '\n')
				}
				s.data = append(s.data, strings.TrimPrefix(line[5:], " ")...)
			}
		}
	}
}
func (s *ResponseAuditSession) flushEventLocked() {
	if len(s.data) > 0 {
		s.observe(&s.down, s.data, s.event)
		s.markOutputLocked()
	}
	s.data = nil
	s.event = ""
}
func (s *ResponseAuditSession) markOutputLocked() {
	if (s.down.text || s.down.reasoning || s.down.tool) && s.current.FirstOutputMs == nil {
		v := time.Since(s.current.StartedAt).Milliseconds()
		s.current.FirstOutputMs = &v
	}
}
func (s *ResponseAuditSession) Finish(status int, disconnected bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if !s.ws {
		if s.stream {
			if len(s.buffer) > 0 || len(s.data) > 0 {
				s.down.invalid = true
			}
		} else if len(s.buffer) > 0 {
			s.observe(&s.down, s.buffer, "")
			s.markOutputLocked()
		}
	}
	s.finishLocked(status, disconnected)
}
func (s *ResponseAuditSession) finishLocked(status int, disconnected bool) {
	s.closed = true
	// Metadata has database limits; bound it before queueing without retaining bodies.
	s.current.Model = auditBound(s.current.Model, 100)
	s.current.Endpoint = auditBound(s.current.Endpoint, 128)
	s.current.RequestID = auditBound(s.current.RequestID, 64)
	s.current.ClientRequestID = auditBound(s.current.ClientRequestID, 64)
	s.current.UsageRequestID = auditBound(s.current.UsageRequestID, 128)
	s.current.UpstreamRequestID = auditBound(s.current.UpstreamRequestID, 128)
	s.current.FinishedAt = time.Now().UTC()
	s.current.HTTPStatus = status
	a := &s.current
	e := &s.down
	a.TextWritten = e.text
	a.ReasoningWritten = e.reasoning
	a.ToolWritten = e.tool
	a.Terminal = e.terminal
	a.TerminalWritten = e.terminal != "" && !a.WriteFailed
	a.UpstreamContentSeen = s.up.text || s.up.reasoning || s.up.tool
	a.UpstreamTerminal = s.up.terminal
	// A close after the successful terminal is ordinary connection teardown.
	a.ClientDisconnected = disconnected && !e.normal
	useful := e.text || e.reasoning || e.tool
	failure := e.failed || a.WriteFailed || a.ClientDisconnected || e.invalid || status >= 400 || !e.normal
	switch {
	case useful && failure:
		a.Status = "partial_failure"
		a.Reason = "output_before_failure"
	case useful:
		a.Status = "success"
		a.Reason = "completed_with_output"
	case e.opaque:
		a.Status = "unknown"
		a.Reason = "unsupported_or_oversized_output"
	case failure:
		a.Status = "failed"
		a.Reason = "failed_before_output"
	default:
		a.Status = "empty"
		a.Reason = "completed_without_output"
	}
	if a.WriteFailed {
		a.Reason = "downstream_write_failed"
	} else if a.ClientDisconnected {
		a.Reason = "client_disconnected"
	} else if !e.normal && !e.failed && status < 400 && !e.opaque {
		a.Reason = "missing_terminal"
	}
	s.svc.Submit(*a)
	s.buffer = nil
	s.data = nil
	s.down.tools = nil
	s.up.tools = nil
}

func auditBound(v string, limit int) string {
	runes := []rune(v)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return v
}

func auditNonempty(v gjson.Result) bool {
	return v.Type == gjson.String && strings.TrimSpace(v.String()) != ""
}
func auditValidTool(name, id, args string) bool {
	return strings.TrimSpace(name) != "" && strings.TrimSpace(id) != "" && gjson.Valid(args) && gjson.Parse(args).IsObject()
}
func (s *ResponseAuditSession) observe(e *auditEvidence, p []byte, event string) {
	if len(p) > responseAuditMaxBytes {
		e.opaque = true
		return
	}
	if strings.TrimSpace(string(p)) == "[DONE]" {
		if e.finish {
			e.normal = true
			e.terminal = "[DONE]"
		} else {
			e.invalid = true
		}
		return
	}
	if !gjson.ValidBytes(p) {
		if s.ws {
			e.opaque = true
		}
		e.invalid = true
		return
	}
	r := gjson.ParseBytes(p)
	t := r.Get("type").String()
	if t == "" {
		t = event
	}
	if r.Get("usage").Exists() || r.Get("response.usage").Exists() || r.Get("message.usage").Exists() {
		s.current.UsagePresent = true
	}
	if (r.Get("error").Exists() && r.Get("error").Type != gjson.Null) || (r.Get("response.error").Exists() && r.Get("response.error").Type != gjson.Null) || t == "error" || t == "response.failed" || t == "response.incomplete" || t == "response.cancelled" || t == "response.canceled" {
		s.observeResponseItems(e, r.Get("response.output"))
		s.observeResponseItems(e, r.Get("output"))
		e.failed = true
		e.terminal = t
		if e.terminal == "" {
			e.terminal = "error"
		}
		return
	}
	if t == "message_stop" {
		e.normal = true
		e.terminal = t
		return
	}
	if t == "response.completed" || t == "response.done" {
		e.normal = true
		e.terminal = t
		s.observeResponseItems(e, r.Get("response.output"))
		return
	}
	if strings.HasPrefix(t, "response.output_text.") || strings.HasPrefix(t, "response.refusal.") {
		if auditNonempty(r.Get("delta")) || auditNonempty(r.Get("text")) || auditNonempty(r.Get("refusal")) {
			e.text = true
		}
		return
	}
	if strings.Contains(t, "reasoning") {
		if auditNonempty(r.Get("delta")) || auditNonempty(r.Get("text")) {
			e.reasoning = true
		}
		return
	}
	if t == "content_block_delta" {
		d := r.Get("delta")
		switch d.Get("type").String() {
		case "text_delta":
			e.text = e.text || auditNonempty(d.Get("text"))
		case "thinking_delta":
			e.reasoning = e.reasoning || auditNonempty(d.Get("thinking"))
		case "signature_delta":
		case "input_json_delta":
			s.toolFragment(e, r.Get("index").Raw, "", "", d.Get("partial_json").String(), false)
		default:
			e.opaque = true
		}
		return
	}
	if t == "content_block_start" {
		b := r.Get("content_block")
		key := r.Get("index").Raw
		switch b.Get("type").String() {
		case "text":
			e.text = e.text || auditNonempty(b.Get("text"))
		case "thinking":
			e.reasoning = e.reasoning || auditNonempty(b.Get("thinking"))
		case "tool_use":
			s.toolFragment(e, key, b.Get("name").String(), b.Get("id").String(), "", false)
			if x := e.tools[key]; x != nil {
				x.emptyObject = b.Get("input").IsObject()
			}
			if b.Get("input").IsObject() && len(b.Get("input").Map()) > 0 {
				s.toolFragment(e, key, "", "", b.Get("input").Raw, false)
			}
		default:
			e.opaque = true
		}
		return
	}
	if t == "content_block_stop" {
		s.toolFragment(e, r.Get("index").Raw, "", "", "", true)
		return
	}
	if t == "response.output_item.done" {
		s.observeResponseItems(e, gjson.Parse("["+r.Get("item").Raw+"]"))
		return
	}
	if t == "response.function_call_arguments.delta" {
		s.toolFragment(e, r.Get("item_id").String(), "", "", r.Get("delta").String(), false)
		return
	}
	if t == "response.output_item.added" {
		i := r.Get("item")
		if i.Get("type").String() == "function_call" {
			s.toolFragment(e, i.Get("id").String(), i.Get("name").String(), i.Get("call_id").String(), "", false)
		}
		return
	}
	if choices := r.Get("choices"); choices.IsArray() {
		for _, c := range choices.Array() {
			d := c.Get("delta")
			if !d.Exists() {
				d = c.Get("message")
			}
			s.observeChatMessage(e, d)
			if c.Get("finish_reason").Type == gjson.String {
				e.finish = true
				if c.Get("message").Exists() {
					e.normal = true
					e.terminal = "finish_reason"
				}
				for key := range e.tools {
					s.toolFragment(e, key, "", "", "", true)
				}
			}
		}
		return
	}
	if t == "message" {
		for _, b := range r.Get("content").Array() {
			s.observeAnthropicBlock(e, b)
		}
		if auditNonempty(r.Get("stop_reason")) {
			e.normal = true
			e.terminal = r.Get("stop_reason").String()
		}
		return
	}
	if r.Get("object").String() == "response" || r.Get("output").IsArray() {
		s.observeResponseItems(e, r.Get("output"))
		switch r.Get("status").String() {
		case "completed":
			e.normal = true
			e.terminal = "completed"
		case "failed", "incomplete", "cancelled", "canceled":
			e.failed = true
			e.terminal = r.Get("status").String()
		}
		return
	}
	switch t {
	case "ping", "message_start", "message_delta", "response.created", "response.in_progress", "response.content_part.added", "response.content_part.done", "response.function_call_arguments.done":
		return
	}
	// Explicitly unknown output must not be labelled empty.
	e.opaque = true
}
func (s *ResponseAuditSession) observeAnthropicBlock(e *auditEvidence, b gjson.Result) {
	switch b.Get("type").String() {
	case "text":
		e.text = e.text || auditNonempty(b.Get("text"))
	case "thinking":
		e.reasoning = e.reasoning || auditNonempty(b.Get("thinking"))
	case "tool_use":
		e.tool = e.tool || auditValidTool(b.Get("name").String(), b.Get("id").String(), b.Get("input").Raw)
	default:
		e.opaque = true
	}
}
func (s *ResponseAuditSession) observeResponseItems(e *auditEvidence, items gjson.Result) {
	for _, i := range items.Array() {
		switch i.Get("type").String() {
		case "message":
			for _, b := range i.Get("content").Array() {
				switch b.Get("type").String() {
				case "output_text", "text":
					e.text = e.text || auditNonempty(b.Get("text"))
				case "refusal":
					e.text = e.text || auditNonempty(b.Get("refusal"))
				default:
					e.opaque = true
				}
			}
		case "function_call":
			e.tool = e.tool || auditValidTool(i.Get("name").String(), i.Get("call_id").String(), i.Get("arguments").String())
		case "custom_tool_call":
			e.tool = e.tool || (auditNonempty(i.Get("name")) && auditNonempty(i.Get("call_id")) && i.Get("input").Type == gjson.String)
		case "reasoning":
			for _, b := range i.Get("summary").Array() {
				e.reasoning = e.reasoning || auditNonempty(b.Get("text"))
			}
			if i.Get("encrypted_content").Exists() && !e.reasoning {
				e.opaque = true
			}
		default:
			e.opaque = true
		}
	}
}
func (s *ResponseAuditSession) observeChatMessage(e *auditEvidence, d gjson.Result) {
	e.text = e.text || auditNonempty(d.Get("content")) || auditNonempty(d.Get("refusal"))
	e.reasoning = e.reasoning || auditNonempty(d.Get("reasoning_content")) || auditNonempty(d.Get("reasoning"))
	if d.Get("content").IsArray() {
		for _, b := range d.Get("content").Array() {
			if b.Get("type").String() == "text" {
				e.text = e.text || auditNonempty(b.Get("text"))
			} else {
				e.opaque = true
			}
		}
	}
	for _, t := range d.Get("tool_calls").Array() {
		f := t.Get("function")
		if t.Get("index").Exists() {
			s.toolFragment(e, t.Get("index").Raw, f.Get("name").String(), t.Get("id").String(), f.Get("arguments").String(), false)
		} else {
			e.tool = e.tool || auditValidTool(f.Get("name").String(), t.Get("id").String(), f.Get("arguments").String())
		}
	}
	if f := d.Get("function_call"); f.Exists() {
		s.toolFragment(e, "legacy", f.Get("name").String(), "legacy", f.Get("arguments").String(), false)
	}
}
func (s *ResponseAuditSession) toolFragment(e *auditEvidence, key, name, id, args string, done bool) {
	if len(key) > 128 || len(name) > 128 || len(id) > 128 {
		e.opaque = true
		return
	}
	if e.tools == nil {
		if done {
			return
		}
		e.tools = make(map[string]*auditTool)
	}
	x := e.tools[key]
	if x == nil {
		if done {
			return
		}
		if len(e.tools) >= 64 {
			e.opaque = true
			return
		}
		x = &auditTool{}
		e.tools[key] = x
	}
	if name != "" {
		x.name = name
	}
	if id != "" {
		x.id = id
	}
	if e.argsBytes+len(args) > responseAuditMaxBytes {
		x.overflow = true
		e.opaque = true
	} else if !x.overflow {
		x.args += args
		e.argsBytes += len(args)
	}
	if done && !x.overflow {
		a := x.args
		if a == "" && x.emptyObject {
			a = "{}"
		}
		e.tool = e.tool || auditValidTool(x.name, x.id, a)
		e.argsBytes -= len(x.args)
		delete(e.tools, key)
	}
}

// Used only by response-model observation hooks and standalone WS writers.
func observeResponseAuditUpstream(ctx context.Context, p []byte, event string) {
	ResponseAuditFromContext(ctx).Upstream(p, event)
}

// Marshal keeps the independent evidence representation explicit and bounded.
func MarshalResponseAudit(a *ResponseAudit) ([]byte, error) { return json.Marshal(a) }
