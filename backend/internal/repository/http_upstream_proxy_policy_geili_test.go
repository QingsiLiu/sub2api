package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestExternalProxyNormalizationGeili(t *testing.T) {
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			raw := scheme + "://user:synthetic-pass@8.8.8.8:8080#" + proxyutil.PublicProxyMarker
			key, parsed, err := normalizeProxyURL(raw)
			require.NoError(t, err)
			require.Equal(t, proxyutil.PublicProxyMarker, parsed.Fragment)
			require.Contains(t, key, "#"+proxyutil.PublicProxyMarker)
			require.Equal(t, "external-proxy", redactExternalProxyLogGeili(key))
			require.Equal(t, "external-proxy", redactExternalProxyLogGeili(buildCacheKey("account_proxy", key, 1, upstreamProtocolModeDefault)))
		})
	}
	key, parsed, err := normalizeProxyURL("http://8.8.8.8:8080#ordinary")
	require.NoError(t, err)
	require.Empty(t, parsed.Fragment)
	require.Equal(t, key, redactExternalProxyLogGeili(key))
}

func TestExternalProxyNormalizedTransportRejectsPrivateGeili(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	_, parsed, err := normalizeProxyURL(server.URL + "#" + proxyutil.PublicProxyMarker)
	require.NoError(t, err)
	transport, err := buildUpstreamTransport(defaultPoolSettings(nil), parsed, upstreamProtocolModeDefault)
	require.NoError(t, err)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	_, err = client.Get("http://example.com/test")
	require.Error(t, err)
	require.Zero(t, hits.Load(), "normalized external proxy must not dial loopback")
}
