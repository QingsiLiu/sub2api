//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupSchedulingGeiliAtomicModesAndMembership(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	createdGroups := []int64{}
	t.Cleanup(func() {
		for _, gid := range createdGroups {
			_, _ = integrationDB.Exec(`DELETE FROM groups WHERE id=$1`, gid)
		}
	})
	for _, gid := range []int64{4, 126} {
		result, err := integrationDB.ExecContext(ctx, `INSERT INTO groups(id,name,platform) VALUES($1,$2,'openai') ON CONFLICT(id) DO NOTHING`, gid, fmt.Sprintf("group-priority-test-%d", gid))
		require.NoError(t, err)
		if n, err := result.RowsAffected(); err == nil && n > 0 {
			createdGroups = append(createdGroups, gid)
		}
	}
	repo := NewAccountRepository(client, integrationDB, nil).(*accountRepository)
	a := &service.Account{Name: "group-priority-a", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: 70, Concurrency: 1}
	require.NoError(t, repo.Create(ctx, a))
	require.NoError(t, repo.BindGroups(ctx, a.ID, []int64{4, 126}))
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM account_groups WHERE account_id=$1`, a.ID)
		_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, a.ID)
		_, _ = integrationDB.Exec(`UPDATE groups SET group_scheduling_enabled=false WHERE id IN (4,126)`)
	})
	get := func() service.GroupSchedulingStateGeili {
		g, err := repo.GetGroupSchedulingGeili(ctx, []int64{4})
		require.NoError(t, err)
		return g[0]
	}
	before := get()
	require.False(t, before.Enabled)
	enabled := true
	require.NoError(t, repo.UpdateGroupSchedulingGeili(ctx, 4, service.GroupSchedulingUpdateGeili{ExpectedVersion: &before.Version, ExpectedMembersVersion: before.MembersVersion, Enabled: &enabled, Source: "manual"}, "test"))
	current := get()
	require.True(t, current.Enabled)
	var row service.GroupSchedulingRowGeili
	for _, r := range current.Rows {
		if r.AccountID == a.ID {
			row = r
		}
	}
	require.Equal(t, 70, row.GroupPriority)
	require.Equal(t, "auto", row.Mode)
	zero := 0
	change := service.GroupSchedulingUpdateGeili{ExpectedVersion: &current.Version, ExpectedMembersVersion: current.MembersVersion, Source: "manual", Rows: []service.GroupSchedulingUpdateRowGeili{{AccountID: a.ID, Mode: "fixed", Priority: &zero}}}
	require.NoError(t, repo.UpdateGroupSchedulingGeili(ctx, 4, change, "test"))
	require.ErrorIs(t, repo.UpdateGroupSchedulingGeili(ctx, 4, change, "stale"), service.ErrGroupSchedulingConflictGeili)
	fixed := get()
	change.ExpectedVersion = &fixed.Version
	change.ExpectedMembersVersion = fixed.MembersVersion
	change.Source = "automatic"
	change.Rows[0].Mode = "auto"
	require.ErrorIs(t, repo.UpdateGroupSchedulingGeili(ctx, 4, change, "auto"), service.ErrGroupSchedulingConflictGeili)
	require.NoError(t, repo.BindGroups(ctx, a.ID, []int64{4}))
	refreshed, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 70, refreshed.Priority)
	p, mode := service.EffectiveGroupPriority(refreshed, 4)
	require.Equal(t, 0, p)
	require.Equal(t, "fixed", mode)
	state := get()
	change.ExpectedVersion = &state.Version
	change.ExpectedMembersVersion = state.MembersVersion
	change.Source = "manual"
	change.Rows[0].Mode = "inherit"
	change.Rows[0].Priority = nil
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = repo.UpdateGroupSchedulingGeili(ctx, 4, change, "race") }(i)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, service.ErrGroupSchedulingConflictGeili)
		}
	}
	require.Equal(t, 1, successes)
	refreshed, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	p, mode = service.EffectiveGroupPriority(refreshed, 4)
	require.Equal(t, 70, p)
	require.Equal(t, "inherit", mode)
	var audits int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM geili_group_scheduling_audits WHERE group_id=4`).Scan(&audits))
	require.GreaterOrEqual(t, audits, 3)
}

func TestGroupSchedulingGeiliCacheCarriesVersionWithoutGlobalMutation(t *testing.T) {
	ctx := context.Background()
	cache := NewSchedulerCache(integrationRedis)
	a := &service.Account{ID: 910004, Platform: service.PlatformOpenAI, Priority: 70, AccountGroups: []service.AccountGroup{{AccountID: 910004, GroupID: 4, Priority: 0, PriorityMode: "fixed", PriorityEnabled: true, PriorityVersion: 7}}}
	require.NoError(t, cache.SetAccount(ctx, a))
	t.Cleanup(func() { _ = cache.DeleteAccount(ctx, a.ID) })
	got, err := cache.GetAccount(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 70, got.Priority)
	p, m := service.EffectiveGroupPriority(got, 4)
	require.Equal(t, 0, p)
	require.Equal(t, "fixed", m)
	require.Equal(t, int64(7), got.AccountGroups[0].PriorityVersion)
	batch, err := cache.(*schedulerCache).GetAccountsByIDsGeili(ctx, []int64{a.ID})
	require.NoError(t, err)
	require.Equal(t, 7, int(batch[a.ID].AccountGroups[0].PriorityVersion))
}
