package service

import "context"

type httpAccountRecoveryScopeKeyGeili struct{}

// WithHTTPAccountRecoveryScopeGeili records the server-owned gateway endpoint
// boundary. It governs account probes only; ordinary retries/admission/usage
// keep their existing request contexts. Client fields cannot supply this key.
func WithHTTPAccountRecoveryScopeGeili(ctx context.Context, allow bool) context.Context {
	return context.WithValue(ctx, httpAccountRecoveryScopeKeyGeili{}, allow)
}

// Direct service callers without an HTTP marker retain their existing contract.
func AccountRecoveryScopeAllowedGeili(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	allow, present := ctx.Value(httpAccountRecoveryScopeKeyGeili{}).(bool)
	return !present || allow
}
