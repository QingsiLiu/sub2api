package service

import (
	"context"
	"log"
	"time"
)

const subscriptionUnsentCleanupBudgetGeili = 2 * time.Minute

// CancelUnsentConsumptionLifecycle is used only after the lifecycle owner has
// proved that no upstream dispatch or accepted asynchronous handoff occurred.
// A disconnect must not cancel this cleanup. It remains bounded, synchronous
// and visible to release draining; no age-based recovery is inferred.
func (s *SubscriptionService) CancelUnsentConsumptionLifecycle(ctx context.Context, sub *UserSubscription) error {
	return cancelUnsentSubscriptionLifecycleGeili(ctx, sub, s.CancelUnsentConsumption)
}

func cancelUnsentSubscriptionLifecycleGeili(ctx context.Context, sub *UserSubscription, cleanupFn SubscriptionCancelFactory) error {
	if sub == nil || sub.AdmissionKey == "" || cleanupFn == nil {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), subscriptionUnsentCleanupBudgetGeili)
	defer cancel()
	var err error
	attempts := 0
	for attempts < 3 {
		attempts++
		err = cleanupFn(cleanup, sub)
		if err == nil {
			return nil
		}
		if cleanup.Err() != nil || attempts == 3 {
			break
		}
		timer := time.NewTimer(time.Duration(attempts) * 50 * time.Millisecond)
		select {
		case <-timer.C:
		case <-cleanup.Done():
			timer.Stop()
			err = cleanup.Err()
		}
		if cleanup.Err() != nil {
			break
		}
	}
	log.Printf("[SubscriptionCleanup] proven-unsent cleanup failed subscription_id=%d admission_key=%q attempts=%d error=%v", sub.ID, sub.AdmissionKey, attempts, err)
	return err
}
