package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrainingDataAssetsMigrationKeepsPayloadsAndOperationalIDsOutOfPostgres(t *testing.T) {
	content, err := FS.ReadFile("191_training_data_assets.sql")
	require.NoError(t, err)
	sql := string(content)
	for _, table := range []string{
		"training_rights", "training_rights_events", "training_captures",
		"training_dataset_releases", "training_dataset_samples",
		"training_deletion_requests", "training_deletion_targets", "training_delivery_audits",
	} {
		require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS "+table)
	}
	require.Contains(t, sql, "rights_id UUID NOT NULL REFERENCES training_rights(rights_id)")
	require.NotContains(t, sql, "request_body")
	require.NotContains(t, sql, "response_body")
	require.NotContains(t, sql, "account_id")
	require.NotContains(t, sql, "group_id")
}
