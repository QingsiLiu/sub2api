//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestResponseAuditPersistenceBillingIsolationAndCorrelation(t *testing.T) {
	ctx := context.Background()
	billing, cmd, log := settlementFixture(t)
	require.NoError(t, billing.PrepareSettlement(ctx, cmd, log))
	_, err := billing.ProcessPendingSettlements(ctx, 128)
	require.NoError(t, err)
	_, err = billing.DeliverSettledUsage(ctx, 128)
	require.NoError(t, err)
	snapshot := func() []string {
		var balance, quota, extra string
		require.NoError(t, integrationDB.QueryRow(`SELECT u.balance::text,k.quota_used::text,COALESCE(a.extra::text,'null') FROM users u JOIN api_keys k ON k.user_id=u.id JOIN accounts a ON a.id=$3 WHERE u.id=$1 AND k.id=$2`, cmd.UserID, cmd.APIKeyID, cmd.AccountID).Scan(&balance, &quota, &extra))
		return []string{balance, quota, extra}
	}
	before := snapshot()
	repo := &responseAuditRepository{db: integrationDB}
	now := time.Now().UTC()
	a := service.ResponseAudit{AuditRequestID: uuid.NewString(), UserID: cmd.UserID, APIKeyID: cmd.APIKeyID, AccountID: cmd.AccountID, Model: "test-model", Endpoint: "/v1/messages", Protocol: "messages", RequestID: "trace-test", UsageRequestID: cmd.RequestID, Status: "empty", Reason: "completed_without_output", StartedAt: now.Add(-time.Second), FinishedAt: now, Terminal: "message_stop", TerminalWritten: true, UsagePresent: true}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM gateway_response_audits WHERE user_id=$1", cmd.UserID)
	})
	require.NoError(t, repo.Save(ctx, &a))
	require.NoError(t, repo.Save(ctx, &a))
	require.Equal(t, before, snapshot())
	f := service.ResponseAuditFilter{From: now.Add(-time.Minute), To: now.Add(time.Minute), UserID: cmd.UserID, Page: 1, PageSize: 50}
	rows, n, err := repo.List(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.Len(t, rows, 1)
	require.Equal(t, "settled", rows[0].SettlementState)
	require.NotNil(t, rows[0].ChargedAmount)
	require.Contains(t, *rows[0].ChargedAmount, "1.25")
	got, err := repo.Get(ctx, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "empty", got.Status)
	stats, err := repo.Stats(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.Total)
	require.EqualValues(t, 1, stats.Counts["empty"])
	require.EqualValues(t, 1, stats.EmptyChargedReceipts)
	key := service.ResponseAuditUsageKey{APIKeyID: cmd.APIKeyID, RequestID: log.RequestID}
	links, err := repo.Lookup(ctx, []service.ResponseAuditUsageKey{key})
	require.NoError(t, err)
	require.NotNil(t, links[key])
	// Same financial identity can be reused by a second physical HTTP request.
	// It must never overwrite the first observation or label the bill unambiguously.
	a.AuditRequestID = uuid.NewString()
	a.Status = "success"
	a.TextWritten = true
	require.NoError(t, repo.Save(ctx, &a))
	links, err = repo.Lookup(ctx, []service.ResponseAuditUsageKey{key})
	require.NoError(t, err)
	require.Nil(t, links[key])
	stats, err = repo.Stats(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 2, stats.Total)
	require.EqualValues(t, 1, stats.EmptyChargedReceipts)
	require.Equal(t, before, snapshot())
	a.AuditRequestID = uuid.NewString()
	a.UsageRequestID = ""
	a.Status = "failed"
	a.FinishedAt = now.AddDate(0, 0, -31)
	a.StartedAt = a.FinishedAt.Add(-time.Second)
	require.NoError(t, repo.Save(ctx, &a))
	require.NoError(t, repo.Cleanup(ctx, now.AddDate(0, 0, -30)))
	rows, n, err = repo.List(ctx, f)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	require.Len(t, rows, 2)
	require.Equal(t, before, snapshot())
}
