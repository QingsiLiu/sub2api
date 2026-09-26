//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func financialRollupTestSchema(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	schema := "financial_rollup_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := integrationDB.ExecContext(ctx, `CREATE SCHEMA `+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := integrationDB.ExecContext(context.Background(), `DROP SCHEMA `+pq.QuoteIdentifier(schema)+` CASCADE`)
		require.NoError(t, e)
	})
	tx := financialRollupTestTx(t, schema)
	_, err = tx.ExecContext(ctx, `CREATE TABLE usage_logs(LIKE public.usage_logs INCLUDING DEFAULTS);
 CREATE UNIQUE INDEX ON usage_logs(id);CREATE INDEX ON usage_logs(created_at);CREATE INDEX ON usage_logs(request_id,api_key_id);
 CREATE TABLE usage_settlement_receipts(LIKE public.usage_settlement_receipts INCLUDING DEFAULTS);
 CREATE UNIQUE INDEX ON usage_settlement_receipts(id);CREATE INDEX ON usage_settlement_receipts(usage_request_id,api_key_id);CREATE INDEX ON usage_settlement_receipts(usage_log_id);`)
	require.NoError(t, err)
	for _, file := range []string{"261_usage_financial_projection.sql", "265_usage_financial_daily_rollups.sql", "266_usage_financial_rollup_time_columns.sql"} {
		body, e := migrations.FS.ReadFile(file)
		require.NoError(t, e)
		_, e = tx.ExecContext(ctx, string(body))
		require.NoError(t, e)
	}
	require.NoError(t, tx.Commit())
	return schema
}
func financialRollupTestTx(t *testing.T, schema string) *sql.Tx {
	t.Helper()
	tx, err := integrationDB.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	_, err = tx.ExecContext(context.Background(), `SET LOCAL search_path=`+pq.QuoteIdentifier(schema)+`,public`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}
func financialRollupExec(t *testing.T, tx *sql.Tx, q string, args ...any) {
	t.Helper()
	_, err := tx.ExecContext(context.Background(), q, args...)
	require.NoError(t, err)
}
func financialRollupSeedLog(t *testing.T, tx *sql.Tx, id int64, day string, cost float64) {
	t.Helper()
	financialRollupExec(t, tx, `INSERT INTO usage_logs(id,user_id,api_key_id,account_id,request_id,model,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,total_cost,actual_cost,duration_ms,created_at) VALUES($1::bigint,1,2,3,'log-'||$1::bigint::text,'fixture',10,20,3,4,$2,$2,100,$3::timestamptz)`, id, cost, day)
}
func financialRollupSeedReceipt(t *testing.T, tx *sql.Tx, id int64, day string, cost float64) {
	t.Helper()
	financialRollupExec(t, tx, `INSERT INTO usage_settlement_receipts(id,request_id,usage_request_id,request_fingerprint,user_id,api_key_id,charged_amount,accounting_date,completed_at,settled_at,state,record_source,record_completeness,detail) VALUES($1::bigint,'finance-'||$1::bigint::text,'log-'||$1::bigint::text,'test',1,2,$2,'2026-09-20',$3::timestamptz,$3::timestamptz,'settled','live','complete','{"input_tokens":7,"output_tokens":8,"cache_creation_tokens":0,"cache_read_tokens":1,"total_cost":0.9,"duration_ms":400}'::jsonb)`, id, cost, day)
}
func financialRollupPublish(t *testing.T, schema string) {
	t.Helper()
	for i := 0; i < 30; i++ {
		tx := financialRollupTestTx(t, schema)
		more, err := newDashboardAggregationRepositoryWithSQL(tx).syncFinancialRollupStep(context.Background(), time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing))
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
		if !more {
			return
		}
	}
	t.Fatal("financial backfill failed to converge")
}
func financialRollupAssertOracle(t *testing.T, tx *sql.Tx) *usagestats.UsageStats {
	t.Helper()
	repo := &usageLogRepository{sql: tx}
	want, err := repo.financialTotals(context.Background(), usagestats.UsageLogFilters{})
	require.NoError(t, err)
	got, err := repo.financialAdminAllTimeTotals(context.Background())
	require.NoError(t, err)
	require.Equal(t, want.TotalRequests, got.TotalRequests)
	require.Equal(t, want.TotalInputTokens, got.TotalInputTokens)
	require.Equal(t, want.TotalOutputTokens, got.TotalOutputTokens)
	require.Equal(t, want.TotalCacheCreationTokens, got.TotalCacheCreationTokens)
	require.Equal(t, want.TotalCacheReadTokens, got.TotalCacheReadTokens)
	require.InDelta(t, want.TotalCost, got.TotalCost, 1e-9)
	require.InDelta(t, want.TotalActualCost, got.TotalActualCost, 1e-9)
	require.InDelta(t, *want.TotalAccountCost, *got.TotalAccountCost, 1e-9)
	require.InDelta(t, want.AverageDurationMs, got.AverageDurationMs, 1e-9)
	require.Equal(t, want.FinancialSummary, got.FinancialSummary)
	return got
}

func TestFinancialRollupExactMutationRecoveryAndMissingBuckets(t *testing.T) {
	schema := financialRollupTestSchema(t)
	seed := financialRollupTestTx(t, schema)
	financialRollupSeedLog(t, seed, 1, "2026-09-22 12:00:00+08", 1)
	financialRollupSeedLog(t, seed, 2, "2026-09-23 12:00:00+08", 2)
	financialRollupSeedReceipt(t, seed, 1, "2026-09-24 12:00:00+08", 4)
	financialRollupExec(t, seed, `UPDATE usage_settlement_receipts SET settled_at='2026-09-25 23:59:59.999999+08' WHERE id=1;
 UPDATE usage_logs SET account_stats_cost=5,account_rate_multiplier=2,duration_ms=1000 WHERE id=2;
 INSERT INTO usage_settlement_receipts(id,request_id,request_fingerprint,user_id,api_key_id,billing_type,charged_amount,accounting_date,settled_at,state,record_source,record_completeness) VALUES(30,'unknown','test',1,2,0,NULL,'2026-09-22','2026-09-25 12:00:00+08','settled','historical_recovery','amount_unknown'),(31,'partial','test',1,2,1,7,'2026-09-22','2026-09-25 12:00:00+08','settled','historical_recovery','partial');`)
	require.NoError(t, seed.Commit())
	financialRollupPublish(t, schema)
	read := financialRollupTestTx(t, schema)
	stats := financialRollupAssertOracle(t, read)
	require.Equal(t, int64(4), stats.TotalRequests)
	require.InDelta(t, 13, stats.TotalActualCost, 1e-9)
	require.InDelta(t, 700, stats.AverageDurationMs, 1e-9)
	require.Equal(t, int64(1), stats.UnknownAmountCount)
	require.False(t, stats.TokenCountsComplete)
	require.NoError(t, read.Commit())
	edits := []string{
		`UPDATE usage_logs SET created_at='2026-09-21 01:00:00+08',actual_cost=20,total_cost=22,duration_ms=NULL,input_tokens=77 WHERE id=2`,
		`UPDATE usage_settlement_receipts SET delivered_at=NOW(),charged_amount=9,detail=detail||'{"duration_ms":800,"total_cost":null}'::jsonb WHERE id=1`,
		`UPDATE usage_settlement_receipts SET completed_at=NULL WHERE id=1`,
		`UPDATE usage_settlement_receipts SET usage_request_id=NULL,usage_log_id=2,completed_at='2026-09-23 12:00:00+08' WHERE id=1`,
		`UPDATE usage_settlement_receipts SET command='{"TerminalFailure":true}'::jsonb WHERE id=1`,
		`DELETE FROM usage_settlement_receipts WHERE id=1`,
		`DELETE FROM usage_logs WHERE id=2`,
	}
	for _, q := range edits {
		change := financialRollupTestTx(t, schema)
		financialRollupExec(t, change, q)
		require.NoError(t, change.Commit())
		dirty := financialRollupTestTx(t, schema)
		financialRollupAssertOracle(t, dirty)
		require.NoError(t, dirty.Commit())
		financialRollupPublish(t, schema)
		clean := financialRollupTestTx(t, schema)
		financialRollupAssertOracle(t, clean)
		require.NoError(t, clean.Commit())
	}
	missing := financialRollupTestTx(t, schema)
	financialRollupExec(t, missing, `DELETE FROM usage_financial_daily_rollups WHERE bucket_date='2026-09-25'`)
	financialRollupAssertOracle(t, missing)
}

func TestFinancialRollupConcurrentCrossDateCommitOrdersAndLowerIDs(t *testing.T) {
	for _, receiptFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(receiptFirst), func(t *testing.T) {
			schema := financialRollupTestSchema(t)
			financialRollupPublish(t, schema)
			logTx := financialRollupTestTx(t, schema)
			receiptTx := financialRollupTestTx(t, schema)
			financialRollupSeedLog(t, logTx, 11, "2026-09-22 23:59:59+08", 0)
			financialRollupSeedReceipt(t, receiptTx, 11, "2026-09-24 00:01:00+08", 6)
			early, late := logTx, receiptTx
			if receiptFirst {
				early, late = receiptTx, logTx
			}
			require.NoError(t, early.Commit())
			financialRollupPublish(t, schema)
			interim := financialRollupTestTx(t, schema)
			financialRollupAssertOracle(t, interim)
			require.NoError(t, interim.Commit())
			require.NoError(t, late.Commit())
			dirty := financialRollupTestTx(t, schema)
			got := financialRollupAssertOracle(t, dirty)
			require.Equal(t, int64(1), got.TotalRequests)
			require.InDelta(t, 6, got.TotalActualCost, 1e-9)
			var events int
			require.NoError(t, dirty.QueryRow(`SELECT COUNT(*) FROM usage_financial_rollup_events`).Scan(&events))
			require.Positive(t, events)
			require.NoError(t, dirty.Commit())
			financialRollupPublish(t, schema)
			final := financialRollupTestTx(t, schema)
			financialRollupAssertOracle(t, final)
		})
	}
}

func TestFinancialRollupWriterIsolationRollbackAndReaderSnapshot(t *testing.T) {
	schema := financialRollupTestSchema(t)
	seed := financialRollupTestTx(t, schema)
	financialRollupSeedLog(t, seed, 1, "2026-09-23 12:00:00+08", 1)
	require.NoError(t, seed.Commit())
	financialRollupPublish(t, schema)
	publisher := financialRollupTestTx(t, schema)
	financialRollupExec(t, publisher, `SELECT id FROM usage_financial_rollup_state WHERE id=1 FOR UPDATE`)
	writer := financialRollupTestTx(t, schema)
	financialRollupExec(t, writer, `SET LOCAL statement_timeout='1s'`)
	financialRollupSeedLog(t, writer, 2, "2026-09-23 13:00:00+08", 2)
	financialRollupSeedReceipt(t, writer, 2, "2026-09-24 13:00:00+08", 5)
	require.NoError(t, writer.Commit())
	require.NoError(t, publisher.Rollback())
	reader := financialRollupTestTx(t, schema)
	before := financialRollupAssertOracle(t, reader)
	failed := financialRollupTestTx(t, schema)
	_, err := newDashboardAggregationRepositoryWithSQL(failed).syncFinancialRollupStep(context.Background(), time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing))
	require.NoError(t, err)
	require.NoError(t, failed.Rollback())
	stillDirty := financialRollupTestTx(t, schema)
	financialRollupAssertOracle(t, stillDirty)
	require.NoError(t, stillDirty.Commit())
	financialRollupPublish(t, schema)
	after := financialRollupAssertOracle(t, reader)
	require.Equal(t, before, after)
}

func TestFinancialRollupRangeDeletionPreservesReceiptFallbackInvalidation(t *testing.T) {
	schema := financialRollupTestSchema(t)
	seed := financialRollupTestTx(t, schema)
	financialRollupSeedLog(t, seed, 1, "2026-09-22 12:00:00+08", 1)
	financialRollupSeedReceipt(t, seed, 1, "2026-09-24 12:00:00+08", 5)
	financialRollupExec(t, seed, `UPDATE usage_settlement_receipts SET detail='{}',record_completeness='partial' WHERE id=1`)
	require.NoError(t, seed.Commit())
	financialRollupPublish(t, schema)
	remove := financialRollupTestTx(t, schema)
	start := time.Date(2026, 9, 22, 0, 0, 0, 0, financialBeijing)
	require.NoError(t, invalidateFinancialRollupsRange(context.Background(), remove, start, start.AddDate(0, 0, 1)))
	financialRollupExec(t, remove, `TRUNCATE usage_logs`)
	require.NoError(t, remove.Commit())
	read := financialRollupTestTx(t, schema)
	got := financialRollupAssertOracle(t, read)
	require.False(t, got.TokenCountsComplete)
	require.Equal(t, int64(1), got.TotalRequests)
	require.NoError(t, read.Commit())
	financialRollupPublish(t, schema)
	final := financialRollupTestTx(t, schema)
	financialRollupAssertOracle(t, final)
}

func TestFinancialRollupIndexedTailPlan(t *testing.T) {
	schema := financialRollupTestSchema(t)
	seed := financialRollupTestTx(t, schema)
	rowsCount := 100000
	if value := os.Getenv("GEILI_FINANCIAL_ROLLUP_PERF_ROWS"); value != "" {
		var err error
		rowsCount, err = strconv.Atoi(value)
		require.NoError(t, err)
		require.GreaterOrEqual(t, rowsCount, 100000)
		require.LessOrEqual(t, rowsCount, 20000000)
	}
	financialRollupExec(t, seed, `INSERT INTO usage_logs(id,user_id,api_key_id,account_id,request_id,model,input_tokens,output_tokens,total_cost,actual_cost,created_at) SELECT g,1,2,3,'bulk-'||g,'fixture',1,2,.01,.01,TIMESTAMPTZ '2026-09-20 00:00:00+08'+(g/$2::integer)*INTERVAL '1 day' FROM generate_series(1,$1::integer) g`, rowsCount, rowsCount/6+1)
	financialRollupExec(t, seed, `INSERT INTO usage_settlement_receipts(request_id,usage_request_id,usage_log_id,request_fingerprint,user_id,api_key_id,charged_amount,accounting_date,completed_at,settled_at,state,record_source,record_completeness,detail,delivered_at) SELECT request_id,request_id,id,'perf',user_id,api_key_id,actual_cost,'2026-09-20',created_at,created_at,'settled','live','complete',to_jsonb(u),created_at FROM usage_logs u WHERE id%100=0;
 DELETE FROM usage_financial_rollup_events;ANALYZE usage_logs;ANALYZE usage_settlement_receipts;`)
	require.NoError(t, seed.Commit())
	financialRollupPublish(t, schema)
	read := financialRollupTestTx(t, schema)
	financialRollupAssertOracle(t, read)
	begin := time.Now()
	stats, err := (&usageLogRepository{sql: read}).financialAdminAllTimeTotals(context.Background())
	require.NoError(t, err)
	elapsed := time.Since(begin)
	require.Equal(t, int64(rowsCount), stats.TotalRequests)
	require.Less(t, elapsed, 3*time.Second)
	t.Logf("FINANCIAL_ROLLUP_PERF rows=%d receipt_rows=%d alltime_elapsed=%s", rowsCount, rowsCount/100, elapsed)
	fullQuery, args, err := financialRollupTotalsQuery(time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing), []string{})
	require.NoError(t, err)
	var fullPlan string
	require.NoError(t, read.QueryRow("EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) "+fullQuery, args...).Scan(&fullPlan))
	t.Logf("FINANCIAL_ROLLUP_FULL_PLAN %s", fullPlan)
	require.NotContains(t, fullPlan, `"JIT"`)

	var plan string
	query := `EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) SELECT ` + financialRollupRawColumns + ` FROM usage_financial_total_records WHERE ` + financialRollupRangeSQL("$1::timestamptz", "")
	require.NoError(t, read.QueryRow(query, time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing)).Scan(&plan))
	require.Contains(t, plan, "Index")
	t.Logf("FINANCIAL_ROLLUP_TAIL_PLAN %s", plan)
	var parsed []struct {
		Plan map[string]any `json:"Plan"`
	}
	require.NoError(t, json.Unmarshal([]byte(plan), &parsed))
	assertFinancialPlanNoSpill(t, parsed[0].Plan)
}

func TestFinancialRollupTwoConsumersRetryAfterSerialization(t *testing.T) {
	schema := financialRollupTestSchema(t)
	seed := financialRollupTestTx(t, schema)
	financialRollupSeedLog(t, seed, 1, "2026-09-22 12:00:00+08", 1)
	require.NoError(t, seed.Commit())
	financialRollupPublish(t, schema)
	first := financialRollupTestTx(t, schema)
	second := financialRollupTestTx(t, schema)
	// Establish both snapshots before either consumer publishes a new state tuple.
	financialRollupExec(t, first, `SELECT COUNT(*) FROM usage_financial_rollup_events`)
	financialRollupExec(t, second, `SELECT COUNT(*) FROM usage_financial_rollup_events`)
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing)
	_, err := newDashboardAggregationRepositoryWithSQL(first).syncFinancialRollupStep(context.Background(), now)
	require.NoError(t, err)
	require.NoError(t, first.Commit())
	_, err = newDashboardAggregationRepositoryWithSQL(second).syncFinancialRollupStep(context.Background(), now)
	var pgErr *pq.Error
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, pq.ErrorCode("40001"), pgErr.Code)
	require.NoError(t, second.Rollback())
	financialRollupPublish(t, schema)
	read := financialRollupTestTx(t, schema)
	financialRollupAssertOracle(t, read)
}

func TestFinancialRollupBackfillPartialAndOlderRecovery(t *testing.T) {
	schema := financialRollupTestSchema(t)
	seed := financialRollupTestTx(t, schema)
	financialRollupSeedLog(t, seed, 1, "2026-09-24 12:00:00+08", 1)
	financialRollupSeedLog(t, seed, 2, "2026-09-25 12:00:00+08", 2)
	require.NoError(t, seed.Commit())
	one := financialRollupTestTx(t, schema)
	more, err := newDashboardAggregationRepositoryWithSQL(one).syncFinancialRollupStep(context.Background(), time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing))
	require.NoError(t, err)
	require.True(t, more)
	require.NoError(t, one.Commit())
	read := financialRollupTestTx(t, schema)
	financialRollupAssertOracle(t, read)
	require.NoError(t, read.Commit())
	financialRollupPublish(t, schema)
	recovery := financialRollupTestTx(t, schema)
	financialRollupSeedReceipt(t, recovery, 3, "2026-09-20 00:00:00+08", 3)
	require.NoError(t, recovery.Commit())
	dirty := financialRollupTestTx(t, schema)
	financialRollupAssertOracle(t, dirty)
	require.NoError(t, dirty.Commit())
	one = financialRollupTestTx(t, schema)
	_, err = newDashboardAggregationRepositoryWithSQL(one).syncFinancialRollupStep(context.Background(), time.Date(2026, 9, 26, 0, 0, 0, 0, financialBeijing))
	require.NoError(t, err)
	require.NoError(t, one.Commit())
	gaps := financialRollupTestTx(t, schema)
	financialRollupAssertOracle(t, gaps)
	require.NoError(t, gaps.Commit())
	financialRollupPublish(t, schema)
	final := financialRollupTestTx(t, schema)
	financialRollupAssertOracle(t, final)
	var buckets int
	require.NoError(t, final.QueryRow(`SELECT COUNT(*) FROM usage_financial_daily_rollups`).Scan(&buckets))
	require.Equal(t, 6, buckets)
}

func TestFinancialRollupStateLockDoesNotBlockSubscriptionFKOrDebit(t *testing.T) {
	schema := financialRollupTestSchema(t)
	seed := financialRollupTestTx(t, schema)
	financialRollupExec(t, seed, `CREATE TABLE parents(id bigint PRIMARY KEY,amount numeric NOT NULL DEFAULT 0);INSERT INTO parents(id) VALUES(1);ALTER TABLE usage_logs ADD FOREIGN KEY(subscription_id) REFERENCES parents(id)`)
	require.NoError(t, seed.Commit())
	publisher := financialRollupTestTx(t, schema)
	financialRollupExec(t, publisher, `SELECT id FROM usage_financial_rollup_state WHERE id=1 FOR UPDATE`)
	writer := financialRollupTestTx(t, schema)
	financialRollupExec(t, writer, `SET LOCAL statement_timeout='1s'`)
	financialRollupSeedLog(t, writer, 1, "2026-09-22 12:00:00+08", 1)
	financialRollupExec(t, writer, `UPDATE usage_logs SET subscription_id=1 WHERE id=1`)
	billing := financialRollupTestTx(t, schema)
	financialRollupExec(t, billing, `SET LOCAL statement_timeout='1s';UPDATE parents SET amount=amount+1 WHERE id=1`)
	require.NoError(t, billing.Commit())
	require.NoError(t, writer.Commit())
	require.NoError(t, publisher.Rollback())
}
