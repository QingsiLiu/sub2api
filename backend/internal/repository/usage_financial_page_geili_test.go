package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestFinancialPageMaterializesOnlySelectedIdentities(t *testing.T) {
	start := time.Date(2026, 9, 27, 0, 0, 0, 0, financialBeijing)
	f := usagestats.UsageLogFilters{UserID: 7, StartTime: &start}
	where, args := financialUsageWhere(f)
	q := financialUsagePageQuery(pagination.DefaultPagination(), f, where, len(args))
	require.Contains(t, q, "financial_page AS MATERIALIZED")
	require.Contains(t, q, "SELECT id,financial_receipt_id,created_at AS financial_sort")
	require.Contains(t, q, "user_id = $1")
	require.Contains(t, q, "accounting_date >=")
	require.Contains(t, q, "f.financial_receipt_id=p.financial_receipt_id")
	require.Contains(t, q, "f.financial_receipt_id IS NULL AND f.id=p.id")
	require.NotContains(t, q, "COUNT(*)")
}

func TestFinancialPageUnboundedSourcesUseIndependentLimits(t *testing.T) {
	q := financialUsagePageQuery(pagination.DefaultPagination(), usagestats.UsageLogFilters{}, "", 0)
	require.Contains(t, q, "usage_financial_total_records")
	require.Contains(t, q, "financial_time_source='log' ORDER BY financial_sort DESC,id DESC LIMIT ($1::bigint+$2::bigint)")
	require.Contains(t, q, "financial_time_source='receipt'")
	require.Contains(t, q, "LIMIT $1 OFFSET $2")
}

func TestFinancialPageRecentProbeAlwaysBoundedAndFallsBack(t *testing.T) {
	end := time.Now().Add(24 * time.Hour)
	f := usagestats.UsageLogFilters{EndTime: &end}
	where, args := financialUsageWhere(f)
	q := financialUsagePageQuery(pagination.DefaultPagination(), f, where, len(args))
	require.Contains(t, q, "legacy_probe AS MATERIALIZED")
	require.Contains(t, q, "LIMIT GREATEST(1000,")
	require.Contains(t, q, "WHERE (SELECT COUNT(*) FROM legacy_probe)>=")
	require.Contains(t, q, "financial_receipt_id IS NULL AND (SELECT COUNT(*) FROM legacy_probe)<")
}
