package service

import (
	"context"
	"net/http"
	"strings"
)

// The production HTTP provider implements this using the same fresh PostgreSQL
// verdict and Redis latch used immediately before native HTTP dispatch.
type nativePluginBalanceVerifierGeili interface {
	CheckNativeBalanceBeforeDispatchGeili(*http.Request, int64) error
}

type nativePluginBalanceVerifierContextKeyGeili struct{}
type nativePluginBalanceVerifierContextGeili struct {
	verifier nativePluginBalanceVerifierGeili
}

// These are the native paid endpoints actually forwarded by OpenAI plugins.
// Check the original HTTP operation, before the plugin builds its private RPC.
// Text requests retain their existing admission flow with no additional query.
func isNativeOpenAIPluginRequestGeili(request *http.Request) bool {
	if request == nil || request.URL == nil || request.Method != http.MethodPost {
		return false
	}
	path := strings.TrimRight(request.URL.Path, "/")
	for _, suffix := range []string{"/images/generations", "/images/edits", "/embeddings"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func withNativePluginBalanceVerifierGeili(request *http.Request, upstream HTTPUpstream) *http.Request {
	if !isNativeOpenAIPluginRequestGeili(request) {
		return request
	}
	verifier, _ := upstream.(nativePluginBalanceVerifierGeili)
	ctx := context.WithValue(request.Context(), nativePluginBalanceVerifierContextKeyGeili{}, nativePluginBalanceVerifierContextGeili{verifier: verifier})
	return request.WithContext(ctx)
}

func checkNativePluginBalanceGeili(ctx context.Context, request *http.Request, account *Account) error {
	if !isNativeOpenAIPluginRequestGeili(request) {
		return nil
	}
	state, _ := ctx.Value(nativePluginBalanceVerifierContextKeyGeili{}).(nativePluginBalanceVerifierContextGeili)
	if state.verifier == nil || account == nil {
		return NewAccountRuntimeAdmissionErrorGeili("verification_unavailable")
	}
	return state.verifier.CheckNativeBalanceBeforeDispatchGeili(request, account.ID)
}
