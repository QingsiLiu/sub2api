//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFinancialProjectionReceiptAuthorityRecoveryAndMidnight(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	// Other integration tests legitimately leave committed fixtures in the same
	// database. Compare only this transaction's new money against its baseline.
	summaryAt := time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing)
	baselineSummary, err := repo.GetFinancialGroupSummary(ctx, summaryAt)
	require.NoError(t, err)
	var baselineTotal float64
	for _, row := range baselineSummary {
		baselineTotal += row.TotalCost
	}
	user := mustCreateUser(t, client, &service.User{Email: "fin-" + uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-fin-" + uuid.NewString(), Name: "financial"})
	account := mustCreateAccount(t, client, &service.Account{Name: "financial"})
	completed := time.Date(2026, 9, 26, 0, 1, 0, 0, financialBeijing)
	// An early zero-cost placeholder must not hide the settled amount.
	log := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: uuid.NewString(), Model: "text", ActualCost: 0, CreatedAt: completed}
	_, err = repo.Create(ctx, log)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_settlement_receipts(request_id,usage_request_id,request_fingerprint,user_id,api_key_id,account_id,billing_type,charged_amount,accounting_date,completed_at,settled_at,state,record_source,record_completeness,detail) VALUES($1,$2,'fp',$3,$4,$5,1,10.84225728,'2026-09-25',$6,$6,'settled','live','complete','{"input_tokens":10,"output_tokens":20,"cache_creation_tokens":0,"cache_read_tokens":0,"total_cost":10.84225728}'::jsonb)`, uuid.NewString(), log.RequestID, user.ID, key.ID, account.ID, completed)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_settlement_receipts(request_id,request_fingerprint,user_id,api_key_id,billing_type,charged_amount,accounting_date,settled_at,state,record_source,record_completeness) VALUES($1,'historical',$2,$3,1,35.94076656,'2026-09-25',$4,'settled','historical_recovery','partial'),($5,'unknown',$2,$3,0,NULL,'2026-09-25',$4,'settled','historical_recovery','amount_unknown')`, uuid.NewString(), user.ID, key.ID, completed, uuid.NewString())
	require.NoError(t, err)
	// Failed async tasks retain audit evidence but are not usage records.
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_settlement_receipts(request_id,request_fingerprint,user_id,api_key_id,billing_type,charged_amount,accounting_date,settled_at,state,record_source,record_completeness,command,delivered_at) VALUES($1,'failure',$2,$3,0,0,'2026-09-25',$4,'settled','live','partial','{"TerminalFailure":true}'::jsonb,$4)`, uuid.NewString(), user.ID, key.ID, completed)
	require.NoError(t, err)
	start := time.Date(2026, 9, 25, 0, 0, 0, 0, financialBeijing)
	end := start.AddDate(0, 0, 1)
	filters := usagestats.UsageLogFilters{UserID: user.ID, StartTime: &start, EndTime: &end}
	stats, err := repo.GetFinancialUsageStats(ctx, filters)
	require.NoError(t, err)
	require.Equal(t, int64(3), stats.TotalRequests)
	require.InDelta(t, 46.78302384, stats.TotalActualCost, 1e-9)
	require.Equal(t, int64(1), stats.UnknownAmountCount)
	require.Equal(t, int64(1), stats.DetailPendingCount)
	require.False(t, stats.StandardCostComplete)
	require.False(t, stats.TokenCountsComplete)
	records, page, err := repo.ListFinancialUsage(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, filters)
	require.NoError(t, err)
	require.Equal(t, int64(3), int64(page.Total))
	require.Len(t, records, 3)
	var linked int
	for _, record := range records {
		if record.ID == log.ID {
			linked++
			require.InDelta(t, 10.84225728, record.ActualCost, 1e-9)
		} else {
			require.Negative(t, record.ID)
			require.Contains(t, record.Financial.UnknownFields, "model")
			require.Nil(t, record.Financial.CompletedAt)
		}
	}
	require.Equal(t, 1, linked)
	filters.DateBasis = "completed"
	stats, err = repo.GetFinancialUsageStats(ctx, filters)
	require.NoError(t, err)
	require.Zero(t, stats.TotalRequests)
	filters.StartTime = &end
	next := end.AddDate(0, 0, 1)
	filters.EndTime = &next
	stats, err = repo.GetFinancialUsageStats(ctx, filters)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.TotalRequests)
	require.InDelta(t, 10.84225728, stats.TotalActualCost, 1e-9)
	// Every reporting surface must include recovery and must remain read-only.
	filters.DateBasis = "accounting"
	filters.StartTime = &start
	filters.EndTime = &end
	trend, e := repo.GetFinancialTrend(ctx, start, end, "day", filters)
	require.NoError(t, e)
	require.Len(t, trend, 1)
	require.InDelta(t, 46.78302384, trend[0].ActualCost, 1e-9)
	require.False(t, trend[0].TokenCountsComplete)
	models, e := repo.GetFinancialModels(ctx, start, end, filters, usagestats.ModelSourceRequested)
	require.NoError(t, e)
	require.Len(t, models, 2)
	groups, e := repo.GetFinancialGroups(ctx, start, end, filters)
	require.NoError(t, e)
	require.Len(t, groups, 1)
	batch, e := repo.GetFinancialBatchAPIKeyStats(ctx, []int64{key.ID}, start, end)
	require.NoError(t, e)
	require.InDelta(t, 46.78302384, batch[key.ID].TotalActualCost, 1e-9)
	users, e := repo.GetFinancialBatchUserStats(ctx, []int64{user.ID}, start, end)
	require.NoError(t, e)
	require.InDelta(t, 46.78302384, users[user.ID].TotalActualCost, 1e-9)
	_, e = repo.GetFinancialUserRanking(ctx, start, end, 100)
	require.NoError(t, e)
	_, e = repo.GetFinancialUserTrend(ctx, start, end, "day", 100)
	require.NoError(t, e)
	_, e = repo.GetFinancialKeyTrend(ctx, start, end, "day", 100)
	require.NoError(t, e)
	_, e = repo.GetFinancialUserBreakdown(ctx, start, end, usagestats.UserBreakdownDimension{UserID: user.ID}, 100)
	require.NoError(t, e)
	groupSummary, e := repo.GetFinancialGroupSummary(ctx, end)
	require.NoError(t, e)
	var groupTotal float64
	for _, row := range groupSummary {
		groupTotal += row.TotalCost
	}
	require.InDelta(t, 46.78302384, groupTotal-baselineTotal, 1e-9)
	dash, e := repo.GetFinancialDashboardStats(ctx, user.ID, 0)
	require.NoError(t, e)
	require.InDelta(t, 46.78302384, dash.TotalActualCost, 1e-9)
	dash, e = repo.GetFinancialDashboardStatsWithBasis(ctx, user.ID, 0, "completed", "America/Los_Angeles")
	require.NoError(t, e)
	require.Equal(t, "completed", dash.DateBasis)
	_, e = repo.GetFinancialAdminDashboardStats(ctx)
	require.NoError(t, e)
	// The derived accounting date must not force a scan over all historical
	// usage rows: explicit branch timestamp bounds use the existing indexes.
	_, e = tx.ExecContext(ctx, "SET LOCAL enable_seqscan=off")
	require.NoError(t, e)
	for _, scope := range []usagestats.UsageLogFilters{{UserID: user.ID, StartTime: &start, EndTime: &end}, {StartTime: &start, EndTime: &end}} {
		where, args := financialUsageWhere(scope)
		var plan string
		require.NoError(t, scanSingleRow(ctx, tx, "EXPLAIN (FORMAT JSON) SELECT SUM(actual_cost) FROM usage_financial_records "+where, args, &plan))
		var nodes []map[string]any
		require.NoError(t, json.Unmarshal([]byte(plan), &nodes))
		assertFinancialPlanIndexed(t, nodes[0]["Plan"].(map[string]any), scope.UserID > 0)
	}
	var rawCount int64
	require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM usage_logs WHERE user_id=$1", []any{user.ID}, &rawCount))
	require.Equal(t, int64(1), rawCount)
	var balance float64
	require.NoError(t, scanSingleRow(ctx, tx, "SELECT balance FROM users WHERE id=$1", []any{user.ID}, &balance))
	require.Equal(t, user.Balance, balance)
}

func assertFinancialPlanIndexed(t *testing.T, node map[string]any, userScoped bool) {
	t.Helper()
	if node["Relation Name"] == "usage_logs" {
		require.NotEqual(t, "Seq Scan", node["Node Type"], node)
		if userScoped {
			encoded, _ := json.Marshal(node)
			require.True(t, strings.Contains(string(encoded), "user_id") || strings.Contains(string(encoded), "request_id"), string(encoded))
		}
	}
	if children, ok := node["Plans"].([]any); ok {
		for _, child := range children {
			assertFinancialPlanIndexed(t, child.(map[string]any), userScoped)
		}
	}
}
