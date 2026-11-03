package middleware

import "net/http"

// Only registered usage reads bypass billing enforcement; other model-free
// requests must retain their existing admission and routing checks.
func isGatewayUsageReadGeili(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/v1/usage", "/antigravity/v1/usage":
		return true
	default:
		return false
	}
}
