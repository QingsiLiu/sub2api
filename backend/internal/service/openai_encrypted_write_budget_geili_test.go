package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type encryptedBudgetCacheGeili struct {
	stubGatewayCache
	mu                sync.Mutex
	writeBudgets      []time.Duration
	readBudget        time.Duration
	delay             time.Duration
	waitUntilCanceled bool
}

func (c *encryptedBudgetCacheGeili) GetOpenAIEncryptedRoutesGeili(ctx context.Context, _ int64, _ []string) (map[string]OpenAIEncryptedRouteGeili, error) {
	deadline, _ := ctx.Deadline()
	c.readBudget = time.Until(deadline)
	return map[string]OpenAIEncryptedRouteGeili{}, nil
}
func (c *encryptedBudgetCacheGeili) SetOpenAIEncryptedRoutesGeili(ctx context.Context, _ int64, _ []OpenAIEncryptedRouteGeili) error {
	deadline, _ := ctx.Deadline()
	c.mu.Lock()
	c.writeBudgets = append(c.writeBudgets, time.Until(deadline))
	c.mu.Unlock()
	if c.waitUntilCanceled {
		<-ctx.Done()
		return ctx.Err()
	}
	timer := time.NewTimer(c.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestEncryptedCaptureGeiliWriteBudgetIndependentFromLookup(t *testing.T) {
	cache := &encryptedBudgetCacheGeili{delay: 150 * time.Millisecond}
	svc, _ := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
	svc.cache = cache
	_, _, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), &APIKey{UserID: 3885}, []byte(encryptedToolHistoryFixtureGeili))
	require.NoError(t, err)
	require.Positive(t, cache.readBudget)
	require.LessOrEqual(t, cache.readBudget, 50*time.Millisecond)
	before := encryptedRouteRecordFailuresGeili.Load()
	persistEncryptedRoutesGeili(encryptedRouteWriteGeili{cache: cache, userID: 3885, records: []OpenAIEncryptedRouteGeili{{Digest: "fixture"}}})
	require.Equal(t, before, encryptedRouteRecordFailuresGeili.Load(), "150ms write must not inherit the 50ms lookup limit")
	require.Len(t, cache.writeBudgets, 1)
	require.Greater(t, cache.writeBudgets[0], 450*time.Millisecond)
	require.LessOrEqual(t, cache.writeBudgets[0], encryptedRouteCaptureWriteBudgetGeili)
}
func TestEncryptedCaptureGeiliProtectSharesOneWriteCap(t *testing.T) {
	cache := &encryptedBudgetCacheGeili{delay: 150 * time.Millisecond}
	state := &openAIEncryptedRouteStateGeili{}
	write := encryptedRouteWriteGeili{cache: cache, userID: 3885, records: []OpenAIEncryptedRouteGeili{{Digest: "fixture"}}}
	persistProtectedEncryptedRoutesGeili(state, write)
	cache.waitUntilCanceled = true
	persistProtectedEncryptedRoutesGeili(state, write)
	persistProtectedEncryptedRoutesGeili(state, write)
	require.Len(t, cache.writeBudgets, 2, "exhausted request cap must skip further writes")
	require.LessOrEqual(t, cache.writeBudgets[1], 350*time.Millisecond)
	require.GreaterOrEqual(t, state.captureSpent, encryptedRouteCaptureWriteBudgetGeili)
}
func TestEncryptedCaptureGeiliInvalidOrFailedOutputDoesNotWrite(t *testing.T) {
	cache := &encryptedBudgetCacheGeili{}
	svc, account := encryptedRecoveryFixtureGeili(&httpUpstreamRecorder{})
	svc.cache = cache
	ctx, _, err := svc.PrepareOpenAIEncryptedTurnGeili(encryptedPolicyCtxGeili(3885), &APIKey{UserID: 3885}, []byte(`{"input":"hello"}`))
	require.NoError(t, err)
	group := int64(131)
	WithOpenAIEncryptedOutputOriginGeili(ctx, &group, account, "gpt-6-astra")
	for _, payload := range []string{`not JSON`, `{"type":"response.failed","response":{"output":[{"type":"reasoning","encrypted_content":"failed"}]}}`, `{"type":"response.output_item.added","item":{"type":"reasoning","encrypted_content":"unfinished"}}`} {
		ObserveOpenAIEncryptedOutputGeili(ctx, []byte(payload))
	}
	require.Empty(t, cache.writeBudgets)
}

func TestEncryptedCaptureGeiliGenerationGapDoesNotConsumeWriteBudget(t *testing.T) {
	cache := &encryptedBudgetCacheGeili{}
	state := &openAIEncryptedRouteStateGeili{}
	write := encryptedRouteWriteGeili{cache: cache, userID: 3885, records: []OpenAIEncryptedRouteGeili{{Digest: "fixture"}}}
	persistProtectedEncryptedRoutesGeili(state, write)
	remaining := encryptedRouteCaptureWriteBudgetGeili - state.captureSpent
	time.Sleep(80 * time.Millisecond) // Upstream generation between output items.
	persistProtectedEncryptedRoutesGeili(state, write)
	require.Len(t, cache.writeBudgets, 2)
	require.Greater(t, cache.writeBudgets[1], remaining-25*time.Millisecond)
}
