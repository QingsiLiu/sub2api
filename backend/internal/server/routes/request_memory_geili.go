package routes

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/memoryguard"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/gin-gonic/gin"
)

var errRequestMemoryCapacityGeili = errors.New("gateway request body memory capacity exhausted")

type requestMemoryIngressStartGeili struct{}

func requestMemoryIngressTimeGeili(r *http.Request) time.Time {
	if started, ok := r.Context().Value(requestMemoryIngressStartGeili{}).(time.Time); ok {
		return started
	}
	return time.Now()
}

// One budget is shared by every alias and protocol registered on this server.
// It accounts for bytes retained throughout a request, including routing retries
// and streaming, rather than only limiting the number of upstream connections.
type requestBodyBudgetGeili struct {
	mu          sync.Mutex
	limit, used int64
}

type requestBodyLeaseGeili struct {
	budget   *requestBodyBudgetGeili
	reserved int64
}

func (l *requestBodyLeaseGeili) resize(n int64) bool {
	l.budget.mu.Lock()
	defer l.budget.mu.Unlock()
	if n < 0 || n > l.budget.limit || n-l.reserved > l.budget.limit-l.budget.used {
		return false
	}
	l.budget.used += n - l.reserved
	l.reserved = n
	return true
}

// The reader reserves before allowing the body reader to fill another chunk.
// Declared lengths reserve before any allocation; unknown/misdeclared lengths
// are still metered as bytes arrive. A one-byte EOF probe does not reject an
// exactly-full budget merely because a reader needs another call to return EOF.
type requestMemoryReaderGeili struct {
	io.ReadCloser
	lease                         *requestBodyLeaseGeili
	consumed, declared, allocated int64
}

// The decoder calls this before allocating its bounded workspace and each
// decoded-output chunk. Small compressed requests only reserve the memory they
// can use instead of claiming a full 128 MiB output buffer in advance.
func (r *requestMemoryReaderGeili) RequestBodyDecompressionReserveGeili(extra int64) error {
	if extra < 0 || extra > r.lease.budget.limit-r.reservationFor(r.consumed) || !r.lease.resize(r.reservationFor(r.consumed)+extra) {
		return errRequestMemoryCapacityGeili
	}
	return nil
}

func (r *requestMemoryReaderGeili) reservationFor(n int64) int64 {
	if r.declared > n {
		n = r.declared
	}
	if r.allocated > n {
		n = r.allocated
	}
	return n
}

// Called by the common body reader BEFORE allocating each chunk. At a full
// budget only one byte is needed to distinguish EOF from an oversized upload.
func (r *requestMemoryReaderGeili) RequestBodyReadChunkCapacityGeili(want int) int {
	r.lease.budget.mu.Lock()
	available := r.lease.budget.limit - r.lease.budget.used + r.lease.reserved - r.consumed
	r.lease.budget.mu.Unlock()
	if available < 1 {
		return 1
	}
	if int64(want) > available {
		want = int(available)
	}
	allocated := r.consumed + int64(want)
	if !r.lease.resize(r.reservationFor(allocated)) {
		return 1
	}
	r.allocated = allocated
	return want
}

func (r *requestMemoryReaderGeili) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	probe := false
	if r.declared >= 0 {
		remaining := r.declared - r.consumed
		if remaining > 0 && int64(len(p)) > remaining {
			p = p[:int(remaining)]
		} else if remaining <= 0 {
			p = p[:1]
			probe = true
		}
	}
	if !probe && !r.lease.resize(r.reservationFor(r.consumed+int64(len(p)))) {
		// EOF consumes no budget. Probe a single byte without allocating a
		// further chunk; any actual new byte must still acquire a reservation.
		p = p[:1]
	}
	n, err := r.ReadCloser.Read(p)
	r.consumed += int64(n)
	if !r.lease.resize(r.reservationFor(r.consumed)) {
		return 0, errRequestMemoryCapacityGeili
	}
	return n, err
}

func requestMemoryAdmissionGeili(cfg *config.Config) gin.HandlerFunc {
	var limit int64
	if cgroupLimit, ok := memoryguard.ContainerMemoryLimit(); ok {
		limit = cgroupLimit / 12 // 6 GiB container: at most 512 MiB of raw in-flight bodies.
	}
	if value, present := os.LookupEnv("GEILI_REQUEST_BODY_BUDGET_BYTES"); present {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && parsed >= 0 {
			limit = parsed // Explicit zero disables admission; malformed values do not.
		} else {
			log.Printf("[RequestMemory] invalid GEILI_REQUEST_BODY_BUDGET_BYTES; using detected default")
		}
	}
	if limit <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	log.Printf("[RequestMemory] in-flight body budget=%d bytes", limit)
	return newRequestMemoryAdmissionGeili(cfg, &requestBodyBudgetGeili{limit: limit})
}

func newRequestMemoryAdmissionGeili(cfg *config.Config, budget *requestBodyBudgetGeili) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !memoryManagedRequestGeili(c.Request) {
			c.Next()
			return
		}
		lease := &requestBodyLeaseGeili{budget: budget}
		defer lease.resize(0)
		// Prereading must not move the original composite request deadline.
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), requestMemoryIngressStartGeili{}, time.Now()))
		maxBytes := int64(256 << 20)
		if cfg != nil && cfg.Gateway.MaxBodySize > 0 {
			maxBytes = cfg.Gateway.MaxBodySize
		}
		path := strings.TrimRight(c.Request.URL.Path, "/")
		if cfg != nil && cfg.Gateway.TextMaxBodySize > 0 && (strings.HasSuffix(path, "/alpha/search") || strings.HasSuffix(path, "/embeddings") || strings.HasSuffix(path, "/systemone")) {
			maxBytes = cfg.Gateway.TextMaxBodySize
		}
		if c.Request.ContentLength > maxBytes {
			writeRequestMemoryErrorGeili(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "Request body too large")
			return
		}
		if preread, ok := c.Request.Body.(*pkghttputil.PrereadBody); ok {
			if int64(len(preread.Bytes())) > maxBytes {
				writeRequestMemoryErrorGeili(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "Request body too large")
				return
			}
			if !lease.resize(int64(len(preread.Bytes()))) {
				writeRequestMemoryOverloadGeili(c)
				return
			}
			c.Next()
			return
		}
		declared := c.Request.ContentLength
		var initial int64
		if declared > 0 {
			if declared > budget.limit {
				writeRequestMemoryOverloadGeili(c)
				return
			}
			initial += declared
		}
		if !lease.resize(initial) {
			writeRequestMemoryOverloadGeili(c)
			return
		}
		original := c.Request.Body
		if original == nil {
			c.Next()
			return
		}
		defer original.Close()
		limited := http.MaxBytesReader(c.Writer, original, maxBytes)
		c.Request.Body = &requestMemoryReaderGeili{ReadCloser: limited, lease: lease, declared: declared}
		body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			if errors.Is(err, errRequestMemoryCapacityGeili) {
				writeRequestMemoryOverloadGeili(c)
				return
			}
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeRequestMemoryErrorGeili(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "Request body too large")
				return
			}
			writeRequestMemoryErrorGeili(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
			return
		}
		if !lease.resize(int64(len(body))) {
			writeRequestMemoryOverloadGeili(c)
			return
		}
		requestmodel.ResetRequestBody(c.Request, body)
		c.Next()
	}
}

func memoryManagedRequestGeili(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost || strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	path := strings.TrimRight(r.URL.Path, "/")
	if strings.Contains(path, "/responses/") {
		return true
	}
	for _, suffix := range []string{"/messages", "/messages/count_tokens", "/chat/completions", "/responses", "/responses/compact", "/embeddings", "/alpha/search", "/systemone"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return strings.Contains(path, "/v1beta/models/") && (strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent") || strings.HasSuffix(path, ":countTokens"))
}

func writeRequestMemoryOverloadGeili(c *gin.Context) {
	handler.MarkOpsRequestMemoryCapacityGeili(c)
	c.Header("Retry-After", "3")
	writeRequestMemoryErrorGeili(c, http.StatusServiceUnavailable, "overloaded_error", "Gateway is handling too many large requests; retry shortly with fewer concurrent requests")
}

func writeRequestMemoryErrorGeili(c *gin.Context, status int, kind, message string) {
	code := "invalid_request_error"
	if status == http.StatusServiceUnavailable {
		code = "gateway_memory_capacity_exceeded"
	}
	if strings.Contains(c.Request.URL.Path, "/v1beta/") {
		googleStatus := "INVALID_ARGUMENT"
		if status == http.StatusServiceUnavailable {
			googleStatus = "UNAVAILABLE"
		}
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": status, "status": googleStatus, "message": message}})
		return
	}
	if strings.Contains(c.Request.URL.Path, "/messages") {
		c.AbortWithStatusJSON(status, gin.H{"type": "error", "error": gin.H{"type": kind, "code": code, "message": message}})
		return
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": kind, "code": code, "message": message}})
}
