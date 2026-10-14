//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAccountRecoveryGeiliRestoredAccountMayRetryOnlyAfterProbe(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) { return true, nil }, nil)
	defer recovery.Stop()
	ctx := WithRequestRecovery(context.Background(), time.Minute)
	excluded := map[int64]struct{}{1: {}, 2: {}}
	require.Equal(t, excluded, recoveredAccountExclusionsGeili(ctx, excluded))
	require.True(t, recovery.RecoverGroupGeili(ctx, 7, "claude-sonnet", nil))
	require.Equal(t, map[int64]struct{}{2: {}}, recoveredAccountExclusionsGeili(ctx, excluded))
	require.Len(t, excluded, 2)
	require.False(t, recovery.RecoverGroupGeili(ctx, 7, "claude-sonnet", nil), "same request must not repeat the group recovery")
}

func TestAccountRecoveryGeiliFreshGroupMembershipRequired(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	var probed bool
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) { probed = true; return true, nil }, nil)
	defer recovery.Stop()
	account, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	repo.mu.Lock()
	changed := repo.accounts[1]
	changed.GroupIDs = []int64{8}
	repo.accounts[1] = changed
	repo.mu.Unlock()
	err = recovery.check(context.Background(), account, "claude-sonnet", "automatic", false, func(_ context.Context, fresh *Account) bool {
		for _, id := range fresh.GroupIDs {
			if id == 7 {
				return true
			}
		}
		return false
	})
	require.ErrorIs(t, err, ErrAccountRuntimeConflictGeili)
	require.False(t, probed)
}
