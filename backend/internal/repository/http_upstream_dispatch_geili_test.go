//go:build unit

package repository

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type cancelledDispatchBodyGeili struct {
	io.Reader
	closes int
}

func (b *cancelledDispatchBodyGeili) Close() error { b.closes++; return nil }

type cancelledDispatchTransportGeili func(*http.Request) (*http.Response, error)

func (f cancelledDispatchTransportGeili) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestUpstreamDispatchGeiliAlreadyCancelledNeverMarksOrCallsTransport(t *testing.T) {
	for _, expired := range []bool{false, true} {
		ctx := context.Background()
		var cancel context.CancelFunc
		if expired {
			ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
		} else {
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		defer cancel()
		ctx, marker := ctxkey.WithUpstreamDispatch(ctx)
		body := &cancelledDispatchBodyGeili{Reader: strings.NewReader("synthetic")}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://upstream.invalid", body)
		require.NoError(t, err)
		calls := 0
		client := &http.Client{Transport: cancelledDispatchTransportGeili(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, context.Canceled
		})}
		resp, err := doUpstreamRequestWithDispatchGeili(client, req)
		require.Nil(t, resp)
		require.ErrorIs(t, err, ctx.Err())
		require.False(t, marker.Started())
		require.Zero(t, calls)
		require.Equal(t, 1, body.closes)
	}
}

func TestUpstreamDispatchGeiliCancellationAfterBoundaryRetainsMarker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, marker := ctxkey.WithUpstreamDispatch(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://upstream.invalid", strings.NewReader("synthetic"))
	require.NoError(t, err)
	calls := 0
	client := &http.Client{Transport: cancelledDispatchTransportGeili(func(r *http.Request) (*http.Response, error) {
		calls++
		require.True(t, marker.Started())
		cancel()
		_ = r.Body.Close()
		return nil, context.Canceled
	})}
	_, err = doUpstreamRequestWithDispatchGeili(client, req)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
	require.True(t, marker.Started())
	_, err = doUpstreamRequestWithDispatchGeili(client, req)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
	require.True(t, marker.Started(), "an existing uncertain marker must never be cleared")
}
