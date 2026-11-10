//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type narrowBalanceRepoGeili struct {
	UserRepository // A full-user read would panic instead of masking a fallback.
	balance        float64
	err            error
	calls          int
	ctx            context.Context
	id             int64
}

func (r *narrowBalanceRepoGeili) GetBillingBalanceGeili(ctx context.Context, id int64) (float64, error) {
	r.calls++
	r.ctx, r.id = ctx, id
	return r.balance, r.err
}

func TestBillingBalanceGeiliDoesNotCacheOrFallbackOnError(t *testing.T) {
	r := &narrowBalanceRepoGeili{balance: 1}
	svc := &BillingCacheService{userRepo: r, durableSettlement: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, balance := range []float64{1, 0, -0.25} {
		r.balance = balance
		got, err := svc.GetUserBalance(ctx, 42)
		require.NoError(t, err)
		require.Equal(t, balance, got)
	}
	r.err = errors.New("database unavailable")
	_, err := svc.GetUserBalance(ctx, 42)
	require.ErrorIs(t, err, r.err)
	require.Equal(t, 4, r.calls)
	require.Equal(t, int64(42), r.id)
	require.Equal(t, ctx, r.ctx)
}

func TestBillingBalanceGeiliKeepsLegacyRepositoryFallback(t *testing.T) {
	r := &balanceLoadUserRepoStub{balance: -0.25}
	got, err := loadBillingBalanceGeili(context.Background(), r, 42)
	require.NoError(t, err)
	require.Equal(t, -0.25, got)
	require.Equal(t, int64(1), r.calls.Load())
}
