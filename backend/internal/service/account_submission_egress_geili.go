package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"net/url"
)

type submissionPublicEgressContextKeyGeili struct{}

func WithSubmissionPublicEgressGeili(ctx context.Context) context.Context {
	return context.WithValue(ctx, submissionPublicEgressContextKeyGeili{}, true)
}
func IsSubmissionPublicEgressGeili(ctx context.Context) bool {
	v, _ := ctx.Value(submissionPublicEgressContextKeyGeili{}).(bool)
	return v
}

func submissionAccountProxyPolicyGeili(a *Account, raw string) string {
	if a == nil || a.Extra["external_submission_form_geili"] != true {
		return raw
	}
	if raw == "" {
		return proxyutil.PublicDirectURLGeili
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = proxyutil.PublicProxyMarker
	return u.String()
}
