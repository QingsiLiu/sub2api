package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type auditDeliveryRepo struct {
	ResponseAuditRepository
	calls atomic.Int32
	save  func(context.Context, *ResponseAudit) error
}

func (r *auditDeliveryRepo) Save(ctx context.Context, a *ResponseAudit) error {
	r.calls.Add(1)
	return r.save(ctx, a)
}
func (r *auditDeliveryRepo) Cleanup(context.Context, time.Time) error { return nil }
func auditDeliveryService(r *auditDeliveryRepo) *ResponseAuditService {
	cfg := &config.Config{}
	cfg.Gateway.UsageRecord.WorkerCount = 1
	cfg.Gateway.UsageRecord.QueueSize = 1
	return NewResponseAuditService(r, cfg)
}
func TestResponseAuditDeliveryRetryAndFailure(t *testing.T) {
	var received ResponseAudit
	r := &auditDeliveryRepo{}
	r.save = func(_ context.Context, a *ResponseAudit) error {
		if r.calls.Load() < 3 {
			return errors.New("temporary outage")
		}
		received = *a
		return nil
	}
	s := auditDeliveryService(r)
	s.Submit(ResponseAudit{AuditRequestID: "immutable", Turn: 2, Status: "empty"})
	s.Stop()
	require.EqualValues(t, 3, r.calls.Load())
	require.Equal(t, "immutable", received.AuditRequestID)
	require.Zero(t, s.failures.Load())
	r = &auditDeliveryRepo{save: func(context.Context, *ResponseAudit) error { return errors.New("database unavailable") }}
	s = auditDeliveryService(r)
	s.Submit(ResponseAudit{Status: "failed"})
	s.Stop()
	require.EqualValues(t, 3, r.calls.Load())
	require.EqualValues(t, 1, s.failures.Load())
}
func TestResponseAuditOverflowNeverRunsInline(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r := &auditDeliveryRepo{save: func(context.Context, *ResponseAudit) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}}
	s := auditDeliveryService(r)
	s.Submit(ResponseAudit{})
	<-started
	s.Submit(ResponseAudit{}) // the only waiting queue slot
	done := make(chan struct{})
	go func() { s.Submit(ResponseAudit{}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(release)
		s.Stop()
		t.Fatal("optional audit blocked the response")
	}
	require.EqualValues(t, 1, s.dropped.Load())
	close(release)
	s.Stop()
	require.EqualValues(t, 2, r.calls.Load())
	s.Submit(ResponseAudit{})
	require.EqualValues(t, 2, s.dropped.Load())
}
func TestResponseAuditTerminalAndUnknownEvidence(t *testing.T) {
	s := auditTestSession(false)
	auditSSE(s, `{"type":"response.completed","response":{"error":null,"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}`)
	s.Write([]byte(": ping\n\n"), 0, errors.New("close after terminal"), true)
	s.Finish(200, true)
	require.Equal(t, "success", s.current.Status)
	require.True(t, s.current.TerminalWritten)
	require.False(t, s.current.ClientDisconnected)
	s = auditTestSession(false)
	auditSSE(s, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","function":{"name":"lookup"}}]}}]}`)
	auditSSE(s, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
	auditSSE(s, `[DONE]`)
	s.Finish(200, false)
	require.False(t, s.current.ToolWritten, "missing chat arguments are not a complete tool call")
	s = auditTestSession(false)
	auditSSE(s, `{"type":"response.incomplete","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}}`)
	s.Finish(200, false)
	require.Equal(t, "partial_failure", s.current.Status)
	s = auditTestSession(true)
	s.Finish(101, true)
	require.Empty(t, s.current.UpstreamRequestID, "local gateway id must not masquerade as upstream id")
}
