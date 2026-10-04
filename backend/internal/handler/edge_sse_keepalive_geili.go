package handler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// geili: 经 Cloudflare 进入的流式请求，在上游久久没有响应时替客户端保持连接。
//
// 背景（2026-09-30）：中转站账号限流时每次要挂 ~31 秒才返回 429，网关在账号上重试、换号都可能
// 花上几分钟。这期间没有任何字节回给客户端，Cloudflare 在约 123 秒（网关入口起算）后主动断开，
// 用户看到 524、工作被打断，而上游若恰好在此后成功，结果也无人接收。
//
// 原则：慢可以，但不由网关替客户端放弃。所以这里不设任何总时限、不改重试与换号策略，
// 只在请求静默超过 edgeKeepaliveFirstBeat 后，按 gateway.stream_keepalive_interval 补发
// 协议自带的静默帧，让 Cloudflare 与 nginx 一直看到字节在流动。
//
// 覆盖范围（2026-10-05 扩面）：原先只保 /v1/messages，而生产上最多的是 Codex 走
// /responses（`/v1/responses` 与 `/backend-api/codex/responses`）与 /chat/completions。
// OpenAI 侧虽然在下游透传路径（openai_gateway_passthrough.go）也有一层心跳，但它起于
// 「上游响应头已到达」之后；选账号、429 挂起、重试、换号这段静默之前无人负责——那正是
// 用户看到 524 / "stream idle for 3m" 的窗口。本写入器装在中间件层，早于选账号，是这段
// 静默的唯一兜底，因此必须按入站协议发对帧。
//
// 设计要点：
//   - 请求侧看到的仍是「什么都没写」：Size/Written/Status 扣除心跳，Header() 是私有副本，
//     failover 的「已写出字节则禁止换号」判定、多分组 Key 的跨组重试都不受心跳影响。
//   - 心跳线程只碰线上的 Header 表，请求线程只碰私有表，二者不会并发读写同一个 map。
//   - 请求线程第一次真正写响应时，心跳永久停止。若此时线上已被心跳提交为 200，
//     错误响应（状态码 >= 400）改写成该协议的流内终止帧，否则客户端会收到夹在 SSE 里的裸 JSON。
//   - 首拍延迟 45 秒：绝大多数硬错误（鉴权、参数、快速限流）在此之前返回，仍走原来的 HTTP 状态码。
//
// 帧选择（必须按入站路径，而不是按分组平台）：客户端按它自己说的协议解析响应，
// 经 /v1/messages 进来的 OpenAI 平台分组，回给客户的仍必须是 Anthropic 帧。
const (
	edgeKeepaliveCtxKey    = "geili_edge_sse_keepalive"
	edgeKeepalivePingFrame = "event: ping\ndata: {\"type\": \"ping\"}\n\n"
	// edgeKeepaliveCommentFrame 是 OpenAI 系的静默帧：SSE 注释行。
	// eventsource/官方 SDK 在解析层直接丢弃，不进事件流，也不会被当成终止事件；
	// 与 openai_compact_sse_keepalive.go 用的是同一形态。
	edgeKeepaliveCommentFrame = ": keepalive\n\n"
)

// edgeKeepaliveProtocol 是入站协议的种类，决定心跳帧与流内错误帧的形态。
type edgeKeepaliveProtocol int

const (
	// edgeProtocolNone 表示该请求不装保活（非候选路径）。
	edgeProtocolNone edgeKeepaliveProtocol = iota
	// edgeProtocolAnthropic 覆盖 /v1/messages（Anthropic Messages 协议）。
	edgeProtocolAnthropic
	// edgeProtocolOpenAIResponses 覆盖 /responses 与 /responses/*（OpenAI Responses 协议）。
	edgeProtocolOpenAIResponses
	// edgeProtocolOpenAIChat 覆盖 /chat/completions（OpenAI Chat Completions 协议）。
	edgeProtocolOpenAIChat
)

// edgeKeepaliveFirstBeat 是变量而非常量，仅为测试缩短等待。
var edgeKeepaliveFirstBeat = 45 * time.Second

// EdgeSSEKeepalive 为经 Cloudflare 进入的流式 POST 请求安装保活写入器。
// 写入器本身是透明的，只有 handler 确认是流式请求后调用 ArmEdgeSSEKeepalive 才会开始心跳。
// gateway.stream_keepalive_interval 为 0 时整体关闭。
func EdgeSSEKeepalive(cfg *config.Config) gin.HandlerFunc {
	return NewEdgeSSEKeepalive(cfg, edgeKeepaliveFirstBeat)
}

// NewEdgeSSEKeepalive 与 EdgeSSEKeepalive 相同，但可指定首拍延迟（供跨包测试缩短等待）。
func NewEdgeSSEKeepalive(cfg *config.Config, firstBeat time.Duration) gin.HandlerFunc {
	interval := time.Duration(0)
	if cfg != nil && cfg.Gateway.StreamKeepaliveInterval > 0 {
		interval = time.Duration(cfg.Gateway.StreamKeepaliveInterval) * time.Second
	}
	return func(c *gin.Context) {
		protocol := edgeKeepaliveProtocolFor(c.Request)
		if interval <= 0 || protocol == edgeProtocolNone {
			c.Next()
			return
		}
		original := c.Writer
		w := newEdgeKeepaliveWriter(original, time.Now(), firstBeat, interval,
			c.Request.Context().Done(), logger.FromContext(c.Request.Context()))
		w.protocol = protocol
		w.ctx = c
		c.Writer = w
		c.Set(edgeKeepaliveCtxKey, w)
		defer func() {
			w.finish()
			if c.Writer == w {
				c.Writer = original
			}
		}()
		c.Next()
	}
}

// edgeKeepaliveCandidate 判断请求是否经 Cloudflare 进入且命中受保活的流式端点。
// 生产站点 nginx 对非 CF 来源直接返回 444，且不会剥离 Cf-Ray；直连域名
// （sub-direct / image-direct）没有这个头，也没有 CF 的等待窗口。
func edgeKeepaliveCandidate(r *http.Request) bool {
	return edgeKeepaliveProtocolFor(r) != edgeProtocolNone
}

// edgeKeepaliveProtocolFor 解析请求应使用哪种静默帧；不属于候选时返回 edgeProtocolNone。
//
// 路径判定按「后缀 + 子路径」而不是等值比较，以覆盖通配与别名的各种写法：
//   - /v1/messages、/v1/messages/            → Anthropic
//   - /v1/responses、/responses、/backend-api/codex/responses、/responses/compact → Responses
//   - /v1/chat/completions、/chat/completions → Chat Completions
//
// /messages/count_tokens 与 /responses/count_tokens 是同步端点，不装心跳：
// 前者不以 /messages 结尾，后者由 Responses 分支显式排除。
func edgeKeepaliveProtocolFor(r *http.Request) edgeKeepaliveProtocol {
	if r == nil || r.Method != http.MethodPost {
		return edgeProtocolNone
	}
	if strings.TrimSpace(r.Header.Get("Cf-Ray")) == "" {
		return edgeProtocolNone
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return edgeProtocolNone
	}
	path := strings.TrimRight(r.URL.Path, "/")
	switch {
	case strings.HasSuffix(path, "/messages"):
		return edgeProtocolAnthropic
	case strings.HasSuffix(path, "/count_tokens"):
		return edgeProtocolNone
	case strings.HasSuffix(path, "/chat/completions"):
		return edgeProtocolOpenAIChat
	case strings.HasSuffix(path, "/responses"), strings.Contains(path, "/responses/"):
		return edgeProtocolOpenAIResponses
	default:
		return edgeProtocolNone
	}
}

// ArmEdgeSSEKeepalive 在 handler 确认请求是流式时启动心跳；未安装写入器或非流式请求时为 no-op，可重复调用。
func ArmEdgeSSEKeepalive(c *gin.Context, stream bool) {
	if c == nil || !stream {
		return
	}
	if v, ok := c.Get(edgeKeepaliveCtxKey); ok {
		if w, ok := v.(*edgeKeepaliveWriter); ok && w != nil {
			w.arm()
		}
	}
}

// EdgeSSEKeepaliveCommitted 报告本请求的边界心跳是否已把响应提交为 200。
// 写回方据此判断错误还能不能用 HTTP 状态码表达；没有保活写入器时恒为 false。
func EdgeSSEKeepaliveCommitted(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, ok := c.Get(edgeKeepaliveCtxKey)
	if !ok {
		return false
	}
	w, ok := v.(*edgeKeepaliveWriter)
	if !ok || w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hbCommitted
}

type edgeKeepaliveWriter struct {
	gin.ResponseWriter // 线上一侧

	mu        sync.Mutex
	header    http.Header // 请求线程看到的私有 Header；创建时复制线上已有的头（CORS、请求 ID 等）
	status    int         // 请求线程记录的状态码，0 表示未设置
	ingress   time.Time
	firstBeat time.Time
	interval  time.Duration
	reqDone   <-chan struct{}
	log       *zap.Logger

	protocol edgeKeepaliveProtocol
	ctx      *gin.Context // 仅用于合成流内终止帧时读取响应 id / 模型名

	armed        bool
	stopped      bool
	hbCommitted  bool // 心跳已把线上响应提交为 200 + SSE
	hbBytes      int
	reqCommitted bool // 请求线程已开始写真实响应
	errorMode    bool // 线上已是 200：后续错误响应要改写成该协议的终止帧
	errFrameSent bool
	stop         chan struct{}
}

func newEdgeKeepaliveWriter(inner gin.ResponseWriter, ingress time.Time, firstBeatDelay, interval time.Duration, reqDone <-chan struct{}, log *zap.Logger) *edgeKeepaliveWriter {
	return &edgeKeepaliveWriter{
		ResponseWriter: inner,
		header:         inner.Header().Clone(),
		ingress:        ingress,
		firstBeat:      ingress.Add(firstBeatDelay),
		interval:       interval,
		reqDone:        reqDone,
		log:            log,
		stop:           make(chan struct{}),
	}
}

// CopyContextForAttempt 等同于 c.Copy()，但与心跳互斥：Copy 会读取 gin 内部的 writer 记账字段（size/status），
// 而心跳线程写响应时会修改它们。多分组 Key 的每次尝试都要 Copy，必须在心跳静止的瞬间进行。
func CopyContextForAttempt(c *gin.Context) *gin.Context {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(edgeKeepaliveCtxKey); ok {
		if w, ok := v.(*edgeKeepaliveWriter); ok && w != nil {
			w.mu.Lock()
			defer w.mu.Unlock()
		}
	}
	return c.Copy()
}

func (w *edgeKeepaliveWriter) arm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.armed || w.stopped || w.reqCommitted {
		return
	}
	w.armed = true
	go w.run()
}

func (w *edgeKeepaliveWriter) run() {
	delay := time.Until(w.firstBeat)
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-w.reqDone:
			return
		case <-timer.C:
		}
		if !w.beat() {
			return
		}
		timer.Reset(w.interval)
	}
}

// pingFrame 返回本请求所用协议的静默帧。
//
// 零值（未显式设置 protocol，例如测试直接构造写入器）按 Anthropic 处理：中间件安装写入器时
// 一定会赋上非 None 的协议，所以零值只可能来自未走中间件的构造路径，保持历史行为最安全。
func (w *edgeKeepaliveWriter) pingFrame() string {
	if w.protocol == edgeProtocolOpenAIResponses || w.protocol == edgeProtocolOpenAIChat {
		// OpenAI 系（Responses / Chat Completions）统一用 SSE 注释行：
		// 官方 SDK 与 eventsource 都会忽略，不会被误判为终止事件。
		return edgeKeepaliveCommentFrame
	}
	return edgeKeepalivePingFrame
}

// beat 在锁内提交（首次）线上响应头并写出一帧心跳；返回 false 表示心跳应当结束。
func (w *edgeKeepaliveWriter) beat() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.reqCommitted {
		return false
	}
	if !w.hbCommitted {
		wire := w.ResponseWriter.Header()
		wire.Set("Content-Type", "text/event-stream")
		wire.Set("Cache-Control", "no-cache")
		wire.Set("X-Accel-Buffering", "no")
		wire.Del("Content-Length")
		w.ResponseWriter.WriteHeader(http.StatusOK)
		w.hbCommitted = true
		if w.log != nil {
			w.log.Info("gateway.edge_keepalive_started",
				zap.Duration("silent_for", time.Since(w.ingress)),
				zap.Int("protocol", int(w.protocol)),
			)
		}
	}
	n, err := w.ResponseWriter.Write([]byte(w.pingFrame()))
	w.hbBytes += n
	if err != nil {
		w.stopLocked()
		return false
	}
	w.ResponseWriter.Flush()
	return true
}

func (w *edgeKeepaliveWriter) stopLocked() {
	if w.stopped {
		return
	}
	w.stopped = true
	close(w.stop)
}

// commitRequestLocked 标记请求线程开始写真实响应，并停止心跳。线上还没有任何字节时，
// 把私有 Header 交给线上并按请求记录的状态码提交，与未包装时的行为一致。
func (w *edgeKeepaliveWriter) commitRequestLocked() {
	w.stopLocked()
	if w.reqCommitted {
		return
	}
	w.reqCommitted = true
	if w.hbCommitted {
		w.errorMode = w.status >= http.StatusBadRequest
		return
	}
	wire := w.ResponseWriter.Header()
	for k := range wire {
		delete(wire, k)
	}
	for k, v := range w.header {
		wire[k] = append([]string(nil), v...)
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(status)
	w.ResponseWriter.WriteHeaderNow()
}

func (w *edgeKeepaliveWriter) Header() http.Header { return w.header }

func (w *edgeKeepaliveWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.reqCommitted && code > 0 {
		w.status = code
	}
}

func (w *edgeKeepaliveWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.commitRequestLocked()
}

func (w *edgeKeepaliveWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.commitRequestLocked()
	if w.errorMode {
		return w.writeErrorFrameLocked(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *edgeKeepaliveWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *edgeKeepaliveWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.commitRequestLocked()
	w.ResponseWriter.Flush()
}

func (w *edgeKeepaliveWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Size 与 Written 只反映请求线程自己写出的内容，心跳字节不计入。
func (w *edgeKeepaliveWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.reqCommitted {
		return -1
	}
	n := w.ResponseWriter.Size()
	if n < 0 {
		return -1
	}
	if n -= w.hbBytes; n < 0 {
		n = 0
	}
	return n
}

func (w *edgeKeepaliveWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.reqCommitted
}

func (w *edgeKeepaliveWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	w.stopLocked()
	w.mu.Unlock()
	return w.ResponseWriter.Hijack()
}

func (w *edgeKeepaliveWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// finish 在 handler 返回后停止心跳。线上已被提交为 200 但 handler 只记录了错误状态码、
// 没有写出错误体时，补一帧终止事件，避免客户端看到没有终止事件的静默断流。
func (w *edgeKeepaliveWriter) finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	// handler 可能只设置了状态码与响应头、没有写正文（如 c.Status(204)）：gin 收尾时只会提交它自己的
	// writermem，写入器里记录的内容必须在这里补提交，否则状态码与响应头会丢失。
	w.commitRequestLocked()
	if w.hbCommitted && !w.errFrameSent && w.status >= http.StatusBadRequest && w.status != 499 {
		_, _ = w.writeErrorFrameLocked(nil)
		w.ResponseWriter.Flush()
	}
}

func (w *edgeKeepaliveWriter) writeErrorFrameLocked(body []byte) (int, error) {
	frame := edgeErrorFrameFor(w.protocol, body, w.status, w.ctx)
	w.errFrameSent = true
	if w.log != nil {
		w.log.Warn("gateway.edge_keepalive_error_frame",
			zap.Int("status", w.status),
			zap.Int("protocol", int(w.protocol)),
			zap.String("error_type", edgeErrorTypeOf(body)),
		)
	}
	if _, err := w.ResponseWriter.Write(frame); err != nil {
		return 0, err
	}
	w.ResponseWriter.Flush()
	return len(body), nil
}

// edgeErrorFrameFor 按入站协议把 handler 写出的错误体渲染成流内终止帧。
//
// 为什么必须分协议：一旦心跳把 200 固化，错误只能走 SSE。Anthropic 客户端认 `event: error`；
// OpenAI Responses 的严格 SDK（Codex CLI）只认 response.completed/failed/incomplete/cancelled，
// 收到通用 error 帧会报 "stream closed before response.completed"；Chat Completions 客户端
// 认 `event: error` + `{"error":{...}}`。
func edgeErrorFrameFor(protocol edgeKeepaliveProtocol, body []byte, status int, c *gin.Context) []byte {
	switch protocol {
	case edgeProtocolOpenAIResponses:
		return edgeResponsesFailedFrame(body, status, c)
	case edgeProtocolOpenAIChat:
		return edgeChatCompletionsErrorFrame(body, status)
	default:
		// 含零值：未显式设置协议的构造路径按 Anthropic 处理，与 pingFrame 保持一致。
		return edgeSSEErrorFrame(body, status)
	}
}

// edgeSSEErrorFields 从错误体里抽取 (type, code, message)，兼容网关两种既有形态：
// Anthropic 的 `{"type":"error","error":{...}}` 与 OpenAI 的 `{"error":{...}}`。
func edgeSSEErrorFields(body []byte, status int) (errType, code, message string) {
	payload := bytes.TrimSpace(body)
	errType = strings.TrimSpace(gjson.GetBytes(payload, "error.type").String())
	if errType == "" {
		if t := strings.TrimSpace(gjson.GetBytes(payload, "type").String()); t != "" && t != "error" {
			errType = t
		}
	}
	if errType == "" {
		errType = "api_error"
	}
	code = strings.TrimSpace(gjson.GetBytes(payload, "error.code").String())
	message = strings.TrimSpace(gjson.GetBytes(payload, "error.message").String())
	if message == "" {
		message = strings.TrimSpace(string(payload))
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return errType, code, message
}

// edgeErrorTypeOf 只取错误类型用于日志，避免把整个错误体写进日志。
func edgeErrorTypeOf(body []byte) string {
	errType, _, _ := edgeSSEErrorFields(body, http.StatusInternalServerError)
	return errType
}

// edgeResponsesFailedFrame 渲染 OpenAI Responses 的 `response.failed` 终止事件。
// 字段集与 handler 侧 writeResponsesFailedSSE 保持一致，复用同一批结构体，
// 保证严格 SDK 必填字段（created_at / sequence_number）不缺失。
func edgeResponsesFailedFrame(body []byte, status int, c *gin.Context) []byte {
	errType, code, message := edgeSSEErrorFields(body, status)
	event := responsesFailedEvent{
		Type: "response.failed",
		Response: responsesFailedBody{
			ID:        synthesizeResponseID(c),
			Object:    "response",
			CreatedAt: time.Now().Unix(),
			Model:     requestModel(c),
			Status:    "failed",
			Output:    []any{},
			Error: responsesFailedError{
				Code:    mapResponsesErrorCode(errType, code),
				Message: message,
			},
		},
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return []byte("event: response.failed\ndata: {\"type\":\"response.failed\",\"sequence_number\":0,\"response\":{\"status\":\"failed\",\"output\":[],\"error\":{\"code\":\"upstream_error\",\"message\":\"Upstream response failed\"}}}\n\n")
	}
	frame := make([]byte, 0, len(payload)+32)
	frame = append(frame, "event: response.failed\ndata: "...)
	frame = append(frame, payload...)
	frame = append(frame, '\n', '\n')
	return frame
}

// edgeChatCompletionsErrorFrame 渲染 Chat Completions 的流内错误帧。
// 形态与 handler 侧 handleStreamingAwareErrorWithCode 的流内分支一致：
// `event: error` + `{"error":{"type":...,"message":...}}`，有 code 时带上。
func edgeChatCompletionsErrorFrame(body []byte, status int) []byte {
	errType, code, message := edgeSSEErrorFields(body, status)
	errorObject := map[string]any{"type": errType, "message": message}
	if code != "" {
		errorObject["code"] = code
	}
	payload, err := json.Marshal(map[string]any{"error": errorObject})
	if err != nil {
		return []byte("event: error\ndata: {\"error\":{\"type\":\"api_error\",\"message\":\"Upstream request failed\"}}\n\n")
	}
	frame := make([]byte, 0, len(payload)+32)
	frame = append(frame, "event: error\ndata: "...)
	frame = append(frame, payload...)
	frame = append(frame, '\n', '\n')
	return frame
}

// edgeSSEErrorFrame 把 handler 写出的错误体改写成 Anthropic 的流内错误帧：
//
//	event: error
//	data: {"type":"error","error":{...}}
//
// 官方 SDK 只在看到 `event: error` 时才会把它当成错误抛出。
func edgeSSEErrorFrame(body []byte, status int) []byte {
	payload := bytes.TrimSpace(body)
	var compact bytes.Buffer
	switch {
	case len(payload) > 0 && json.Valid(payload) && json.Compact(&compact, payload) == nil && gjson.GetBytes(payload, "type").String() == "error":
		payload = compact.Bytes()
	default:
		message := strings.TrimSpace(gjson.GetBytes(payload, "error.message").String())
		if message == "" {
			message = strings.TrimSpace(string(payload))
		}
		if message == "" {
			message = http.StatusText(status)
		}
		errType := gjson.GetBytes(payload, "error.type").String()
		if errType == "" {
			errType = "api_error"
		}
		wrapped, err := json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": errType, "message": message},
		})
		if err != nil {
			return []byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"upstream error\"}}\n\n")
		}
		payload = wrapped
	}
	frame := make([]byte, 0, len(payload)+32)
	frame = append(frame, "event: error\ndata: "...)
	frame = append(frame, payload...)
	frame = append(frame, '\n', '\n')
	return frame
}
