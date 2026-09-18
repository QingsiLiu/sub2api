package ctxkey

import (
	"context"
	"testing"
)

func TestUpstreamDispatchIsStickyAcrossDetachedContext(t *testing.T) {
	ctx, state := WithUpstreamDispatch(context.Background())
	if state.Started() {
		t.Fatal("new request dispatched")
	}
	MarkUpstreamDispatched(context.WithoutCancel(ctx))
	if !state.Started() {
		t.Fatal("dispatch marker lost")
	}
	MarkUpstreamDispatched(context.Background())
}
