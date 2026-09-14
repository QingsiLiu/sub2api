// Package subscription contains Geili-only multi-group subscription rules.
// Keep upstream service code limited to small adapters around this package.
package subscription

// Entitlement identifies a group covered by one shared subscription.
type Entitlement struct {
	SubscriptionID int64
	GroupID        int64
}

// GroupPair is the initial product bundle. IDs are configuration inputs so
// deployments can override them without changing authorization code.
type GroupPair struct {
	GPTGroupID  int64
	GrokGroupID int64
}

// Contains reports whether a subscription entitlement covers groupID.
func Contains(entitlements []Entitlement, subscriptionID, groupID int64) bool {
	for _, e := range entitlements {
		if e.SubscriptionID == subscriptionID && e.GroupID == groupID {
			return true
		}
	}
	return false
}
