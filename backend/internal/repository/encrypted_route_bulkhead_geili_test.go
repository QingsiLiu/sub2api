package repository

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestEncryptedRouteBulkheadGeiliBoundsAdmissionAndRecovery(t *testing.T) {
	var gate encryptedRouteBulkheadGeili
	first, err := gate.acquire(time.Now())
	require.NoError(t, err)
	second, err := gate.acquire(time.Now())
	require.NoError(t, err)
	_, err = gate.acquire(time.Now())
	require.ErrorIs(t, err, errEncryptedRouteBusyGeili)
	first.finish(context.DeadlineExceeded)
	until := gate.blockedUntil
	for range 100 {
		_, err = gate.acquire(time.Now())
		require.ErrorIs(t, err, errEncryptedRouteBusyGeili)
	}
	require.Equal(t, until, gate.blockedUntil, "rejections cannot keep the circuit open forever")
	second.finish(nil)
	require.Equal(t, until, gate.blockedUntil, "an older success cannot clear the failure")
	probe, err := gate.acquire(until)
	require.NoError(t, err)
	require.True(t, probe.probe)
	_, err = gate.acquire(until.Add(time.Second))
	require.ErrorIs(t, err, errEncryptedRouteBusyGeili, "one recovery probe, even after cooldown")
	probe.finish(nil)
	require.True(t, gate.blockedUntil.IsZero())
	first, err = gate.acquire(time.Now())
	require.NoError(t, err)
	second, err = gate.acquire(time.Now())
	require.NoError(t, err)
	first.finish(nil)
	second.finish(nil)
	require.Zero(t, gate.inFlight)
}

func TestEncryptedRouteBulkheadGeiliOlderFailureDoesNotAllowAnotherProbe(t *testing.T) {
	var gate encryptedRouteBulkheadGeili
	old, err := gate.acquire(time.Now())
	require.NoError(t, err)
	failed, err := gate.acquire(time.Now())
	require.NoError(t, err)
	failed.finish(context.DeadlineExceeded)
	probe, err := gate.acquire(gate.blockedUntil)
	require.NoError(t, err)
	old.finish(context.DeadlineExceeded)
	probe.finish(nil)
	require.False(t, gate.blockedUntil.IsZero(), "a stale probe cannot clear a later failure generation")
	next, err := gate.acquire(gate.blockedUntil)
	require.NoError(t, err)
	next.finish(nil)
	require.True(t, gate.blockedUntil.IsZero())
}

func TestEncryptedRouteBulkheadGeiliOnlyAvailabilityFailuresTrip(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, &net.OpError{Op: "read", Err: errors.New("closed")}, &pq.Error{Code: "57014"}, &pq.Error{Code: "53300"}, &pq.Error{Code: "08006"}} {
		require.True(t, encryptedRouteAvailabilityFailureGeili(err), "%v", err)
	}
	for _, err := range []error{nil, context.Canceled, errEncryptedRouteBusyGeili, errors.New("invalid metadata"), &pq.Error{Code: "23503"}, &pq.Error{Code: "22001"}} {
		require.False(t, encryptedRouteAvailabilityFailureGeili(err), "%v", err)
	}
}

func TestEncryptedRouteBulkheadGeiliDenialRetainsHintAndRejectsWrite(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	c := NewGatewayCache(rdb, db).(*gatewayCache)
	r := encryptedSourceFixtureGeili()
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	require.NoError(t, mr.Set(encryptedRouteKeyGeili(3885, r.Digest), string(raw)))
	permit, err := c.encryptedRouteGuard.acquire(time.Now())
	require.NoError(t, err)
	permit.finish(context.DeadlineExceeded)
	got, err := c.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.Nil(t, got)
	require.ErrorIs(t, err, errEncryptedRouteBusyGeili)
	var hint interface{ KnownOpenAIEncryptedSourceHintGeili() bool }
	require.ErrorAs(t, err, &hint)
	require.True(t, hint.KnownOpenAIEncryptedSourceHintGeili())
	err = c.SetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []service.OpenAIEncryptedRouteGeili{r})
	require.ErrorIs(t, err, errEncryptedRouteBusyGeili, "a rejected write must never report persistence success")
	require.NoError(t, mock.ExpectationsWereMet(), "no query may reach PostgreSQL during cooldown")
}

func TestEncryptedRouteBulkheadGeiliSingleRetryRetainsCapAndCooldown(t *testing.T) {
	var gate encryptedRouteBulkheadGeili
	first, err := gate.acquire(time.Now())
	require.NoError(t, err)
	first.finish(context.DeadlineExceeded)
	until := gate.blockedUntil
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	failure := &encryptedRouteLookupErrorGeili{admitted: true, cause: context.DeadlineExceeded}
	retryCtx, allowed := service.WithOpenAIEncryptedMetadataRetryGeili(ctx, failure)
	require.True(t, allowed)
	_, allowed = service.WithOpenAIEncryptedMetadataRetryGeili(ctx, failure)
	require.False(t, allowed, "one admitted failure can mint only one retry capability")
	retry, err := gate.acquireWithRetry(time.Now(), service.ConsumeOpenAIEncryptedMetadataRetryGeili(retryCtx))
	require.NoError(t, err)
	require.False(t, retry.probe)
	require.False(t, service.ConsumeOpenAIEncryptedMetadataRetryGeili(retryCtx))
	second, err := gate.acquireWithRetry(time.Now(), true)
	require.NoError(t, err)
	_, err = gate.acquireWithRetry(time.Now(), true)
	require.ErrorIs(t, err, errEncryptedRouteBusyGeili, "retry capabilities cannot exceed the two in-flight limit")
	retry.finish(nil)
	second.finish(nil)
	require.Equal(t, until, gate.blockedUntil, "a successful reserved retry cannot close the circuit")
	_, allowed = service.WithOpenAIEncryptedMetadataRetryGeili(ctx, &encryptedRouteLookupErrorGeili{cause: errEncryptedRouteBusyGeili})
	require.False(t, allowed, "admission rejection does not grant retry privilege")
	var canceled encryptedRouteBulkheadGeili
	permit, err := canceled.acquire(time.Now())
	require.NoError(t, err)
	permit.finish(context.Canceled)
	require.True(t, canceled.blockedUntil.IsZero(), "ordinary client cancellation does not globally reject other sessions")
}

func TestEncryptedRouteBulkheadGeiliClientCanceledQueryDoesNotTrip(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	c := NewGatewayCache(nil, db).(*gatewayCache)
	r := encryptedSourceFixtureGeili()
	mock.ExpectQuery("UPDATE openai_encrypted_sources_geili").WillDelayFor(time.Second).WillReturnRows(encryptedSourceRowsGeili(r))
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(20*time.Millisecond, cancel)
	defer timer.Stop()
	_, err = c.GetOpenAIEncryptedRoutesGeili(ctx, 3885, []string{r.Digest})
	require.Error(t, err)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.True(t, c.encryptedRouteGuard.blockedUntil.IsZero())
	mock.ExpectQuery("UPDATE openai_encrypted_sources_geili").WillReturnRows(encryptedSourceRowsGeili(r))
	_, err = c.GetOpenAIEncryptedRoutesGeili(context.Background(), 3885, []string{r.Digest})
	require.NoError(t, err, "the next user's metadata read remains immediately available")
	require.NoError(t, mock.ExpectationsWereMet())
}
