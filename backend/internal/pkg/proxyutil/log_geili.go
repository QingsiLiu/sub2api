package proxyutil

import "strings"

// RedactExternalProxyLogGeili also accepts transport pool keys containing URLs.
// The internal marker survives normalization; external authentication never logs.
func RedactExternalProxyLogGeili(value string) string {
	if strings.Contains(value, "#"+PublicProxyMarker) {
		return "external-proxy"
	}
	return value
}
