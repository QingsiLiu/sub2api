package service

import "context"

type AccountManualSchedulingRepositoryGeili interface {
	SetManualAccountSchedulingGeili(context.Context, int64, bool) error
}

// Errors can turn scheduling off too. An explicit manual reason survives later
// errors, while status=error alone remains eligible for automatic recovery.
func AccountManuallyStoppedGeili(a *Account) bool {
	if a == nil {
		return false
	}
	return a.Status == "inactive" || a.Status == StatusDisabled ||
		a.Status == StatusActive && !a.Schedulable ||
		a.RuntimeState != nil && a.RuntimeState.StopReason == "manual"
}
