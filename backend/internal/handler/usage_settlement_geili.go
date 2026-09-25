package handler

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// The closure builds and persists the final priced snapshot synchronously. It
// must never enter the optional drop/sample queue before PrepareSettlement.
var durableUsageRecords = struct {
	sync.Mutex
	active int
	idle   chan struct{}
}{}

func beginDurableUsageRecord() func() {
	durableUsageRecords.Lock()
	if durableUsageRecords.active == 0 {
		durableUsageRecords.idle = make(chan struct{})
	}
	durableUsageRecords.active++
	durableUsageRecords.Unlock()
	return func() {
		durableUsageRecords.Lock()
		durableUsageRecords.active--
		if durableUsageRecords.active == 0 {
			close(durableUsageRecords.idle)
		}
		durableUsageRecords.Unlock()
	}
}

// Called after the HTTP server has stopped accepting/drained requests and before
// infrastructure teardown. Ingress continues detached from client disconnects.
func DrainDurableUsageRecords(ctx context.Context) error {
	durableUsageRecords.Lock()
	if durableUsageRecords.active == 0 {
		durableUsageRecords.Unlock()
		return nil
	}
	done := durableUsageRecords.idle
	durableUsageRecords.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runDurableUsageRecordTask(task service.UsageRecordTask) {
	done := beginDurableUsageRecord()
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer func() {
		if v := recover(); v != nil {
			logger.L().Error("usage_settlement.ingress_panic", zap.Any("panic", v))
		}
	}()
	task(ctx)
}

func hasObservedOpenAIUsage(result *service.OpenAIForwardResult) bool {
	if result == nil {
		return false
	}
	u := result.Usage
	return u.InputTokens > 0 || u.OutputTokens > 0 || u.CacheCreationInputTokens > 0 || u.CacheReadInputTokens > 0 || u.ImageInputTokens > 0 || u.ImageOutputTokens > 0 || result.ImageCount > 0 || result.VideoCount > 0 || result.SearchCount > 0 || result.WebSearchCalls > 0 || result.AudioUsage != nil
}
