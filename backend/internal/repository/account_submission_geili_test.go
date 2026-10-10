package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"regexp"
	"testing"
	"time"
)

func submissionMockRow(t *testing.T, platform string, submitted bool) *sqlmock.Rows {
	t.Helper()
	config, err := json.Marshal(service.AccountSubmissionConfig{Name: "submission-fixture", Platform: platform, Concurrency: 1, Priority: 50, RateMultiplier: 0})
	require.NoError(t, err)
	var submittedAt, accountID any
	if submitted {
		submittedAt = time.Now()
		accountID = int64(9)
	}
	return sqlmock.NewRows([]string{"id", "created_by", "config", "created_at", "expires_at", "revoked_at", "submitted_at", "account_id"}).AddRow(1, 1, config, time.Now(), time.Now().Add(time.Hour), nil, submittedAt, accountID)
}

func TestAccountSubmissionRollbackSanitizesCredentialWriteFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FROM account_submission_invites_geili WHERE token_hash=\$1 FOR UPDATE`).WithArgs("hash").WillReturnRows(submissionMockRow(t, service.PlatformOpenAI, false))
	mock.ExpectQuery(`SELECT revoked_at IS NULL`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"usable"}).AddRow(true))
	mock.ExpectQuery(`INSERT INTO accounts`).WillReturnError(errors.New("database detail includes synthetic-private-key"))
	mock.ExpectRollback()
	err = NewAccountSubmissionRepository(db).SubmitInvite(context.Background(), "hash", "synthetic-private-key")
	require.ErrorIs(t, err, service.ErrSubmissionUnavailable)
	require.NotContains(t, err.Error(), "synthetic-private-key")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountSubmissionOutboxFailureRollsBackAllWrites(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FOR UPDATE`).WillReturnRows(submissionMockRow(t, service.PlatformAnthropic, false))
	mock.ExpectQuery(`SELECT revoked_at IS NULL`).WillReturnRows(sqlmock.NewRows([]string{"usable"}).AddRow(true))
	mock.ExpectQuery(`(?s)INSERT INTO accounts .*'inactive',false,true`).WithArgs("submission-fixture", service.PlatformAnthropic, `{"api_key":"synthetic-private-key","base_url":"https://api.anthropic.com"}`, `{}`, nil, 1, 50, 0.0).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectExec(`UPDATE account_submission_invites_geili`).WithArgs(int64(9), int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WillReturnError(errors.New("outbox unavailable"))
	mock.ExpectRollback()
	require.ErrorIs(t, NewAccountSubmissionRepository(db).SubmitInvite(context.Background(), "hash", "synthetic-private-key"), service.ErrSubmissionUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountSubmissionCommittedRetryDoesNotWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FOR UPDATE`).WillReturnRows(submissionMockRow(t, service.PlatformAnthropic, true))
	mock.ExpectRollback()
	require.NoError(t, NewAccountSubmissionRepository(db).SubmitInvite(context.Background(), "hash", "replacement-must-not-persist"))
	require.NoError(t, mock.ExpectationsWereMet())
}
