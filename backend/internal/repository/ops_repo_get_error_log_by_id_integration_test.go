//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGetErrorLogByID_APIKeyPrefixAndUpstreamStatus(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE ops_error_logs RESTART IDENTITY CASCADE")
	repo := NewOpsRepository(integrationDB).(*opsRepository)

	var plainID int64
	err := integrationDB.QueryRowContext(ctx, `
		INSERT INTO ops_error_logs (
			error_phase, error_type, severity, status_code, created_at
		) VALUES (
			'upstream', 'upstream_error', 'error', 500, NOW()
		) RETURNING id`,
	).Scan(&plainID)
	require.NoError(t, err)

	plain, err := repo.GetErrorLogByID(ctx, plainID)
	require.NoError(t, err)
	require.Empty(t, plain.APIKeyPrefix)

	validID, err := repo.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{
		ErrorPhase:   "request",
		ErrorType:    "api_error",
		Severity:     "error",
		StatusCode:   402,
		CreatedAt:    time.Now(),
		APIKeyPrefix: "sk-valid",
	})
	require.NoError(t, err)

	valid, err := repo.GetErrorLogByID(ctx, validID)
	require.NoError(t, err)
	require.Equal(t, "sk-valid", valid.APIKeyPrefix)

	zero := 0
	credentialFailureID, err := repo.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{
		ErrorPhase:         "account_auth",
		ErrorType:          "upstream_error",
		Severity:           "error",
		StatusCode:         503,
		UpstreamStatusCode: &zero,
		CreatedAt:          time.Now(),
	})
	require.NoError(t, err)

	credentialFailure, err := repo.GetErrorLogByID(ctx, credentialFailureID)
	require.NoError(t, err)
	require.NotNil(t, credentialFailure.UpstreamStatusCode)
	require.Zero(t, *credentialFailure.UpstreamStatusCode)
}

func TestOpsCompositeGroupSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := NewOpsRepository(integrationDB).(*opsRepository)
	id, err := repo.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{RequestID: "composite-groups-roundtrip", RequestedGroupIDs: []int64{22, 11}, Model: "gpt-unavailable", RequestedModel: "gpt-unavailable", ErrorPhase: "routing", ErrorType: "invalid_request_error", Severity: "P3", StatusCode: 400, CreatedAt: time.Now()})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_error_logs WHERE id=$1", id) })
	detail, err := repo.GetErrorLogByID(ctx, id)
	require.NoError(t, err)
	require.Nil(t, detail.GroupID)
	require.Equal(t, []int64{22, 11}, detail.RequestedGroupIDs)
	require.Equal(t, "gpt-unavailable", detail.RequestedModel)
	list, err := repo.ListErrorLogs(ctx, &service.OpsErrorLogFilter{RequestID: "composite-groups-roundtrip", View: "all"})
	require.NoError(t, err)
	require.Len(t, list.Errors, 1)
	require.Equal(t, detail.RequestedGroupIDs, list.Errors[0].RequestedGroupIDs)
}
