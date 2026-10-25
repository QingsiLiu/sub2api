//go:build integration

package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// This proxy owns one local fixture pool only. Startup/cancel packets pass
// unchanged; only the first ReadyForQuery after a source SELECT Parse is held.
// Packet contents and DSNs are never recorded, and cleanup joins every worker.
func encryptedSourceWireDelayGeili(t *testing.T, delay time.Duration) (string, *atomic.Int64) {
	t.Helper()
	address, err := url.Parse(integrationEncryptedDSNGeili)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	upstreamAddress := address.Host
	address.Host = listener.Addr().String()
	var mu sync.Mutex
	connections := []net.Conn{}
	closing := false
	stopped := make(chan struct{})
	acceptedDone := make(chan struct{})
	var workers sync.WaitGroup
	delayed := &atomic.Int64{}
	register := func(conn net.Conn) {
		mu.Lock()
		defer mu.Unlock()
		if closing {
			_ = conn.Close()
		} else {
			connections = append(connections, conn)
		}
	}
	readFrame := func(conn net.Conn) ([]byte, error) {
		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			return nil, err
		}
		length := int(binary.BigEndian.Uint32(header[1:]))
		if length < 4 || length > 1<<20 {
			return nil, io.ErrUnexpectedEOF
		}
		frame := make([]byte, 5+length-4)
		copy(frame, header)
		_, err := io.ReadFull(conn, frame[5:])
		return frame, err
	}
	go func() {
		defer close(acceptedDone)
		for {
			down, err := listener.Accept()
			if err != nil {
				return
			}
			register(down)
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { _ = down.Close() }()
				up, err := net.DialTimeout("tcp", upstreamAddress, time.Second)
				if err != nil {
					return
				}
				register(up)
				defer func() { _ = up.Close() }()
				header := make([]byte, 4)
				if _, err = io.ReadFull(down, header); err != nil {
					return
				}
				length := int(binary.BigEndian.Uint32(header))
				if length < 8 || length > 1<<20 {
					return
				}
				startup := make([]byte, length)
				copy(startup, header)
				if _, err = io.ReadFull(down, startup[4:]); err != nil {
					return
				}
				if _, err = up.Write(startup); err != nil {
					return
				}
				if binary.BigEndian.Uint32(startup[4:8]) == 80877102 {
					_, _ = io.Copy(down, up)
					return
				}
				var sourcePending atomic.Bool
				frontendDone := make(chan struct{})
				go func() {
					defer close(frontendDone)
					defer func() { _ = up.Close() }()
					for {
						frame, err := readFrame(down)
						if err != nil {
							return
						}
						if frame[0] == 'P' {
							body := frame[5:]
							if n := bytes.IndexByte(body, 0); n >= 0 {
								body = body[n+1:]
								if n = bytes.IndexByte(body, 0); n >= 0 {
									query := string(body[:n])
									if strings.HasPrefix(query, "SELECT cipher_digest") && strings.Contains(query, "FROM openai_encrypted_sources_geili") {
										sourcePending.Store(true)
									}
								}
							}
						}
						if _, err = up.Write(frame); err != nil {
							return
						}
					}
				}()
				for {
					frame, err := readFrame(up)
					if err != nil {
						break
					}
					if frame[0] == 'Z' && sourcePending.Swap(false) {
						delayed.Add(1)
						timer := time.NewTimer(delay)
						select {
						case <-timer.C:
						case <-stopped:
							timer.Stop()
						}
						timer.Stop()
					}
					if _, err = down.Write(frame); err != nil {
						break
					}
				}
				_ = down.Close()
				_ = up.Close()
				<-frontendDone
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptedDone
		close(stopped)
		mu.Lock()
		closing = true
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return address.String(), delayed
}

func TestEncryptedSourceGeiliDelayedPGReadyProvesSoftDeadlineAndFreshBudgetAuthority(t *testing.T) {
	for _, mutation := range []string{"original", "conflict", "deleted"} {
		t.Run(mutation, func(t *testing.T) {
			ctx := context.Background()
			dsn, delayed := encryptedSourceWireDelayGeili(t, 120*time.Millisecond)
			db, err := sql.Open("postgres", dsn)
			require.NoError(t, err)
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			t.Cleanup(func() { _ = db.Close() })
			require.NoError(t, db.PingContext(ctx))
			var userID int64
			require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,role,status,balance,concurrency) VALUES($1,'test','user','active',10,10) RETURNING id`, uuid.NewString()+"@wire-delay.invalid").Scan(&userID))
			r := encryptedSourceFixtureGeili()
			key := encryptedRouteKeyGeili(userID, r.Digest)
			t.Cleanup(func() {
				_, _ = integrationDB.Exec("DELETE FROM users WHERE id=$1", userID)
				_ = integrationRedis.Del(context.Background(), key).Err()
			})
			cache := requireEncryptedSourceCacheGeili(t, NewGatewayCache(integrationRedis, db))
			require.NoError(t, cache.SetOpenAIEncryptedRoutesGeili(ctx, userID, []service.OpenAIEncryptedRouteGeili{r}))
			presence, ok := cache.(interface {
				HasOpenAIEncryptedSourceHintGeili(context.Context, int64, []string) (bool, error)
			})
			require.True(t, ok)
			hint, err := presence.HasOpenAIEncryptedSourceHintGeili(ctx, userID, []string{r.Digest})
			require.NoError(t, err)
			require.True(t, hint)
			short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			start := time.Now()
			got, err := cache.GetOpenAIEncryptedRoutesGeili(short, userID, []string{r.Digest})
			initialFailure := err
			softElapsed := time.Since(start)
			cancel()
			require.Nil(t, got)
			require.Error(t, err)
			require.GreaterOrEqual(t, softElapsed, 100*time.Millisecond, "driver context cannot interrupt a held ReadyForQuery packet")
			require.Less(t, softElapsed, 350*time.Millisecond, "controlled proxy delay must finish without leaked readers")
			require.Equal(t, int64(1), delayed.Load())
			switch mutation {
			case "conflict":
				_, err = integrationDB.Exec("UPDATE openai_encrypted_sources_geili SET conflict=TRUE WHERE user_id=$1", userID)
			case "deleted":
				_, err = integrationDB.Exec("DELETE FROM openai_encrypted_sources_geili WHERE user_id=$1", userID)
			default:
				err = nil
			}
			require.NoError(t, err)
			fresh, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			retryCtx, admitted := service.WithOpenAIEncryptedMetadataRetryGeili(fresh, initialFailure)
			require.True(t, admitted)
			start = time.Now()
			got, err = cache.GetOpenAIEncryptedRoutesGeili(retryCtx, userID, []string{r.Digest})
			freshElapsed := time.Since(start)
			cancel()
			require.NoError(t, err)
			require.Less(t, freshElapsed, 250*time.Millisecond)
			require.Equal(t, int64(2), delayed.Load())
			if mutation == "deleted" {
				require.Empty(t, got)
			} else {
				require.Equal(t, r.AccountID, got[r.Digest].AccountID)
				require.Equal(t, r.GroupID, got[r.Digest].GroupID)
				require.Equal(t, r.Model, got[r.Digest].Model)
				require.Equal(t, r.BackendFingerprint, got[r.Digest].BackendFingerprint)
				require.Equal(t, mutation == "conflict", got[r.Digest].Conflict)
			}
			t.Logf("owned SQL ReadyForQuery delay: mutation=%s 50ms context wall=%s fresh250ms wall=%s", mutation, softElapsed, freshElapsed)
		})
	}
}
