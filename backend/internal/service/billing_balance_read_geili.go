package service

import "context"

// A narrow, uncached read keeps SQL authoritative without fetching profile and
// group membership at each billing eligibility check. Other implementations
// retain the existing repository contract.
type billingBalanceReaderGeili interface {
	GetBillingBalanceGeili(context.Context, int64) (float64, error)
}

func loadBillingBalanceGeili(ctx context.Context, repo UserRepository, userID int64) (float64, error) {
	if reader, ok := repo.(billingBalanceReaderGeili); ok {
		return reader.GetBillingBalanceGeili(ctx, userID)
	}
	user, err := repo.GetByID(ctx, userID)
	if err != nil {
		return 0, err
	}
	return user.Balance, nil
}
