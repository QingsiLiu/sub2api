//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestEncryptedRouteBulkheadGeiliRealPGLockStormPreservesBusinessPool(t *testing.T) {
	ctx := context.Background()
	metadata, err := sql.Open("postgres", integrationEncryptedDSNGeili)
	require.NoError(t, err)
	metadata.SetMaxOpenConns(2)
	metadata.SetMaxIdleConns(2)
	t.Cleanup(func() { _ = metadata.Close() })
	var userID int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',10,10) RETURNING id`, uuid.NewString()+"@metadata-bulkhead.invalid").Scan(&userID))
	r := encryptedSourceFixtureGeili()
	key := encryptedRouteKeyGeili(userID, r.Digest)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM users WHERE id=$1", userID)
		_ = integrationRedis.Del(ctx, key).Err()
	})
	c := NewGatewayCache(integrationRedis, metadata).(*gatewayCache)
	c.encryptedRouteLastPrune.Store(time.Now().Unix())
	require.NoError(t, c.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{r}))
	lock, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = lock.Rollback() }()
	_, err = lock.ExecContext(ctx, "LOCK TABLE ONLY openai_encrypted_sources_geili IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 64)
	var workers sync.WaitGroup
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			lookup, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_, failure := c.GetOpenAIEncryptedRoutesGeili(lookup, userID, []string{r.Digest})
			results <- failure
		}()
	}
	started := time.Now()
	close(start)
	workers.Wait()
	close(results)
	knownHints := 0
	var admittedFailure error
	for failure := range results {
		require.Error(t, failure)
		var hint interface{ KnownOpenAIEncryptedSourceHintGeili() bool }
		require.ErrorAs(t, failure, &hint)
		if hint.KnownOpenAIEncryptedSourceHintGeili() {
			knownHints++
		}
		if actual, ok := failure.(*encryptedRouteLookupErrorGeili); ok && actual.admitted {
			admittedFailure = failure
		}
	}
	// The 10ms Redis budget may expire under the concurrent burst. It must not
	// supply authority, while every successfully read presence hint is retained.
	require.Positive(t, knownHints)
	c.encryptedRouteGuard.mu.Lock()
	generation := c.encryptedRouteGuard.generation
	until := c.encryptedRouteGuard.blockedUntil
	active := c.encryptedRouteGuard.inFlight
	c.encryptedRouteGuard.mu.Unlock()
	require.GreaterOrEqual(t, generation, uint64(1))
	require.LessOrEqual(t, generation, uint64(2), "64 callers may cause at most two canceled PG attempts")
	require.Zero(t, active)
	require.Zero(t, metadata.Stats().WaitCount, "bulkhead rejects instead of waiting for database/sql slots")
	// Preserve the original tracked-session single retry inside its cumulative
	// 250ms cap, even while unrelated callers are rejected during cooldown.
	retryDeadline, stopRetry := context.WithDeadline(ctx, started.Add(250*time.Millisecond))
	retryCtx, allowed := service.WithOpenAIEncryptedMetadataRetryGeili(retryDeadline, admittedFailure)
	require.True(t, allowed)
	_, retryErr := c.GetOpenAIEncryptedRoutesGeili(retryCtx, userID, []string{r.Digest})
	stopRetry()
	require.Error(t, retryErr)
	require.False(t, errors.Is(retryErr, errEncryptedRouteBusyGeili), "the previously admitted request retains its one retry")
	require.Less(t, time.Since(started), 350*time.Millisecond)
	c.encryptedRouteGuard.mu.Lock()
	generationAfterRetry := c.encryptedRouteGuard.generation
	until = c.encryptedRouteGuard.blockedUntil
	c.encryptedRouteGuard.mu.Unlock()
	require.LessOrEqual(t, generationAfterRetry, uint64(3), "only the two initial attempts and one reserved retry may cancel")
	for range 100 {
		_, failure := c.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
		require.ErrorIs(t, failure, errEncryptedRouteBusyGeili)
		require.ErrorIs(t, c.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{r}), errEncryptedRouteBusyGeili)
	}
	c.encryptedRouteGuard.mu.Lock()
	deadlineAfterBurst := c.encryptedRouteGuard.blockedUntil
	c.encryptedRouteGuard.mu.Unlock()
	require.Equal(t, until, deadlineAfterBurst, "request volume cannot extend cooldown")
	// Metadata holds an exclusive table lock, but the independent business pool
	// can still read and atomically update this user's financial balance.
	businessCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	billing, err := integrationDB.BeginTx(businessCtx, nil)
	require.NoError(t, err)
	defer func() { _ = billing.Rollback() }()
	var balance float64
	require.NoError(t, billing.QueryRowContext(businessCtx, "UPDATE users SET balance=balance-0.25 WHERE id=$1 RETURNING balance", userID).Scan(&balance))
	require.Equal(t, 9.75, balance)
	require.NoError(t, billing.Rollback())
	require.NoError(t, lock.Rollback())
	_, err = integrationDB.Exec("UPDATE openai_encrypted_sources_geili SET conflict=TRUE WHERE user_id=$1", userID)
	require.NoError(t, err)
	// An unlocked table alone cannot bypass cooldown. Once it expires, the
	// recovery probe must use fresh PG truth rather than the stale Redis entry.
	_, err = c.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.True(t, errors.Is(err, errEncryptedRouteBusyGeili))
	if remaining := time.Until(until); remaining > 0 {
		time.Sleep(remaining + 10*time.Millisecond)
	}
	got, err := c.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.True(t, got[r.Digest].Conflict)
	_, err = integrationDB.Exec("DELETE FROM openai_encrypted_sources_geili WHERE user_id=$1", userID)
	require.NoError(t, err)
	got, err = c.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.Empty(t, got)
	t.Logf("64 concurrent lookups: initial canceled PG attempts=%d, including reserved retry=%d, pool waits=%d; financial transaction remained available", generation, generationAfterRetry, metadata.Stats().WaitCount)
}
