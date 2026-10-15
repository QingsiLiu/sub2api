package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type manualBalanceRepositoryGeili struct {
	AccountRepository
	blocked  bool
	writes   int
	writeErr error
	stateErr error
}

func (r *manualBalanceRepositoryGeili) SetManualAccountBalanceStopGeili(_ context.Context, id int64, blocked bool) error {
	r.writes++
	if r.writeErr != nil {
		return r.writeErr
	}
	r.blocked = blocked
	return nil
}
func (r *manualBalanceRepositoryGeili) GetByID(context.Context, int64) (*Account, error) {
	return &Account{ID: 17, Status: "inactive", Schedulable: false}, nil
}
func (r *manualBalanceRepositoryGeili) GetAccountRuntimeGeili(context.Context, int64) (*AccountRuntimeStateGeili, error) {
	return &AccountRuntimeStateGeili{AccountID: 17, BalanceBlocked: r.blocked}, r.stateErr
}

func TestManualBalanceGeiliReturnsFreshStoppedState(t *testing.T) {
	repo := &manualBalanceRepositoryGeili{}
	limits := &RateLimitService{accountRepo: repo}
	for _, blocked := range []bool{true, false} {
		account, err := limits.SetManualBalanceStopGeili(context.Background(), 17, blocked)
		require.NoError(t, err)
		require.Equal(t, "inactive", account.Status)
		require.False(t, account.Schedulable)
		require.Equal(t, blocked, account.RuntimeState.BalanceBlocked)
	}
	require.Equal(t, 2, repo.writes)
}

func TestManualBalanceGeiliPropagatesWriteAndPresentationErrors(t *testing.T) {
	want := errors.New("storage unavailable")
	for _, tc := range []struct {
		name string
		repo *manualBalanceRepositoryGeili
	}{
		{"write", &manualBalanceRepositoryGeili{writeErr: want}},
		{"read", &manualBalanceRepositoryGeili{stateErr: want}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account, err := (&RateLimitService{accountRepo: tc.repo}).SetManualBalanceStopGeili(context.Background(), 17, true)
			require.Nil(t, account)
			require.ErrorIs(t, err, want)
		})
	}
	for _, limits := range []*RateLimitService{nil, {}, {accountRepo: struct{ AccountRepository }{}}} {
		_, err := limits.SetManualBalanceStopGeili(context.Background(), 17, true)
		require.ErrorIs(t, err, ErrAccountManualBalanceUnavailableGeili)
	}
}
