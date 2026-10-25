package service

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

type encryptedMetadataRetryKeyGeili struct{}
type encryptedMetadataRetryGeili struct {
	used     atomic.Bool
	deadline time.Time
}

// WithOpenAIEncryptedMetadataRetryGeili grants the existing single metadata
// retry only after a repository operation actually acquired SQL admission.
// Neither client input nor an admission rejection can create this capability.
func WithOpenAIEncryptedMetadataRetryGeili(ctx context.Context, failure error) (context.Context, bool) {
	deadline, bounded := ctx.Deadline()
	if !bounded || ctx.Err() != nil || time.Until(deadline) > 250*time.Millisecond || ctx.Value(encryptedMetadataRetryKeyGeili{}) != nil {
		return ctx, false
	}
	var admitted interface{ ClaimOpenAIEncryptedMetadataRetryGeili() bool }
	if !errors.As(failure, &admitted) || !admitted.ClaimOpenAIEncryptedMetadataRetryGeili() {
		return ctx, false
	}
	return context.WithValue(ctx, encryptedMetadataRetryKeyGeili{}, &encryptedMetadataRetryGeili{deadline: deadline}), true
}

// ConsumeOpenAIEncryptedMetadataRetryGeili is repository-only admission state:
// a capability is request-local, deadline-bound, and usable exactly once.
func ConsumeOpenAIEncryptedMetadataRetryGeili(ctx context.Context) bool {
	retry, ok := ctx.Value(encryptedMetadataRetryKeyGeili{}).(*encryptedMetadataRetryGeili)
	return ok && ctx.Err() == nil && time.Now().Before(retry.deadline) && retry.used.CompareAndSwap(false, true)
}
