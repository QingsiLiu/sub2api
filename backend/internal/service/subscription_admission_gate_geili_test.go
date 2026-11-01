package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAdmissionGateGeiliHotSubscriptionDoesNotConsumeOtherSlots(t *testing.T) {
	g := subscriptionAdmissionGateGeili{limit: 2}
	hold, err := g.acquire(context.Background(), 1)
	require.NoError(t, err)
	var wg sync.WaitGroup
	var running, maximum atomic.Int32
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := g.acquire(context.Background(), 1)
			if err != nil {
				t.Error(err)
				return
			}
			n := running.Add(1)
			for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
			}
			running.Add(-1)
			release()
		}()
	}
	require.Eventually(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.keys[1].refs == 33
	}, time.Second, time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	other, err := g.acquire(ctx, 2)
	require.NoError(t, err, "hot-subscription waiters must not occupy the other global slot")
	other()
	hold()
	wg.Wait()
	require.EqualValues(t, 1, maximum.Load())
	require.Empty(t, g.keys)
	require.Empty(t, g.slots)
}

func TestAdmissionGateGeiliCanceledGlobalWaitReleasesSubscription(t *testing.T) {
	g := subscriptionAdmissionGateGeili{limit: 1}
	hold, err := g.acquire(context.Background(), 1)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := g.acquire(ctx, 2)
		done <- err
	}()
	require.Eventually(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.keys[2] != nil
	}, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	g.mu.Lock()
	require.NotContains(t, g.keys, int64(2))
	g.mu.Unlock()
	hold()
	hold() // Cleanup paths can safely invoke release twice.
	again, err := g.acquire(context.Background(), 2)
	require.NoError(t, err)
	again()
	require.Empty(t, g.keys)
}

func TestAdmissionGateGeiliCanceledKeyWaitDoesNotRemoveHolder(t *testing.T) {
	g := subscriptionAdmissionGateGeili{limit: 2}
	hold, err := g.acquire(context.Background(), 1)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := g.acquire(ctx, 1)
		done <- err
	}()
	require.Eventually(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.keys[1].refs == 2
	}, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	g.mu.Lock()
	require.Equal(t, 1, g.keys[1].refs)
	g.mu.Unlock()
	hold()
	require.Empty(t, g.keys)
}

func TestAdmissionGateGeiliBudgetReservesPoolCapacity(t *testing.T) {
	for _, tc := range []struct{ pool, limit int }{{24, 8}, {12, 4}, {6, 2}, {2, 1}, {1, 1}, {96, 8}} {
		s := &SubscriptionService{}
		cfg := &config.Config{}
		cfg.Database.MaxOpenConns = tc.pool
		s.initAdmissionGateGeili(cfg)
		require.Equal(t, tc.limit, s.admissionGateGeili.limit)
	}
}

type admissionBeginDriverGeili struct {
	dialect.Driver
	begins  chan struct{}
	unblock chan struct{}
	err     error
}

func (d *admissionBeginDriverGeili) Dialect() string { return dialect.Postgres }

func (d *admissionBeginDriverGeili) Tx(ctx context.Context) (dialect.Tx, error) {
	d.begins <- struct{}{}
	select {
	case <-d.unblock:
		return nil, d.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestAdmitConsumptionGeiliQueuesBeforeOpeningTransaction(t *testing.T) {
	sentinel := errors.New("controlled begin failure")
	d := &admissionBeginDriverGeili{begins: make(chan struct{}, 4), unblock: make(chan struct{}), err: sentinel}
	s := &SubscriptionService{entClient: dbent.NewClient(dbent.Driver(d))}
	s.admissionGateGeili.limit = 2
	first := make(chan error, 1)
	go func() {
		_, err := s.AdmitConsumption(context.Background(), &UserSubscription{ID: 1}, 1)
		first <- err
	}()
	<-d.begins // First request has reached BeginTx and owns its admission slot.
	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() {
		_, err := s.AdmitConsumption(ctx, &UserSubscription{ID: 1}, 1)
		second <- err
	}()
	require.Eventually(t, func() bool {
		s.admissionGateGeili.mu.Lock()
		defer s.admissionGateGeili.mu.Unlock()
		return s.admissionGateGeili.keys[1].refs == 2
	}, time.Second, time.Millisecond)
	third := make(chan error, 1)
	go func() {
		_, err := s.AdmitConsumption(context.Background(), &UserSubscription{ID: 2}, 2)
		third <- err
	}()
	select {
	case <-d.begins:
	case <-time.After(time.Second):
		t.Fatal("a different subscription could not open its transaction")
	}
	cancel()
	require.ErrorIs(t, <-second, context.Canceled)
	require.Empty(t, d.begins, "the canceled same-subscription waiter must never lease a connection")
	close(d.unblock)
	require.ErrorIs(t, <-first, sentinel)
	require.ErrorIs(t, <-third, sentinel)
	require.Empty(t, s.admissionGateGeili.keys)
}
