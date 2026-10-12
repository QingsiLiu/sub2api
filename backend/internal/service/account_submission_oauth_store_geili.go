package service

import (
	"context"
	"time"
)

// SubmissionOAuthSessionStoreGeili keeps invitation PKCE state server-side and
// shares the capability across instances without a storage dependency.
type SubmissionOAuthSessionStoreGeili interface {
	Save(context.Context, string, string, []byte, time.Duration) error
	Take(context.Context, string, string) ([]byte, error)
}
