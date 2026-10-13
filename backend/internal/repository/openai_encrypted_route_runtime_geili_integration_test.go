//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestEncryptedSourceGeiliRealRedisAndPGCaptureBudgetAndUserForeignKeyLock(t *testing.T) {
	ctx := context.Background()
	var userID int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',10,10) RETURNING id`, uuid.NewString()+"@source-lock.invalid").Scan(&userID))
	a := requireEncryptedSourceCacheGeili(t, NewGatewayCache(integrationRedis, integrationDB))
	r := encryptedSourceFixtureGeili()
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM users WHERE id=$1", userID)
		_ = integrationRedis.Del(context.Background(), encryptedRouteKeyGeili(userID, r.Digest)).Err()
	})
	// Use the old short deadline for healthy-write baseline with real Redis8, not miniredis.
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	start := time.Now()
	err := a.SetOpenAIEncryptedRoutesGeili(short, userID, []service.OpenAIEncryptedRouteGeili{r})
	cancel()
	require.NoError(t, err)
	t.Logf("healthy short-budget capture elapsed=%s", time.Since(start))
	key := encryptedRouteKeyGeili(userID, r.Digest)
	raw, err := integrationRedis.Get(ctx, key).Result()
	require.NoError(t, err)
	require.Contains(t, raw, r.Digest)
	// Force a cache miss so the FK locking case tests an actual INSERT, not UPDATE.
	_, err = integrationDB.Exec("DELETE FROM openai_encrypted_sources_geili WHERE user_id=$1", userID)
	require.NoError(t, err)
	blocker, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID)
	require.NoError(t, err)
	short, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
	start = time.Now()
	err = a.SetOpenAIEncryptedRoutesGeili(short, userID, []service.OpenAIEncryptedRouteGeili{r})
	cancel()
	require.Error(t, err)
	require.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)
	var n int
	require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM openai_encrypted_sources_geili WHERE user_id=$1", userID).Scan(&n))
	require.Zero(t, n)
	require.NoError(t, blocker.Rollback())
	short, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
	require.NoError(t, a.SetOpenAIEncryptedRoutesGeili(short, userID, []service.OpenAIEncryptedRouteGeili{r}))
	cancel()
	// Existing billing only changes non-key balance fields. NO KEY UPDATE must
	// remain compatible with the provenance foreign-key KEY SHARE check.
	balanceTx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = balanceTx.Rollback() }()
	_, err = balanceTx.ExecContext(ctx, `UPDATE users SET balance=balance WHERE id=$1`, userID)
	require.NoError(t, err)
	second := r
	second.Digest = strings.Repeat("c", 64)
	short, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
	require.NoError(t, a.SetOpenAIEncryptedRoutesGeili(short, userID, []service.OpenAIEncryptedRouteGeili{second}))
	cancel()
	require.NoError(t, balanceTx.Rollback())
	t.Cleanup(func() {
		_ = integrationRedis.Del(context.Background(), encryptedRouteKeyGeili(userID, second.Digest)).Err()
	})
	b := requireEncryptedSourceCacheGeili(t, NewGatewayCache(integrationRedis, integrationDB))
	got, err := b.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.Equal(t, r.AccountID, got[r.Digest].AccountID)
}

func TestEncryptedSourceGeiliRealPGWriteBudgetAllowsTransientLockButBoundsPersistentLock(t *testing.T) {
	ctx := context.Background()
	var userID int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',10,10) RETURNING id`, uuid.NewString()+"@write-budget.invalid").Scan(&userID))
	r := encryptedSourceFixtureGeili()
	cache := requireEncryptedSourceCacheGeili(t, NewGatewayCache(integrationRedis, integrationDB))
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM users WHERE id=$1", userID)
		_ = integrationRedis.Del(context.Background(), encryptedRouteKeyGeili(userID, r.Digest)).Err()
	})
	blocker, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID)
	require.NoError(t, err)
	released := make(chan error, 1)
	go func() { time.Sleep(150 * time.Millisecond); released <- blocker.Rollback() }()
	writeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	start := time.Now()
	err = cache.SetOpenAIEncryptedRoutesGeili(writeCtx, userID, []service.OpenAIEncryptedRouteGeili{r})
	cancel()
	require.NoError(t, err)
	require.NoError(t, <-released)
	require.GreaterOrEqual(t, time.Since(start), 140*time.Millisecond)
	_, err = integrationDB.Exec("DELETE FROM openai_encrypted_sources_geili WHERE user_id=$1", userID)
	require.NoError(t, err)
	persistent, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = persistent.Rollback() }()
	_, err = persistent.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID)
	require.NoError(t, err)
	writeCtx, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
	start = time.Now()
	err = cache.SetOpenAIEncryptedRoutesGeili(writeCtx, userID, []service.OpenAIEncryptedRouteGeili{r})
	cancel()
	require.Error(t, err)
	require.GreaterOrEqual(t, time.Since(start), 450*time.Millisecond)
	var count int
	require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM openai_encrypted_sources_geili WHERE user_id=$1", userID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, persistent.Rollback())
	invalid := r
	invalid.BackendFingerprint = "invalid"
	require.Error(t, cache.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{invalid}))
	require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM openai_encrypted_sources_geili WHERE user_id=$1", userID).Scan(&count))
	require.Zero(t, count)
}

func TestEncryptedSourceGeiliRealPGOverridesStaleRedisAfterFailedConflictPublication(t *testing.T) {
	ctx := context.Background()
	var userID int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',10,10) RETURNING id`, uuid.NewString()+"@stale-authority.invalid").Scan(&userID))
	r := encryptedSourceFixtureGeili()
	key := encryptedRouteKeyGeili(userID, r.Digest)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM users WHERE id=$1", userID)
		_ = integrationRedis.Del(context.Background(), key).Err()
	})
	cache := requireEncryptedSourceCacheGeili(t, NewGatewayCache(integrationRedis, integrationDB))
	require.NoError(t, cache.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{r}))
	opts := *integrationRedis.Options()
	unavailable := redis.NewClient(&opts)
	require.NoError(t, unavailable.Close())
	writer := requireEncryptedSourceCacheGeili(t, NewGatewayCache(unavailable, integrationDB))
	conflict := r
	conflict.AccountID++
	require.NoError(t, writer.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{conflict}))
	raw, err := integrationRedis.Get(ctx, key).Result()
	require.NoError(t, err)
	var stale service.OpenAIEncryptedRouteGeili
	require.NoError(t, json.Unmarshal([]byte(raw), &stale))
	require.False(t, stale.Conflict)
	var before time.Time
	require.NoError(t, integrationDB.QueryRow("SELECT last_accessed_at FROM openai_encrypted_sources_geili WHERE user_id=$1 AND cipher_digest=$2", userID, r.Digest).Scan(&before))
	ttlBefore := integrationRedis.TTL(ctx, key).Val()
	lookup, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	got, err := cache.GetOpenAIEncryptedRoutesGeili(lookup, userID, []string{r.Digest})
	cancel()
	require.NoError(t, err)
	require.True(t, got[r.Digest].Conflict)
	require.Equal(t, r.AccountID, got[r.Digest].AccountID)
	var after time.Time
	require.NoError(t, integrationDB.QueryRow("SELECT last_accessed_at FROM openai_encrypted_sources_geili WHERE user_id=$1 AND cipher_digest=$2", userID, r.Digest).Scan(&after))
	require.Equal(t, before, after)
	require.LessOrEqual(t, integrationRedis.TTL(ctx, key).Val(), ttlBefore, "cache hits must not extend their fixed TTL")
	_, err = integrationDB.Exec("DELETE FROM openai_encrypted_sources_geili WHERE user_id=$1", userID)
	require.NoError(t, err)
	got, err = cache.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, writer.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{r}))
	_, err = integrationDB.Exec("UPDATE openai_encrypted_sources_geili SET last_accessed_at=NOW()-INTERVAL '181 days' WHERE user_id=$1", userID)
	require.NoError(t, err)
	got, err = cache.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.Empty(t, got)
	// A mixed lookup refreshes only the Redis miss, while checking both SQL origins.
	_, err = integrationDB.Exec("UPDATE openai_encrypted_sources_geili SET last_accessed_at=NOW()-INTERVAL '1 day' WHERE user_id=$1", userID)
	require.NoError(t, err)
	second := r
	second.Digest = strings.Repeat("d", 64)
	require.NoError(t, writer.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{second}))
	_, err = integrationDB.Exec("UPDATE openai_encrypted_sources_geili SET last_accessed_at=NOW()-INTERVAL '1 day' WHERE user_id=$1", userID)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRow("SELECT last_accessed_at FROM openai_encrypted_sources_geili WHERE user_id=$1 AND cipher_digest=$2", userID, r.Digest).Scan(&before))
	got, err = cache.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest, second.Digest})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.NoError(t, integrationDB.QueryRow("SELECT last_accessed_at FROM openai_encrypted_sources_geili WHERE user_id=$1 AND cipher_digest=$2", userID, r.Digest).Scan(&after))
	require.Equal(t, before, after)
	require.NoError(t, integrationDB.QueryRow("SELECT last_accessed_at FROM openai_encrypted_sources_geili WHERE user_id=$1 AND cipher_digest=$2", userID, second.Digest).Scan(&after))
	require.True(t, after.After(before))
	t.Cleanup(func() {
		_ = integrationRedis.Del(context.Background(), encryptedRouteKeyGeili(userID, second.Digest)).Err()
	})
}

func TestEncryptedSourceGeiliRealPGBlackholeRedisCannotExceedLookupOrWriteBudget(t *testing.T) {
	ctx := context.Background()
	var userID int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',10,10) RETURNING id`, uuid.NewString()+"@redis-budget.invalid").Scan(&userID))
	t.Cleanup(func() { _, _ = integrationDB.Exec("DELETE FROM users WHERE id=$1", userID) })
	parent := encryptedBlackholeRedisGeili(t)
	cache := requireEncryptedSourceCacheGeili(t, NewGatewayCache(parent, integrationDB))
	r := encryptedSourceFixtureGeili()
	write, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	start := time.Now()
	err := cache.SetOpenAIEncryptedRoutesGeili(write, userID, []service.OpenAIEncryptedRouteGeili{r})
	elapsed := time.Since(start)
	cancel()
	require.NoError(t, err)
	require.Less(t, elapsed, 500*time.Millisecond)
	writeElapsed := elapsed
	lookup, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	start = time.Now()
	got, err := cache.GetOpenAIEncryptedRoutesGeili(lookup, userID, []string{r.Digest})
	elapsed = time.Since(start)
	cancel()
	require.NoError(t, err)
	require.Equal(t, r.AccountID, got[r.Digest].AccountID)
	require.Less(t, elapsed, 75*time.Millisecond, "50ms deadline plus scheduling tolerance must beat parent's 3s socket timeout")
	require.False(t, parent.Options().ContextTimeoutEnabled)
	require.Equal(t, 3*time.Second, parent.Options().ReadTimeout)
	var count int
	require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM openai_encrypted_sources_geili WHERE user_id=$1", userID).Scan(&count))
	require.Equal(t, 1, count)
	t.Logf("blackhole Redis: write=%s lookup=%s", writeElapsed, elapsed)
}

// One accept goroutine owns all connections; cleanup closes every socket and
// joins the goroutine. No Redis command or handshake ever receives a response.
func encryptedBlackholeRedisGeili(t *testing.T) *redis.Client {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var mu sync.Mutex
	connections := []net.Conn{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range connections {
			_ = conn.Close()
		}
	})
	parent := redis.NewClient(&redis.Options{Addr: listener.Addr().String(), ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	t.Cleanup(func() { _ = parent.Close() })
	return parent
}
