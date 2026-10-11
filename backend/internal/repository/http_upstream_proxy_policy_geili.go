package repository

import "github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"

// Pool keys contain authentication material. Never emit external credentials.
func redactExternalProxyLogGeili(value string) string {
	return proxyutil.RedactExternalProxyLogGeili(value)
}
