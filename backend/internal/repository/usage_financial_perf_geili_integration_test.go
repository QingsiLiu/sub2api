//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Optional, disposable production-shaped hot-user cardinality check. Unlike the
// index-feasibility test this uses the normal planner and default parallelism.
func TestFinancialHotUserQueryPerformanceGeili(t *testing.T) {
	if os.Getenv("GEILI_FINANCIAL_PERF") != "1" {
		t.Skip("set GEILI_FINANCIAL_PERF=1 for 20000-row EXPLAIN ANALYZE fixture")
	}
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	user := mustCreateUser(t, client, &service.User{Email: "financial-perf-" + uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-fin-perf-" + uuid.NewString(), Name: "financial"})
	account := mustCreateAccount(t, client, &service.Account{Name: "financial"})
	prefix := uuid.NewString()
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,input_tokens,output_tokens,actual_cost,total_cost,created_at) SELECT $1,$2,$3,$4||g,'fixture',10,20,.1,.1,TIMESTAMPTZ '2026-09-26 01:00:00+08'+g*INTERVAL '1 millisecond' FROM generate_series(1,20000) g`, user.ID, key.ID, account.ID, prefix)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_settlement_receipts(request_id,usage_request_id,usage_log_id,request_fingerprint,user_id,api_key_id,account_id,charged_amount,accounting_date,completed_at,settled_at,state,record_source,record_completeness,detail,delivered_at) SELECT request_id,request_id,id,'perf',user_id,api_key_id,account_id,actual_cost,'2026-09-26',created_at,created_at,'settled','live','complete',to_jsonb(u),created_at FROM usage_logs u WHERE u.api_key_id=$1`, key.ID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "ANALYZE usage_logs")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "ANALYZE usage_settlement_receipts")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SET LOCAL statement_timeout='15s'")
	require.NoError(t, err)
	start := time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing)
	end := start.AddDate(0, 0, 1)
	where, args := financialUsageWhere(usagestats.UsageLogFilters{UserID: user.ID, StartTime: &start, EndTime: &end})
	queries := []struct {
		name, query string
		args        []any
	}{
		{"sum", "SELECT COUNT(*),SUM(actual_cost) FROM usage_financial_records", nil},
		{"day", "SELECT " + financialAggregateColumns + " FROM usage_financial_records " + where, args},
		{"list", fmt.Sprintf("SELECT to_jsonb(page) FROM (SELECT * FROM usage_financial_records f %s ORDER BY %s LIMIT 21) page", where, financialOrder(pagination.PaginationParams{SortBy: "created_at"})), args},
	}
	for _, q := range queries {
		var plan string
		begin := time.Now()
		require.NoError(t, scanSingleRow(ctx, tx, "EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+q.query, q.args, &plan))
		t.Logf("FINANCIAL_PERF %s elapsed=%s plan=%s", q.name, time.Since(begin), plan)
		require.NotContains(t, plan, "jsonb_populate_record")
		require.NotContains(t, plan, "Function Scan")
		var planData []struct {
			ExecutionTime float64        `json:"Execution Time"`
			Plan          map[string]any `json:"Plan"`
		}
		require.NoError(t, json.Unmarshal([]byte(plan), &planData))
		require.Len(t, planData, 1)
		require.Less(t, planData[0].ExecutionTime, 3000.0, "20k hot-key report must not regress to quadratic joins or wide-row spills")
		assertFinancialPlanNoSpill(t, planData[0].Plan)

		// The old OR anti-join hashed only user/key and filtered millions of matching
		// hot-user pairs. A complete request identity must appear as an equality.
		if strings.Contains(plan, "Join Filter") {
			require.Contains(t, plan, "request_id")
		}
	}
}

func assertFinancialPlanNoSpill(t *testing.T, node map[string]any) {
	t.Helper()
	if blocks, ok := node["Temp Written Blocks"].(float64); ok {
		require.Zero(t, blocks, "report spilled temporary blocks")
	}
	if removed, ok := node["Rows Removed by Join Filter"].(float64); ok {
		require.Less(t, removed, 100000.0, "hot-user identity join regressed")
	}
	if children, ok := node["Plans"].([]any); ok {
		for _, child := range children {
			assertFinancialPlanNoSpill(t, child.(map[string]any))
		}
	}
}
