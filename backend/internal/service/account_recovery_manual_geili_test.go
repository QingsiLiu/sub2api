//go:build unit

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountRecoveryGeiliAutomaticCannotOverrideManualDebt(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	require.NoError(t, repo.MarkAccountBalanceBlockedGeili(context.Background(), 1, "manual"))
	var probes atomic.Int64
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) {
		probes.Add(1)
		return true, nil
	}, func(context.Context, *Account) (bool, bool, error) {
		t.Fatal("recovery must not query supplier balances")
		return true, true, nil
	})
	defer recovery.Stop()
	a, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.ErrorIs(t, recovery.check(context.Background(), a, "m", "automatic", true), ErrAccountBalanceBlockedGeili)
	require.Zero(t, probes.Load())
	require.Empty(t, repo.activations)
}

func TestAccountRecoveryGeiliFailedConfirmedProbeKeepsManualDebt(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "incomplete", true: "supplier error"}[complete], func(t *testing.T) {
			repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
			require.NoError(t, repo.MarkAccountBalanceBlockedGeili(context.Background(), 1, "manual"))
			recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) {
				if complete {
					return true, errors.New("supplier unavailable")
				}
				return false, nil
			}, nil)
			defer recovery.Stop()
			_, err := recovery.RecheckAccountGeili(context.Background(), 1, "m", true)
			require.ErrorIs(t, err, ErrAccountProbeUnhealthyGeili)
			a, err := repo.GetByID(context.Background(), 1)
			require.NoError(t, err)
			require.True(t, a.RuntimeState.BalanceBlocked)
			require.False(t, a.Schedulable)
			require.Empty(t, repo.activations)
		})
	}
}

func TestAccountRecoveryGeiliConcurrentManualMarkInvalidatesProbe(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	recovery := NewAccountRecoveryServiceGeili(repo, func(ctx context.Context, _ *Account, _ string) (bool, error) {
		return true, repo.MarkAccountBalanceBlockedGeili(ctx, 1, "manual during probe")
	}, nil)
	defer recovery.Stop()
	_, err := recovery.RecheckAccountGeili(context.Background(), 1, "m", true)
	require.ErrorIs(t, err, ErrAccountRuntimeConflictGeili)
	a, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, a.RuntimeState.BalanceBlocked)
	require.Equal(t, "manual during probe", a.RuntimeState.BalanceReason)
	require.Empty(t, repo.activations)
}

func TestAccountRecoveryGeiliManualDebtOwnerExcludesChildProbe(t *testing.T) {
	parent, child := stoppedRecoveryAccountGeili(1, 7), stoppedRecoveryAccountGeili(2, 7)
	child.ParentAccountID = &parent.ID
	repo := newRecoveryRuntimeRepoGeili(parent, child)
	require.NoError(t, repo.MarkAccountBalanceBlockedGeili(context.Background(), 1, "manual"))
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) {
		t.Fatal("child cannot override its credential owner's manual debt mark")
		return true, nil
	}, nil)
	defer recovery.Stop()
	_, err := recovery.RecheckAccountGeili(context.Background(), 2, "m", true)
	require.ErrorIs(t, err, ErrAccountBalanceBlockedGeili)
	require.Empty(t, repo.records)
}

func TestAccountRecoveryGeiliCredentialOwnerAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		usable bool
	}{
		{"inactive", false}, {"credential cooldown", false}, {"expired", false},
		{"global rate limit", true}, {"global overload", true}, {"manual dimension stop", true},
		{"expiration without auto pause", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, child := stoppedRecoveryAccountGeili(1, 7), stoppedRecoveryAccountGeili(2, 7)
			parent.Status = StatusActive
			future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
			switch tc.name {
			case "inactive":
				parent.Status = "inactive"
			case "credential cooldown":
				parent.TempUnschedulableUntil = &future
			case "expired":
				parent.AutoPauseOnExpired, parent.ExpiresAt = true, &past
			case "global rate limit":
				parent.RateLimitResetAt = &future
			case "global overload":
				parent.OverloadUntil = &future
			case "expiration without auto pause":
				parent.ExpiresAt = &past
			}
			child.ParentAccountID = &parent.ID
			repo := newRecoveryRuntimeRepoGeili(parent, child)
			var probes atomic.Int64
			recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) {
				probes.Add(1)
				return true, nil
			}, nil)
			defer recovery.Stop()
			_, err := recovery.RecheckAccountGeili(context.Background(), 2, "m", true)
			if tc.usable {
				require.NoError(t, err)
				require.Equal(t, int64(1), probes.Load(), "dimension limits do not disable shared credentials")
			} else {
				require.ErrorIs(t, err, ErrAccountProbeUnhealthyGeili)
				require.Zero(t, probes.Load())
				require.Empty(t, repo.records)
			}
		})
	}
}

func TestAccountRecoveryGeiliProbeErrorDoesNotCreateDebt(t *testing.T) {
	repo := newRecoveryRuntimeRepoGeili(stoppedRecoveryAccountGeili(1, 7))
	recovery := NewAccountRecoveryServiceGeili(repo, func(context.Context, *Account, string) (bool, error) {
		return false, ErrAccountBalanceBlockedGeili
	}, nil)
	defer recovery.Stop()
	_, err := recovery.RecheckAccountGeili(context.Background(), 1, "m", false)
	require.ErrorIs(t, err, ErrAccountProbeUnhealthyGeili)
	a, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.False(t, a.RuntimeState.BalanceBlocked)
	require.Equal(t, "probe_failed", repo.records[0].Reason)
}

type recoveryWiringRepoGeili struct {
	*recoveryRuntimeRepoGeili
	balanceReads atomic.Int64
}

func (r *recoveryWiringRepoGeili) InitializeAccountBalanceBlocksGeili(context.Context, int) (int, error) {
	r.balanceReads.Add(1)
	return 0, nil
}
func (r *recoveryWiringRepoGeili) ListAccountBalanceBlockedGeili(context.Context, int64, int) ([]int64, bool, error) {
	r.balanceReads.Add(1)
	return nil, false, nil
}

func TestAccountRecoveryGeiliWiringDoesNotInferOrPollDebt(t *testing.T) {
	repo := &recoveryWiringRepoGeili{recoveryRuntimeRepoGeili: newRecoveryRuntimeRepoGeili()}
	limits := &RateLimitService{accountRepo: repo}
	AttachAccountRecoveryGeili(&AccountTestService{}, limits, &CNProviderBalanceService{})
	require.NotNil(t, limits.AccountRecoveryGeili())
	limits.AccountRecoveryGeili().Stop()
	require.Zero(t, repo.balanceReads.Load())
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

func TestAccountRecoveryGeiliPresentationUsesActualState(t *testing.T) {
	future := time.Now().Add(time.Hour)
	for _, tc := range []struct {
		name, status, wantState, wantReason string
		schedulable, manualDebt, cooling    bool
	}{
		{"active ignores retired stop metadata", StatusActive, "enabled", "", true, false, false},
		{"inactive stays stopped", "inactive", "stopped", "", false, false, false},
		{"actual cooling window", StatusActive, "temporarily_stopped", "temporary_failure", true, false, true},
		{"manual debt mark", StatusActive, "balance_stopped", "balance", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := stoppedRecoveryAccountGeili(1, 7)
			a.Status, a.Schedulable = tc.status, tc.schedulable
			if tc.cooling {
				a.TempUnschedulableUntil = &future
			}
			a.RuntimeState = &AccountRuntimeStateGeili{BalanceBlocked: tc.manualDebt, StopReason: "temporary_failure", NextCheckAt: &future,
				Models: map[string]AccountModelHealthGeili{"m": {Blocked: true, FailureCount: 5, NextCheckAt: &future}}}
			view := PresentAccountRuntimeGeili(&a)
			require.Equal(t, tc.wantState, view["state"])
			require.Equal(t, tc.wantReason, view["stop_reason"])
			require.Nil(t, view["next_check_at"], "retired failure timers are not probe schedules")
			require.Empty(t, view["models"], "retired blocked flags are not current model prohibitions")
		})
	}
}

func TestAccountRecoveryGeiliPresentationKeepsRecoveryRecords(t *testing.T) {
	future := time.Now().Add(time.Minute)
	probe := &AccountProbeResultGeili{Model: "m", Reason: "probe_failed", NextProbeAt: &future}
	a := stoppedRecoveryAccountGeili(1, 7)
	a.RuntimeState = &AccountRuntimeStateGeili{LastProbe: probe,
		Models: map[string]AccountModelHealthGeili{"m": {Blocked: true, LastProbe: probe}}}
	view := PresentAccountRuntimeGeili(&a)
	require.Equal(t, "stopped", view["state"])
	require.Equal(t, &future, view["next_check_at"])
	require.Equal(t, "failed", view["last_probe"].(map[string]any)["status"])
	model := view["models"].(map[string]any)["m"].(map[string]any)
	require.Equal(t, false, model["blocked"])
	require.Equal(t, &future, model["next_check_at"])
	require.Equal(t, "failed", model["last_probe"].(map[string]any)["status"])
}
