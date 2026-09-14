package subscription

import "context"

// EntitlementRepository is the minimal seam required by auth and billing.
// Implementations should use the request transaction or a short-lived cache.
type EntitlementRepository interface {
	ListByUserAndGroup(ctx context.Context, userID, groupID int64) ([]Entitlement, error)
}

// Resolver centralizes multi-group authorization and prevents each upstream
// call site from implementing subtly different fallback behavior.
type Resolver struct {
	Repo EntitlementRepository
}

func (r Resolver) HasAccess(ctx context.Context, userID, groupID int64) (bool, error) {
	id, err := r.ResolveSubscriptionID(ctx, userID, groupID)
	return id > 0, err
}

// ResolveSubscriptionID returns the shared subscription that grants access to
// groupID. Billing must use this ID rather than the request's group ID.
func (r Resolver) ResolveSubscriptionID(ctx context.Context, userID, groupID int64) (int64, error) {
	if r.Repo == nil {
		return 0, nil
	}
	items, err := r.Repo.ListByUserAndGroup(ctx, userID, groupID)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}
	return items[0].SubscriptionID, nil
}
