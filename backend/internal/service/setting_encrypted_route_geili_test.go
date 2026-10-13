//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestEncryptedRouteSettingsGeiliRefreshFailureLogIsClassifiedOnly(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	s := NewSettingService(&settingRepoStub{err: errors.New("postgres://private-user:secret@host/db raw cipher sensitive")}, nil)
	before := OpenAIEncryptedRouteSettingsRefreshFailuresGeili()
	require.Equal(t, "off", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	require.Equal(t, before+1, OpenAIEncryptedRouteSettingsRefreshFailuresGeili())
	require.Contains(t, output.String(), "encrypted_route.settings_refresh_failed")
	require.Contains(t, output.String(), "error_class=backend_error")
	for _, sensitive := range []string{"postgres", "private-user", "secret", "cipher", "sensitive"} {
		require.NotContains(t, output.String(), sensitive)
	}
}

func TestEncryptedRouteSettingsGeiliDefaultsValidationAndCacheIsolation(t *testing.T) {
	require.Equal(t, "off", ParseOpenAIEncryptedRouteSettingsGeili("").Mode)
	for _, raw := range []string{`null`, `{"mode":"typo"}`, `{"mode":"protect","protected_user_ids":[-1]}`, `{"mode":"protect","protected_user_ids":[3885,3885]}`} {
		require.Equal(t, "off", ParseOpenAIEncryptedRouteSettingsGeili(raw).Mode)
	}
	v := OpenAIEncryptedRouteSettingsGeili{Mode: "protect", ProtectedUserIDs: []int64{3885}, ReasoningRecoveryEnabled: true}
	data, _ := json.Marshal(v)
	repo := &settingRepoStub{values: map[string]string{SettingKeyOpenAIEncryptedRouteGeili: string(data)}}
	s := NewSettingService(repo, nil)
	got := s.GetOpenAIEncryptedRouteSettingsGeili(context.Background())
	require.Equal(t, v, got)
	got.ProtectedUserIDs[0] = 99
	require.Equal(t, int64(3885), s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).ProtectedUserIDs[0])
	repo.values[SettingKeyOpenAIEncryptedRouteGeili] = `{"mode":"observe","protected_user_ids":[]}`
	require.Equal(t, "protect", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	s.invalidateOpenAIEncryptedRouteSettingsGeili()
	require.Equal(t, "observe", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	repo.err = errors.New("db unavailable")
	s.invalidateOpenAIEncryptedRouteSettingsGeili()
	require.Equal(t, "observe", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
}

type blockedEncryptedSettingsRepoGeili struct {
	settingRepoStub
	started chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func (r *blockedEncryptedSettingsRepoGeili) GetValue(ctx context.Context, key string) (string, error) {
	if r.calls.Add(1) == 1 {
		close(r.started)
	}
	select {
	case <-r.release:
		return `{"mode":"protect","protected_user_ids":[3885]}`, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func TestEncryptedRouteSettingsGeiliColdWaitHonorsOwnBudget(t *testing.T) {
	repo := &blockedEncryptedSettingsRepoGeili{started: make(chan struct{}), release: make(chan struct{})}
	s := NewSettingService(repo, nil)
	leaderDone := make(chan struct{})
	go func() { defer close(leaderDone); s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()) }()
	<-repo.started
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	require.Equal(t, "off", s.GetOpenAIEncryptedRouteSettingsGeili(canceled).Mode)
	require.Less(t, time.Since(start), 20*time.Millisecond)
	short, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	start = time.Now()
	require.Equal(t, "off", s.GetOpenAIEncryptedRouteSettingsGeili(short).Mode)
	require.Less(t, time.Since(start), 35*time.Millisecond)
	<-leaderDone
	require.EqualValues(t, 1, repo.calls.Load())
}
func TestEncryptedRouteSettingsGeiliExpiredPeersUseLastGoodWithoutWaiting(t *testing.T) {
	repo := &blockedEncryptedSettingsRepoGeili{started: make(chan struct{}), release: make(chan struct{})}
	s := NewSettingService(repo, nil)
	s.encryptedRouteSettingsCache.Store(&cachedOpenAIEncryptedRouteSettingsGeili{settings: OpenAIEncryptedRouteSettingsGeili{Mode: "observe", ProtectedUserIDs: []int64{3885}}})
	leaderDone := make(chan struct{})
	go func() { defer close(leaderDone); s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()) }()
	<-repo.started
	start := time.Now()
	require.Equal(t, "observe", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	require.Less(t, time.Since(start), 20*time.Millisecond)
	close(repo.release)
	<-leaderDone
	require.Equal(t, "protect", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	require.EqualValues(t, 1, repo.calls.Load())
}
func TestEncryptedRouteSettingsGeiliInvalidateDiscardsInFlightOldRead(t *testing.T) {
	repo := &blockedEncryptedSettingsRepoGeili{started: make(chan struct{}), release: make(chan struct{})}
	s := NewSettingService(repo, nil)
	leaderDone := make(chan struct{})
	go func() { defer close(leaderDone); s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()) }()
	<-repo.started
	s.invalidateOpenAIEncryptedRouteSettingsGeili()
	close(repo.release)
	<-leaderDone
	cached := s.encryptedRouteSettingsCache.Load()
	require.NotNil(t, cached)
	require.Nil(t, cached.refreshing)
	require.True(t, cached.expires.IsZero())
	require.Equal(t, "protect", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	require.EqualValues(t, 2, repo.calls.Load())
}
func BenchmarkEncryptedRouteSettingsGeiliOffHotRead(b *testing.B) {
	s := NewSettingService(&settingRepoStub{}, nil)
	s.encryptedRouteSettingsCache.Store(&cachedOpenAIEncryptedRouteSettingsGeili{settings: DefaultOpenAIEncryptedRouteSettingsGeili(), expires: time.Now().Add(time.Hour)})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.GetOpenAIEncryptedRouteSettingsGeili(context.Background())
	}
}

type timeoutEncryptedSettingsRepoGeili struct {
	settingRepoStub
	timeout atomic.Bool
	calls   atomic.Int64
}

func (r *timeoutEncryptedSettingsRepoGeili) GetValue(ctx context.Context, _ string) (string, error) {
	r.calls.Add(1)
	if r.timeout.Load() {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return `{"mode":"protect","protected_user_ids":[3885]}`, nil
}
func TestEncryptedRouteSettingsGeiliColdTimeoutCachesOffThenRetriesAndRecovers(t *testing.T) {
	repo := &timeoutEncryptedSettingsRepoGeili{}
	repo.timeout.Store(true)
	s := NewSettingService(repo, nil)
	before := OpenAIEncryptedRouteSettingsRefreshFailuresGeili()
	start := time.Now()
	require.Equal(t, "off", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	require.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)
	require.Equal(t, before+1, OpenAIEncryptedRouteSettingsRefreshFailuresGeili())
	cached := s.encryptedRouteSettingsCache.Load()
	require.WithinDuration(t, time.Now().Add(5*time.Second), cached.expires, 100*time.Millisecond)
	repo.timeout.Store(false)
	require.Equal(t, "off", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	require.EqualValues(t, 1, repo.calls.Load(), "cold failure stays cached even after the database recovers")
	// Expire without a five-second sleep, exercising the real refresh path.
	s.encryptedRouteSettingsCache.Store(&cachedOpenAIEncryptedRouteSettingsGeili{settings: cached.settings})
	repo.timeout.Store(true)
	require.Equal(t, "off", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	require.EqualValues(t, 2, repo.calls.Load(), "expired fallback retries the repository")
	require.Equal(t, before+2, OpenAIEncryptedRouteSettingsRefreshFailuresGeili())
	repo.timeout.Store(false)
	s.invalidateOpenAIEncryptedRouteSettingsGeili()
	require.Equal(t, "protect", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
	require.EqualValues(t, 3, repo.calls.Load())
	repo.timeout.Store(true)
	s.invalidateOpenAIEncryptedRouteSettingsGeili()
	require.Equal(t, "protect", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode, "failed refresh retains last good policy")
	require.Equal(t, before+3, OpenAIEncryptedRouteSettingsRefreshFailuresGeili())
}

func TestEncryptedRouteSettingsGeiliMissingSettingIsExpectedDefault(t *testing.T) {
	for _, err := range []error{ErrSettingNotFound, fmt.Errorf("wrapped: %w", ErrSettingNotFound)} {
		s := NewSettingService(&settingRepoStub{err: err}, nil)
		before := OpenAIEncryptedRouteSettingsRefreshFailuresGeili()
		require.Equal(t, "off", s.GetOpenAIEncryptedRouteSettingsGeili(context.Background()).Mode)
		require.Equal(t, before, OpenAIEncryptedRouteSettingsRefreshFailuresGeili())
	}
}
