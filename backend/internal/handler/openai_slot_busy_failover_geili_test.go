//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type saturatedConcurrencyCacheGeili struct {
	fakeConcurrencyCache
	queueFull bool
}

func (c *saturatedConcurrencyCacheGeili) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return false, nil
}

func (c *saturatedConcurrencyCacheGeili) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	return !c.queueFull, nil
}

func newSlotBusyTestGeili(t *testing.T, cache *saturatedConcurrencyCacheGeili, optIn bool) (*OpenAIGatewayHandler, *gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &OpenAIGatewayHandler{
		gatewayService:    &service.OpenAIGatewayService{},
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatClaude, 0),
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if optIn {
		enableOpenAISlotBusyFailoverGeili(c)
	}
	return h, c, w
}

func slotBusySelectionGeili(id int64) *service.AccountSelectionResult {
	account := &service.Account{ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1}
	return &service.AccountSelectionResult{
		Account:  account,
		WaitPlan: &service.AccountWaitPlan{AccountID: id, MaxConcurrency: 1, Timeout: 20 * time.Millisecond, MaxWaiting: 1},
	}
}

func TestOpenAISlotBusyFailoverGeili(t *testing.T) {
	groupID := int64(4)

	// Reference: the response written today when the sticky slot never frees.
	h, c, original := newSlotBusyTestGeili(t, &saturatedConcurrencyCacheGeili{}, false)
	streamStarted := false
	release, result := h.acquireResponsesAccountSlot(c, &groupID, "", slotBusySelectionGeili(1), false, &streamStarted, zap.NewNop())
	require.Nil(t, release)
	require.Equal(t, openAISlotAcquireFailed, result, "loops that do not opt in keep the official behavior")
	require.Equal(t, http.StatusTooManyRequests, original.Code)
	require.Contains(t, original.Body.String(), gatewayConcurrencyLimitCode)

	t.Run("opted-in timeout defers the error and asks for another account", func(t *testing.T) {
		h, c, w := newSlotBusyTestGeili(t, &saturatedConcurrencyCacheGeili{}, true)
		streamStarted := false
		release, result := h.acquireResponsesAccountSlot(c, &groupID, "", slotBusySelectionGeili(1), false, &streamStarted, zap.NewNop())
		require.Nil(t, release)
		require.Equal(t, openAISlotAcquireBusyGeili, result)
		require.Zero(t, w.Body.Len(), "nothing is written while another account may still take the request")

		require.True(t, writeOpenAISlotBusyExhaustedGeili(c), "no other account: the original error is written")
		require.Equal(t, original.Code, w.Code)
		require.Equal(t, original.Body.String(), w.Body.String())
		require.False(t, writeOpenAISlotBusyExhaustedGeili(c), "the deferred error is written once")
	})

	t.Run("switch budget is bounded", func(t *testing.T) {
		h, c, w := newSlotBusyTestGeili(t, &saturatedConcurrencyCacheGeili{}, true)
		streamStarted := false
		for i := 0; i < openAISlotBusyMaxSwitchesGeili; i++ {
			_, result := h.acquireResponsesAccountSlot(c, &groupID, "", slotBusySelectionGeili(int64(10+i)), false, &streamStarted, zap.NewNop())
			require.Equal(t, openAISlotAcquireBusyGeili, result)
		}
		_, result := h.acquireResponsesAccountSlot(c, &groupID, "", slotBusySelectionGeili(99), false, &streamStarted, zap.NewNop())
		require.Equal(t, openAISlotAcquireFailed, result)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("full wait queue also tries another account", func(t *testing.T) {
		h, c, w := newSlotBusyTestGeili(t, &saturatedConcurrencyCacheGeili{queueFull: true}, true)
		streamStarted := false
		_, result := h.acquireResponsesAccountSlot(c, &groupID, "", slotBusySelectionGeili(1), false, &streamStarted, zap.NewNop())
		require.Equal(t, openAISlotAcquireBusyGeili, result)
		require.Zero(t, w.Body.Len())
		require.True(t, writeOpenAISlotBusyExhaustedGeili(c))
		require.Equal(t, http.StatusTooManyRequests, w.Code)
		require.Contains(t, w.Body.String(), gatewayQueueFullCode)
	})

	t.Run("client cancellation never switches", func(t *testing.T) {
		h, c, _ := newSlotBusyTestGeili(t, &saturatedConcurrencyCacheGeili{}, true)
		ctx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		c.Request = c.Request.WithContext(ctx)
		streamStarted := false
		_, result := h.acquireResponsesAccountSlot(c, &groupID, "", slotBusySelectionGeili(1), false, &streamStarted, zap.NewNop())
		require.NotEqual(t, openAISlotAcquireBusyGeili, result)
	})

	t.Run("admission clears a deferred error", func(t *testing.T) {
		_, c, _ := newSlotBusyTestGeili(t, &saturatedConcurrencyCacheGeili{}, true)
		require.True(t, deferOpenAISlotBusyGeili(c, func() { t.Fatal("must not be written after admission") }))
		clearOpenAISlotBusyGeili(c)
		require.False(t, writeOpenAISlotBusyExhaustedGeili(c))
	})
}

func TestOpenAISlotBusyRequeueGeili(t *testing.T) {
	t.Run("only saturated exclusions are re-admitted once", func(t *testing.T) {
		_, c, _ := newSlotBusyTestGeili(t, &saturatedConcurrencyCacheGeili{}, true)
		excluded := map[int64]struct{}{6453: {}}
		markOpenAISlotBusyAccountGeili(c, 6453)
		require.True(t, service.BusyRequeuePendingGeili(selectContextGeili(c, excluded)),
			"while a saturated candidate can be re-admitted, the recovery probe is pointless")
		require.True(t, requeueBusyAccountsGeili(c, excluded))
		require.Empty(t, excluded, "the saturated account becomes a candidate again")
		require.False(t, requeueBusyAccountsGeili(c, excluded), "the requeue budget is bounded")
	})

	t.Run("a real failure is never re-admitted", func(t *testing.T) {
		_, c, _ := newSlotBusyTestGeili(t, &saturatedConcurrencyCacheGeili{}, true)
		excluded := map[int64]struct{}{6453: {}, 6400: {}}
		markOpenAISlotBusyAccountGeili(c, 6453)
		require.False(t, requeueBusyAccountsGeili(c, excluded), "an account that failed upstream must stay excluded")
		require.Len(t, excluded, 2)
	})

	t.Run("without a saturated exclusion the context is never marked", func(t *testing.T) {
		_, c, _ := newSlotBusyTestGeili(t, &saturatedConcurrencyCacheGeili{}, true)
		require.False(t, service.BusyRequeuePendingGeili(selectContextGeili(c, nil)))
	})
}
