package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// FinancialRollupHealth reports queue depth and how far the consumer is behind
// the dates it must have materialized for now.
func (r *dashboardAggregationRepository) FinancialRollupHealth(ctx context.Context, now time.Time) (service.FinancialRollupHealth, error) {
	today := financialRollupDay(now.In(financialBeijing))
	h := service.FinancialRollupHealth{Today: today, CoverageTarget: today.AddDate(0, 0, 1-financialFactDays())}
	var oldest, closed, coverage sql.NullTime
	err := scanSingleRow(ctx, r.sql, `SELECT
 (SELECT COUNT(*) FROM usage_financial_rollup_events),
 (SELECT created_at FROM usage_financial_rollup_events ORDER BY id LIMIT 1),
 (SELECT closed_before FROM usage_financial_rollup_state WHERE id=1),
 (SELECT coverage_start FROM usage_financial_fact_state WHERE id=1)`, nil, &h.Pending, &oldest, &closed, &coverage)
	if err != nil {
		return h, err
	}
	if oldest.Valid {
		h.OldestEventAge = now.Sub(oldest.Time)
	}
	if closed.Valid {
		day := financialRollupDay(closed.Time)
		h.ClosedBefore = &day
	}
	if coverage.Valid {
		day := financialRollupDay(coverage.Time)
		h.CoverageStart = &day
	}
	return h, nil
}
