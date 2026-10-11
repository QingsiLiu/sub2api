package dto

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExternalProxyGeiliPasswordIsWriteOnly(t *testing.T) {
	p := &service.Proxy{Name: "[external] owner", Password: "synthetic-proxy-secret"}
	b, err := json.Marshal(ProxyFromServiceAdmin(p))
	require.NoError(t, err)
	require.NotContains(t, string(b), p.Password)
	p.Name = "ordinary"
	require.Equal(t, p.Password, ProxyFromServiceAdmin(p).Password)
}
