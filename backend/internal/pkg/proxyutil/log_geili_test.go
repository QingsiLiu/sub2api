package proxyutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExternalProxyLogGeiliMasksURLsAndTransportKeys(t *testing.T) {
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		value := scheme + "://holder:fake-password@proxy.example:8080#" + PublicProxyMarker
		require.Equal(t, "external-proxy", RedactExternalProxyLogGeili(value))
		require.Equal(t, "external-proxy", RedactExternalProxyLogGeili("account:42|"+value+"|tls:default"))
	}
	for _, value := range []string{"", "direct", "http://proxy.example:8080", "ordinary-transport-key"} {
		require.Equal(t, value, RedactExternalProxyLogGeili(value))
	}
}
