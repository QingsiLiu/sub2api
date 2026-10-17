// Package-level striped locks for the non-persisted Account caches.
//
// geili hook: GetModelMapping and GetHeaderOverrides lazily fill cache fields on
// Account, but one *Account is shared by concurrent requests for the same account,
// so the unlocked read-modify-write in the official code races. Account is copied by
// value in a few call sites, so a mutex field would make every copy a lock copy;
// instead we serialize per account pointer with a fixed stripe array.
package service

import (
	"sync"
	"unsafe"
)

const accountCacheStripeCount = 64

var accountCacheStripes [accountCacheStripeCount]sync.Mutex

// lockAccountCache serializes cache access for one Account until the returned
// function is called. Callers must not call another locked getter while holding it.
func lockAccountCache(a *Account) func() {
	if a == nil {
		return func() {}
	}
	stripe := &accountCacheStripes[(uintptr(unsafe.Pointer(a))>>4)%accountCacheStripeCount]
	stripe.Lock()
	return stripe.Unlock
}
