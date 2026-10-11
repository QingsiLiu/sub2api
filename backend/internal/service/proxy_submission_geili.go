package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"strings"
)

func IsExternalProxyGeili(p *Proxy) bool {
	return p != nil && strings.HasPrefix(p.Name, proxyutil.ExternalProxyNamePrefix)
}
func protectExternalProxyEditGeili(ctx context.Context, p *Proxy, r *UpdateProxyRequest) error {
	if !IsExternalProxyGeili(p) {
		return nil
	}
	if r.Name != nil && !strings.HasPrefix(*r.Name, proxyutil.ExternalProxyNamePrefix) {
		return ErrSubmissionInvalid
	}
	if r.Host != nil && proxyutil.ValidatePublicProxyHost(ctx, *r.Host) != nil {
		return ErrSubmissionInvalid
	}
	// Masked administrator edit forms cannot accidentally erase a write-only password.
	if r.Password != nil && *r.Password == "" {
		r.Password = nil
	}
	return nil
}
func protectAdminExternalProxyEditGeili(ctx context.Context, p *Proxy, input *UpdateProxyInput) error {
	if !IsExternalProxyGeili(p) {
		return nil
	}
	if input.Name != "" {
		input.Name = proxyutil.ExternalProxyNamePrefix + strings.TrimPrefix(input.Name, proxyutil.ExternalProxyNamePrefix)
		if len(input.Name) > 100 {
			return ErrSubmissionInvalid
		}
	}
	request := UpdateProxyRequest{Password: input.Password}
	if input.Host != "" {
		request.Host = &input.Host
	}
	if err := protectExternalProxyEditGeili(ctx, p, &request); err != nil {
		return err
	}
	input.Password = request.Password
	return nil
}
