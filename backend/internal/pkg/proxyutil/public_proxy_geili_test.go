package proxyutil

import (
	"context"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/netip"
	"net/url"
	"testing"
)

func TestPublicProxyGeiliRejectsReservedAndRebinding(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "0.1.2.3", "192.0.2.1", "198.18.0.1", "240.0.0.1", "fc00::1", "64:ff9b::a00:1", "2001:db8::1", "2002:a00:1::1", "::ffff:127.0.0.1"} {
		require.False(t, IsPublicProxyIP(netip.MustParseAddr(raw)), raw)
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		require.True(t, IsPublicProxyIP(netip.MustParseAddr(raw)), raw)
	}
	old := publicProxyLookup
	t.Cleanup(func() { publicProxyLookup = old })
	publicProxyLookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	require.NoError(t, ValidatePublicProxyHost(context.Background(), "proxy.example"))
	publicProxyLookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	_, err := (publicProxyDialer{}).DialContext(context.Background(), "tcp", "proxy.example:8080")
	require.Error(t, err)
}
func TestPublicProxyGeiliProtocolsAndInternalMarker(t *testing.T) {
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		u, err := url.Parse(scheme + "://proxy.example:8080#" + PublicProxyMarker)
		require.NoError(t, err)
		transport := &http.Transport{Proxy: http.ProxyURL(u)}
		require.NoError(t, ConfigureTransportProxy(transport, u))
		require.NotNil(t, transport.DialContext)
		if scheme == "http" || scheme == "https" {
			p, err := transport.Proxy(nil)
			require.NoError(t, err)
			require.Empty(t, p.Fragment)
			require.Equal(t, "proxy.example", p.Hostname())
		} else {
			require.Nil(t, transport.Proxy, "a WebSocket transport must not apply the SOCKS proxy twice")
		}
	}
}
