package service

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// FinancialUsageRollupRepository maintains exact financial all-time summaries.
// It is independent of optional operational dashboard aggregation/retention.
type FinancialUsageRollupRepository interface {
	SyncFinancialUsageRollups(context.Context, time.Time) error
}

func (s *DashboardAggregationService) startFinancialRollupsGeili() {
	repo, ok := s.repo.(FinancialUsageRollupRepository)
	if !ok {
		return
	}
	run := func() {
		if !atomic.CompareAndSwapInt32(&s.financialRollupRunning, 0, 1) {
			return
		}
		defer atomic.StoreInt32(&s.financialRollupRunning, 0)
		ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationTimeout)
		defer cancel()
		if err := repo.SyncFinancialUsageRollups(ctx, time.Now()); err != nil {
			logger.LegacyPrintf("service.dashboard_aggregation", "[FinancialRollup] 汇总未完成，查询继续使用脏桶原始数据: %v", err)
		}
	}
	go run()
	s.timingWheel.ScheduleRecurring("dashboard:financial-rollup", 10*time.Second, run)
}
