package service

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type subscriptionCancelFactoryKey struct{}
type SubscriptionCancelFactory func(context.Context, *UserSubscription) error

func WithSubscriptionAdmissionCancellation(ctx context.Context, f SubscriptionCancelFactory) context.Context {
	return context.WithValue(ctx, subscriptionCancelFactoryKey{}, f)
}

// SubscriptionTurnAdmission follows a logical request, not the transport's local
// turn counter (which restarts on failover). The transport serializes turns.
type SubscriptionTurnAdmission struct {
	mu       sync.Mutex
	ctx      context.Context
	fallback *UserSubscription
	current  *UserSubscription
	dispatch *ctxkey.UpstreamDispatch
}

func NewSubscriptionTurnAdmission(ctx context.Context, sub *UserSubscription) *SubscriptionTurnAdmission {
	return &SubscriptionTurnAdmission{ctx: ctx, fallback: sub}
}
func (s *SubscriptionTurnAdmission) Begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fallback == nil || s.current != nil {
		return nil
	}
	sub, err := AdmitSubscriptionTurn(s.ctx, s.fallback)
	if err != nil {
		return err
	}
	s.current = sub
	s.dispatch = ctxkey.NextUpstreamDispatch(s.ctx)
	return nil
}
func (s *SubscriptionTurnAdmission) Current() *UserSubscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}
func (s *SubscriptionTurnAdmission) End(billingScheduled, retry bool) {
	s.mu.Lock()
	if retry && !billingScheduled {
		s.mu.Unlock()
		return
	}
	sub, state := s.current, s.dispatch
	s.current = nil
	s.dispatch = nil
	s.mu.Unlock()
	if !billingScheduled && sub != nil && !state.Started() {
		s.cancel(sub)
	}
}
func (s *SubscriptionTurnAdmission) cancel(sub *UserSubscription) {
	if f, ok := s.ctx.Value(subscriptionCancelFactoryKey{}).(SubscriptionCancelFactory); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = f(ctx, sub)
	}
}
func (s *SubscriptionTurnAdmission) CloseUnsent() { s.End(false, false) }
