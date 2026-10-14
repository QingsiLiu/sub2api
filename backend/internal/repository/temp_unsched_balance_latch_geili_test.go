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

func TestAccountBalanceLatchGeiliPersistsAcrossInstancesAndLegacyReset(t *testing.T) {
	server := miniredis.RunT(t)
	first := redis.NewClient(&redis.Options{Addr: server.Addr()})
	second := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer first.Close()
	defer second.Close()
	a := NewTempUnschedCache(first)
	b := NewTempUnschedCache(second).(service.AccountBalanceLatchCacheGeili)
	latch := a.(service.AccountBalanceLatchCacheGeili)
	ctx := context.Background()
	token, err := latch.SetAccountBalanceLatchGeili(ctx, 17)
	require.NoError(t, err)
	require.Len(t, token, 32)
	require.NoError(t, a.SetTempUnsched(ctx, 17, &service.TempUnschedState{UntilUnix: time.Now().Add(time.Minute).Unix()}))
	require.NoError(t, a.DeleteTempUnsched(ctx, 17))
	server.FastForward(48 * time.Hour)
	observed, err := b.GetAccountBalanceLatchGeili(ctx, 17)
	require.NoError(t, err)
	require.Equal(t, token, observed)
	require.Zero(t, server.TTL(accountBalanceLatchPrefixGeili+"17"))
}

func TestAccountBalanceLatchGeiliOldGrantCannotClearNewDebt(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	cache := NewTempUnschedCache(client).(service.AccountBalanceLatchCacheGeili)
	ctx := context.Background()
	old, err := cache.SetAccountBalanceLatchGeili(ctx, 17)
	require.NoError(t, err)
	fresh, err := cache.SetAccountBalanceLatchGeili(ctx, 17)
	require.NoError(t, err)
	require.NotEqual(t, old, fresh)
	cleared, err := cache.ClearAccountBalanceLatchGeili(ctx, 17, old)
	require.NoError(t, err)
	require.False(t, cleared)
	token, err := cache.GetAccountBalanceLatchGeili(ctx, 17)
	require.NoError(t, err)
	require.Equal(t, fresh, token)
	cleared, err = cache.ClearAccountBalanceLatchGeili(ctx, 17, fresh)
	require.NoError(t, err)
	require.True(t, cleared)
	token, err = cache.GetAccountBalanceLatchGeili(ctx, 17)
	require.NoError(t, err)
	require.Empty(t, token)
	cleared, err = cache.ClearAccountBalanceLatchGeili(ctx, 17, "")
	require.NoError(t, err)
	require.True(t, cleared)
}

func TestAccountBalanceLatchGeiliMalformedMarkerFailsClosed(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	cache := NewTempUnschedCache(client).(service.AccountBalanceLatchCacheGeili)
	server.Set(accountBalanceLatchPrefixGeili+"17", "malformed")
	_, err := cache.GetAccountBalanceLatchGeili(context.Background(), 17)
	require.Error(t, err)
}
