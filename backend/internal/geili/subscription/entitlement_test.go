package subscription

import "testing"

func TestContains(t *testing.T) {
	items := []Entitlement{{SubscriptionID: 10, GroupID: 4}, {SubscriptionID: 10, GroupID: 88}}
	if !Contains(items, 10, 88) {
		t.Fatal("expected bundled group entitlement")
	}
	if Contains(items, 11, 88) || Contains(items, 10, 47) {
		t.Fatal("unexpected entitlement")
	}
}
