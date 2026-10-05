package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type auditCaptureRepo struct{ records chan service.ResponseAudit }

func (r *auditCaptureRepo) Save(_ context.Context, a *service.ResponseAudit) error {
	r.records <- *a
	return nil
}
func (*auditCaptureRepo) List(context.Context, service.ResponseAuditFilter) ([]service.ResponseAudit, int64, error) {
	return nil, 0, nil
}
func (*auditCaptureRepo) Get(context.Context, int64) (*service.ResponseAudit, error) { return nil, nil }
func (*auditCaptureRepo) Stats(context.Context, service.ResponseAuditFilter) (*service.ResponseAuditStats, error) {
	return nil, nil
}
func (*auditCaptureRepo) Lookup(context.Context, []service.ResponseAuditUsageKey) (map[service.ResponseAuditUsageKey]*service.ResponseAudit, error) {
	return nil, nil
}
func (*auditCaptureRepo) Cleanup(context.Context, time.Time) error { return nil }

func TestResponseAuditMiddlewarePreservesWireAndRecordsHTTP200Failure(t *testing.T) {
	repo := &auditCaptureRepo{records: make(chan service.ResponseAudit, 2)}
	cfg := &config.Config{}
	cfg.Gateway.UsageRecord.WorkerCount = 1
	cfg.Gateway.UsageRecord.QueueSize = 4
	svc := service.NewResponseAuditService(repo, cfg)
	defer svc.Stop()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 7, UserID: 3})
		c.Next()
	}, ResponseOutputAudit(svc))
	const payload = "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n"
	r.POST("/v1/responses", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("X-Test", "unchanged")
		_, _ = c.Writer.WriteString(payload)
		c.Writer.Flush()
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/responses", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, payload, w.Body.String())
	require.Equal(t, "unchanged", w.Header().Get("X-Test"))
	select {
	case a := <-repo.records:
		require.Equal(t, "failed", a.Status)
		require.Equal(t, 200, a.HTTPStatus)
		require.EqualValues(t, 7, a.APIKeyID)
	case <-time.After(time.Second):
		t.Fatal("audit not persisted")
	}
	// Dedicated media paths must not acquire a text observer.
	r.POST("/v1/images/generations", func(c *gin.Context) {
		require.Nil(t, service.ResponseAuditFromContext(c.Request.Context()))
		c.Status(http.StatusOK)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/images/generations", nil))
}

func TestResponseAuditRecordsKeepaliveWireStatus(t *testing.T) {
	for _, tc := range []struct {
		name      string
		heartbeat bool
		bodyless  bool
		status    int
	}{
		{"fast failure", false, false, http.StatusBadGateway},
		{"failure after heartbeat", true, false, http.StatusOK},
		{"bodyless failure after heartbeat", true, true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &auditCaptureRepo{records: make(chan service.ResponseAudit, 1)}
			cfg := &config.Config{}
			cfg.Gateway.StreamKeepaliveInterval = 1
			cfg.Gateway.UsageRecord.WorkerCount = 1
			svc := service.NewResponseAuditService(repo, cfg)
			defer svc.Stop()
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 7, UserID: 3})
				c.Next()
			}, ResponseOutputAudit(svc), NewEdgeSSEKeepalive(cfg, time.Millisecond))
			r.POST("/v1/responses", func(c *gin.Context) {
				ArmEdgeSSEKeepalive(c, tc.heartbeat)
				if tc.heartbeat {
					require.Eventually(t, func() bool { return EdgeSSEKeepaliveCommitted(c) }, time.Second, time.Millisecond)
				}
				if tc.bodyless {
					c.Status(http.StatusBadGateway)
				} else {
					c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "synthetic failure"}})
				}
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			req.Header.Set("Cf-Ray", "synthetic-audit-SIN")
			w := newSyncRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.code())
			if tc.heartbeat {
				require.Contains(t, w.body(), ": keepalive")
				require.Contains(t, w.body(), "event: response.failed")
			}
			select {
			case a := <-repo.records:
				require.Equal(t, tc.status, a.HTTPStatus)
				require.Equal(t, "failed", a.Status)
				require.False(t, a.TextWritten)
				require.False(t, a.ToolWritten)
				if tc.heartbeat {
					require.True(t, a.TerminalWritten)
				}
			case <-time.After(time.Second):
				t.Fatal("audit not persisted")
			}
		})
	}
}
