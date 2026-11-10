package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageWalletGateGeiliIndependentUsersAndCancelledWait(t *testing.T) {
	var gate usageWalletGateGeili
	first, err := gate.acquire(context.Background(), 7)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	other, err := gate.acquire(ctx, 8)
	require.NoError(t, err, "another wallet must not wait behind user7")
	other()
	blocked, err := gate.acquire(ctx, 7)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, blocked)
	first()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	require.Empty(t, gate.keys, "owners and cancelled waiters must not leak entries")
}

func TestUsageWalletGateGeiliSerializesOnlySameUser(t *testing.T) {
	var gate usageWalletGateGeili
	var active, maximum, completed atomic.Int64
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan struct{})
	errors := make(chan error, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := gate.acquire(ctx, 7)
			if err != nil {
				errors <- err
				return
			}
			n := active.Add(1)
			for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			completed.Add(1)
			release()
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, int64(32), completed.Load())
	require.Equal(t, int64(1), maximum.Load())
	require.Empty(t, gate.keys)
}

func TestUsageWalletGateGeiliReleaseIsIdempotent(t *testing.T) {
	var gate usageWalletGateGeili
	release, err := gate.acquire(context.Background(), 7)
	require.NoError(t, err)
	release()
	release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err = gate.acquire(ctx, 7)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, release)
	require.Empty(t, gate.keys)
}
