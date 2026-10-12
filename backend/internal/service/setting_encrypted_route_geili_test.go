//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

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
