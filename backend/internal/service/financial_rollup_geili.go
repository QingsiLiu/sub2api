package service

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// FinancialUsageRollupRepository maintains exact financial all-time summaries
// and the materialized recent financial facts that dashboard reads use.
type FinancialUsageRollupRepository interface {
	SyncFinancialUsageRollups(context.Context, time.Time) error
}

// FinancialRollupHealthRepository reports consumer backlog for alerting.
type FinancialRollupHealthRepository interface {
	FinancialRollupHealth(context.Context, time.Time) (FinancialRollupHealth, error)
}

// FinancialRollupHealth is a point-in-time view of the financial rollup queue.
// Dates are Beijing calendar days.
type FinancialRollupHealth struct {
	Pending        int64
	OldestEventAge time.Duration
	Today          time.Time
	ClosedBefore   *time.Time
	CoverageStart  *time.Time
	CoverageTarget time.Time
}

// Lagging reports whether daily rollups or materialized facts are behind.
// Readers stay exact while lagging (they fall back to source views), but slow.
func (h FinancialRollupHealth) Lagging() bool {
	return h.ClosedBefore == nil || h.ClosedBefore.Before(h.Today) ||
		h.CoverageStart == nil || h.CoverageStart.After(h.CoverageTarget)
}

const (
	// Repository runs stop starting steps after ~2m; this only bounds a
	// single step (10m) that began near the end of the budget.
	financialRollupRunTimeout      = 15 * time.Minute
	financialRollupHealthInterval  = 5 * time.Minute
	financialRollupDelayedAge      = 15 * time.Minute
	financialRollupStalledAge      = time.Hour
	financialRollupLagDelayedAfter = 30 * time.Minute
)

// financialRollupAlert returns "", "delayed" or "stalled". lagSince is when
// the current lag was first observed (zero when not lagging).
func financialRollupAlert(h FinancialRollupHealth, lagSince, now time.Time) string {
	lag := time.Duration(0)
	if !lagSince.IsZero() {
		lag = now.Sub(lagSince)
	}
	switch {
	case h.OldestEventAge >= financialRollupStalledAge || lag >= financialRollupStalledAge:
		return "stalled"
	case h.OldestEventAge >= financialRollupDelayedAge || lag >= financialRollupLagDelayedAfter:
		return "delayed"
	}
	return ""
}

func (s *DashboardAggregationService) startFinancialRollupsGeili() {
	repo, ok := s.repo.(FinancialUsageRollupRepository)
	if !ok {
		return
	}
	health, _ := s.repo.(FinancialRollupHealthRepository)
	var lastCheck, lagSince time.Time
	run := func() {
		if !atomic.CompareAndSwapInt32(&s.financialRollupRunning, 0, 1) {
			return
		}
		defer atomic.StoreInt32(&s.financialRollupRunning, 0)
		ctx, cancel := context.WithTimeout(context.Background(), financialRollupRunTimeout)
		defer cancel()
		if err := repo.SyncFinancialUsageRollups(ctx, time.Now()); err != nil {
			logger.LegacyPrintf("service.dashboard_aggregation", "[FinancialRollup] 汇总未完成，查询继续使用脏桶原始数据: %v", err)
		}
		// Guarded by financialRollupRunning, so lastCheck/lagSince need no lock.
		now := time.Now()
		if health == nil || now.Sub(lastCheck) < financialRollupHealthInterval {
			return
		}
		lastCheck = now
		hctx, hcancel := context.WithTimeout(context.Background(), 10*time.Second)
		h, err := health.FinancialRollupHealth(hctx, now)
		hcancel()
		if err != nil {
			logger.LegacyPrintf("service.dashboard_aggregation", "[FinancialRollup] 健康检查失败: %v", err)
			return
		}
		if !h.Lagging() {
			lagSince = time.Time{}
		} else if lagSince.IsZero() {
			lagSince = now
		}
		attrs := []any{"pending", h.Pending, "oldest_event_age", h.OldestEventAge.Round(time.Second).String(), "lagging", h.Lagging(), "closed_before", financialRollupHealthDate(h.ClosedBefore), "coverage_start", financialRollupHealthDate(h.CoverageStart), "coverage_target", h.CoverageTarget.Format("2006-01-02")}
		switch financialRollupAlert(h, lagSince, now) {
		case "stalled":
			slog.Error("ALERT financial rollup stalled", attrs...)
		case "delayed":
			slog.Warn("ALERT financial rollup delayed", attrs...)
		}
	}
	go run()
	s.timingWheel.ScheduleRecurring("dashboard:financial-rollup", 10*time.Second, run)
}

func financialRollupHealthDate(day *time.Time) string {
	if day == nil {
		return ""
	}
	return day.Format("2006-01-02")
}
