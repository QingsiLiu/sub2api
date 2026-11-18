package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type transportSnapshotRepoGeili struct {
	AccountRepository
	accounts       []Account
	group          *int64
	includeGrouped bool
}

func (r *transportSnapshotRepoGeili) ListModelAvailabilityCandidates(_ context.Context, group *int64, _ []string, includeGrouped bool) ([]Account, error) {
	r.group, r.includeGrouped = group, includeGrouped
	var pool []Account
	for _, a := range r.accounts {
		if a.IsActive() && a.Schedulable {
			pool = append(pool, a)
		}
	}
	return pool, nil
}
func (r *transportSnapshotRepoGeili) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Account, error) {
	return r.accounts[1:2], nil
}
func TestSchedulerSnapshotTransportCooldownGeili(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Minute)
	a := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Priority: 1, TempUnschedulableUntil: &until, TempUnschedulableReason: `{"matched_keyword":"openai_transport_health"}`}
	b := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 5}
	ordinary := a
	ordinary.ID = 3
	ordinary.TempUnschedulableReason = `{"matched_keyword":"other_rule"}`
	rateLimited := a
	rateLimited.ID = 4
	rateLimited.RateLimitResetAt = &until
	disabled := a
	disabled.ID = 5
	disabled.Schedulable = false
	expired := a
	expired.ID = 6
	before := now.Add(-time.Minute)
	expired.AutoPauseOnExpired = true
	expired.ExpiresAt = &before
	overload := a
	overload.ID = 7
	overload.OverloadUntil = &until
	repo := &transportSnapshotRepoGeili{accounts: []Account{a, b, ordinary, rateLimited, disabled, expired, overload}}
	snapshot := &SchedulerSnapshotService{accountRepo: repo, cfg: &config.Config{RunMode: config.RunModeStandard}}
	pool, err := snapshot.loadAccountsFromDB(context.Background(), SchedulerBucket{GroupID: 42, Platform: PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, pool, 6, "upstream configured pool retains all timed blocks, including Geili transport cooldowns")
	require.Equal(t, int64(1), pool[0].ID)
	require.Equal(t, int64(2), pool[1].ID)
	require.False(t, pool[0].IsSchedulable(), "retaining membership must not admit an active block")
	for _, i := range []int{2, 3, 4, 5} {
		require.False(t, pool[i].IsSchedulable(), "all active timed blocks remain excluded at request time")
	}
	require.Equal(t, int64(42), *repo.group)
	require.False(t, repo.includeGrouped)
	// Exactly the same stored snapshot can admit A after expiry, with no DB/cache clearing.
	elapsed := now.Add(-time.Second)
	pool[0].TempUnschedulableUntil = &elapsed
	require.True(t, pool[0].IsSchedulable())
	quota := b
	quota.ID = 8
	quota.Extra = map[string]any{"quota_daily_limit": 10.0, "quota_daily_used": 10.0, "quota_daily_start": now.Add(-time.Hour).Format(time.RFC3339)}
	cooledQuota := quota
	cooledQuota.ID = 9
	cooledQuota.TempUnschedulableUntil = &until
	cooledQuota.TempUnschedulableReason = a.TempUnschedulableReason
	repo.accounts = append(repo.accounts, quota, cooledQuota)
	pool, err = snapshot.loadAccountsFromDB(context.Background(), SchedulerBucket{GroupID: 42, Platform: PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, pool, 8, "quota membership remains in the configured pool")
	require.Equal(t, int64(8), pool[6].ID)
	require.Equal(t, int64(9), pool[7].ID)
	require.False(t, pool[6].IsSchedulable(), "request-time quota gate must still reject exhausted accounts")
	for _, simple := range []bool{false, true} {
		if simple {
			snapshot.cfg.RunMode = config.RunModeSimple
		}
		_, err = snapshot.loadAccountsFromDB(context.Background(), SchedulerBucket{Platform: PlatformOpenAI}, false)
		require.NoError(t, err)
		require.Nil(t, repo.group)
		require.Equal(t, simple, repo.includeGrouped)
	}
}
