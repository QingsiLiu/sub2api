//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSettlementFinancialIdentityAllowsNewAdmissionButRejectsAmountOrModel(t *testing.T) {
	a := &service.UsageBillingCommand{RequestID: "r", APIKeyID: 1, UserID: 2, BalanceCost: 1, Model: "m", SubscriptionAdmissionKey: "old"}
	b := *a
	b.SubscriptionAdmissionKey = "retry"
	require.True(t, sameSettlementFinancialCommand(a, &b))
	b.BalanceCost = 2
	require.False(t, sameSettlementFinancialCommand(a, &b))
	b = *a
	b.Model = "other"
	require.False(t, sameSettlementFinancialCommand(a, &b))
}

func TestUsageLogDirectFallbackBypassesFullQueues(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	log := &service.UsageLog{RequestID: "r", UserID: 1, APIKeyID: 2, AccountID: 3, Model: "m", CreatedAt: time.Now()}
	r := &usageLogRepository{sql: db, db: db, createBatchCh: make(chan usageLogCreateRequest), bestEffortBatchCh: make(chan usageLogBestEffortRequest)}
	mock.ExpectQuery(`INSERT INTO usage_logs`).WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, log.CreatedAt))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inserted, err := r.CreateDirect(ctx, log)
	require.NoError(t, err)
	require.True(t, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogFailedBatchUsesFreshFallbackContext(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	prepared := prepareUsageLogInsert(&service.UsageLog{RequestID: "r", UserID: 1, APIKeyID: 2, AccountID: 3, Model: "m", CreatedAt: time.Now()})
	mock.ExpectExec(`INSERT INTO usage_logs`).WillReturnResult(sqlmock.NewResult(1, 1))
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	(&usageLogRepository{}).flushBestEffortBatchContext(ctx, db, []usageLogBestEffortRequest{{prepared: prepared, apiKeyID: 2, resultCh: done}})
	require.NoError(t, <-done)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSettlementErrorCodesNeverPersistDriverPayload(t *testing.T) {
	require.Equal(t, "identity_or_amount_conflict", settlementErrorCode(service.ErrUsageBillingRequestConflict))
	require.Equal(t, "retryable_storage_or_validation_error", settlementErrorCode(errors.New("secret SQL argument")))
}

func TestSettlementShutdownDoesNotRetryEveryUnvisitedLease(t *testing.T) {
	for _, delivery := range []bool{false, true} {
		name := "settlement"
		if delivery {
			name = "delivery"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			if delivery {
				mock.ExpectQuery(`WITH selected`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3).AddRow(4))
			} else {
				command := `{"RequestID":"shutdown","UserID":1,"APIKeyID":2,"BalanceCost":1}`
				mock.ExpectQuery(`WITH selected`).WillReturnRows(sqlmock.NewRows([]string{"id", "command"}).AddRow(1, command).AddRow(2, command).AddRow(3, command).AddRow(4, command))
			}
			// Cancellation occurs inside the first leased row. Exactly one bounded
			// detached failure update is allowed; rows2-4 must not be started at all.
			mock.ExpectBegin().WillDelayFor(time.Second)
			mock.ExpectExec(`UPDATE usage_settlement_receipts`).WillReturnResult(sqlmock.NewResult(0, 1))
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			started := time.Now()
			r := &usageBillingRepository{db: db}
			if delivery {
				_, err = r.DeliverSettledUsage(ctx, 4)
			} else {
				_, err = r.ProcessPendingSettlements(ctx, 4)
			}
			require.Error(t, err)
			require.Less(t, time.Since(started), 500*time.Millisecond)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
