package service

import (
	"context"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrAccountManualBalanceUnavailableGeili = infraerrors.New(http.StatusServiceUnavailable, "ACCOUNT_MANUAL_BALANCE_UNAVAILABLE", "Manual upstream balance controls unavailable")
	ErrAccountManualBalanceShadowGeili      = infraerrors.New(http.StatusBadRequest, "ACCOUNT_MANUAL_BALANCE_USE_PARENT", "Manage the unpaid label on this shadow account's parent")
)

// Manual balance controls are optional; ordinary account repositories and runtime
// recovery mocks need not implement an administrative write capability.
type AccountManualBalanceRepositoryGeili interface {
	SetManualAccountBalanceStopGeili(context.Context, int64, bool) error
}

func (s *RateLimitService) SetManualBalanceStopGeili(ctx context.Context, id int64, blocked bool) (*Account, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrAccountManualBalanceUnavailableGeili
	}
	writer, ok := s.accountRepo.(AccountManualBalanceRepositoryGeili)
	if !ok {
		return nil, ErrAccountManualBalanceUnavailableGeili
	}
	if err := writer.SetManualAccountBalanceStopGeili(ctx, id, blocked); err != nil {
		return nil, err
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if reader, ok := s.accountRepo.(interface {
		GetAccountRuntimeGeili(context.Context, int64) (*AccountRuntimeStateGeili, error)
	}); ok {
		account.RuntimeState, err = reader.GetAccountRuntimeGeili(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	return account, nil
}
