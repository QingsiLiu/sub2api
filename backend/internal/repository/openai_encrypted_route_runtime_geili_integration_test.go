//go:build integration

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestEncryptedSourceGeiliRealRedisAndPGCaptureBudgetAndUserForeignKeyLock(t *testing.T) {
	ctx := context.Background()
	var userID int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',10,10) RETURNING id`, uuid.NewString()+"@source-lock.invalid").Scan(&userID))
	a := NewGatewayCache(integrationRedis, integrationDB).(service.OpenAIEncryptedRouteCacheGeili)
	r := encryptedSourceFixtureGeili()
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM users WHERE id=$1", userID)
		_ = integrationRedis.Del(context.Background(), encryptedRouteKeyGeili(userID, r.Digest)).Err()
	})
	// Use exactly the production capture deadline with real Redis8, not miniredis.
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	start := time.Now()
	err := a.SetOpenAIEncryptedRoutesGeili(short, userID, []service.OpenAIEncryptedRouteGeili{r})
	cancel()
	require.NoError(t, err)
	t.Logf("healthy production-budget capture elapsed=%s", time.Since(start))
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
	b := NewGatewayCache(integrationRedis, integrationDB).(service.OpenAIEncryptedRouteCacheGeili)
	got, err := b.GetOpenAIEncryptedRoutesGeili(ctx, userID, []string{r.Digest})
	require.NoError(t, err)
	require.Equal(t, r.AccountID, got[r.Digest].AccountID)
}
