package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestFinancialMigrationRepairsInterruptedConcurrentIndexes(t *testing.T) {
	db, mock := newSQLMock(t)
	for i, indexName := range financialUsageMigrationIndexes {
		mock.ExpectQuery("SELECT EXISTS").WithArgs(indexName).WillReturnRows(sqlmock.NewRows([]string{"invalid"}).AddRow(i == 1))
		if i == 1 {
			mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS " + indexName).WillReturnResult(sqlmock.NewResult(0, 0))
		}
	}
	require.NoError(t, prepareNonTransactionalMigration(context.Background(), db, financialUsageIndexesMigration))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFinancialMigrationPostValidationRejectsInvalidOrMissing(t *testing.T) {
	for _, badIndex := range []int{0, 2, 4} {
		t.Run(financialUsageMigrationIndexes[badIndex], func(t *testing.T) {
			db, mock := newSQLMock(t)
			for i, indexName := range financialUsageMigrationIndexes {
				mock.ExpectQuery("SELECT EXISTS").WithArgs(indexName).WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(i != badIndex))
				if i == badIndex {
					break
				}
			}
			err := validateFinancialMigrationIndexes(context.Background(), db, financialUsageIndexesMigration)
			require.ErrorContains(t, err, "missing or invalid")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestFinancialMigrationNeverMarksInvalidIndexApplied(t *testing.T) {
	db, mock := newSQLMock(t)
	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").WithArgs(financialUsageIndexesMigration).WillReturnError(sql.ErrNoRows)
	for _, indexName := range financialUsageMigrationIndexes {
		mock.ExpectQuery("SELECT EXISTS").WithArgs(indexName).WillReturnRows(sqlmock.NewRows([]string{"invalid"}).AddRow(false))
	}
	mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS fixture ON t\\(id\\)").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT EXISTS").WithArgs(financialUsageMigrationIndexes[0]).WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(false))
	mock.ExpectExec("SELECT pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 1))
	err := applyMigrationsFS(context.Background(), db, fstest.MapFS{financialUsageIndexesMigration: &fstest.MapFile{Data: []byte("CREATE INDEX CONCURRENTLY IF NOT EXISTS fixture ON t(id);")}})
	require.ErrorContains(t, err, "missing or invalid")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFinancialTriggerMigrationUsesShortLockDeadlineAndRollback(t *testing.T) {
	db, mock := newSQLMock(t)
	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").WithArgs(financialRollupTriggerMigration).WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL lock_timeout = '5s'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT 1").WillReturnError(errors.New("canceling statement due to lock timeout"))
	mock.ExpectRollback()
	mock.ExpectExec("SELECT pg_advisory_unlock").WillReturnResult(sqlmock.NewResult(0, 1))
	err := applyMigrationsFS(context.Background(), db, fstest.MapFS{financialRollupTriggerMigration: &fstest.MapFile{Data: []byte("SELECT 1;")}})
	require.ErrorContains(t, err, "lock timeout")
	require.NoError(t, mock.ExpectationsWereMet())
}
