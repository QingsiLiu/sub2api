package service

import "context"

type accountRuntimePresentationKeyGeili struct{}

// Runtime metadata is for administrator presentation and fault recovery. The
// ordinary account/scheduler projection must not add a runtime database read.
func WithAccountRuntimePresentationGeili(ctx context.Context) context.Context {
	return context.WithValue(ctx, accountRuntimePresentationKeyGeili{}, true)
}

func AccountRuntimePresentationAllowedGeili(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(accountRuntimePresentationKeyGeili{}).(bool)
	return allowed
}
