package repository

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestFinancialPageMaterializesOnlySelectedIdentities(t *testing.T) {
	start := time.Date(2026, 9, 27, 0, 0, 0, 0, financialBeijing)
	f := usagestats.UsageLogFilters{UserID: 7, StartTime: &start}
	where, args := financialUsageWhere(f)
	q := financialUsagePageQuery(pagination.DefaultPagination(), f, where, len(args), "")
	require.Contains(t, q, "financial_page AS MATERIALIZED")
	require.Contains(t, q, "SELECT id,financial_receipt_id,created_at AS financial_sort")
	require.Contains(t, q, "user_id = $1")
	require.Contains(t, q, "accounting_date >=")
	require.Contains(t, q, "f.financial_receipt_id=p.financial_receipt_id")
	require.Contains(t, q, "f.financial_receipt_id IS NULL AND f.id=p.id")
	require.NotContains(t, q, "COUNT(*)")
}

func TestFinancialPageUnboundedSourcesUseIndependentLimits(t *testing.T) {
	q := financialUsagePageQuery(pagination.PaginationParams{SortBy: "id"}, usagestats.UsageLogFilters{}, "", 0, "")
	require.Contains(t, q, "usage_financial_total_records")
	require.Contains(t, q, "financial_time_source='log' ORDER BY financial_sort DESC NULLS LAST,id DESC LIMIT ($1::bigint+$2::bigint)")
	require.Contains(t, q, "financial_time_source='receipt'")
	require.Contains(t, q, "LIMIT $1 OFFSET $2")
}

func TestFinancialPageReceiptCutoffIsExactAndFallsBack(t *testing.T) {
	end := time.Now().Add(24 * time.Hour)
	f := usagestats.UsageLogFilters{EndTime: &end}
	where, args := financialUsageWhere(f)
	q := financialUsagePageQuery(pagination.DefaultPagination(), f, where, len(args), "")
	require.Contains(t, q, "receipt_cutoff AS MATERIALIZED")
	require.Contains(t, q, "COUNT(financial_sort)>=")
	require.Contains(t, q, "u.created_at >= (SELECT oldest FROM receipt_cutoff)")
	require.Contains(t, q, "financial_receipt_id IS NULL AND NOT (SELECT bounded FROM receipt_cutoff)")
}

func financialOrder(params pagination.PaginationParams) string {
	order := strings.ToUpper(params.NormalizedSortOrder(pagination.SortOrderDesc))
	column := "created_at"
	switch params.SortBy {
	case "model":
		column = "COALESCE(NULLIF(TRIM(requested_model), ''), model)"
	case "accounting_date", "completed_at", "created_at", "actual_cost", "id":
		column = params.SortBy
	}
	return column + " " + order + " NULLS LAST, id " + order
}

func TestFinancialPageJITSettingIsTransactionLocal(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL jit=off").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("WITH").WillReturnRows(sqlmock.NewRows([]string{"record"}).AddRow(`{"id":1,"user_id":7,"api_key_id":8,"actual_cost":1}`))
	mock.ExpectCommit()
	records, _, err := repo.ListFinancialUsage(context.Background(), pagination.DefaultPagination(), usagestats.UsageLogFilters{UserID: 7})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestFinancialPageFailureRollsBackPlannerSettings(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL jit=off").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("WITH").WillReturnError(errors.New("page failed"))
	mock.ExpectRollback()
	_, _, err := repo.ListFinancialUsage(context.Background(), pagination.DefaultPagination(), usagestats.UsageLogFilters{})
	require.ErrorContains(t, err, "page failed")
	require.NoError(t, mock.ExpectationsWereMet())
}
