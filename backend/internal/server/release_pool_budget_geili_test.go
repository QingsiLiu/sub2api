package server

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/releaselifecycle"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type metadataPoolBudgetCacheGeili struct{ service.GatewayCache }

func (*metadataPoolBudgetCacheGeili) EncryptedRoutePoolStatsGeili() sql.DBStats {
	return sql.DBStats{MaxOpenConnections: 2}
}

func TestReleaseControlGeiliReportsAuxiliaryConnectionBudget(t *testing.T) {
	t.Setenv("GEILI_RELEASE_CONTROL", "false")
	c, err := ProvideReleaseControl(&config.Config{Database: config.DatabaseConfig{MaxOpenConns: 24, MaxIdleConns: 8}}, nil, nil, nil, &metadataPoolBudgetCacheGeili{})
	require.NoError(t, err)
	c.controller = releaselifecycle.New(false, false)
	w := httptest.NewRecorder()
	c.writeStatus(w, 200)
	var status map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	require.EqualValues(t, 24, status["database_max_open_conns"])
	require.EqualValues(t, 2, status["database_auxiliary_max_open_conns"])
	require.EqualValues(t, 26, status["database_total_max_open_conns"])
}
