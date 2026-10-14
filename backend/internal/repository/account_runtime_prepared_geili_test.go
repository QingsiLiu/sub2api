package repository

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func preparedRuntimeRepositoryGeili(t *testing.T) (*accountRepository, *sql.DB, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	repo := NewAccountRepository(nil, db, nil).(*accountRepository)
	var once sync.Once
	closeDB := func() {
		once.Do(func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
		})
	}
	t.Cleanup(func() {
		closeDB()
		require.NoError(t, mock.ExpectationsWereMet())
	})
	return repo, db, mock, closeDB
}

func preparedAdmissionRowsGeili(allowed, balance bool, reason string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"allowed", "balance_blocked", "reason", "credential_account_id"}).AddRow(allowed, balance, reason, int64(17))
}

func TestAccountRuntimePreparedGeiliAdmissionReusesPlanButReadsCurrentState(t *testing.T) {
	repo, _, mock, _ := preparedRuntimeRepositoryGeili(t)
	prepare := mock.ExpectPrepare(accountRuntimeAdmissionSQLGeili).WillBeClosed()
	prepare.ExpectQuery().WithArgs(int64(17), "claude-opus-5").WillReturnRows(preparedAdmissionRowsGeili(true, false, ""))
	prepare.ExpectQuery().WithArgs(int64(17), "claude-opus-5").WillReturnRows(preparedAdmissionRowsGeili(false, true, "balance"))
	prepare.ExpectQuery().WithArgs(int64(17), "other-model").WillReturnRows(preparedAdmissionRowsGeili(false, false, "model_failure"))
	first, err := repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "claude-opus-5")
	require.NoError(t, err)
	require.True(t, first.Allowed)
	second, err := repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "claude-opus-5")
	require.NoError(t, err)
	require.False(t, second.Allowed)
	require.True(t, second.BalanceBlocked)
	require.Equal(t, "balance", second.Reason)
	third, err := repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "other-model")
	require.NoError(t, err)
	require.Equal(t, "model_failure", third.Reason)
	require.True(t, first.Allowed, "a later read must not mutate or replace an earlier result")
}

func TestAccountRuntimePreparedGeiliStateReadAndHealthySuccessUseOnePlan(t *testing.T) {
	repo, _, mock, _ := preparedRuntimeRepositoryGeili(t)
	prepare := mock.ExpectPrepare(accountRuntimeGetSQLGeili).WillBeClosed()
	prepare.ExpectQuery().WithArgs(int64(17)).WillReturnRows(sqlmock.NewRows([]string{"account_id"}))
	prepare.ExpectQuery().WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(`{"model":{"failure_count":2,"blocked":true}}`))
	prepare.ExpectQuery().WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(`{}`))
	state, err := repo.GetAccountRuntimeGeili(context.Background(), 17)
	require.NoError(t, err)
	require.Equal(t, int64(17), state.AccountID)
	require.Empty(t, state.Models)
	state, err = repo.GetAccountRuntimeGeili(context.Background(), 17)
	require.NoError(t, err)
	require.True(t, state.Models["model"].Blocked)
	// Healthy completion performs a fresh read but creates no row, transaction,
	// outbox event or cached result. The third DB result is already different.
	require.NoError(t, repo.ResetAccountModelSuccessGeili(context.Background(), 17, "model"))
}

func TestAccountRuntimePreparedGeiliPrepareErrorFailsClosedAndRetries(t *testing.T) {
	repo, _, mock, _ := preparedRuntimeRepositoryGeili(t)
	failed := errors.New("temporary prepare failure")
	mock.ExpectPrepare(accountRuntimeAdmissionSQLGeili).WillReturnError(failed)
	prepare := mock.ExpectPrepare(accountRuntimeAdmissionSQLGeili).WillBeClosed()
	prepare.ExpectQuery().WithArgs(int64(17), "model").WillReturnRows(preparedAdmissionRowsGeili(false, true, "balance"))
	gate, err := repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "model")
	require.ErrorIs(t, err, failed)
	require.False(t, gate.Allowed)
	gate, err = repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "model")
	require.NoError(t, err)
	require.True(t, gate.BalanceBlocked)
}

func TestAccountRuntimePreparedGeiliQueryAndScanErrorsFailClosed(t *testing.T) {
	for _, failure := range []string{"query", "rows", "scan", "not found"} {
		t.Run(failure, func(t *testing.T) {
			repo, _, mock, _ := preparedRuntimeRepositoryGeili(t)
			query := mock.ExpectPrepare(accountRuntimeAdmissionSQLGeili).WillBeClosed().ExpectQuery().WithArgs(int64(17), "model")
			switch failure {
			case "query":
				query.WillReturnError(errors.New("database unavailable"))
			case "rows":
				query.WillReturnRows(preparedAdmissionRowsGeili(true, false, "").RowError(0, errors.New("read interrupted")))
			case "scan":
				query.WillReturnRows(sqlmock.NewRows([]string{"allowed", "balance_blocked", "reason", "credential_account_id"}).AddRow("invalid-boolean", false, "", 17))
			default:
				query.WillReturnRows(sqlmock.NewRows([]string{"allowed"}))
			}
			gate, err := repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "model")
			require.Error(t, err)
			if gate != nil {
				require.False(t, gate.Allowed)
			}
			if failure == "not found" {
				require.ErrorIs(t, err, service.ErrAccountNotFound)
			}
		})
	}
}

func TestAccountRuntimePreparedGeiliConcurrentReadersShareOneStatement(t *testing.T) {
	repo, _, mock, _ := preparedRuntimeRepositoryGeili(t)
	mock.MatchExpectationsInOrder(false)
	prepare := mock.ExpectPrepare(accountRuntimeAdmissionSQLGeili).WillDelayFor(15 * time.Millisecond).WillBeClosed()
	const readers = 24
	for i := 0; i < readers; i++ {
		prepare.ExpectQuery().WithArgs(int64(17), "model").WillReturnRows(preparedAdmissionRowsGeili(false, true, "balance"))
	}
	start := make(chan struct{})
	results := make(chan error, readers)
	var workers sync.WaitGroup
	for i := 0; i < readers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			gate, err := repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "model")
			if err == nil && (!gate.BalanceBlocked || gate.Allowed) {
				err = errors.New("fresh prepared admission must retain balance denial")
			}
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
}

func TestAccountRuntimePreparedGeiliTransactionViewNeverUsesPoolStatement(t *testing.T) {
	repo, db, mock, _ := preparedRuntimeRepositoryGeili(t)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	prepare := mock.ExpectPrepare(accountRuntimeGetSQLGeili).WillBeClosed()
	prepare.ExpectQuery().WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(`{}`))
	_, err := repo.GetAccountRuntimeGeili(context.Background(), 17)
	require.NoError(t, err)
	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	view := *repo
	view.client, view.sql = tx.Client(), tx.Client()
	// MaxOpenConns=1 means accidentally executing a pool statement from this
	// view would wait for its own transaction connection and hit the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mock.ExpectQuery(accountRuntimeGetSQLGeili).WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(`{"model":{"blocked":true}}`))
	mock.ExpectQuery(accountRuntimeAdmissionSQLGeili).WithArgs(int64(17), "model").WillReturnRows(preparedAdmissionRowsGeili(false, true, "balance"))
	state, err := view.GetAccountRuntimeGeili(ctx, 17)
	require.NoError(t, err)
	require.True(t, state.Models["model"].Blocked)
	gate, err := view.CheckAccountRuntimeAdmissionGeili(ctx, 17, "model")
	require.NoError(t, err)
	require.True(t, gate.BalanceBlocked)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
}

func TestAccountRuntimePreparedGeiliDatabaseCloseClosesBothDriverStatements(t *testing.T) {
	repo, _, mock, closeDB := preparedRuntimeRepositoryGeili(t)
	mock.ExpectPrepare(accountRuntimeGetSQLGeili).WillBeClosed().ExpectQuery().WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(`{}`))
	mock.ExpectPrepare(accountRuntimeAdmissionSQLGeili).WillBeClosed().ExpectQuery().WithArgs(int64(17), "model").WillReturnRows(preparedAdmissionRowsGeili(true, false, ""))
	_, err := repo.GetAccountRuntimeGeili(context.Background(), 17)
	require.NoError(t, err)
	_, err = repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "model")
	require.NoError(t, err)
	closeDB()
	require.NoError(t, mock.ExpectationsWereMet())
	gate, err := repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "model")
	require.Error(t, err)
	require.False(t, gate.Allowed)
}

func TestAccountRuntimePreparedGeiliCancelledWaiterDoesNotWaitForAnotherPrepare(t *testing.T) {
	repo, _, mock, _ := preparedRuntimeRepositoryGeili(t)
	// Block preparation through the slot's notification to make cancellation
	// deterministic, without scheduling-dependent sleeps or real DB calls.
	reads := repo.runtimeReadsGeili
	reads.admission.preparing = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	gate, err := repo.CheckAccountRuntimeAdmissionGeili(ctx, 17, "model")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, gate.Allowed)
	close(reads.admission.preparing)
	reads.admission.preparing = nil
	prepare := mock.ExpectPrepare(accountRuntimeAdmissionSQLGeili).WillBeClosed()
	prepare.ExpectQuery().WithArgs(int64(17), "model").WillReturnRows(preparedAdmissionRowsGeili(true, false, ""))
	gate, err = repo.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "model")
	require.NoError(t, err)
	require.True(t, gate.Allowed)
}

func TestAccountRuntimePreparedGeiliQueryErrorDoesNotReplaceOrCacheStatementResult(t *testing.T) {
	repo, _, mock, _ := preparedRuntimeRepositoryGeili(t)
	prepare := mock.ExpectPrepare(accountRuntimeGetSQLGeili).WillBeClosed()
	failed := errors.New("read failure")
	prepare.ExpectQuery().WithArgs(int64(17)).WillReturnError(failed)
	prepare.ExpectQuery().WithArgs(int64(17)).WillReturnRows(runtimeRowsGeili(`{}`))
	state, err := repo.GetAccountRuntimeGeili(context.Background(), 17)
	require.ErrorIs(t, err, failed)
	require.Nil(t, state)
	state, err = repo.GetAccountRuntimeGeili(context.Background(), 17)
	require.NoError(t, err)
	require.Equal(t, int64(17), state.AccountID)
}

func TestAccountRuntimePreparedGeiliOtherPoolAndAdminConstructor(t *testing.T) {
	repo, _, _, _ := preparedRuntimeRepositoryGeili(t)
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	view := *repo
	view.sql = db
	// An injected different pool must not use the original pool's statement.
	mock.ExpectQuery(accountRuntimeAdmissionSQLGeili).WithArgs(int64(17), "model").WillReturnRows(preparedAdmissionRowsGeili(false, true, "balance"))
	gate, err := view.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "model")
	require.NoError(t, err)
	require.True(t, gate.BalanceBlocked)
	admin := NewAdminAccountRepository(nil, db, nil).(*accountRepository)
	mock.ExpectPrepare(accountRuntimeAdmissionSQLGeili).WillBeClosed().ExpectQuery().WithArgs(int64(17), "model").WillReturnRows(preparedAdmissionRowsGeili(true, false, ""))
	gate, err = admin.CheckAccountRuntimeAdmissionGeili(context.Background(), 17, "model")
	require.NoError(t, err)
	require.True(t, gate.Allowed)
}
