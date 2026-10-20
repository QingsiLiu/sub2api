package handler

import (
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type openAITextRetryActionGeili int

const (
	openAITextRetryNoneGeili openAITextRetryActionGeili = iota
	openAITextRetryAgainGeili
	openAITextRetryStopGeili
)

type openAITextRetryStateGeili struct {
	enabled   bool
	rounds    int
	retryable map[int64]struct{}
}

func newOpenAITextRetryStateGeili(enabled bool) *openAITextRetryStateGeili {
	return &openAITextRetryStateGeili{enabled: enabled, retryable: make(map[int64]struct{})}
}

func (s *openAITextRetryStateGeili) record(a *service.Account, err *service.UpstreamFailoverError) {
	if s.enabled && service.CanRetryOpenAITextFailureGeili(a, err) {
		s.retryable[a.ID] = struct{}{}
	} else if a != nil {
		delete(s.retryable, a.ID)
	}
}

// A spent pool is a backoff point inside the existing client request. Only
// replay-safe text faults are re-admitted; every real dispatch still passes the
// ordinary scheduler, admission and billing gates. Heartbeats are not output.
func (s *openAITextRetryStateGeili) recover(c *gin.Context, gateway *service.OpenAIGatewayService, groupID *int64, model, platform string, excluded map[int64]struct{}, capability service.OpenAIEndpointCapability, selectionErr error) openAITextRetryActionGeili {
	if !s.enabled || platform != service.PlatformOpenAI || c == nil || c.Request == nil ||
		service.GeiliTextForwardWrittenSize(c) > 0 || service.IsResponseCommitted(c) ||
		selectionErr != nil && !errors.Is(selectionErr, service.ErrNoAvailableAccounts) {
		return openAITextRetryNoneGeili
	}
	plan := gateway.OpenAITextRetryPlanGeili(c.Request.Context(), groupID, model, excluded, s.retryable, capability)
	if plan.Delay <= 0 {
		return openAITextRetryNoneGeili
	}
	// Hitting the per-round switch count is not proof the pool is spent. Keep
	// all failed IDs excluded while any fresh account can still serve the turn.
	if selectionErr == nil && plan.Untried {
		return openAITextRetryAgainGeili
	}
	// No tight loop when a relay keeps failing but has no Retry-After header.
	backoff := time.Duration(min(s.rounds+1, 5)) * time.Second
	if plan.Delay < backoff {
		plan.Delay = backoff
	}
	logger.FromContext(c.Request.Context()).Warn("openai.text_retry_wait",
		zap.String("model", model), zap.Int("retry_round", s.rounds+1),
		zap.Int64("delay_ms", plan.Delay.Milliseconds()), zap.Int("re_admitted_count", len(plan.AccountIDs)))
	if !waitForRequestRecovery(c, plan.Delay) {
		return openAITextRetryStopGeili
	}
	for _, id := range plan.AccountIDs {
		delete(excluded, id)
	}
	s.rounds++
	return openAITextRetryAgainGeili
}
