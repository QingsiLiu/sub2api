package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGeminiSupplierRecoveryGeiliRequestAndModelIsolation(t *testing.T) {
	ctx := WithRequestRecovery(context.Background(), time.Minute)
	s := &GatewayService{}
	a := &Account{ID: 10, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"public": "actual"}}}
	require.False(t, s.geminiSupplierAccountExcludedGeili(ctx, a, "public"))
	RecordGeminiSupplierFailureGeili(ctx, a, "actual", false)
	require.True(t, GeminiSupplierFailuresSeenGeili(ctx))
	require.True(t, s.geminiSupplierAccountExcludedGeili(ctx, a, "public"))
	require.False(t, s.geminiSupplierAccountExcludedGeili(ctx, a, "different"))
	require.False(t, s.geminiSupplierAccountExcludedGeili(WithRequestRecovery(context.Background(), time.Minute), a, "public"))
	// A separately bound group still shares the same parent recovery state.
	groupCtx := context.WithValue(ctx, struct{ name string }{"group"}, 2)
	require.True(t, s.geminiSupplierAccountExcludedGeili(groupCtx, a, "public"))
	RecordGeminiSupplierFailureGeili(ctx, a, "actual", true)
	require.True(t, s.geminiSupplierAccountExcludedGeili(groupCtx, a, "different"))
	other := *a
	other.ID = 11
	require.False(t, s.geminiSupplierAccountExcludedGeili(ctx, &other, "public"))
	nativeCtx := WithGeminiSupplierSelectionModelGeili(ctx, "actual")
	require.True(t, s.geminiSupplierAccountExcludedGeili(nativeCtx, a, "different"))
	// Recording does not start or extend the request's recovery deadline.
	state := requestRecoveryFromContext(ctx)
	require.True(t, state.deadline.IsZero())
	require.Equal(t, StatusActive, a.Status)
	require.True(t, a.Schedulable)
}

func TestGeminiSupplierRecoveryGeiliConcurrentAccess(t *testing.T) {
	ctx := WithRequestRecovery(context.Background(), time.Minute)
	a := &Account{ID: 20, Platform: PlatformGemini}
	s := &GatewayService{}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RecordGeminiSupplierFailureGeili(ctx, a, "model", false)
			_ = s.geminiSupplierAccountExcludedGeili(ctx, a, "model")
			_ = GeminiSupplierFailuresSeenGeili(ctx)
		}()
	}
	wg.Wait()
	require.True(t, s.geminiSupplierAccountExcludedGeili(ctx, a, "model"))
}
