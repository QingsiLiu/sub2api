package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type financialRollupServiceStub struct {
	*dashboardAggregationRepoTestStub
	calls   atomic.Int32
	started chan context.Context
	finish  chan struct{}
}

func (s *financialRollupServiceStub) SyncFinancialUsageRollups(ctx context.Context, at time.Time) error {
	s.calls.Add(1)
	s.started <- ctx
	select {
	case <-s.finish:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestFinancialRollupStartsWithOptionalAggregationDisabledAndCannotOverlap(t *testing.T) {
	wheel, err := NewTimingWheelService()
	require.NoError(t, err)
	defer wheel.Stop()
	repo := &financialRollupServiceStub{dashboardAggregationRepoTestStub: &dashboardAggregationRepoTestStub{}, started: make(chan context.Context, 2), finish: make(chan struct{})}
	svc := &DashboardAggregationService{repo: repo, timingWheel: wheel}
	svc.Start()
	select {
	case ctx := <-repo.started:
		_, has := ctx.Deadline()
		require.True(t, has)
	case <-time.After(time.Second):
		t.Fatal("financial maintenance must run with optional aggregation disabled")
	}
	defer close(repo.finish)
	svc.startFinancialRollupsGeili()
	require.Eventually(t, func() bool { return atomic.LoadInt32(&svc.financialRollupRunning) == 1 }, time.Second, time.Millisecond)
	// The duplicate starter cannot call the repository while the first owns CAS.
	select {
	case <-repo.started:
		t.Fatal("overlapping financial workers")
	case <-time.After(30 * time.Millisecond):
	}
	require.Equal(t, int32(1), repo.calls.Load())
}

type financialDashboardFreshStub struct {
	UsageLogRepository
	FinancialAdminUsageRepository
	stats *usagestats.DashboardStats
	calls int
}

func (s *financialDashboardFreshStub) GetFinancialAdminDashboardStats(context.Context) (*usagestats.DashboardStats, error) {
	s.calls++
	return s.stats, nil
}
func TestFinancialDashboardBypassesTTLResponseCache(t *testing.T) {
	repo := &financialDashboardFreshStub{stats: &usagestats.DashboardStats{TotalActualCost: 3}}
	cache := &dashboardCacheStub{get: func(context.Context) (string, error) {
		t.Fatal("financial report read a stale TTL response")
		return "", nil
	}}
	svc := &DashboardService{usageRepo: repo, cache: cache}
	first, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3.0, first.TotalActualCost)
	require.False(t, first.StatsStale)
	require.NotEmpty(t, first.StatsUpdatedAt)
	repo.stats = &usagestats.DashboardStats{TotalActualCost: 9}
	second, err := svc.GetDashboardStats(context.Background())
	require.NoError(t, err)
	require.Equal(t, 9.0, second.TotalActualCost)
	require.Equal(t, 2, repo.calls)
	require.Equal(t, int32(0), atomic.LoadInt32(&cache.getCalls))
}

func TestFinancialRollupAlertGeili(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	today := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	target := today.AddDate(0, 0, -7)
	healthy := FinancialRollupHealth{Today: today, ClosedBefore: &today, CoverageStart: &target, CoverageTarget: target}
	require.False(t, healthy.Lagging())
	require.Equal(t, "", financialRollupAlert(healthy, time.Time{}, now))

	queued := healthy
	queued.OldestEventAge = 14 * time.Minute
	require.Equal(t, "", financialRollupAlert(queued, time.Time{}, now))
	queued.OldestEventAge = 15 * time.Minute
	require.Equal(t, "delayed", financialRollupAlert(queued, time.Time{}, now))
	queued.OldestEventAge = time.Hour
	require.Equal(t, "stalled", financialRollupAlert(queued, time.Time{}, now))

	yesterday := today.AddDate(0, 0, -1)
	late := target.AddDate(0, 0, 3)
	for _, h := range []FinancialRollupHealth{
		{Today: today, ClosedBefore: &yesterday, CoverageStart: &target, CoverageTarget: target},
		{Today: today, ClosedBefore: &today, CoverageStart: &late, CoverageTarget: target},
		{Today: today, ClosedBefore: &today, CoverageTarget: target},
		{Today: today, CoverageStart: &target, CoverageTarget: target},
	} {
		require.True(t, h.Lagging())
		// Backfill right after a release or at midnight is expected to lag briefly.
		require.Equal(t, "", financialRollupAlert(h, now.Add(-29*time.Minute), now))
		require.Equal(t, "delayed", financialRollupAlert(h, now.Add(-30*time.Minute), now))
		require.Equal(t, "stalled", financialRollupAlert(h, now.Add(-time.Hour), now))
	}
}
