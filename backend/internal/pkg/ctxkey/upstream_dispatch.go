package ctxkey

import (
	"context"
	"sync/atomic"
)

type dispatchContextKey struct{}
type dispatchSlot struct {
	current atomic.Pointer[UpstreamDispatch]
}

// Each serial websocket turn gets its own sticky marker while retries retain it.
type UpstreamDispatch struct{ started atomic.Bool }

func WithUpstreamDispatch(ctx context.Context) (context.Context, *UpstreamDispatch) {
	slot := &dispatchSlot{}
	state := &UpstreamDispatch{}
	slot.current.Store(state)
	return context.WithValue(ctx, dispatchContextKey{}, slot), state
}
func NextUpstreamDispatch(ctx context.Context) *UpstreamDispatch {
	state := &UpstreamDispatch{}
	if slot, ok := ctx.Value(dispatchContextKey{}).(*dispatchSlot); ok {
		slot.current.Store(state)
	}
	return state
}
func MarkUpstreamDispatched(ctx context.Context) {
	if slot, ok := ctx.Value(dispatchContextKey{}).(*dispatchSlot); ok {
		if state := slot.current.Load(); state != nil {
			state.started.Store(true)
		}
	}
}
func (s *UpstreamDispatch) Started() bool { return s != nil && s.started.Load() }
