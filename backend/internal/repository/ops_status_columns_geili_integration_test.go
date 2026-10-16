//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsStatusColumnsGeiliRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := NewOpsRepository(integrationDB).(*opsRepository)
	status := func(v int) *int { return &v }
	for _, tc := range []struct {
		name     string
		client   *int
		upstream *int
		legacy   int
	}{
		{name: "capacity_failure", client: status(503), upstream: status(400), legacy: 400},
		{name: "recovered", client: status(200), upstream: status(403), legacy: 403},
		{name: "local_failure", client: status(400), legacy: 400},
		{name: "credential_failure", client: status(503), upstream: status(0), legacy: 0},
		{name: "unknown_client", upstream: status(502), legacy: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestID := fmt.Sprintf("ops-status-geili-%s-%d", tc.name, time.Now().UnixNano())
			var id int64
			err := integrationDB.QueryRowContext(ctx, `INSERT INTO ops_error_logs
				(error_phase, error_type, severity, status_code, upstream_status_code, request_id, created_at)
				VALUES ('upstream', 'upstream_error', 'P2', $1, $2, $3, NOW()) RETURNING id`,
				tc.client, tc.upstream, requestID).Scan(&id)
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_error_logs WHERE id=$1", id) })

			detail, err := repo.GetErrorLogByID(ctx, id)
			require.NoError(t, err)
			require.Equal(t, tc.legacy, detail.StatusCode)
			require.Equal(t, tc.client, detail.ClientStatusCode)
			require.Equal(t, tc.upstream, detail.UpstreamStatusCode)

			list, err := repo.ListErrorLogs(ctx, &service.OpsErrorLogFilter{
				RequestID: requestID, View: "all", Phase: "upstream", IncludeRecoveredUpstream: true,
			})
			require.NoError(t, err)
			require.Len(t, list.Errors, 1)
			require.Equal(t, tc.legacy, list.Errors[0].StatusCode)
			require.Equal(t, tc.client, list.Errors[0].ClientStatusCode)
			require.Equal(t, tc.upstream, list.Errors[0].UpstreamStatusCode)
		})
	}
}
