package handler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type handlerSettlementRepo struct{ service.UsageBillingRepository }

func (*handlerSettlementRepo) PrepareSettlement(context.Context, *service.UsageBillingCommand, *service.UsageLog) error {
	return nil
}
func (*handlerSettlementRepo) ProcessPendingSettlements(context.Context, int) ([]service.UsageSettlementApplied, error) {
	return nil, nil
}
func (*handlerSettlementRepo) DeliverSettledUsage(context.Context, int) (service.UsageSettlementDeliveryStats, error) {
	return service.UsageSettlementDeliveryStats{}, nil
}
func (*handlerSettlementRepo) SettlementHealth(context.Context) (service.UsageSettlementHealth, error) {
	return service.UsageSettlementHealth{}, nil
}

func durableHandlerServices() (*service.GatewayService, *service.OpenAIGatewayService) {
	repo := &handlerSettlementRepo{}
	cfg := &config.Config{}
	gateway := service.NewGatewayService(nil, nil, nil, repo, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	openai := service.NewOpenAIGatewayService(nil, nil, repo, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	return gateway, openai
}

func TestSettlementHandlerDurableTasksBypassFullDropAndSampleQueues(t *testing.T) {
	for _, policy := range []string{"drop", "sample"} {
		t.Run(policy, func(t *testing.T) {
			pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 1, TaskTimeout: 2 * time.Second, OverflowPolicy: policy, OverflowSamplePercent: 0, AutoScaleEnabled: false})
			release := make(chan struct{})
			started := make(chan struct{})
			t.Cleanup(func() { close(release); pool.Stop() })
			pool.Submit(func(context.Context) { close(started); <-release })
			<-started
			pool.Submit(func(context.Context) { <-release })
			dropped := false
			for i := 0; i < 100; i++ {
				if pool.Submit(func(context.Context) {}).Dropped() {
					dropped = true
					break
				}
			}
			require.True(t, dropped, "full legacy queue must expose a drop before testing durable bypass")
			gateway, openai := durableHandlerServices()
			g := &GatewayHandler{gatewayService: gateway, usageRecordWorkerPool: pool}
			o := &OpenAIGatewayHandler{gatewayService: openai, usageRecordWorkerPool: pool}
			parent := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-durable")
			parent = context.WithValue(parent, ctxkey.RequestID, "server-durable")
			parent, cancel := context.WithCancel(parent)
			cancel()
			submits := []func(context.Context, service.UsageRecordTask){g.submitUsageRecordTask, g.submitMandatoryUsageRecordTask, o.submitUsageRecordTask, o.submitMandatoryUsageRecordTask}
			var called atomic.Int64
			for _, submit := range submits {
				before := called.Load()
				submit(parent, func(ctx context.Context) {
					require.NoError(t, ctx.Err())
					require.Equal(t, "client-durable", ctx.Value(ctxkey.ClientRequestID))
					require.Equal(t, "server-durable", ctx.Value(ctxkey.RequestID))
					_, deadline := ctx.Deadline()
					require.True(t, deadline)
					called.Add(1)
				})
				require.Equal(t, before+1, called.Load(), "durable closure must finish inline before submit returns")
			}
			require.Equal(t, int64(4), called.Load())
		})
	}
}

func TestSettlementHandlerObservedUsageIncludesPartialFinalMetering(t *testing.T) {
	cases := map[string]*service.OpenAIForwardResult{
		"input": {Usage: service.OpenAIUsage{InputTokens: 1}}, "output": {Usage: service.OpenAIUsage{OutputTokens: 1}},
		"cache write": {Usage: service.OpenAIUsage{CacheCreationInputTokens: 1}}, "cache read": {Usage: service.OpenAIUsage{CacheReadInputTokens: 1}},
		"image input": {Usage: service.OpenAIUsage{ImageInputTokens: 1}}, "image output": {Usage: service.OpenAIUsage{ImageOutputTokens: 1}},
		"image": {ImageCount: 1}, "video": {VideoCount: 1}, "search": {SearchCount: 1}, "web search": {WebSearchCalls: 1}, "audio": {AudioUsage: &service.AudioUsage{Mode: "tts", DurationOrUnits: 1}},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			require.True(t, hasObservedOpenAIUsage(r), "partial network error must not discard already observed billing usage")
		})
	}
	require.False(t, hasObservedOpenAIUsage(nil))
	require.False(t, hasObservedOpenAIUsage(&service.OpenAIForwardResult{}))
}

func TestSettlementHandlerDrainWaitsCancelsAndRecoversPanic(t *testing.T) {
	require.NotPanics(t, func() { runDurableUsageRecordTask(func(context.Context) { panic("synthetic-test") }) })
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() { runDurableUsageRecordTask(func(context.Context) { close(started); <-release }); close(done) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, DrainDurableUsageRecords(ctx), context.DeadlineExceeded)
	close(release)
	<-done
	require.NoError(t, DrainDurableUsageRecords(context.Background()))
	// Shutdown never registers new work after beginning a drain. The earlier
	// panic must also have released its in-flight registration.
	require.NoError(t, DrainDurableUsageRecords(context.Background()))
}
