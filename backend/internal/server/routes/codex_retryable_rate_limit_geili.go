package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	codexSlowDownDefaultRetryAfter = 5
	codexSlowDownMaxRetryAfter     = 60
	codexSlowDownMaxHeldBody       = 64 * 1024
)

// codexRetryableRateLimit reshapes transient pre-stream 429s for official Codex
// clients into 503 slow_down. Codex ends the turn on any pre-stream 429
// ("exceeded retry limit, last status: 429"), but retries 503 slow_down and
// honours Retry-After. It wraps opsErrorLogger so Ops still records the
// original 429. Quota/subscription/key-limit 429s are final and stay unchanged.
func codexRetryableRateLimit(settings *service.SettingService, opsErrorLogger gin.HandlerFunc) gin.HandlerFunc {
	next := func(c *gin.Context) {
		if opsErrorLogger != nil {
			opsErrorLogger(c)
			return
		}
		c.Next()
	}
	return func(c *gin.Context) {
		if !isCodexResponsesHTTPRequest(c) {
			next(c)
			return
		}
		parent := c.Writer
		w := &codexRateLimitWriter{ResponseWriter: parent}
		c.Writer = w
		defer func() {
			w.finish(func() int { return codexSlowDownFallbackRetryAfter(c, settings) })
			if c.Writer == w {
				c.Writer = parent
			}
		}()
		next(c)
	}
}

func isCodexResponsesHTTPRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.Method != http.MethodPost || c.Request.URL == nil {
		return false
	}
	path := strings.TrimRight(c.Request.URL.Path, "/")
	if !strings.HasSuffix(path, "/responses") && !strings.HasSuffix(path, "/responses/compact") {
		return false
	}
	return openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator"))
}

func codexSlowDownFallbackRetryAfter(c *gin.Context, settings *service.SettingService) int {
	seconds := codexSlowDownDefaultRetryAfter
	if settings != nil && c != nil && c.Request != nil {
		if cfg, err := settings.GetRateLimit429CooldownSettings(c.Request.Context()); err == nil && cfg != nil && cfg.Enabled && cfg.CooldownSeconds > 0 {
			seconds = cfg.CooldownSeconds
		}
	}
	return min(max(seconds, 1), codexSlowDownMaxRetryAfter)
}

var codexTransientRateLimitCodes = map[string]bool{
	"gateway_queue_full":        true,
	"gateway_concurrency_limit": true,
	"no_available_accounts":     true,
	"upstream_rate_limited":     true,
	"user_rpm_exceeded":         true,
	"group_rpm_exceeded":        true,
}

var codexRPMRateLimitMessages = map[string]bool{
	"user requests-per-minute limit exceeded":  true,
	"group requests-per-minute limit exceeded": true,
}

// isCodexTransientRateLimit is an allow-list: anything not recognised as a
// short-lived upstream, concurrency or RPM limit keeps its original 429.
func isCodexTransientRateLimit(body []byte) bool {
	kind := strings.ToLower(gjson.GetBytes(body, "error.type").String())
	code := strings.ToLower(gjson.GetBytes(body, "error.code").String())
	message := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	switch {
	case codexTransientRateLimitCodes[code]:
		return true
	case isUpstreamSideRateLimit(kind, code, message):
		return true
	case (kind == "rate_limit_exceeded" || code == "rate_limit_exceeded") && codexRPMRateLimitMessages[message]:
		return true
	case code == "" && (kind == "upstream_error" || kind == "overloaded_error"):
		lower := strings.ToLower(message)
		for _, final := range []string{"quota", "billing", "balance", "credit", "insufficient", "usage limit", "usage_limit", "subscription", "spend"} {
			if strings.Contains(lower, final) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// codexRateLimitWriter holds back only an uncommitted 429; every other status,
// and every streamed byte, goes straight to the parent writer.
type codexRateLimitWriter struct {
	gin.ResponseWriter
	mu      sync.Mutex
	holding bool
	written bool
	status  int
	body    bytes.Buffer
}

func (w *codexRateLimitWriter) WriteHeader(code int) {
	w.mu.Lock()
	if w.holding {
		if !w.written {
			w.status = code
			if code != http.StatusTooManyRequests {
				w.holding = false
				w.mu.Unlock()
				w.ResponseWriter.WriteHeader(code)
				return
			}
		}
		w.mu.Unlock()
		return
	}
	if code == http.StatusTooManyRequests && !w.ResponseWriter.Written() {
		w.holding = true
		w.status = code
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	w.ResponseWriter.WriteHeader(code)
}

func (w *codexRateLimitWriter) WriteHeaderNow() {
	w.mu.Lock()
	if w.holding {
		w.written = true
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *codexRateLimitWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	if w.holding {
		w.written = true
		if w.body.Len()+len(data) <= codexSlowDownMaxHeldBody {
			n, err := w.body.Write(data)
			w.mu.Unlock()
			return n, err
		}
		// Not a gateway error envelope; release unchanged.
		w.releaseLocked()
		w.mu.Unlock()
	} else {
		w.mu.Unlock()
	}
	return w.ResponseWriter.Write(data)
}

func (w *codexRateLimitWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *codexRateLimitWriter) Status() int {
	w.mu.Lock()
	if w.holding {
		defer w.mu.Unlock()
		return w.status
	}
	w.mu.Unlock()
	return w.ResponseWriter.Status()
}

func (w *codexRateLimitWriter) Size() int {
	w.mu.Lock()
	if w.holding {
		defer w.mu.Unlock()
		if !w.written {
			return -1
		}
		return w.body.Len()
	}
	w.mu.Unlock()
	return w.ResponseWriter.Size()
}

func (w *codexRateLimitWriter) Written() bool {
	w.mu.Lock()
	if w.holding {
		defer w.mu.Unlock()
		return w.written
	}
	w.mu.Unlock()
	return w.ResponseWriter.Written()
}

func (w *codexRateLimitWriter) Flush() {
	w.mu.Lock()
	if w.holding {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	w.ResponseWriter.Flush()
}

// releaseLocked forwards the held 429 unchanged. Caller holds w.mu.
func (w *codexRateLimitWriter) releaseLocked() {
	w.holding = false
	w.ResponseWriter.WriteHeader(w.status)
	if w.body.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.body.Bytes())
	} else if w.written {
		w.ResponseWriter.WriteHeaderNow()
	}
	w.body.Reset()
}

func (w *codexRateLimitWriter) finish(fallbackRetryAfter func() int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.holding {
		return
	}
	body := w.body.Bytes()
	if !isCodexTransientRateLimit(body) {
		w.releaseLocked()
		w.ResponseWriter.WriteHeaderNow()
		return
	}
	message := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	if message == "" {
		message = "Rate limited, please retry later"
	}
	payload, err := json.Marshal(gin.H{"error": gin.H{"type": "rate_limit_error", "code": "slow_down", "message": message}})
	if err != nil {
		w.releaseLocked()
		return
	}
	w.holding = false
	headers := w.ResponseWriter.Header()
	if seconds, err := strconv.Atoi(strings.TrimSpace(headers.Get("Retry-After"))); err != nil || seconds <= 0 {
		headers.Set("Retry-After", strconv.Itoa(fallbackRetryAfter()))
	}
	headers.Set("Content-Type", "application/json; charset=utf-8")
	headers.Del("Content-Length")
	w.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.ResponseWriter.Write(payload)
	w.body.Reset()
}
