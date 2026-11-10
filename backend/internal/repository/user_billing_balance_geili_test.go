package repository

import (
	"context"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBillingBalanceGeiliReadsCurrentBalanceAndExcludesDeleted(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	u, err := client.User.Create().SetEmail("balance@example.invalid").SetPasswordHash("unused").SetBalance(7).Save(ctx)
	require.NoError(t, err)
	for _, balance := range []float64{7, 0, -0.25, 12.5} {
		require.NoError(t, client.User.UpdateOneID(u.ID).SetBalance(balance).Exec(ctx))
		got, err := repo.GetBillingBalanceGeili(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, balance, got)
	}
	require.NoError(t, client.User.UpdateOneID(u.ID).SetDeletedAt(time.Now()).Exec(ctx))
	_, err = repo.GetBillingBalanceGeili(ctx, u.ID)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	_, err = repo.GetBillingBalanceGeili(ctx, u.ID+1000)
	require.ErrorIs(t, err, service.ErrUserNotFound)
}

func TestBillingBalanceGeiliUsesTransactionContext(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	u, err := client.User.Create().SetEmail("balance-tx@example.invalid").SetPasswordHash("unused").SetBalance(7).Save(ctx)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, tx.User.UpdateOneID(u.ID).SetBalance(3).Exec(ctx))
	got, err := repo.GetBillingBalanceGeili(dbent.NewTxContext(ctx, tx), u.ID)
	require.NoError(t, err)
	require.Equal(t, 3.0, got)
	require.NoError(t, tx.Rollback())
	got, err = repo.GetBillingBalanceGeili(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, 7.0, got)
}
