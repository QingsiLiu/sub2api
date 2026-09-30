package handler

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	testFirstBeat = 40 * time.Millisecond
	testInterval  = 25 * time.Millisecond
)

// syncRecorder 让测试能在心跳线程写入的同时安全读取；httptest.ResponseRecorder 本身不是并发安全的。
// 这只是测试装置：线上真实的 http.ResponseWriter 只会被写入器内部持锁访问。
type syncRecorder struct {
	mu  sync.Mutex
	rec *httptest.ResponseRecorder
}

func newSyncRecorder() *syncRecorder { return &syncRecorder{rec: httptest.NewRecorder()} }

func (s *syncRecorder) Header() http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Header()
}

func (s *syncRecorder) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Write(b)
}

func (s *syncRecorder) WriteHeader(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec.WriteHeader(code)
}

func (s *syncRecorder) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec.Flush()
}

func (s *syncRecorder) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Body.String()
}

func (s *syncRecorder) code() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Code
}

func (s *syncRecorder) headerGet(k string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Header().Get(k)
}

func newKeepaliveHarness(t *testing.T) (*edgeKeepaliveWriter, *syncRecorder, context.CancelFunc) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := newSyncRecorder()
	inner, _ := gin.CreateTestContext(rec)
	inner.Writer.Header().Set("X-Client-Request-Id", "req-1") // 上游中间件已设置的头
	ctx, cancel := context.WithCancel(context.Background())
	w := newEdgeKeepaliveWriter(inner.Writer, time.Now(), testFirstBeat, testInterval, ctx.Done(), nil)
	t.Cleanup(func() { cancel(); w.finish() })
	return w, rec, cancel
}

func pingCount(body string) int { return strings.Count(body, "event: ping") }

func TestEdgeKeepalive_TransparentWhenNotArmed(t *testing.T) {
	w, rec, _ := newKeepaliveHarness(t)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "7")
	w.WriteHeader(http.StatusTooManyRequests)
	require.False(t, w.Written(), "只记录状态码，尚未提交")
	require.Equal(t, http.StatusTooManyRequests, w.Status())
	_, err := w.Write([]byte(`{"type":"error"}`))
	require.NoError(t, err)

	require.Equal(t, http.StatusTooManyRequests, rec.code())
	require.Equal(t, `{"type":"error"}`, rec.body())
	require.Equal(t, "7", rec.headerGet("Retry-After"))
	require.Equal(t, "req-1", rec.headerGet("X-Client-Request-Id"), "上游中间件设置的头必须保留")
	require.True(t, w.Written())
}

func TestEdgeKeepalive_FastResponseNeverBeats(t *testing.T) {
	w, rec, _ := newKeepaliveHarness(t)
	w.arm()
	w.Header().Set("Content-Type", "text/event-stream")
	_, err := w.Write([]byte("event: message_start\ndata: {}\n\n"))
	require.NoError(t, err)

	time.Sleep(4 * testFirstBeat)
	require.Zero(t, pingCount(rec.body()))
	require.Equal(t, "event: message_start\ndata: {}\n\n", rec.body())
	require.Equal(t, http.StatusOK, rec.code())
}

func TestEdgeKeepalive_BeatsWhileRequestIsSilentAndHidesItFromTheRequestSide(t *testing.T) {
	w, rec, _ := newKeepaliveHarness(t)
	w.arm()
	w.Header().Set("Retry-After", "9") // 请求线程改私有 Header，不得影响线上

	require.Eventually(t, func() bool { return pingCount(rec.body()) >= 3 }, 2*time.Second, 5*time.Millisecond)

	require.Equal(t, http.StatusOK, rec.code())
	require.Equal(t, "text/event-stream", rec.headerGet("Content-Type"))
	require.Equal(t, "no", rec.headerGet("X-Accel-Buffering"), "否则 nginx 会把心跳攒在缓冲里")
	require.Equal(t, "req-1", rec.headerGet("X-Client-Request-Id"))
	require.Empty(t, rec.headerGet("Retry-After"))

	// 请求侧的视角仍是「什么都没写」，failover 的「已写出字节则禁止换号」判定不受心跳影响。
	require.Equal(t, -1, w.Size())
	require.False(t, w.Written())
	require.Equal(t, http.StatusOK, w.Status())
}

func TestEdgeKeepalive_RealStreamAppendsAfterPingsAndStopsHeartbeat(t *testing.T) {
	w, rec, _ := newKeepaliveHarness(t)
	w.arm()
	require.Eventually(t, func() bool { return pingCount(rec.body()) >= 2 }, 2*time.Second, 5*time.Millisecond)

	real := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
	w.Header().Set("Content-Type", "text/event-stream")
	n, err := w.Write([]byte(real))
	require.NoError(t, err)
	require.Equal(t, len(real), n)
	w.Flush()

	before := pingCount(rec.body())
	time.Sleep(6 * testInterval)
	require.Equal(t, before, pingCount(rec.body()), "请求线程开始写真实响应后心跳必须永久停止")
	require.True(t, strings.HasSuffix(rec.body(), real))
	require.True(t, w.Written())
	require.Equal(t, len(real), w.Size(), "Size 扣除心跳字节")
}

func TestEdgeKeepalive_ErrorAfterCommitBecomesSSEErrorFrame(t *testing.T) {
	w, rec, _ := newKeepaliveHarness(t)
	w.arm()
	require.Eventually(t, func() bool { return pingCount(rec.body()) >= 1 }, 2*time.Second, 5*time.Millisecond)

	body := `{"type":"error","error":{"type":"rate_limit_error","message":"Upstream rate limit exceeded, please retry later"}}`
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	n, err := w.Write([]byte(body))
	require.NoError(t, err)
	require.Equal(t, len(body), n, "对请求线程假装写入了完整错误体")

	require.Equal(t, http.StatusOK, rec.code(), "线上状态码已固化为 200")
	require.Equal(t, http.StatusTooManyRequests, w.Status(), "请求线程仍能读到自己设置的状态码")
	require.True(t, strings.HasSuffix(rec.body(), "event: error\ndata: "+body+"\n\n"), rec.body())
	require.NotContains(t, rec.body(), "\n"+body+"\n", "不得出现夹在 SSE 里的裸 JSON")
	require.NotEqual(t, "application/json", rec.headerGet("Content-Type"))
}

func TestEdgeKeepalive_ErrorBeforeCommitKeepsHTTPStatus(t *testing.T) {
	w, rec, _ := newKeepaliveHarness(t)
	w.arm()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_, err := w.Write([]byte(`{"type":"error","error":{"type":"upstream_error","message":"x"}}`))
	require.NoError(t, err)

	require.Equal(t, http.StatusBadGateway, rec.code(), "快速失败仍然是原来的 HTTP 状态码")
	require.Equal(t, "application/json", rec.headerGet("Content-Type"))
	require.Zero(t, pingCount(rec.body()))
}

func TestEdgeSSEErrorFrame(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{"anthropic json passes through compacted", "{\n  \"type\": \"error\",\n  \"error\": {\"type\": \"overloaded_error\", \"message\": \"busy\"}\n}", 529,
			`event: error` + "\n" + `data: {"type":"error","error":{"type":"overloaded_error","message":"busy"}}` + "\n\n"},
		{"json without top-level type is wrapped", `{"error":{"type":"rate_limit_error","message":"slow"}}`, 429,
			`event: error` + "\n" + `data: {"error":{"message":"slow","type":"rate_limit_error"},"type":"error"}` + "\n\n"},
		{"plain text is wrapped", "Bad Gateway", 502,
			`event: error` + "\n" + `data: {"error":{"message":"Bad Gateway","type":"api_error"},"type":"error"}` + "\n\n"},
		{"empty body falls back to status text", "", 503,
			`event: error` + "\n" + `data: {"error":{"message":"Service Unavailable","type":"api_error"},"type":"error"}` + "\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, string(edgeSSEErrorFrame([]byte(tc.body), tc.status)))
		})
	}
}

func TestEdgeSSEErrorFrame_IsRecognisedByOpsErrorLogger(t *testing.T) {
	frame := edgeSSEErrorFrame([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Upstream rate limit exceeded, please retry later"}}`), 429)
	parsed, ok := parseOpsSSEFailure([]byte(edgeKeepalivePingFrame + string(frame)))
	require.True(t, ok, "错误看板依赖它把挂在 200 流上的失败记为失败请求")
	require.True(t, parsed.StreamFailure)
	require.Equal(t, "rate_limit_error", parsed.ErrorType)
}

func TestEdgeKeepalive_FinishTerminatesBodylessErrorStream(t *testing.T) {
	w, rec, _ := newKeepaliveHarness(t)
	w.arm()
	require.Eventually(t, func() bool { return pingCount(rec.body()) >= 1 }, 2*time.Second, 5*time.Millisecond)
	w.WriteHeader(http.StatusServiceUnavailable) // 只设状态码、没有写体

	w.finish()

	require.Contains(t, rec.body(), "event: error")
	require.Contains(t, rec.body(), "Service Unavailable")
}

func TestEdgeKeepalive_FinishStaysSilentForClientClosedRequest(t *testing.T) {
	w, rec, _ := newKeepaliveHarness(t)
	w.arm()
	require.Eventually(t, func() bool { return pingCount(rec.body()) >= 1 }, 2*time.Second, 5*time.Millisecond)
	w.WriteHeader(499)

	w.finish()

	require.NotContains(t, rec.body(), "event: error")
}

func TestEdgeKeepalive_ClientDisconnectStopsHeartbeat(t *testing.T) {
	w, rec, cancel := newKeepaliveHarness(t)
	w.arm()
	require.Eventually(t, func() bool { return pingCount(rec.body()) >= 1 }, 2*time.Second, 5*time.Millisecond)

	cancel()
	time.Sleep(3 * testInterval) // 让在途的一拍写完
	settled := pingCount(rec.body())
	time.Sleep(6 * testInterval)
	require.Equal(t, settled, pingCount(rec.body()))
}

func TestEdgeKeepalive_ArmIsIdempotentAndSkippedAfterRequestCommit(t *testing.T) {
	w, rec, _ := newKeepaliveHarness(t)
	_, err := w.Write([]byte("data: x\n\n"))
	require.NoError(t, err)
	w.arm()
	w.arm()
	time.Sleep(4 * testFirstBeat)
	require.Zero(t, pingCount(rec.body()))
}

// go test -race：心跳线程提交线上响应头的同时，请求线程在反复改自己的 Header 并最终写响应。
func TestEdgeKeepalive_NoRaceBetweenHeartbeatCommitAndRequestHeaderWrites(t *testing.T) {
	for i := 0; i < 20; i++ {
		gin.SetMode(gin.TestMode)
		rec := newSyncRecorder()
		inner, _ := gin.CreateTestContext(rec)
		ctx, cancel := context.WithCancel(context.Background())
		w := newEdgeKeepaliveWriter(inner.Writer, time.Now(), time.Millisecond, time.Millisecond, ctx.Done(), nil)
		w.arm()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadline := time.Now().Add(15 * time.Millisecond)
			for time.Now().Before(deadline) {
				w.Header().Set("X-Spin", "1")
				w.Header().Add("X-Spin-2", "2")
				_ = w.Size()
				_ = w.Written()
				_ = w.Status()
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message_start\ndata: {}\n\n"))
			w.Flush()
		}()
		wg.Wait()
		cancel()
		w.finish()
	}
}

func TestEdgeKeepaliveCandidate(t *testing.T) {
	newReq := func(method, path string, headers map[string]string) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}
	cf := map[string]string{"Cf-Ray": "a431221ddf69ce83-SIN"}
	for _, tc := range []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"cloudflare messages", newReq(http.MethodPost, "/v1/messages", cf), true},
		{"trailing slash", newReq(http.MethodPost, "/v1/messages/", cf), true},
		{"direct connection", newReq(http.MethodPost, "/v1/messages", nil), false},
		{"blank ray", newReq(http.MethodPost, "/v1/messages", map[string]string{"Cf-Ray": " "}), false},
		{"count_tokens", newReq(http.MethodPost, "/v1/messages/count_tokens", cf), false},
		{"responses", newReq(http.MethodPost, "/v1/responses", cf), false},
		{"chat completions", newReq(http.MethodPost, "/v1/chat/completions", cf), false},
		{"GET", newReq(http.MethodGet, "/v1/messages", cf), false},
		{"websocket upgrade", newReq(http.MethodPost, "/v1/messages", map[string]string{"Cf-Ray": "x", "Upgrade": "websocket"}), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, edgeKeepaliveCandidate(tc.req))
		})
	}
}

func newKeepaliveServer(t *testing.T, cfg *config.Config, handler gin.HandlerFunc) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/messages", NewEdgeSSEKeepalive(cfg, testFirstBeat), handler)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func postMessages(t *testing.T, srv *httptest.Server, cfRay string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", strings.NewReader(`{"stream":true}`))
	require.NoError(t, err)
	if cfRay != "" {
		req.Header.Set("Cf-Ray", cfRay)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func keepaliveCfg(seconds int) *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.StreamKeepaliveInterval = seconds
	return cfg
}

// 端到端：真实 HTTP 连接上，慢请求在最终结果到达之前就能收到心跳；随后是完整的真实流。
func TestEdgeKeepalive_EndToEndSlowSuccessOverRealHTTP(t *testing.T) {
	const handlerDelay = 300 * time.Millisecond
	srv := newKeepaliveServer(t, keepaliveCfg(1), func(c *gin.Context) {
		ArmEdgeSSEKeepalive(c, true)
		time.Sleep(handlerDelay)
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		c.Writer.Flush()
		_, _ = c.Writer.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})

	start := time.Now()
	resp := postMessages(t, srv, "a431221ddf69ce83-SIN")
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	require.NoError(t, err)
	firstByteAfter := time.Since(start)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "event: ping\n", first)
	require.Less(t, firstByteAfter, handlerDelay-100*time.Millisecond, "第一个字节必须早于最终结果，否则 Cloudflare 看不到任何数据")

	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	body := first + string(rest)
	require.Contains(t, body, "event: message_start")
	require.True(t, strings.HasSuffix(body, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	require.Less(t, strings.Index(body, "event: ping"), strings.Index(body, "event: message_start"))
}

func TestEdgeKeepalive_EndToEndSlowFailureIsDeliveredAsSSEErrorNotDropped(t *testing.T) {
	srv := newKeepaliveServer(t, keepaliveCfg(1), func(c *gin.Context) {
		ArmEdgeSSEKeepalive(c, true)
		time.Sleep(200 * time.Millisecond)
		c.JSON(http.StatusTooManyRequests, gin.H{"type": "error", "error": gin.H{"type": "rate_limit_error", "message": "Upstream rate limit exceeded, please retry later"}})
	})

	resp := postMessages(t, srv, "a431221ddf69ce83-SIN")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), "event: ping")
	require.True(t, strings.HasSuffix(string(body), "event: error\ndata: {\"error\":{\"message\":\"Upstream rate limit exceeded, please retry later\",\"type\":\"rate_limit_error\"},\"type\":\"error\"}\n\n"), string(body))
}

func TestEdgeKeepalive_EndToEndFastFailureKeepsOriginalHTTPStatus(t *testing.T) {
	srv := newKeepaliveServer(t, keepaliveCfg(1), func(c *gin.Context) {
		ArmEdgeSSEKeepalive(c, true)
		c.JSON(http.StatusTooManyRequests, gin.H{"type": "error", "error": gin.H{"type": "rate_limit_error", "message": "x"}})
	})

	resp := postMessages(t, srv, "a431221ddf69ce83-SIN")
	body, _ := io.ReadAll(resp.Body)

	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	require.NotContains(t, string(body), "event: ping")
}

func TestEdgeKeepalive_EndToEndDoesNothingOutsideItsScope(t *testing.T) {
	slow := func(c *gin.Context) {
		ArmEdgeSSEKeepalive(c, true)
		time.Sleep(200 * time.Millisecond)
		c.String(http.StatusOK, "done")
	}
	t.Run("no Cf-Ray (direct domain)", func(t *testing.T) {
		resp := postMessages(t, newKeepaliveServer(t, keepaliveCfg(1), slow), "")
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, "done", string(body))
	})
	t.Run("kill switch: interval 0", func(t *testing.T) {
		resp := postMessages(t, newKeepaliveServer(t, keepaliveCfg(0), slow), "a431221ddf69ce83-SIN")
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, "done", string(body))
	})
	t.Run("non-stream request is never armed", func(t *testing.T) {
		srv := newKeepaliveServer(t, keepaliveCfg(1), func(c *gin.Context) {
			ArmEdgeSSEKeepalive(c, false)
			time.Sleep(200 * time.Millisecond)
			c.String(http.StatusOK, "done")
		})
		resp := postMessages(t, srv, "a431221ddf69ce83-SIN")
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, "done", string(body))
	})
}

func TestEdgeKeepalive_WriterIsRestoredAfterTheRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(recorder)
	var seen gin.ResponseWriter
	engine.POST("/v1/messages", NewEdgeSSEKeepalive(keepaliveCfg(1), testFirstBeat), func(c *gin.Context) {
		seen = c.Writer
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Cf-Ray", "x")
	engine.ServeHTTP(recorder, req)
	require.IsType(t, &edgeKeepaliveWriter{}, seen)
	require.NotNil(t, c)
	require.Equal(t, "ok", recorder.Body.String())
}

func TestEdgeKeepalive_StatusOnlyResponseIsStillCommitted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	_, engine := gin.CreateTestContext(recorder)
	engine.POST("/v1/messages", NewEdgeSSEKeepalive(keepaliveCfg(1), testFirstBeat), func(c *gin.Context) {
		c.Header("X-Custom", "kept")
		c.Status(http.StatusNoContent) // 没有正文
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Cf-Ray", "x")
	engine.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "kept", recorder.Header().Get("X-Custom"))
}

func TestEdgeKeepalive_HandlerThatWritesNothingStillGetsItsHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	_, engine := gin.CreateTestContext(recorder)
	engine.POST("/v1/messages", NewEdgeSSEKeepalive(keepaliveCfg(1), testFirstBeat), func(c *gin.Context) {
		c.Header("X-Custom", "kept")
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Cf-Ray", "x")
	engine.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "kept", recorder.Header().Get("X-Custom"))
}

// 与生产中间件顺序一致：ops 采集器在外，保活写入器在内。心跳和错误帧都要经过采集器。
func opsKeepaliveRouter(t *testing.T, handler gin.HandlerFunc) *gin.Engine {
	t.Helper()
	setupOpsErrorLogTestQueue(t, 4)
	gin.SetMode(gin.TestMode)
	ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(ops))
	router.POST("/v1/messages", NewEdgeSSEKeepalive(keepaliveCfg(1), testFirstBeat), handler)
	return router
}

func serveWithCfRay(router *gin.Engine) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("Cf-Ray", "a431221ddf69ce83-SIN")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestEdgeKeepalive_SlowUpstreamFailureIsStillRecordedByOpsAsFailedRequest(t *testing.T) {
	router := opsKeepaliveRouter(t, func(c *gin.Context) {
		setOpsRequestContext(c, "claude-opus-5-5", true)
		ArmEdgeSSEKeepalive(c, true)
		time.Sleep(4 * testFirstBeat)
		c.JSON(http.StatusTooManyRequests, gin.H{"type": "error", "error": gin.H{"type": "rate_limit_error", "message": "Upstream rate limit exceeded, please retry later"}})
	})

	rec := serveWithCfRay(router)

	require.Equal(t, http.StatusOK, rec.Code, "心跳已把线上响应提交为 200")
	require.Contains(t, rec.Body.String(), "event: ping")
	require.Contains(t, rec.Body.String(), "event: error")
	require.Equal(t, int64(1), OpsErrorLogQueueLength(), "挂在 200 流上的失败必须仍然进错误看板")
	job := <-opsErrorLogQueue
	require.Equal(t, "rate_limit_error", job.entry.ErrorType)
	require.Contains(t, job.entry.ErrorMessage, "Upstream rate limit exceeded")
}

func TestEdgeKeepalive_SlowSuccessLeavesNoOpsErrorRow(t *testing.T) {
	router := opsKeepaliveRouter(t, func(c *gin.Context) {
		setOpsRequestContext(c, "claude-opus-5-5", true)
		ArmEdgeSSEKeepalive(c, true)
		time.Sleep(4 * testFirstBeat)
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		c.Writer.Flush()
		_, _ = c.Writer.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})

	rec := serveWithCfRay(router)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "event: ping")
	require.Contains(t, rec.Body.String(), "event: message_stop")
	require.Zero(t, OpsErrorLogQueueLength(), "心跳不得被误判为失败")
}

func TestEdgeKeepalive_FastFailureKeepsHTTPStatusAndOpsRow(t *testing.T) {
	router := opsKeepaliveRouter(t, func(c *gin.Context) {
		setOpsRequestContext(c, "claude-opus-5-5", true)
		ArmEdgeSSEKeepalive(c, true)
		c.JSON(http.StatusBadGateway, gin.H{"type": "error", "error": gin.H{"type": "upstream_error", "message": "Upstream service temporarily unavailable"}})
	})

	rec := serveWithCfRay(router)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.NotContains(t, rec.Body.String(), "event: ping")
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusBadGateway, job.entry.StatusCode)
}
