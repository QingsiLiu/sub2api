package repository

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAccountSubmissionUsageFailureIsSanitized(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL statement_timeout").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT a.id,a.name").WillReturnError(errors.New("synthetic-private-key and token"))
	mock.ExpectRollback()
	_, err = NewAccountSubmissionRepository(db).(service.AccountSubmissionUsageRepository).SubmissionUsage(context.Background(), "hash", time.Now(), time.Now())
	require.ErrorIs(t, err, service.ErrSubmissionUnavailable)
	require.NotContains(t, err.Error(), "synthetic-private-key")
	require.NoError(t, mock.ExpectationsWereMet())
}
