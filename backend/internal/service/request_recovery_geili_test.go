package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequestRecoveryGeiliClockStartsAtFailureAndSurvivesGroups(t *testing.T) {
	ctx := WithRequestRecovery(context.Background(), 600*time.Second)
	state := requestRecoveryFromContext(ctx)
	now := time.Now()
	state.now = func() time.Time { return now }
	now = now.Add(40 * time.Minute) // Initial generation does not consume recovery time.
	require.True(t, RequestRecoveryAllowed(ctx))
	initial, cancel := RequestRecoveryContext(ctx)
	cancel()
	_, hasDeadline := initial.Deadline()
	require.False(t, hasDeadline)
	require.True(t, BeginRequestRecovery(ctx))
	deadline := state.deadline
	otherGroup := WithRequestRecovery(context.WithValue(ctx, ctxkeyRecoveryTest{}, "second-group"), time.Hour)
	now = now.Add(599 * time.Second)
	require.True(t, BeginRequestRecovery(otherGroup))
	require.Equal(t, deadline, state.deadline)
	require.Equal(t, time.Second, RequestRecoveryRemaining(otherGroup))
	now = now.Add(time.Second)
	require.False(t, BeginRequestRecovery(otherGroup))
	require.False(t, RequestRecoveryAllowed(otherGroup))
	require.Zero(t, RequestRecoveryRemaining(otherGroup))
}

type ctxkeyRecoveryTest struct{}

func TestRequestRecoveryGeiliPreservesParentDeadlineAndCancellation(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx := WithRequestRecovery(parent, time.Hour)
	require.True(t, BeginRequestRecovery(ctx))
	attempt, cancelAttempt := RequestRecoveryContext(ctx)
	defer cancelAttempt()
	parentDeadline, _ := parent.Deadline()
	attemptDeadline, _ := attempt.Deadline()
	require.Equal(t, parentDeadline, attemptDeadline)
	// WithoutCancel must not restore a larger window after recovery started.
	detached, cancelDetached := RequestRecoveryContext(context.WithoutCancel(ctx))
	defer cancelDetached()
	detachedDeadline, _ := detached.Deadline()
	require.Equal(t, parentDeadline, detachedDeadline)
	require.LessOrEqual(t, RequestRecoveryRemaining(ctx), 50*time.Millisecond)
	cancel()
	require.False(t, RequestRecoveryAllowed(ctx))
	require.False(t, ClaimThinkingSignatureRecovery(ctx))
	require.False(t, ClaimRequestRecoverySelectionWait(ctx))
}

func TestRequestRecoveryGeiliSignatureAndSelectionClaimsAreRequestScoped(t *testing.T) {
	ctx := WithRequestRecovery(context.Background(), time.Minute)
	var claims atomic.Int32
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ClaimThinkingSignatureRecovery(context.WithValue(ctx, ctxkeyRecoveryTest{}, "attempt")) {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, claims.Load())
	for range 3 {
		require.True(t, ClaimRequestRecoverySelectionWait(ctx))
	}
	require.False(t, ClaimRequestRecoverySelectionWait(WithRequestRecovery(ctx, time.Hour)))
}

func TestRequestRecoveryGeiliAttemptDeadlineExpires(t *testing.T) {
	ctx := WithRequestRecovery(context.Background(), 25*time.Millisecond)
	require.True(t, BeginRequestRecovery(ctx))
	attempt, cancel := RequestRecoveryContext(ctx)
	defer cancel()
	select {
	case <-attempt.Done():
		require.ErrorIs(t, attempt.Err(), context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("recovery attempt did not stop at the shared deadline")
	}
	require.NoError(t, ctx.Err(), "budget expiry must remain distinguishable from client cancellation")
	require.False(t, RequestRecoveryAllowed(ctx))
}

func TestRequestRecoveryGeiliLongModels(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "gpt-6", "gpt-6-astra", "gpt-6-astra-20261008"} {
		require.True(t, IsLongThinkingRequestModel(model), model)
	}
	for _, model := range []string{"claude-opus-4-6", "gpt-6-sol", "gpt-6-luna", "deepseek-reasoner"} {
		require.False(t, IsLongThinkingRequestModel(model), model)
	}
}
