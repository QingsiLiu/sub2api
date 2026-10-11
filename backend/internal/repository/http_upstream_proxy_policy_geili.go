package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"strings"
)

// Pool keys contain authentication material. Never emit external credentials.
func redactExternalProxyLogGeili(value string) string {
	if strings.Contains(value, "#"+proxyutil.PublicProxyMarker) {
		return "external-proxy"
	}
	return value
}
