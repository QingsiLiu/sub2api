package admin

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyExportSkipsExternalGeili(t *testing.T) {
	router, admin := setupProxyDataRouter()
	admin.proxies = []service.Proxy{{ID: 1, Name: "[external] owner", Protocol: "http", Host: "8.8.8.8", Port: 8080, Password: "synthetic-proxy-secret"}, {ID: 2, Name: "ordinary", Protocol: "http", Host: "127.0.0.1", Port: 8081, Password: "ordinary-password"}}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/proxies/data", nil))
	require.Equal(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), "synthetic-proxy-secret")
	var data proxyDataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &data))
	require.Equal(t, 1, data.Data.SkippedExternalProxies)
	require.Len(t, data.Data.Proxies, 1)
	require.Equal(t, "ordinary-password", data.Data.Proxies[0].Password)
}
