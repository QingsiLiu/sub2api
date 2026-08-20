package trainingdata

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUpsertRightsRejectsIncompleteEligibleGrantBeforeWriting(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	_, err = UpsertRights(context.Background(), db, RightsUpsertInput{
		ScopeType: "user", ScopeRef: validTestSubjectRef(), Status: RightsEligible,
		AllowedPurposes: []string{"model_training"}, AllowedDatasetTypes: []string{"chat"},
		AllowedRecipients: []string{"buyer-a"}, EffectiveAt: time.Now().UTC(),
	}, "operator", "test")
	require.ErrorContains(t, err, "consent or contract")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertRightsRejectsInvalidEvidenceDigestBeforeWriting(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	_, err = UpsertRights(context.Background(), db, RightsUpsertInput{
		ScopeType: "user", ScopeRef: validTestSubjectRef(), Status: RightsUnknown,
		EffectiveAt: time.Now().UTC(), EvidenceSHA256: "not-a-digest",
	}, "operator", "test")
	require.ErrorContains(t, err, "evidence_sha256")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateDeletionRequestCommitsRequestAndTargetsTogether(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	deletionID := "00000000-0000-0000-0000-000000000001"
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO training_deletion_requests")).
		WillReturnRows(sqlmock.NewRows([]string{"deletion_id"}).AddRow(deletionID))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO training_deletion_targets")).
		WillReturnResult(sqlmock.NewResult(0, 6))
	mock.ExpectCommit()

	got, err := CreateDeletionRequest(context.Background(), db, "user", validTestSubjectRef(), "test", "erase", "delete-1")
	require.NoError(t, err)
	require.Equal(t, deletionID, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateDeletionRequestRejectsIdempotencyKeyFromAnotherScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO training_deletion_requests")).
		WillReturnRows(sqlmock.NewRows([]string{"deletion_id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT deletion_id::text, scope_type, scope_ref")).
		WillReturnRows(sqlmock.NewRows([]string{"deletion_id", "scope_type", "scope_ref"}).
			AddRow("00000000-0000-0000-0000-000000000002", "api_key", validTestSubjectRef()))
	mock.ExpectRollback()

	_, err = CreateDeletionRequest(context.Background(), db, "user", validTestSubjectRef(), "test", "erase", "shared-key")
	require.ErrorContains(t, err, "different deletion scope")
	require.NoError(t, mock.ExpectationsWereMet())
}

func validTestSubjectRef() string {
	return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
