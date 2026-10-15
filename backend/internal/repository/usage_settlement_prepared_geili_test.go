package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func preparedSettlementRepositoryGeili(t *testing.T) (*usageBillingRepository, *sql.DB, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	// One physical connection makes each cached logical statement correspond
	// to exactly one driver Prepare, including the concurrent callers.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	repo := NewUsageBillingRepository(nil, db).(*usageBillingRepository)
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

func settlementClaimQueryGeili(delivery bool) string {
	if delivery {
		return deliverySettlementClaimSQLGeili
	}
	return pendingSettlementClaimSQLGeili
}

func settlementClaimRowsGeili(delivery bool) *sqlmock.Rows {
	if delivery {
		return sqlmock.NewRows([]string{"id"})
	}
	return sqlmock.NewRows([]string{"id", "command"})
}

func pollPreparedSettlementGeili(ctx context.Context, repo *usageBillingRepository, delivery bool, limit int) error {
	if delivery {
		_, err := repo.DeliverSettledUsage(ctx, limit)
		return err
	}
	_, err := repo.ProcessPendingSettlements(ctx, limit)
	return err
}

type settlementClaimTokensGeili struct {
	mu     sync.Mutex
	tokens map[string]bool
}

func (r *settlementClaimTokensGeili) Match(value driver.Value) bool {
	token, ok := value.(string)
	if !ok {
		return false
	}
	if _, err := uuid.Parse(token); err != nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[token] = true
	return true
}

func TestUsageSettlementPreparedGeiliConstructorPreparesEachPlanLazilyAndExecutesFreshArguments(t *testing.T) {
	repo, _, mock, _ := preparedSettlementRepositoryGeili(t)
	require.NotNil(t, repo.claimsGeili)
	require.Nil(t, repo.claimsGeili.pending.stmt)
	require.Nil(t, repo.claimsGeili.delivery.stmt)
	tokens := &settlementClaimTokensGeili{tokens: make(map[string]bool)}
	pending := mock.ExpectPrepare(pendingSettlementClaimSQLGeili).WillBeClosed()
	pending.ExpectQuery().WithArgs(int64(1), tokens, settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(false)).RowsWillBeClosed()
	pending.ExpectQuery().WithArgs(int64(4), tokens, settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(false)).RowsWillBeClosed()
	delivery := mock.ExpectPrepare(deliverySettlementClaimSQLGeili).WillBeClosed()
	delivery.ExpectQuery().WithArgs(int64(2), tokens, settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(true)).RowsWillBeClosed()
	delivery.ExpectQuery().WithArgs(int64(4), tokens, settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(true)).RowsWillBeClosed()
	require.NoError(t, pollPreparedSettlementGeili(context.Background(), repo, false, 1))
	require.Nil(t, repo.claimsGeili.delivery.stmt, "a pending poll must not eagerly prepare delivery")
	require.NoError(t, pollPreparedSettlementGeili(context.Background(), repo, false, 0))
	require.NoError(t, pollPreparedSettlementGeili(context.Background(), repo, true, 2))
	require.NoError(t, pollPreparedSettlementGeili(context.Background(), repo, true, 128))
	require.Len(t, tokens.tokens, 4, "every poll must mint a new lease token")
}

func TestUsageSettlementPreparedGeiliPlanNeverCachesClaimRows(t *testing.T) {
	for _, delivery := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "delivery"}[delivery], func(t *testing.T) {
			repo, _, mock, _ := preparedSettlementRepositoryGeili(t)
			prepare := mock.ExpectPrepare(settlementClaimQueryGeili(delivery)).WillBeClosed()
			for i := int64(1); i <= 2; i++ {
				rows := settlementClaimRowsGeili(delivery)
				if delivery {
					rows.AddRow(i)
				} else {
					rows.AddRow(i, []byte("{}"))
				}
				prepare.ExpectQuery().WithArgs(i, "token-"+string(rune('a'+i)), float64(120)).WillReturnRows(rows).RowsWillBeClosed()
			}
			for i := int64(1); i <= 2; i++ {
				rows, err := repo.querySettlementClaimGeili(context.Background(), delivery, i, "token-"+string(rune('a'+i)), float64(120))
				require.NoError(t, err)
				require.True(t, rows.Next())
				var id int64
				if delivery {
					require.NoError(t, rows.Scan(&id))
				} else {
					var command []byte
					require.NoError(t, rows.Scan(&id, &command))
				}
				require.Equal(t, i, id)
				require.False(t, rows.Next())
				require.NoError(t, rows.Err())
				require.NoError(t, rows.Close())
			}
		})
	}
}

func TestUsageSettlementPreparedGeiliFailedPrepareRetriesOnNextPoll(t *testing.T) {
	for _, delivery := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "delivery"}[delivery], func(t *testing.T) {
			repo, _, mock, _ := preparedSettlementRepositoryGeili(t)
			failed := errors.New("prepare unavailable")
			query := settlementClaimQueryGeili(delivery)
			mock.ExpectPrepare(query).WillReturnError(failed)
			require.ErrorIs(t, pollPreparedSettlementGeili(context.Background(), repo, delivery, 4), failed)
			mock.ExpectPrepare(query).WillBeClosed().ExpectQuery().WithArgs(int64(4), sqlmock.AnyArg(), settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(delivery)).RowsWillBeClosed()
			require.NoError(t, pollPreparedSettlementGeili(context.Background(), repo, delivery, 4))
		})
	}
}

func TestUsageSettlementPreparedGeiliQueryErrorDoesNotFallbackOrPoisonPlan(t *testing.T) {
	for _, delivery := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "delivery"}[delivery], func(t *testing.T) {
			repo, db, mock, _ := preparedSettlementRepositoryGeili(t)
			query := settlementClaimQueryGeili(delivery)
			failed := errors.New("claim outcome unavailable")
			prepare := mock.ExpectPrepare(query).WillBeClosed()
			prepare.ExpectQuery().WithArgs(int64(4), sqlmock.AnyArg(), settlementLease.Seconds()).WillReturnError(failed)
			// This trap must remain unmet after the failed poll, even if an
			// incorrect fallback were to discard its error and return failed.
			fallback := errors.New("forbidden second claim")
			mock.ExpectQuery(query).WillReturnError(fallback)
			require.ErrorIs(t, pollPreparedSettlementGeili(context.Background(), repo, delivery, 4), failed)
			require.Error(t, mock.ExpectationsWereMet(), "the fallback trap must not have executed")
			// Consume only the test trap so normal expectation cleanup remains strict.
			_, err := db.QueryContext(context.Background(), query)
			require.ErrorIs(t, err, fallback)
			prepare.ExpectQuery().WithArgs(int64(4), sqlmock.AnyArg(), settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(delivery)).RowsWillBeClosed()
			require.NoError(t, pollPreparedSettlementGeili(context.Background(), repo, delivery, 4))
		})
	}
}

func TestUsageSettlementPreparedGeiliScanAndRowsErrorsCloseBeforeFinancialWork(t *testing.T) {
	for _, delivery := range []bool{false, true} {
		for _, scanFailure := range []bool{false, true} {
			t.Run(map[bool]string{false: "pending", true: "delivery"}[delivery]+map[bool]string{false: "/rows", true: "/scan"}[scanFailure], func(t *testing.T) {
				repo, _, mock, _ := preparedSettlementRepositoryGeili(t)
				rows := settlementClaimRowsGeili(delivery)
				var id any = int64(1)
				if scanFailure {
					id = "invalid-id"
				}
				if delivery {
					rows.AddRow(id)
				} else {
					rows.AddRow(id, []byte("{}"))
				}
				failed := errors.New("claim rows interrupted")
				if !scanFailure {
					rows.RowError(0, failed)
				}
				mock.ExpectPrepare(settlementClaimQueryGeili(delivery)).WillBeClosed().ExpectQuery().WithArgs(int64(4), sqlmock.AnyArg(), settlementLease.Seconds()).WillReturnRows(rows).RowsWillBeClosed()
				err := pollPreparedSettlementGeili(context.Background(), repo, delivery, 4)
				require.Error(t, err)
				if !scanFailure {
					require.ErrorIs(t, err, failed)
				}
				// No Begin/Exec is expected: unusable claim rows cannot debit money
				// or release a possibly owned lease with an invented outcome.
			})
		}
	}
}

func TestUsageSettlementPreparedGeiliCancelledContextDoesNotPrepareOrWaitForAnotherPrepare(t *testing.T) {
	for _, delivery := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "delivery"}[delivery], func(t *testing.T) {
			repo, _, mock, _ := preparedSettlementRepositoryGeili(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			require.ErrorIs(t, pollPreparedSettlementGeili(ctx, repo, delivery, 4), context.Canceled)
			slot := &repo.claimsGeili.pending
			if delivery {
				slot = &repo.claimsGeili.delivery
			}
			require.Nil(t, slot.stmt)
			slot.preparing = make(chan struct{})
			waiter, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer stop()
			require.ErrorIs(t, pollPreparedSettlementGeili(waiter, repo, delivery, 4), context.DeadlineExceeded)
			close(slot.preparing)
			slot.preparing = nil
			mock.ExpectPrepare(settlementClaimQueryGeili(delivery)).WillBeClosed().ExpectQuery().WithArgs(int64(4), sqlmock.AnyArg(), settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(delivery)).RowsWillBeClosed()
			require.NoError(t, pollPreparedSettlementGeili(context.Background(), repo, delivery, 4))
		})
	}
}

func TestUsageSettlementPreparedGeiliConcurrentPollsShareSinglePrepare(t *testing.T) {
	for _, delivery := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "delivery"}[delivery], func(t *testing.T) {
			repo, _, mock, _ := preparedSettlementRepositoryGeili(t)
			mock.MatchExpectationsInOrder(false)
			prepare := mock.ExpectPrepare(settlementClaimQueryGeili(delivery)).WillDelayFor(15 * time.Millisecond).WillBeClosed()
			const workers = 24
			for i := 0; i < workers; i++ {
				prepare.ExpectQuery().WithArgs(int64(4), sqlmock.AnyArg(), settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(delivery)).RowsWillBeClosed()
			}
			start := make(chan struct{})
			errs := make(chan error, workers)
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					errs <- pollPreparedSettlementGeili(context.Background(), repo, delivery, 4)
				}()
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
		})
	}
}

func TestUsageSettlementPreparedGeiliDatabaseCloseClosesBothDriverPlans(t *testing.T) {
	repo, _, mock, closeDB := preparedSettlementRepositoryGeili(t)
	for _, delivery := range []bool{false, true} {
		mock.ExpectPrepare(settlementClaimQueryGeili(delivery)).WillBeClosed().ExpectQuery().WithArgs(int64(4), sqlmock.AnyArg(), settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(delivery)).RowsWillBeClosed()
		require.NoError(t, pollPreparedSettlementGeili(context.Background(), repo, delivery, 4))
	}
	closeDB()
	require.NoError(t, mock.ExpectationsWereMet())
	require.Error(t, pollPreparedSettlementGeili(context.Background(), repo, false, 4))
	require.Error(t, pollPreparedSettlementGeili(context.Background(), repo, true, 4))
}

func TestUsageSettlementPreparedGeiliExplicitRepositoryRetainsRawClaimContract(t *testing.T) {
	repo, db, mock, _ := preparedSettlementRepositoryGeili(t)
	explicit := &usageBillingRepository{db: db}
	for _, delivery := range []bool{false, true} {
		mock.ExpectQuery(settlementClaimQueryGeili(delivery)).WithArgs(int64(4), sqlmock.AnyArg(), settlementLease.Seconds()).WillReturnRows(settlementClaimRowsGeili(delivery)).RowsWillBeClosed()
		require.NoError(t, pollPreparedSettlementGeili(context.Background(), explicit, delivery, 4))
	}
	require.Nil(t, repo.claimsGeili.pending.stmt)
	require.Nil(t, repo.claimsGeili.delivery.stmt)
}
