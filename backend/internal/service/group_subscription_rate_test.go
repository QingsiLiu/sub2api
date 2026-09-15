package service

import "testing"

func TestGroupBillingRateMultiplier(t *testing.T) {
	group := &Group{RateMultiplier: 0.8, SubscriptionRateMultiplier: subscriptionRateTestPtr(1.4)}
	if got := group.BillingRateMultiplier(false); got != 0.8 {
		t.Fatalf("balance multiplier = %v, want 0.8", got)
	}
	if got := group.BillingRateMultiplier(true); got != 1.4 {
		t.Fatalf("subscription multiplier = %v, want 1.4", got)
	}
}

func TestGroupBillingRateMultiplierLegacyFixtureFallback(t *testing.T) {
	group := &Group{RateMultiplier: 0.8}
	if got := group.BillingRateMultiplier(true); got != 0.8 {
		t.Fatalf("legacy subscription multiplier = %v, want balance fallback 0.8", got)
	}
}

func subscriptionRateTestPtr(v float64) *float64 { return &v }

func TestSubscriptionZeroMultiplierAndRequestCopy(t *testing.T) {
	zero := 0.0
	original := &APIKey{Group: &Group{RateMultiplier: 0.8, SubscriptionRateMultiplier: &zero}}
	normalized := SubscriptionBillingKey(original)
	if normalized.Group.RateMultiplier != 0 || original.Group.RateMultiplier != 0.8 {
		t.Fatal("zero subscription rate must not rewrite the cached balance rate")
	}
}
