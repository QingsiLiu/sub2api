package repository

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestOpenAITransportHealthGeiliRedisLifecycle(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Now().Truncate(time.Millisecond)
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first := NewTempUnschedCache(client).(service.OpenAITransportHealthCacheGeili)
	second := NewTempUnschedCache(client).(service.OpenAITransportHealthCacheGeili)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, err := first.RecordOpenAITransportFailureGeili(ctx, 6300)
		require.NoError(t, err)
	}
	require.NoError(t, second.ResetOpenAITransportFailuresGeili(ctx, 6300))
	for i := 1; i <= 3; i++ {
		decision, err := second.RecordOpenAITransportFailureGeili(ctx, 6300)
		require.NoError(t, err)
		require.EqualValues(t, i, decision.Count)
		require.Equal(t, i == 3, decision.Tripped)
	}
	until, err := first.OpenAITransportBlockedUntilGeili(ctx, 6300)
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Minute), until)
	// A success that was already in flight must not lift the block.
	require.NoError(t, first.ResetOpenAITransportFailuresGeili(ctx, 6300))
	stillBlocked, err := second.OpenAITransportBlockedUntilGeili(ctx, 6300)
	require.NoError(t, err)
	require.Equal(t, until, stillBlocked)
	decision, err := first.RecordOpenAITransportFailureGeili(ctx, 6300)
	require.NoError(t, err)
	require.False(t, decision.Tripped, "in-flight failures cannot extend quarantine")
	require.Equal(t, until, decision.Until)
	server.SetTime(now.Add(61 * time.Second))
	decision, err = first.RecordOpenAITransportFailureGeili(ctx, 6300)
	require.NoError(t, err)
	require.EqualValues(t, 1, decision.Count)
	require.False(t, decision.Tripped)
	server.FastForward(service.OpenAITransportFailureTTLGeili)
	decision, err = first.RecordOpenAITransportFailureGeili(ctx, 6300)
	require.NoError(t, err)
	require.EqualValues(t, 1, decision.Count, "stale streak expires")
	other, err := first.OpenAITransportBlockedUntilGeili(ctx, 6254)
	require.NoError(t, err)
	require.True(t, other.IsZero(), "other accounts are isolated")
}

func TestOpenAITransportHealthGeiliRedisAtomicAndIndependent(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewTempUnschedCache(client).(service.OpenAITransportHealthCacheGeili)
	var wg sync.WaitGroup
	results := make(chan service.OpenAITransportHealthDecisionGeili, 30)
	errors := make(chan error, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := store.RecordOpenAITransportFailureGeili(context.Background(), 6300)
			results <- decision
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	trips := 0
	for decision := range results {
		if decision.Tripped {
			trips++
		}
	}
	require.Equal(t, 1, trips)
	pool := NewTempUnschedCache(client).(service.OpenAIAPIKeyHealthCache)
	count, tripped, err := pool.RecordOpenAIAPIKeyHealthFailure(context.Background(), 6300, 1, 3)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.False(t, tripped, "pool breaker has a different namespace")
}

func TestOpenAITransportHealthGeiliSlowEstablishedRedisIsBounded(t *testing.T) {
	server := miniredis.RunT(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var hang atomic.Bool
	var connections sync.Map
	t.Cleanup(func() {
		listener.Close()
		connections.Range(func(key, value any) bool { key.(net.Conn).Close(); return true })
	})
	go func() {
		for {
			down, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Store(down, true)
			go func() {
				defer down.Close()
				up, err := net.Dial("tcp", server.Addr())
				if err != nil {
					return
				}
				connections.Store(up, true)
				defer up.Close()
				go io.Copy(up, down)
				buffer := make([]byte, 4096)
				for {
					n, err := up.Read(buffer)
					if err != nil {
						return
					}
					if !hang.Load() {
						if _, err = down.Write(buffer[:n]); err != nil {
							return
						}
					}
				}
			}()
		}
	}()
	parent := redis.NewClient(&redis.Options{Addr: listener.Addr().String(), ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	t.Cleanup(func() { parent.Close() })
	store := NewTempUnschedCache(parent).(service.OpenAITransportHealthCacheGeili)
	require.NoError(t, parent.Ping(context.Background()).Err())
	hang.Store(true)
	began := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = store.OpenAITransportBlockedUntilGeili(ctx, 6300)
	require.Error(t, err)
	require.Less(t, time.Since(began), 600*time.Millisecond, "already-open socket must honor health timeout without retries")
	require.False(t, parent.Options().ContextTimeoutEnabled, "health view must not alter other Redis callers")
	require.Equal(t, 3*time.Second, parent.Options().ReadTimeout)
}
