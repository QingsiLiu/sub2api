package service

import "github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"

func redactExternalProxyLogGeili(value string) string {
	return proxyutil.RedactExternalProxyLogGeili(value)
}
