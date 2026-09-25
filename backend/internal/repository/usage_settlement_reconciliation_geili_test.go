package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestSettlementReconciliationBoundedBeforeEligibility(t *testing.T) {
	limitAt := strings.Index(settlementReconciliationSQL, "ORDER BY id DESC LIMIT ($1+1)")
	eligibleAt := strings.Index(settlementReconciliationSQL, "WHERE state='settled'")
	require.Positive(t, limitAt)
	require.Greater(t, eligibleAt, limitAt)
	require.NotContains(t, strings.ToUpper(settlementReconciliationSQL), "FOR UPDATE")
	require.NotContains(t, strings.ToUpper(settlementReconciliationSQL), "UPDATE ")
	require.Contains(t, settlementReconciliationSQL, "u.id=r.usage_log_id")
	require.Contains(t, settlementReconciliationSQL, "sr.request_key=NULLIF(r.admission_key,'')")
	require.Contains(t, settlementReconciliationSQL, "request_id=r.request_id AND api_key_id=r.api_key_id")
}

func TestSettlementReconciliationPreservesExactDiscrepancyAndCoverage(t *testing.T) {
	db, mock := newSQLMock(t)
	now := time.Date(2026, 9, 26, 1, 2, 3, 123456000, time.UTC)
	oldest := now.Add(-time.Minute)
	cols := []string{"scanned", "checked", "last", "first", "more", "oldest", "newest", "amount", "missing", "identity", "dedup", "subchecked", "subskipped", "submismatch", "net", "absolute", "subnet"}
	mock.ExpectQuery("WITH tail AS MATERIALIZED").WithArgs(1000, now.Add(-5*time.Minute), now).WillReturnRows(sqlmock.NewRows(cols).AddRow(1000, 998, 12, 1011, true, oldest, now, 2, 1, 0, 1, 500, 3, 1, "0.0000000001", "2.0000000001", "-0.0100000000"))
	result, err := reconcileRecentSettlements(context.Background(), db, 9000, now)
	require.NoError(t, err)
	require.Equal(t, 1000, result.Limit)
	require.Equal(t, int64(2), result.SkippedCount)
	require.True(t, result.HasMore)
	require.Equal(t, "recent_delivered_sample", result.Coverage)
	require.Equal(t, "0.0000000001", result.NetDiscrepancyUSD)
	require.Equal(t, "2.0000000001", result.AbsoluteDiscrepancyUSD)
	require.Equal(t, "-0.0100000000", result.SubscriptionNetDiscrepancyUSD)
	require.Equal(t, oldest, *result.OldestCheckedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSettlementReconciliationReadErrorIsNotHealthyZero(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectQuery("WITH tail AS MATERIALIZED").WillReturnError(errors.New("database unavailable"))
	_, err := (&usageBillingRepository{db: db}).ReconcileRecentSettlements(context.Background(), 100)
	require.ErrorContains(t, err, "database unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}
