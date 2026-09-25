package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestFinancialWhereUsesBeijingAccountingAndAllIdentityFilters(t *testing.T) {
	start := time.Date(2026, 9, 25, 16, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	billing := int8(1)
	requestType := int16(2)
	flag := true
	filters := usagestats.UsageLogFilters{UserID: 7, APIKeyID: 9, SubscriptionID: 12, GroupID: 11, RequestID: " req ", Model: "gpt-x", ModelFilterSource: usagestats.ModelSourceRequested, RequestType: &requestType, NativeCompactionV2: &flag, BillingType: &billing, StartTime: &start, EndTime: &end}
	where, args := financialUsageWhere(filters)
	require.Contains(t, where, "user_id = $1 AND api_key_id = $2 AND subscription_id = $3 AND group_id = $4 AND request_id = $5")
	require.Contains(t, where, "accounting_date >=")
	require.Contains(t, where, "financial_time_source <> 'log' OR created_at >=")
	require.Equal(t, "2026-09-26", args[len(args)-4])
	require.Equal(t, "2026-09-27", args[len(args)-2])
	filters.DateBasis = "completed"
	where, args = financialUsageWhere(filters)
	require.Contains(t, where, "completed_at >=")
	require.Equal(t, start, args[len(args)-2])
	require.Equal(t, end, args[len(args)-1])
}

func TestFinancialDecodePartialNeverInventsMissingMetadata(t *testing.T) {
	record, err := decodeFinancialUsage([]byte(`{"id":-31,"user_id":7,"api_key_id":9,"subscription_id":12,"billing_type":1,"request_id":"historic","actual_cost":10.84225728,"account_id":null,"model":null,"created_at":null,"completed_at":null,"accounting_date":"2026-09-25","record_source":"historical_recovery","record_completeness":"partial","detail_pending":false}`))
	require.NoError(t, err)
	require.Equal(t, int64(-31), record.ID)
	require.InDelta(t, 10.84225728, record.ActualCost, 1e-10)
	require.Nil(t, record.Financial.CompletedAt)
	require.Contains(t, record.Financial.UnknownFields, "account_id")
	require.Contains(t, record.Financial.UnknownFields, "model")
	require.Contains(t, record.Financial.UnknownFields, "input_tokens")
	require.Contains(t, record.Financial.UnknownFields, "total_cost")
	require.Contains(t, record.Financial.UnknownFields, "created_at")
	require.NotContains(t, record.Financial.UnknownFields, "actual_cost")
}

func TestFinancialDecodeAmountUnknownNotZeroEvidence(t *testing.T) {
	record, err := decodeFinancialUsage([]byte(`{"id":-32,"user_id":7,"api_key_id":9,"actual_cost":null,"record_source":"historical_recovery","record_completeness":"amount_unknown"}`))
	require.NoError(t, err)
	require.Contains(t, record.Financial.UnknownFields, "actual_cost")
}

func TestFinancialDecodePreservesKnownZeroFreeUsage(t *testing.T) {
	data := map[string]any{"id": 1, "user_id": 7, "api_key_id": 9, "account_id": 3, "model": "free", "actual_cost": 0, "input_tokens": 0, "output_tokens": 0, "total_cost": 0, "record_source": "live", "record_completeness": "complete"}
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	record, err := decodeFinancialUsage(raw)
	require.NoError(t, err)
	require.NotContains(t, record.Financial.UnknownFields, "actual_cost")
	require.NotContains(t, record.Financial.UnknownFields, "input_tokens")
	require.NotContains(t, record.Financial.UnknownFields, "model")
}

func TestFinancialMissingProjectionFailsClosed(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	mock.ExpectQuery("usage_financial_total_records").WillReturnError(errors.New("relation usage_financial_records does not exist"))
	_, err := repo.GetFinancialUsageStats(context.Background(), usagestats.UsageLogFilters{UserID: 7})
	require.ErrorContains(t, err, "does not exist")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFinancialAggregateTracksUnknownAndPending(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	cols := []string{"gi", "gu", "i", "u", "requests", "input", "output", "write", "read", "cost", "actual", "account", "duration", "balance", "subscription", "pending", "unknown", "partial", "cost_complete", "tokens_complete"}
	mock.ExpectQuery("WITH financial_scope").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows(cols).AddRow(1, 1, nil, nil, 3, 1, 2, 0, 0, 2.0, 12.0, 2.0, 10.0, 2.0, 10.0, 1, 1, 2, false, false))
	stats, err := repo.GetFinancialUsageStats(context.Background(), usagestats.UsageLogFilters{UserID: 7})
	require.NoError(t, err)
	require.Equal(t, 12.0, stats.TotalActualCost)
	require.Equal(t, 2.0, stats.BalanceActualCost)
	require.Equal(t, 10.0, stats.SubscriptionActualCost)
	require.Equal(t, int64(1), stats.UnknownAmountCount)
	require.Equal(t, int64(1), stats.DetailPendingCount)
	require.False(t, stats.StandardCostComplete)
	require.False(t, stats.TokenCountsComplete)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFinancialPeriodTodayDoesNotExcludeCurrentDate(t *testing.T) {
	start := time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing)
	end := start.Add(12 * time.Hour)
	_, args := financialUsageWhere(usagestats.UsageLogFilters{StartTime: &start, EndTime: &end})
	require.Equal(t, []any{"2026-09-26", start, "2026-09-27", start.AddDate(0, 0, 1)}, args)
}
