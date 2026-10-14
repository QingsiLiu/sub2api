//go:build unit

package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

type recoveryLatchFixtureGeili struct {
	mu      sync.Mutex
	token   string
	readErr error
}

func (l *recoveryLatchFixtureGeili) SetAccountBalanceLatchGeili(context.Context, int64) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.token += "x"
	return l.token, nil
}
func (l *recoveryLatchFixtureGeili) GetAccountBalanceLatchGeili(context.Context, int64) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.token, l.readErr
}
func (l *recoveryLatchFixtureGeili) ClearAccountBalanceLatchGeili(_ context.Context, _ int64, expected string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.token != "" && l.token != expected {
		return false, nil
	}
	l.token = ""
	return true, nil
}

func TestAccountRecoveryGeiliLatchRequiresRechargeAndCompleteProbe(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	latch := &recoveryLatchFixtureGeili{token: "debt"}
	probes := 0
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) { probes++; return true, nil }, nil)
	recovery.balanceLatch = latch
	defer recovery.Stop()
	_, err := recovery.RecheckAccountGeili(context.Background(), 1, "claude-sonnet", false)
	require.ErrorIs(t, err, ErrAccountRechargeConfirmationRequiredGeili)
	require.Zero(t, probes)
	state, err := repo.GetAccountRuntimeGeili(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, state.BalanceBlocked, "Redis-only observed debt must become durable before any paid probe")
	_, err = recovery.RecheckAccountGeili(context.Background(), 1, "claude-sonnet", true)
	require.NoError(t, err)
	require.Equal(t, 1, probes)
	token, err := latch.GetAccountBalanceLatchGeili(context.Background(), 1)
	require.NoError(t, err)
	require.Empty(t, token)
}

func TestAccountRecoveryGeiliNewDebtLatchSurvivesLateSuccessfulGrant(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	latch := &recoveryLatchFixtureGeili{token: "debt"}
	recovery := NewAccountRecoveryServiceGeili(repo, func(ctx context.Context, _ *Account, _ string) (bool, error) {
		_, err := latch.SetAccountBalanceLatchGeili(ctx, 1)
		return true, err
	}, nil)
	recovery.balanceLatch = latch
	defer recovery.Stop()
	_, err := recovery.RecheckAccountGeili(context.Background(), 1, "claude-sonnet", true)
	require.ErrorIs(t, err, ErrAccountRuntimeConflictGeili)
	token, err := latch.GetAccountBalanceLatchGeili(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, "debtx", token)
}

func TestAccountRecoveryGeiliLatchReadFailurePreventsProbe(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) {
		t.Fatal("must not dispatch when debt latch is unknown")
		return false, nil
	}, nil)
	recovery.balanceLatch = &recoveryLatchFixtureGeili{readErr: errors.New("redis unavailable")}
	defer recovery.Stop()
	_, err := recovery.RecheckAccountGeili(context.Background(), 1, "claude-sonnet", true)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, GatewayFailureScopeRequest, failure.Scope)
}

func TestAccountRecoveryGeiliBalanceVerificationCannotConsumeNewDebtVersion(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	require.NoError(t, repo.MarkAccountBalanceBlockedGeili(context.Background(), 1, "balance"))
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) {
		t.Fatal("new debt must invalidate old balance evidence")
		return false, nil
	}, func(ctx context.Context, _ *Account) (bool, bool, error) {
		return true, true, repo.MarkAccountBalanceBlockedGeili(ctx, 1, "new balance failure")
	})
	defer recovery.Stop()
	_, err := recovery.RecheckAccountGeili(context.Background(), 1, "claude-sonnet", false)
	require.ErrorIs(t, err, ErrAccountRuntimeConflictGeili)
}

func TestAccountRecoveryGeiliShared30SecondDeadlineAcrossGroups(t *testing.T) {
	ctx := WithRequestRecovery(context.Background(), time.Minute)
	state := requestRecoveryFromContext(ctx)
	state.accountRecoveryDeadline = time.Now().Add(-time.Second)
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) {
		t.Fatal("expired foreground budget must not create recovery job")
		return false, nil
	}, nil)
	defer recovery.Stop()
	require.False(t, recovery.RecoverGroupGeili(ctx, 7, "claude-sonnet", nil))
	require.Empty(t, recovery.jobs)
}
