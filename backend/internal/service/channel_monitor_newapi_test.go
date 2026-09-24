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
