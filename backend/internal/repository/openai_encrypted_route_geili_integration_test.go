//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestEncryptedSourcesGeiliDurabilityConflictRetentionAndBillingIsolation(t *testing.T) {
	ctx := context.Background()
	var userID int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',123.45,5) RETURNING id`, uuid.NewString()+"@encrypted-source.invalid").Scan(&userID))
	t.Cleanup(func() { _, _ = integrationDB.Exec("DELETE FROM users WHERE id=$1", userID) })
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	a := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb, integrationDB))
	r := encryptedSourceFixtureGeili()
	require.NoError(t, a.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{r}))
	// A process restart and Redis eviction still recover origin from SQL.
	mr.FlushAll()
	b := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb, integrationDB))
	got, err := b.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.Equal(t, r.AccountID, got[r.Digest].AccountID)
	// Same backend selected through another authorized group is compatible.
	regrouped := r
	regrouped.GroupID = 128
	require.NoError(t, b.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{regrouped}))
	got, err = b.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.False(t, got[r.Digest].Conflict)
	other := r
	other.AccountID = 6025
	require.NoError(t, b.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{other}))
	got, err = a.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.True(t, got[r.Digest].Conflict)
	require.Equal(t, r.AccountID, got[r.Digest].AccountID)
	var balance float64
	require.NoError(t, integrationDB.QueryRow("SELECT balance FROM users WHERE id=$1", userID).Scan(&balance))
	require.InDelta(t, 123.45, balance, 0.000001)
	_, err = integrationDB.Exec("UPDATE openai_encrypted_sources_geili SET last_accessed_at=NOW()-INTERVAL '181 days' WHERE user_id=$1", userID)
	require.NoError(t, err)
	mr.FastForward(25 * time.Hour)
	got, err = b.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.Empty(t, got)
}
