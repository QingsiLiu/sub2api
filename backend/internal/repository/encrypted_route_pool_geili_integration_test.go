//go:build integration

package repository

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestEncryptedRoutePoolGeiliProductionProviderLifecycle(t *testing.T) {
	u, err := url.Parse(integrationEncryptedDSNGeili)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	password, _ := u.User.Password()
	cfg := &config.Config{Timezone: "UTC", Database: config.DatabaseConfig{
		Host: u.Hostname(), Port: port, User: u.User.Username(), Password: password,
		DBName: strings.TrimPrefix(u.Path, "/"), SSLMode: "disable",
	}}
	provided, err := ProvideGatewayCache(integrationRedis, cfg)
	require.NoError(t, err)
	c := provided.(*gatewayCache)
	t.Cleanup(func() { _ = c.CloseEncryptedRouteCacheGeili() })
	require.NotSame(t, integrationDB, c.encryptedRouteDB)
	require.Equal(t, 2, c.encryptedRouteDB.Stats().MaxOpenConnections)
	var app string
	require.NoError(t, c.encryptedRouteDB.QueryRow("SELECT current_setting('application_name')").Scan(&app))
	require.Equal(t, "geili_encrypted_provenance", app)
	require.NoError(t, c.CloseEncryptedRouteCacheGeili())
	require.Error(t, c.encryptedRouteDB.Ping())
	require.NoError(t, integrationDB.Ping())
	require.NoError(t, integrationRedis.Ping(context.Background()).Err())
	// Caller-injected test/business pools remain owned by their caller.
	injected := NewGatewayCache(integrationRedis, integrationDB).(*gatewayCache)
	require.NoError(t, injected.CloseEncryptedRouteCacheGeili())
	require.NoError(t, integrationDB.Ping())
}
