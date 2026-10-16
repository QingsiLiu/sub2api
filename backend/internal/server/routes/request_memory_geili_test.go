package routes

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func memoryTestRouterGeili(budget *requestBodyBudgetGeili, cfg *config.Config, paths []string, next gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(newRequestMemoryAdmissionGeili(cfg, budget))
	for _, path := range paths {
		r.POST(path, next)
	}
	return r
}

func budgetUsedGeili(b *requestBodyBudgetGeili) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

func TestRequestMemoryGeiliSharedBudgetAndStreamingRelease(t *testing.T) {
	body := []byte(`{"model":"keep","input":"unchanged"}`)
	b := &requestBodyBudgetGeili{limit: int64(len(body))}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	r := memoryTestRouterGeili(b, nil, []string{"/v1/messages", "/responses"}, func(c *gin.Context) {
		calls.Add(1)
		got, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, err)
		require.Equal(t, body, got)
		if c.Request.URL.Path == "/v1/messages" {
			close(entered)
			<-release
		}
		c.Status(200)
	})
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(first, httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(body)))
	}()
	<-entered
	require.Equal(t, int64(len(body)), budgetUsedGeili(b), "stream retains the lease after its body is read")
	second := httptest.NewRecorder()
	r.ServeHTTP(second, httptest.NewRequest("POST", "/responses", bytes.NewReader(body)))
	require.Equal(t, 503, second.Code)
	require.Equal(t, "3", second.Header().Get("Retry-After"))
	require.Equal(t, int64(1), calls.Load(), "overload cannot invoke the supplier/billing handler")
	require.Contains(t, second.Body.String(), "gateway_memory_capacity_exceeded")
	require.Equal(t, 1, strings.Count(second.Body.String(), `"error"`))
	close(release)
	<-done
	require.Zero(t, budgetUsedGeili(b))
	third := httptest.NewRecorder()
	r.ServeHTTP(third, httptest.NewRequest("POST", "/responses", bytes.NewReader(body)))
	require.Equal(t, 200, third.Code)
	require.Zero(t, budgetUsedGeili(b))
}

func TestRequestMemoryGeiliKnownUnknownAndMisdeclaredBodies(t *testing.T) {
	for _, declared := range []int64{-1, 0, 3, 7, 100} {
		t.Run(fmt.Sprintf("declared_%d", declared), func(t *testing.T) {
			body := []byte("abcdefg")
			b := &requestBodyBudgetGeili{limit: 100}
			r := memoryTestRouterGeili(b, nil, []string{"/responses"}, func(c *gin.Context) {
				got, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, err)
				require.Equal(t, body, got)
				require.Equal(t, int64(len(body)), budgetUsedGeili(b))
				c.Status(200)
			})
			req := httptest.NewRequest("POST", "/responses", bytes.NewReader(body))
			req.ContentLength = declared
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code)
			require.Zero(t, budgetUsedGeili(b))
		})
	}
	for _, declared := range []int64{-1, 7} {
		b := &requestBodyBudgetGeili{limit: 7}
		r := memoryTestRouterGeili(b, nil, []string{"/responses"}, func(c *gin.Context) { c.Status(200) })
		req := httptest.NewRequest("POST", "/responses", strings.NewReader("abcdefg"))
		req.ContentLength = declared
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, "exactly-full unknown and known bodies still detect EOF")
		require.Zero(t, budgetUsedGeili(b))
	}
}

type memoryReadFailureGeili struct{}

func (memoryReadFailureGeili) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (memoryReadFailureGeili) Close() error             { return nil }

func TestRequestMemoryGeiliErrorsAndPanicRelease(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		declared int64
		limit    int64
		status   int
	}{{"unknown overflow", "12345678", -1, 7, 503}, {"declared overflow", "12345678", 8, 7, 503}, {"per request cap", "12345678", 8, 100, 413}} {
		t.Run(tc.name, func(t *testing.T) {
			b := &requestBodyBudgetGeili{limit: tc.limit}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 7}}
			if tc.status != 413 {
				cfg.Gateway.MaxBodySize = 100
			}
			r := memoryTestRouterGeili(b, cfg, []string{"/responses"}, func(c *gin.Context) { t.Error("rejected request reached handler") })
			req := httptest.NewRequest("POST", "/responses", strings.NewReader(tc.body))
			req.ContentLength = tc.declared
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code)
			require.Zero(t, budgetUsedGeili(b))
		})
	}
	b := &requestBodyBudgetGeili{limit: 1024}
	r := memoryTestRouterGeili(b, nil, []string{"/responses"}, func(c *gin.Context) { t.Error("read failure reached handler") })
	req := httptest.NewRequest("POST", "/responses", nil)
	req.Body = memoryReadFailureGeili{}
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 400, w.Code)
	require.Zero(t, budgetUsedGeili(b))
	r = memoryTestRouterGeili(b, nil, []string{"/responses"}, func(c *gin.Context) { panic("supplier panic") })
	require.Panics(t, func() {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/responses", strings.NewReader("body")))
	})
	require.Zero(t, budgetUsedGeili(b))
}

func TestRequestMemoryGeiliCompressedBytesAndHeadroom(t *testing.T) {
	body := []byte(" \n{\"model\":\"keep\",\"input\":\"" + strings.Repeat("x", 1<<20) + "\"} \n")
	var encoded bytes.Buffer
	gz := gzip.NewWriter(&encoded)
	_, err := gz.Write(body)
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	zs, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	require.NoError(t, err)
	zstdBody := zs.EncodeAll(body, nil)
	zs.Close()
	for _, compression := range []struct {
		encoding string
		body     []byte
	}{{"gzip", encoded.Bytes()}, {"zstd", zstdBody}} {
		for _, fits := range []bool{true, false} {
			limit := int64(128 << 20) // Real default for the 1.5 GiB Stage container.
			if !fits {
				limit = 1 << 20
			}
			b := &requestBodyBudgetGeili{limit: limit}
			called := false
			r := memoryTestRouterGeili(b, nil, []string{"/v1/messages"}, func(c *gin.Context) {
				called = true
				got, e := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
				require.NoError(t, e)
				require.Equal(t, body, got)
				require.Empty(t, c.GetHeader("Content-Encoding"))
				require.Equal(t, int64(len(body)), budgetUsedGeili(b))
				c.Status(200)
			})
			req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(compression.body))
			req.Header.Set("Content-Encoding", compression.encoding)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if fits {
				require.Equal(t, 200, w.Code)
				require.True(t, called)
			} else {
				require.Equal(t, 503, w.Code)
				require.False(t, called)
			}
			require.Zero(t, budgetUsedGeili(b))
		}
	}
}

func TestRequestMemoryGeiliGoogleOverloadIsLocalCapacity(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Next()
		marked, ok := c.Get("ops_routing_capacity_limited")
		require.True(t, ok)
		require.Equal(t, true, marked)
	}, newRequestMemoryAdmissionGeili(nil, &requestBodyBudgetGeili{limit: 1}))
	r.POST("/v1beta/models/gemini:generateContent", func(c *gin.Context) { t.Fatal("overload dispatched") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/v1beta/models/gemini:generateContent", strings.NewReader("large")))
	require.Equal(t, 503, w.Code)
	require.JSONEq(t, `{"error":{"code":503,"status":"UNAVAILABLE","message":"Gateway is handling too many large requests; retry shortly with fewer concurrent requests"}}`, w.Body.String())
}

type slowMemoryReadGeili struct{ io.Reader }

func (r slowMemoryReadGeili) Read(p []byte) (int, error) {
	time.Sleep(20 * time.Millisecond)
	return r.Reader.Read(p)
}
func (slowMemoryReadGeili) Close() error { return nil }

func TestRequestMemoryGeiliPrereadTimeRemainsInCompositeDeadline(t *testing.T) {
	r := gin.New()
	var ingress, afterRead time.Time
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{BillingSource: "balance", RoutingMode: "composite", GroupIDs: []int64{22, 11}})
		c.Next()
		deadline, ok := c.Request.Context().Deadline()
		require.True(t, ok)
		require.Equal(t, ingress.Add(90*time.Second), deadline)
		require.True(t, afterRead.Sub(ingress) >= 20*time.Millisecond)
	}, newRequestMemoryAdmissionGeili(nil, &requestBodyBudgetGeili{limit: 1024}), func(c *gin.Context) {
		ingress = requestMemoryIngressTimeGeili(c.Request)
		afterRead = time.Now()
	}, explicitKeyRouting(service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil), nil, &handler.Handlers{}, &config.Config{Gateway: config.GatewayConfig{KeyGroupRequestTimeoutSeconds: 90}}))
	r.POST("/v1/responses", func(c *gin.Context) { t.Fatal("fixture dispatched") })
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-6-astra"}`))
	req.Body = slowMemoryReadGeili{req.Body}
	r.ServeHTTP(httptest.NewRecorder(), req)
}

func TestRequestMemoryGeiliPrereadAndRetryAreByteIdentical(t *testing.T) {
	body := []byte("\xef\xbb\xbf \n{\"model\":\"keep\",\"input\":\"a\tb\"} ")
	b := &requestBodyBudgetGeili{limit: int64(len(body))}
	r := memoryTestRouterGeili(b, nil, []string{"/responses"}, func(c *gin.Context) {
		first, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		require.NoError(t, err)
		require.Equal(t, body, first)
		for i := 0; i < 3; i++ {
			clone := c.Request.Clone(c.Request.Context())
			requestmodel.ResetRequestBody(clone, first)
			again, e := pkghttputil.ReadRequestBodyWithPrealloc(clone)
			require.NoError(t, e)
			require.Equal(t, body, again)
			require.Equal(t, &first[0], &again[0])
		}
		c.Status(200)
	})
	for _, preread := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/responses", bytes.NewReader(body))
		if preread {
			requestmodel.ResetRequestBody(req, body)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code)
		require.Zero(t, budgetUsedGeili(b))
	}
}

func TestRequestMemoryGeiliAliasesAndExclusions(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/antigravity/v1/messages", "/messages/count_tokens", "/responses", "/v1/responses/compact", "/backend-api/codex/responses", "/v1/chat/completions", "/chat/completions", "/v1beta/models/gemini-4:generateContent", "/antigravity/v1beta/models/gemini:streamGenerateContent"} {
		require.True(t, memoryManagedRequestGeili(httptest.NewRequest("POST", path, nil)), path)
	}
	for _, path := range []string{"/v1/images/edits", "/v1/images/generations/async", "/v1/videos", "/v1/files", "/v1/live", "/v1beta/cachedContents", "/v1beta/models/gemini:predictLongRunning"} {
		require.False(t, memoryManagedRequestGeili(httptest.NewRequest("POST", path, nil)), path)
	}
	require.False(t, memoryManagedRequestGeili(httptest.NewRequest("GET", "/responses", nil)))
	req := httptest.NewRequest("POST", "/responses", nil)
	req.Header.Set("Upgrade", "websocket")
	require.False(t, memoryManagedRequestGeili(req))
	// Authentication terminates before admission even when no memory is free.
	r := gin.New()
	r.Use(func(c *gin.Context) { c.AbortWithStatus(401) }, newRequestMemoryAdmissionGeili(nil, &requestBodyBudgetGeili{limit: 0}))
	r.POST("/responses", func(c *gin.Context) { t.Fatal("unauthorized handler") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/responses", strings.NewReader("large")))
	require.Equal(t, 401, w.Code)
}

func TestRequestMemoryGeiliConcurrentLargeRequestBound(t *testing.T) {
	const size = 2 << 20
	body := bytes.Repeat([]byte("x"), size)
	b := &requestBodyBudgetGeili{limit: 4 * size}
	var entered, rejected atomic.Int64
	release := make(chan struct{})
	r := memoryTestRouterGeili(b, nil, []string{"/v1/messages", "/responses"}, func(c *gin.Context) {
		got, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, size, len(got))
		entered.Add(1)
		<-release
		c.Status(200)
	})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "/responses"
			if i%2 == 0 {
				path = "/v1/messages"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", path, bytes.NewReader(body)))
			if w.Code == 503 {
				rejected.Add(1)
			} else {
				require.Equal(t, 200, w.Code)
			}
		}(i)
	}
	require.Eventually(t, func() bool { return entered.Load()+rejected.Load() == 32 }, 5*time.Second, time.Millisecond)
	require.Greater(t, entered.Load(), int64(0))
	require.LessOrEqual(t, entered.Load(), int64(4))
	require.LessOrEqual(t, budgetUsedGeili(b), int64(4*size))
	close(release)
	wg.Wait()
	require.Zero(t, budgetUsedGeili(b))
}

func TestRequestMemoryGeiliPreservesExistingTextCaps(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1024, TextMaxBodySize: 4}}
	b := &requestBodyBudgetGeili{limit: 1024}
	r := memoryTestRouterGeili(b, cfg, []string{"/v1/embeddings", "/v1/messages"}, func(c *gin.Context) { c.Status(200) })
	for _, tc := range []struct {
		path   string
		status int
	}{{"/v1/embeddings", 413}, {"/v1/messages", 200}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", tc.path, strings.NewReader("12345")))
		require.Equal(t, tc.status, w.Code)
		require.Zero(t, budgetUsedGeili(b))
	}
}
