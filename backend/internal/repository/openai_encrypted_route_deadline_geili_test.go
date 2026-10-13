package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestEncryptedSourceGeiliRedisViewKeepsParentOptionsAndLifecycle(t *testing.T) {
	mr := miniredis.RunT(t)
	parent := redis.NewClient(&redis.Options{Addr: mr.Addr(), ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	t.Cleanup(func() { _ = parent.Close() })
	retries := parent.Options().MaxRetries
	cache, ok := NewGatewayCache(parent).(*gatewayCache)
	require.True(t, ok)
	require.True(t, cache.encryptedRouteRDB.Options().ContextTimeoutEnabled)
	require.Zero(t, cache.encryptedRouteRDB.Options().MaxRetries)
	require.Equal(t, 50*time.Millisecond, cache.encryptedRouteRDB.Options().ReadTimeout)
	require.False(t, parent.Options().ContextTimeoutEnabled)
	require.Equal(t, 3*time.Second, parent.Options().ReadTimeout)
	require.Equal(t, retries, parent.Options().MaxRetries)
	require.NoError(t, cache.encryptedRouteRDB.Ping(context.Background()).Err())
	require.NoError(t, parent.Ping(context.Background()).Err())
	require.NoError(t, parent.Close())
	require.Error(t, cache.encryptedRouteRDB.Ping(context.Background()).Err(), "view lifecycle belongs to parent")
	var _ service.OpenAIEncryptedRouteCacheGeili = cache
}
