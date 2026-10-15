package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestManualAccountBalanceGeiliStopsParentAndShadowsAtomically(t *testing.T) {
	for _, blocked := range []bool{true, false} {
		t.Run(map[bool]string{true: "mark", false: "clear"}[blocked], func(t *testing.T) {
			repo, mock := runtimeTestRepositoryGeili(t)
			expectRuntimeWriteStartGeili(mock, `{}`)
			mock.ExpectQuery(`SELECT parent_account_id FROM accounts`).WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"parent_account_id"}).AddRow(nil))
			mock.ExpectQuery(`SELECT id FROM accounts WHERE parent_account_id=\$1.*ORDER BY id FOR UPDATE`).WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(21).AddRow(23)).RowsWillBeClosed()
			reason, stop := "", "manual"
			var at any
			if blocked {
				reason, stop, at = "manual_insufficient_balance", "balance", sqlmock.AnyArg()
			}
			mock.ExpectExec(`UPDATE account_runtime_geili SET`).WithArgs(int64(17), int64(3), blocked, reason, at, stop, false, "{}", "null", nil).WillReturnResult(sqlmock.NewResult(0, 1))
			expectRuntimeOutboxGeili(mock)
			mock.ExpectExec(`UPDATE accounts SET status='inactive',schedulable=FALSE`).WithArgs("{17,21,23}", reason).WillReturnResult(sqlmock.NewResult(0, 3))
			for _, id := range []int64{21, 23} {
				mock.ExpectExec(`INSERT INTO scheduler_outbox`).WithArgs(service.SchedulerOutboxEventAccountChanged, id, nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
			}
			mock.ExpectCommit()
			require.NoError(t, repo.SetManualAccountBalanceStopGeili(context.Background(), 17, blocked))
		})
	}
}

func TestManualAccountBalanceGeiliRejectsShadowAndMissingAccount(t *testing.T) {
	t.Run("shadow", func(t *testing.T) {
		repo, mock := runtimeTestRepositoryGeili(t)
		expectRuntimeWriteStartGeili(mock, `{}`)
		mock.ExpectQuery(`SELECT parent_account_id FROM accounts`).WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"parent_account_id"}).AddRow(3))
		mock.ExpectRollback()
		require.ErrorIs(t, repo.SetManualAccountBalanceStopGeili(context.Background(), 17, true), service.ErrAccountManualBalanceShadowGeili)
	})
	t.Run("missing", func(t *testing.T) {
		repo, mock := runtimeTestRepositoryGeili(t)
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT id FROM accounts .* FOR UPDATE`).WithArgs(int64(17)).WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()
		require.ErrorIs(t, repo.SetManualAccountBalanceStopGeili(context.Background(), 17, true), service.ErrAccountNotFound)
	})
}

func TestManualAccountBalanceGeiliWriteFailureRollsBack(t *testing.T) {
	repo, mock := runtimeTestRepositoryGeili(t)
	expectRuntimeWriteStartGeili(mock, `{}`)
	mock.ExpectQuery(`SELECT parent_account_id FROM accounts`).WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"parent_account_id"}).AddRow(nil))
	mock.ExpectQuery(`SELECT id FROM accounts WHERE parent_account_id=\$1`).WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"id"})).RowsWillBeClosed()
	mock.ExpectExec(`UPDATE account_runtime_geili SET`).WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()
	require.ErrorIs(t, repo.SetManualAccountBalanceStopGeili(context.Background(), 17, true), context.DeadlineExceeded)
}
