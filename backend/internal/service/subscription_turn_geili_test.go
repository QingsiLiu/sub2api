package service

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSubscriptionTurnAdmissionRetryAndLocalTurnReset(t *testing.T) {
	ctx, _ := ctxkey.WithUpstreamDispatch(context.Background())
	n := 0
	cancelled := []string{}
	ctx = WithSubscriptionAdmissionFactory(ctx, func(context.Context) (*UserSubscription, error) {
		n++
		return &UserSubscription{ID: 1, AdmissionKey: fmt.Sprint(n)}, nil
	})
	ctx = WithSubscriptionAdmissionCancellation(ctx, func(_ context.Context, s *UserSubscription) error {
		cancelled = append(cancelled, s.AdmissionKey)
		return nil
	})
	rights := NewSubscriptionTurnAdmission(ctx, &UserSubscription{ID: 1})
	require.NoError(t, rights.Begin())
	first := rights.Current()
	ctxkey.MarkUpstreamDispatched(ctx)
	rights.End(true, false)
	require.NoError(t, rights.Begin())
	second := rights.Current()
	require.NotEqual(t, first.AdmissionKey, second.AdmissionKey)
	ctxkey.MarkUpstreamDispatched(ctx)
	rights.End(false, true)
	require.NoError(t, rights.Begin())
	require.Equal(t, second.AdmissionKey, rights.Current().AdmissionKey, "retry of transport-local turn 1 must preserve logical turn 2")
	rights.End(true, false)
	require.Empty(t, cancelled)
	require.NoError(t, rights.Begin())
	third := rights.Current()
	rights.CloseUnsent()
	require.Equal(t, []string{third.AdmissionKey}, cancelled)
	require.Equal(t, "1", first.AdmissionKey, "captured billing snapshot must remain immutable")
}
func TestSubscriptionTurnAdmissionUnknownConsumptionNotCancelled(t *testing.T) {
	ctx, _ := ctxkey.WithUpstreamDispatch(context.Background())
	called := false
	ctx = WithSubscriptionAdmissionFactory(ctx, func(context.Context) (*UserSubscription, error) {
		return &UserSubscription{ID: 1, AdmissionKey: "unknown"}, nil
	})
	ctx = WithSubscriptionAdmissionCancellation(ctx, func(context.Context, *UserSubscription) error { called = true; return nil })
	rights := NewSubscriptionTurnAdmission(ctx, &UserSubscription{ID: 1})
	require.NoError(t, rights.Begin())
	ctxkey.MarkUpstreamDispatched(ctx)
	rights.CloseUnsent()
	require.False(t, called)
}
