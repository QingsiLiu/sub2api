package subscription

import (
	"context"
	"testing"
)

type entitlementRepoStub struct{}

func (entitlementRepoStub) ListByUserAndGroup(_ context.Context, _, groupID int64) ([]Entitlement, error) {
	return []Entitlement{{SubscriptionID: 42, GroupID: groupID}}, nil
}

func TestContains(t *testing.T) {
	items := []Entitlement{{SubscriptionID: 10, GroupID: 4}, {SubscriptionID: 10, GroupID: 88}}
	if !Contains(items, 10, 88) {
		t.Fatal("expected bundled group entitlement")
	}
	if Contains(items, 11, 88) || Contains(items, 10, 47) {
		t.Fatal("unexpected entitlement")
	}
}

func TestResolverReturnsSharedSubscriptionIDForEachGroup(t *testing.T) {
	r := Resolver{Repo: entitlementRepoStub{}}
	for _, groupID := range []int64{4, 88} {
		id, err := r.ResolveSubscriptionID(context.Background(), 7, groupID)
		if err != nil || id != 42 {
			t.Fatalf("group %d resolved subscription = %d, err=%v; want 42", groupID, id, err)
		}
	}
}
