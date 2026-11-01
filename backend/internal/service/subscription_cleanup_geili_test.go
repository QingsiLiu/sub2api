//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cancellationRollbackDriverGeili struct {
	dialect.Driver
	begins, rollingBack, unblockRollback chan struct{}
	err                                  error
}

func (d *cancellationRollbackDriverGeili) Dialect() string { return dialect.Postgres }
func (d *cancellationRollbackDriverGeili) Tx(context.Context) (dialect.Tx, error) {
	d.begins <- struct{}{}
	return &cancellationRollbackTxGeili{driver: d}, nil
}

type cancellationRollbackTxGeili struct {
	driver *cancellationRollbackDriverGeili
}

func (tx *cancellationRollbackTxGeili) Exec(context.Context, string, any, any) error {
	return tx.driver.err
}
func (tx *cancellationRollbackTxGeili) Query(context.Context, string, any, any) error {
	return tx.driver.err
}
func (tx *cancellationRollbackTxGeili) Commit() error { return nil }
func (tx *cancellationRollbackTxGeili) Rollback() error {
	tx.driver.rollingBack <- struct{}{}
	<-tx.driver.unblockRollback
	return nil
}

func TestCancellationGateGeiliHoldsSlotUntilRollbackFinishes(t *testing.T) {
	sentinel := errors.New("controlled parent lock failure")
	d := &cancellationRollbackDriverGeili{begins: make(chan struct{}, 4), rollingBack: make(chan struct{}, 4), unblockRollback: make(chan struct{}), err: sentinel}
	s := &SubscriptionService{entClient: dbent.NewClient(dbent.Driver(d))}
	first := make(chan error, 1)
	go func() {
		first <- s.CancelUnsentConsumption(context.Background(), &UserSubscription{ID: 1, AdmissionKey: "first"})
	}()
	<-d.begins
	<-d.rollingBack
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := make(chan error, 1)
	go func() { second <- s.CancelUnsentConsumption(ctx, &UserSubscription{ID: 1, AdmissionKey: "second"}) }()
	require.Eventually(t, func() bool {
		s.cancellationGateGeili.mu.Lock()
		defer s.cancellationGateGeili.mu.Unlock()
		return s.cancellationGateGeili.keys[1].refs == 2
	}, time.Second, time.Millisecond)
	require.Empty(t, d.begins, "a rollback in progress must retain the subscription slot")
	cancel()
	require.ErrorIs(t, <-second, context.Canceled)
	close(d.unblockRollback)
	require.ErrorIs(t, <-first, sentinel)
	require.Empty(t, s.cancellationGateGeili.keys)
}

func TestCancellationGateGeiliQueuesBeforeTransactionAndBypassesAdmission(t *testing.T) {
	sentinel := errors.New("controlled cancellation begin failure")
	d := &admissionBeginDriverGeili{begins: make(chan struct{}, 8), unblock: make(chan struct{}), err: sentinel}
	s := &SubscriptionService{entClient: dbent.NewClient(dbent.Driver(d))}
	s.cancellationGateGeili.limit = 2
	s.admissionGateGeili.limit = 1
	holdAdmission, err := s.admissionGateGeili.acquire(context.Background(), 1)
	require.NoError(t, err)
	defer holdAdmission()
	first := make(chan error, 1)
	go func() {
		first <- s.CancelUnsentConsumption(context.Background(), &UserSubscription{ID: 1, AdmissionKey: "first"})
	}()
	select {
	case <-d.begins:
	case <-time.After(time.Second):
		t.Fatal("cleanup queued behind the unrelated admission budget")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := make(chan error, 1)
	go func() { second <- s.CancelUnsentConsumption(ctx, &UserSubscription{ID: 1, AdmissionKey: "second"}) }()
	require.Eventually(t, func() bool {
		s.cancellationGateGeili.mu.Lock()
		defer s.cancellationGateGeili.mu.Unlock()
		return s.cancellationGateGeili.keys[1].refs == 2
	}, time.Second, time.Millisecond)
	third := make(chan error, 1)
	go func() {
		third <- s.CancelUnsentConsumption(context.Background(), &UserSubscription{ID: 2, AdmissionKey: "third"})
	}()
	select {
	case <-d.begins:
	case <-time.After(time.Second):
		t.Fatal("another subscription could not start cleanup")
	}
	fourth := make(chan error, 1)
	go func() { fourth <- s.CancelUnsentConsumption(ctx, &UserSubscription{ID: 3, AdmissionKey: "fourth"}) }()
	require.Eventually(t, func() bool {
		s.cancellationGateGeili.mu.Lock()
		defer s.cancellationGateGeili.mu.Unlock()
		return s.cancellationGateGeili.keys[3] != nil
	}, time.Second, time.Millisecond)
	require.Empty(t, d.begins, "same-subscription and global-budget waiters must not lease connections")
	cancel()
	require.ErrorIs(t, <-second, context.Canceled)
	require.ErrorIs(t, <-fourth, context.Canceled)
	close(d.unblock)
	require.ErrorIs(t, <-first, sentinel)
	require.ErrorIs(t, <-third, sentinel)
	require.Empty(t, s.cancellationGateGeili.keys)
}

func TestCancellationGateGeiliReservesPoolCapacity(t *testing.T) {
	for _, tc := range []struct{ pool, want int }{{24, 2}, {12, 2}, {6, 1}, {2, 1}, {1, 1}, {96, 2}} {
		s := &SubscriptionService{}
		cfg := &config.Config{}
		cfg.Database.MaxOpenConns = tc.pool
		s.initAdmissionGateGeili(cfg)
		require.Equal(t, tc.want, s.cancellationGateGeili.limit)
	}
}

func TestUnsentLifecycleCleanupGeiliOutlivesDisconnectedCallerAndOldBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	err := cancelUnsentSubscriptionLifecycleGeili(ctx, &UserSubscription{ID: 1, AdmissionKey: "proven-unsent"}, func(cleanup context.Context, _ *UserSubscription) error {
		require.NoError(t, cleanup.Err())
		deadline, ok := cleanup.Deadline()
		require.True(t, ok)
		require.InDelta(t, 120, time.Until(deadline).Seconds(), 1)
		select {
		case <-time.After(5100 * time.Millisecond):
			return cleanup.Err()
		case <-cleanup.Done():
			return cleanup.Err()
		}
	})
	require.NoError(t, err)
	require.Greater(t, time.Since(started), 5*time.Second)
}

func TestUnsentLifecycleCleanupGeiliRetriesAreBoundedAndErrorsRemainVisible(t *testing.T) {
	sentinel := errors.New("controlled cleanup failure")
	sub := &UserSubscription{ID: 1, AdmissionKey: "proven-unsent"}
	attempts := 0
	err := cancelUnsentSubscriptionLifecycleGeili(context.Background(), sub, func(context.Context, *UserSubscription) error {
		attempts++
		if attempts < 3 {
			return sentinel
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, attempts)
	attempts = 0
	err = cancelUnsentSubscriptionLifecycleGeili(context.Background(), sub, func(context.Context, *UserSubscription) error {
		attempts++
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 3, attempts)
}
