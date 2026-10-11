package dto

import "github.com/Wei-Shaw/sub2api/internal/service"

func proxyPasswordForResponseGeili(p *service.Proxy) string {
	if service.IsExternalProxyGeili(p) {
		return ""
	}
	return p.Password
}
