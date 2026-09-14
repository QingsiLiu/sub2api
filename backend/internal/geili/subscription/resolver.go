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
	if r.Repo == nil {
		return false, nil
	}
	items, err := r.Repo.ListByUserAndGroup(ctx, userID, groupID)
	if err != nil {
		return false, err
	}
	return len(items) > 0, nil
}
