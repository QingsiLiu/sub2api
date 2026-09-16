package service

import (
	"context"
	"sync"
)

type keyRPMContextKey struct{}
type keyRequestRPM struct {
	mu      sync.Mutex
	results map[int64]error
}

// WithKeyRequestRPM shares the user-level RPM decision across group attempts.
// Each concrete group retains its own independent capacity check.
func WithKeyRequestRPM(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyRPMContextKey{}, &keyRequestRPM{results: map[int64]error{}})
}
func onceKeyRequestRPM(ctx context.Context, userID int64, check func() error) error {
	state, ok := ctx.Value(keyRPMContextKey{}).(*keyRequestRPM)
	if !ok {
		return check()
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if result, exists := state.results[userID]; exists {
		return result
	}
	result := check()
	state.results[userID] = result
	return result
}
