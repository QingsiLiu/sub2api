package trainingdata

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUpsertCaptureIndexUsesCurrentSchemaArgumentCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	arguments := make([]driver.Value, 29)
	for index := range arguments {
		arguments[index] = sqlmock.AnyArg()
	}
	mock.ExpectExec("INSERT INTO training_captures").WithArgs(arguments...).WillReturnResult(sqlmock.NewResult(0, 1))
	now := time.Now().UTC()
	err = upsertCaptureIndex(context.Background(), db, CaptureIndex{
		CaptureID: "00000000-0000-0000-0000-000000000001", UserSubjectRef: "user-ref",
		APIKeySubjectRef: "key-ref", StartedAt: now, FinishedAt: now,
		CaptureStatus: "complete", RawObjectPrefix: "captures/one", RawManifestKey: "captures/one/manifest.json",
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
