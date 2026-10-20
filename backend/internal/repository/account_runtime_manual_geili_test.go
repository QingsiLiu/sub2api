package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestManualAccountBalanceGeiliChangesOnlyLabelAtomically(t *testing.T) {
	for _, blocked := range []bool{true, false} {
		t.Run(map[bool]string{true: "mark", false: "clear"}[blocked], func(t *testing.T) {
			repo, mock := runtimeTestRepositoryGeili(t)
			expectRuntimeWriteStartGeili(mock, `{}`)
			mock.ExpectQuery(`SELECT parent_account_id FROM accounts`).WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"parent_account_id"}).AddRow(nil))
			reason, stop := "", ""
			var at any
			if blocked {
				reason, at = "manual_insufficient_balance", sqlmock.AnyArg()
			}
			mock.ExpectExec(`UPDATE account_runtime_geili SET`).WithArgs(int64(17), int64(3), blocked, reason, at, stop, false, "{}", "null", nil).WillReturnResult(sqlmock.NewResult(0, 1))
			expectRuntimeOutboxGeili(mock)
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
	mock.ExpectExec(`UPDATE account_runtime_geili SET`).WillReturnError(context.DeadlineExceeded)
	mock.ExpectRollback()
	require.ErrorIs(t, repo.SetManualAccountBalanceStopGeili(context.Background(), 17, true), context.DeadlineExceeded)
}
