//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchNewAPIBalanceSnapshot(t *testing.T) {
	swapMonitorHTTPClient(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/tenant/api/user/self", r.URL.Path)
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		require.Equal(t, "42", r.Header.Get("New-Api-User"))
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"quota": 123.5}})
	}))
	defer server.Close()
	monitorHTTPClient = server.Client()

	snapshot := fetchNewAPIBalanceSnapshot(context.Background(), server.URL+"/tenant/v1", "token", map[string]string{"New-Api-User": "42"})
	require.True(t, snapshot.Success)
	require.Equal(t, "newapi", snapshot.Source)
	require.Equal(t, "quota", snapshot.Currency)
	require.NotNil(t, snapshot.Balance)
	require.InDelta(t, 123.5, *snapshot.Balance, 0.001)
}

func TestFetchNewAPIBalanceSnapshotUnauthorized(t *testing.T) {
	swapMonitorHTTPClient(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	monitorHTTPClient = server.Client()

	snapshot := fetchNewAPIBalanceSnapshot(context.Background(), server.URL, "token", nil)
	require.False(t, snapshot.Success)
	require.True(t, snapshot.CredentialInvalid)
}

func TestNewAPIBalanceRejectsNonFiniteQuota(t *testing.T) {
	for _, raw := range []string{`"NaN"`, `"+Inf"`, `"1e999"`, `null`, `{}`} {
		_, ok := newAPIQuotaNumber(json.RawMessage(raw))
		require.False(t, ok, raw)
	}
}

func TestNewAPIBalanceDoesNotFollowRedirect(t *testing.T) {
	swapMonitorHTTPClient(t)
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "/other", http.StatusFound)
	}))
	defer server.Close()
	monitorHTTPClient = server.Client()
	snapshot := fetchNewAPIBalanceSnapshot(context.Background(), server.URL, "test-token", nil)
	require.False(t, snapshot.Success)
	require.Equal(t, 1, calls)
}

func TestNewAPIBalanceModeSwitchRequiresNewCredential(t *testing.T) {
	repo := &quotaModeRepoStub{monitor: &ChannelMonitor{ID: 1, CheckMode: MonitorCheckModeNewAPIBalance}}
	svc := newQuotaModeService(repo)
	mode := MonitorCheckModeProbe
	_, err := svc.Update(context.Background(), 1, ChannelMonitorUpdateParams{CheckMode: &mode})
	require.ErrorIs(t, err, ErrChannelMonitorMissingAPIKey)
}

func TestNewAPIBalanceV2DecryptFailureDoesNotQuery(t *testing.T) {
	repo := &quotaModeRepoStub{monitor: &ChannelMonitor{ID: 1, CheckMode: MonitorCheckModeNewAPIBalance, APIKeyDecryptFailed: true}}
	svc := newQuotaModeService(repo)
	svc.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV2}})
	_, err := svc.RunCheck(context.Background(), 1)
	require.ErrorIs(t, err, ErrChannelMonitorAPIKeyDecryptFailed)
	require.Empty(t, repo.history)
}
