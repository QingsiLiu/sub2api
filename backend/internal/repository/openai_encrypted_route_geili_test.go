package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func encryptedSourceFixtureGeili() service.OpenAIEncryptedRouteGeili {
	return service.OpenAIEncryptedRouteGeili{Digest: strings.Repeat("a", 64), GroupID: 131, AccountID: 6217, Model: "gpt-6-astra", BackendFingerprint: strings.Repeat("b", 64), GeneratedAt: time.Now().UTC()}
}
func encryptedSourceRowsGeili(r service.OpenAIEncryptedRouteGeili) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"cipher_digest", "group_id", "account_id", "model", "backend_fingerprint", "conflict", "generated_at"}).AddRow(r.Digest, r.GroupID, r.AccountID, r.Model, r.BackendFingerprint, r.Conflict, r.GeneratedAt)
}

func requireEncryptedSourceCacheGeili(t *testing.T, cache service.GatewayCache) service.OpenAIEncryptedRouteCacheGeili {
	t.Helper()
	source, ok := cache.(service.OpenAIEncryptedRouteCacheGeili)
	require.True(t, ok, "gateway cache must implement durable encrypted source interface")
	return source
}

func TestEncryptedSourceGeiliDurableCacheMissAndUserIsolation(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	a := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb, db))
	b := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb, db))
	r := encryptedSourceFixtureGeili()
	mock.ExpectQuery("UPDATE openai_encrypted_sources_geili").WithArgs(int64(3885), sqlmock.AnyArg()).WillReturnRows(encryptedSourceRowsGeili(r))
	got, err := a.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest, r.Digest})
	require.NoError(t, err)
	require.Equal(t, r.AccountID, got[r.Digest].AccountID)
	mock.ExpectQuery("SELECT cipher_digest").WillReturnRows(encryptedSourceRowsGeili(r))
	got, err = b.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.NoError(t, err)
	require.Equal(t, r.AccountID, got[r.Digest].AccountID)
	mock.ExpectQuery("UPDATE openai_encrypted_sources_geili").WithArgs(int64(99), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"cipher_digest", "group_id", "account_id", "model", "backend_fingerprint", "conflict", "generated_at"}))
	got, err = b.GetOpenAIEncryptedRoutesGeili(context.Background(), 99, []string{r.Digest})
	require.NoError(t, err)
	require.Empty(t, got)
	mr.FastForward(25 * time.Hour)
	mock.ExpectQuery("UPDATE openai_encrypted_sources_geili").WithArgs(int64(3885), sqlmock.AnyArg()).WillReturnRows(encryptedSourceRowsGeili(r))
	_, err = a.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	raw, err := mr.Get(encryptedRouteKeyGeili(3885, r.Digest))
	require.NoError(t, err)
	require.NotContains(t, raw, "encrypted_content")
	require.NotContains(t, raw, "api_key")
}
func TestEncryptedSourceGeiliRedisOutageUsesDatabase(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 10 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	mr.Close()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := encryptedSourceFixtureGeili()
	mock.ExpectQuery("UPDATE openai_encrypted_sources_geili").WillReturnRows(encryptedSourceRowsGeili(r))
	got, err := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb, db)).GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.NoError(t, err)
	require.Equal(t, r.AccountID, got[r.Digest].AccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestEncryptedSourceGeiliImmutableOriginAndMonotonicConflict(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	c, ok := NewGatewayCache(rdb, db).(*gatewayCache)
	require.True(t, ok)
	c.encryptedRouteLastPrune.Store(time.Now().Unix())
	old := encryptedSourceFixtureGeili()
	old.Conflict = true
	incoming := old
	incoming.AccountID = 6025
	q := "INSERT INTO openai_encrypted_sources_geili"
	mock.ExpectBegin()
	mock.ExpectQuery(q).WithArgs(int64(3885), incoming.Digest, incoming.GroupID, incoming.AccountID, incoming.Model, incoming.BackendFingerprint, true, incoming.GeneratedAt).WillReturnRows(encryptedSourceRowsGeili(old))
	mock.ExpectCommit()
	require.NoError(t, c.SetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []service.OpenAIEncryptedRouteGeili{incoming}))
	// Simulate a write started before the conflicting insert reaching Redis late.
	late := old
	late.Conflict = false
	mock.ExpectBegin()
	mock.ExpectQuery(q).WillReturnRows(encryptedSourceRowsGeili(late))
	mock.ExpectCommit()
	require.NoError(t, c.SetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []service.OpenAIEncryptedRouteGeili{late}))
	mock.ExpectQuery("SELECT cipher_digest").WillReturnRows(encryptedSourceRowsGeili(old))
	got, err := c.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{old.Digest})
	require.NoError(t, err)
	require.True(t, got[old.Digest].Conflict)
	require.Equal(t, old.AccountID, got[old.Digest].AccountID)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestEncryptedSourceGeiliValidationAndDBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	c := requireEncryptedSourceCacheGeili(t, NewGatewayCache(nil, db))
	r := encryptedSourceFixtureGeili()
	_, err = c.GetOpenAIEncryptedRoutesGeili(context.Background(), 0, []string{r.Digest})
	require.Error(t, err)
	_, err = c.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{"plaintext"})
	require.Error(t, err)
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE openai_encrypted_sources_geili")).WillReturnError(fmt.Errorf("db unavailable"))
	_, err = c.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "db unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEncryptedSourceGeiliCacheOnlyCannotSupplyAuthority(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	r := encryptedSourceFixtureGeili()
	raw, _ := json.Marshal(r)
	require.NoError(t, mr.Set(encryptedRouteKeyGeili(3885, r.Digest), string(raw)))
	got, err := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb)).GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.Error(t, err)
	require.Nil(t, got)
	var hint interface{ KnownOpenAIEncryptedSourceHintGeili() bool }
	require.ErrorAs(t, err, &hint)
	require.True(t, hint.KnownOpenAIEncryptedSourceHintGeili())
}

func TestEncryptedSourceGeiliCacheMissDoesNotRefreshUnrelatedHitTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := encryptedSourceFixtureGeili()
	raw, _ := json.Marshal(r)
	key := encryptedRouteKeyGeili(3885, r.Digest)
	require.NoError(t, mr.Set(key, string(raw)))
	mr.SetTTL(key, time.Hour)
	c := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb, db))
	missing := strings.Repeat("c", 64)
	mock.ExpectQuery("WITH touched").WithArgs(int64(3885), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnRows(encryptedSourceRowsGeili(r))
	got, err := c.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest, missing})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, time.Hour, mr.TTL(key))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEncryptedSourceGeiliControlledFailurePhaseWithoutSensitiveMessage(t *testing.T) {
	r := encryptedSourceFixtureGeili()
	c := requireEncryptedSourceCacheGeili(t, NewGatewayCache(nil))
	err := c.SetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []service.OpenAIEncryptedRouteGeili{r})
	var diagnostic interface{ EncryptedRouteStoragePhaseGeili() string }
	require.ErrorAs(t, err, &diagnostic)
	require.Equal(t, "store_unavailable", diagnostic.EncryptedRouteStoragePhaseGeili())
	invalid := r
	invalid.Model = ""
	err = c.SetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []service.OpenAIEncryptedRouteGeili{invalid})
	require.ErrorAs(t, err, &diagnostic)
	require.Equal(t, "validate_model", diagnostic.EncryptedRouteStoragePhaseGeili())
	wrapped := encryptedRouteStorageFailureGeili("insert", fmt.Errorf("sensitive fixture text: %w", context.DeadlineExceeded))
	require.True(t, errors.Is(wrapped, context.DeadlineExceeded))
	require.NotContains(t, wrapped.Error(), "sensitive fixture text")
	require.ErrorAs(t, wrapped, &diagnostic)
	require.Equal(t, "insert", diagnostic.EncryptedRouteStoragePhaseGeili())
}

func TestEncryptedSourceGeiliCachedAuthorityRequiresSuccessfulDurableRead(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := encryptedSourceFixtureGeili()
	raw, _ := json.Marshal(r)
	require.NoError(t, mr.Set(encryptedRouteKeyGeili(3885, r.Digest), string(raw)))
	cache := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb, db))
	mock.ExpectQuery("SELECT cipher_digest").WillReturnError(context.DeadlineExceeded)
	got, err := cache.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, got)
	mock.ExpectQuery("SELECT cipher_digest").WillReturnRows(sqlmock.NewRows([]string{"cipher_digest", "group_id", "account_id", "model", "backend_fingerprint", "conflict", "generated_at"}))
	got, err = cache.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEncryptedSourceGeiliLookupHintIsPresenceOnlyAndDiagnosticsAreControlled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cached  bool
		invalid bool
		want    bool
	}{{"known", true, false, true}, {"unknown", false, false, false}, {"invalid cache", true, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			defer func() { _ = rdb.Close() }()
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			r := encryptedSourceFixtureGeili()
			if tc.invalid {
				r.BackendFingerprint = "invalid"
			}
			if tc.cached {
				raw, _ := json.Marshal(r)
				require.NoError(t, mr.Set(encryptedRouteKeyGeili(3885, r.Digest), string(raw)))
			}
			pattern := "UPDATE openai_encrypted_sources_geili"
			if tc.want {
				pattern = "SELECT cipher_digest"
			}
			cause := fmt.Errorf("secret database diagnostic: %w", context.DeadlineExceeded)
			mock.ExpectQuery(pattern).WillReturnError(cause)
			got, err := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb, db)).GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
			require.Nil(t, got)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NotContains(t, err.Error(), "secret")
			var hint interface {
				KnownOpenAIEncryptedSourceHintGeili() bool
				EncryptedRouteLookupPhaseGeili() string
			}
			require.ErrorAs(t, err, &hint)
			require.Equal(t, tc.want, hint.KnownOpenAIEncryptedSourceHintGeili())
			require.Equal(t, "query", hint.EncryptedRouteLookupPhaseGeili())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestEncryptedSourceGeiliCachedRowsFailureReturnsNoAuthority(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	r := encryptedSourceFixtureGeili()
	raw, _ := json.Marshal(r)
	require.NoError(t, mr.Set(encryptedRouteKeyGeili(3885, r.Digest), string(raw)))
	mock.ExpectQuery("SELECT cipher_digest").WillReturnRows(encryptedSourceRowsGeili(r).RowError(0, fmt.Errorf("private row failure")))
	got, err := requireEncryptedSourceCacheGeili(t, NewGatewayCache(rdb, db)).GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.Nil(t, got)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private")
	var hint interface {
		KnownOpenAIEncryptedSourceHintGeili() bool
		EncryptedRouteLookupPhaseGeili() string
	}
	require.ErrorAs(t, err, &hint)
	require.True(t, hint.KnownOpenAIEncryptedSourceHintGeili())
	require.Equal(t, "rows", hint.EncryptedRouteLookupPhaseGeili())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEncryptedSourceGeiliPreHintOnlyValidatesOwnedPresenceWithoutSQL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	gateway, ok := NewGatewayCache(rdb, db).(*gatewayCache)
	require.True(t, ok)
	r := encryptedSourceFixtureGeili()
	raw, _ := json.Marshal(r)
	require.NoError(t, mr.Set(encryptedRouteKeyGeili(3885, r.Digest), string(raw)))
	hint, err := gateway.HasOpenAIEncryptedSourceHintGeili(context.Background(), 3885, []string{r.Digest, r.Digest})
	require.NoError(t, err)
	require.True(t, hint)
	hint, err = gateway.HasOpenAIEncryptedSourceHintGeili(context.Background(), 3886, []string{r.Digest})
	require.NoError(t, err)
	require.False(t, hint)
	invalid := r
	invalid.BackendFingerprint = "invalid"
	raw, _ = json.Marshal(invalid)
	require.NoError(t, mr.Set(encryptedRouteKeyGeili(3885, r.Digest), string(raw)))
	hint, err = gateway.HasOpenAIEncryptedSourceHintGeili(context.Background(), 3885, []string{r.Digest})
	require.NoError(t, err)
	require.False(t, hint)
	_, err = gateway.HasOpenAIEncryptedSourceHintGeili(context.Background(), 3885, []string{"plaintext"})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet(), "pre-hint must never call SQL or grant a cached route")
}
