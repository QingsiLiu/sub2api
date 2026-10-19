package service

import "context"

// A saturated account leaves the candidate set for the current request even
// though it is healthy and will have a free slot soon. When such an exclusion
// is the only reason selection found nothing, the caller re-admits it and waits
// again. That wait is cheaper and more accurate than the account-recovery probe,
// which only restores accounts that are not schedulable and therefore skips a
// busy one. This flag tells the recovery gate that the caller still holds a
// saturated candidate, so probing cannot help and must not delay the request.
type busyRequeuePendingKeyGeili struct{}

func WithBusyRequeuePendingGeili(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, busyRequeuePendingKeyGeili{}, true)
}

func ClearBusyRequeuePendingGeili(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, busyRequeuePendingKeyGeili{}, false)
}

func BusyRequeuePendingGeili(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	pending, _ := ctx.Value(busyRequeuePendingKeyGeili{}).(bool)
	return pending
}
