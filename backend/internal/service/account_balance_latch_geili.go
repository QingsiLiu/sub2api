package service

import "context"

// Optional capability of the shared Redis cache. A missing token means no debt
// latch; a new observation replaces the token so an older grant cannot clear it.
// It is independent of ordinary temporary-state reset keys and has no expiry.
type AccountBalanceLatchCacheGeili interface {
	SetAccountBalanceLatchGeili(context.Context, int64) (string, error)
	GetAccountBalanceLatchGeili(context.Context, int64) (string, error)
	ClearAccountBalanceLatchGeili(context.Context, int64, string) (bool, error)
}
